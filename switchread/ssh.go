package switchread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The only commands this package will ever send to a switch.
const (
	CmdPagerOff = "terminal length 0"
	CmdStatus   = "show interfaces status"
	CmdVLAN     = "show vlan brief"
	CmdTrunk    = "show interfaces trunk"
	CmdMAC      = "show mac address-table dynamic"
)

var allowed = map[string]bool{
	CmdPagerOff: true, CmdStatus: true, CmdVLAN: true, CmdTrunk: true, CmdMAC: true,
}

// AllowedCommands lists the exact command strings that may be sent.
func AllowedCommands() []string {
	return []string{CmdPagerOff, CmdStatus, CmdVLAN, CmdTrunk, CmdMAC}
}

// CheckCommand refuses everything that is not exactly one of the allowlisted commands.
// Whitespace around and between words is normalised; no abbreviations, no other case,
// no control characters, no chaining.
func CheckCommand(cmd string) error {
	for _, r := range cmd {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("switch command refused: control character in %q", cmd)
		}
	}
	if !allowed[strings.Join(strings.Fields(cmd), " ")] {
		return fmt.Errorf("switch command refused (not on the read-only allowlist): %q", cmd)
	}
	return nil
}

// Config says how to reach the switch.
type Config struct {
	Host         string // host or host:port (default port 22)
	User         string
	KeyFile      string
	KnownHosts   string // OpenSSH known_hosts; host keys are verified strictly
	HostKeyAlias string // look the target up in known_hosts under this name instead of Host
	Jump         string // optional user@host[:port] hop, same key
	Timeout      time.Duration
}

// Missing returns the names of required flags that are not set.
func (c Config) Missing() []string {
	var m []string
	for _, f := range []struct{ flag, v string }{
		{"-switch-host", c.Host}, {"-switch-user", c.User}, {"-switch-key", c.KeyFile}, {"-switch-known-hosts", c.KnownHosts},
	} {
		if strings.TrimSpace(f.v) == "" {
			m = append(m, f.flag)
		}
	}
	return m
}

// Configured reports whether every required setting is present.
func (c Config) Configured() bool { return len(c.Missing()) == 0 }

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 20 * time.Second
}

func withPort(h string) string {
	if _, _, err := net.SplitHostPort(h); err == nil {
		return h
	}
	return net.JoinHostPort(strings.Trim(h, "[]"), "22")
}

// Client fetches snapshots from a switch.
type Client struct{ Cfg Config }

// Fetch reads all four tables in one SSH session.
func (c *Client) Fetch(ctx context.Context) (*Snapshot, error) {
	outs, err := c.Run(ctx, []string{CmdStatus, CmdVLAN, CmdTrunk, CmdMAC})
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Status: ParseInterfacesStatus(outs[0]),
		VLANs:  ParseVLANBrief(outs[1]),
		Trunks: ParseTrunks(outs[2]),
		MACs:   ParseMACTable(outs[3]),
	}, nil
}

// Run sends `terminal length 0` and then each command (all checked against the allowlist
// before anything is sent) and returns each command's output.
func (c *Client) Run(ctx context.Context, cmds []string) ([]string, error) {
	for _, cmd := range cmds {
		if err := CheckCommand(cmd); err != nil {
			return nil, err
		}
	}
	if !c.Cfg.Configured() {
		return nil, fmt.Errorf("switch not configured: set %s", strings.Join(c.Cfg.Missing(), ", "))
	}
	ctx, cancel := context.WithTimeout(ctx, c.Cfg.timeout()*time.Duration(2+len(cmds)))
	defer cancel()

	client, closeAll, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer closeAll()

	sess, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("switch session: %w", err)
	}
	defer sess.Close()
	if err := sess.RequestPty("vt100", 200, 200, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		return nil, fmt.Errorf("switch pty: %w", err)
	}
	in, err := sess.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		return nil, fmt.Errorf("switch shell: %w", err)
	}
	sh := newShell(out)
	// whatever the context does, never leave the connection hanging
	go func() { <-ctx.Done(); sess.Close() }()

	if _, err := sh.untilPrompt(ctx, c.Cfg.timeout()); err != nil {
		return nil, fmt.Errorf("waiting for the switch prompt: %w", err)
	}
	send := func(cmd string) (string, error) {
		if err := CheckCommand(cmd); err != nil { // belt and braces: checked again at the wire
			return "", err
		}
		if _, err := io.WriteString(in, cmd+"\r"); err != nil {
			return "", err
		}
		raw, err := sh.untilPrompt(ctx, c.Cfg.timeout())
		if err != nil {
			return "", fmt.Errorf("%q: %w", cmd, err)
		}
		return cleanOutput(raw, cmd), nil
	}
	if _, err := send(CmdPagerOff); err != nil {
		return nil, err
	}
	res := make([]string, len(cmds))
	for i, cmd := range cmds {
		if res[i], err = send(cmd); err != nil {
			return nil, err
		}
		if strings.Contains(res[i], "--More--") {
			return nil, fmt.Errorf("%q: the switch is paging output", cmd)
		}
	}
	return res, nil
}

// dial connects (optionally through the jump host) with strict host-key checking.
func (c *Client) dial(ctx context.Context) (*ssh.Client, func(), error) {
	keyBytes, err := os.ReadFile(c.Cfg.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("switch key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("switch key: %w", err)
	}
	// IOS 15.2 only does RSA with SHA-1 signatures ("ssh-rsa"); offer exactly that for RSA keys.
	if as, ok := signer.(ssh.AlgorithmSigner); ok && signer.PublicKey().Type() == ssh.KeyAlgoRSA {
		if s2, err := ssh.NewSignerWithAlgorithms(as, []string{ssh.KeyAlgoRSA}); err == nil {
			signer = s2
		}
	}
	kh, err := knownhosts.New(c.Cfg.KnownHosts)
	if err != nil {
		return nil, nil, fmt.Errorf("switch known_hosts: %w", err)
	}
	target := withPort(c.Cfg.Host)
	check := ssh.HostKeyCallback(kh)
	if a := strings.TrimSpace(c.Cfg.HostKeyAlias); a != "" {
		_, port, _ := net.SplitHostPort(target)
		alias := net.JoinHostPort(a, port)
		check = func(_ string, remote net.Addr, key ssh.PublicKey) error { return kh(alias, remote, key) }
	}
	auth := []ssh.AuthMethod{ssh.PublicKeys(signer)}
	d := net.Dialer{Timeout: c.Cfg.timeout()}

	var closers []func()
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	var conn net.Conn
	if j := strings.TrimSpace(c.Cfg.Jump); j != "" {
		user, host, ok := strings.Cut(j, "@")
		if !ok || user == "" || host == "" {
			return nil, nil, errors.New("-switch-jump must look like user@host")
		}
		jcfg := &ssh.ClientConfig{User: user, Auth: auth, HostKeyCallback: kh, Timeout: c.Cfg.timeout()}
		jaddr := withPort(host)
		jc, err := d.DialContext(ctx, "tcp", jaddr)
		if err != nil {
			return nil, nil, fmt.Errorf("jump host: %w", err)
		}
		jcc, jch, jrq, err := ssh.NewClientConn(jc, jaddr, jcfg)
		if err != nil {
			jc.Close()
			return nil, nil, fmt.Errorf("jump host: %w", err)
		}
		jump := ssh.NewClient(jcc, jch, jrq)
		closers = append(closers, func() { jump.Close() })
		if conn, err = jump.Dial("tcp", target); err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("jump to switch: %w", err)
		}
	} else {
		var err error
		if conn, err = d.DialContext(ctx, "tcp", target); err != nil {
			return nil, nil, fmt.Errorf("switch connect: %w", err)
		}
	}
	cfg := &ssh.ClientConfig{
		User:              c.Cfg.User,
		Auth:              auth,
		HostKeyCallback:   check,
		HostKeyAlgorithms: []string{ssh.KeyAlgoRSA, ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512},
		Timeout:           c.Cfg.timeout(),
	}
	cfg.KeyExchanges = []string{"curve25519-sha256", "ecdh-sha2-nistp256", "diffie-hellman-group14-sha256", ssh.InsecureKeyExchangeDH14SHA1}
	cc, ch, rq, err := ssh.NewClientConn(conn, target, cfg)
	if err != nil {
		conn.Close()
		closeAll()
		return nil, nil, fmt.Errorf("switch ssh: %w", err)
	}
	cl := ssh.NewClient(cc, ch, rq)
	closers = append(closers, func() { cl.Close() })
	return cl, closeAll, nil
}

// shell reads the interactive session in a goroutine so reads can time out.
type shell struct {
	ch  chan []byte
	buf bytes.Buffer
	err error
}

func newShell(r io.Reader) *shell {
	s := &shell{ch: make(chan []byte, 64)}
	go func() {
		defer close(s.ch)
		b := make([]byte, 4096)
		for {
			n, err := r.Read(b)
			if n > 0 {
				s.ch <- append([]byte(nil), b[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

var promptRe = regexp.MustCompile(`(?m)^[A-Za-z0-9._()-]+[#>] ?$`)

// untilPrompt returns everything received up to and including the next prompt line.
func (s *shell) untilPrompt(ctx context.Context, d time.Duration) (string, error) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		txt := s.buf.String()
		if i := lastLine(txt); promptRe.MatchString(txt[i:]) {
			s.buf.Reset()
			return txt, nil
		}
		select {
		case b, ok := <-s.ch:
			if !ok {
				return "", errors.New("connection closed by the switch")
			}
			s.buf.Write(b)
		case <-timer.C:
			return "", errors.New("timed out waiting for the switch")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func lastLine(s string) int {
	s = strings.TrimRight(s, " ")
	if i := strings.LastIndexAny(s, "\n\r"); i >= 0 {
		return i + 1
	}
	return 0
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// cleanOutput drops the echoed command and the trailing prompt line and normalises line ends.
func cleanOutput(raw, cmd string) string {
	raw = ansiRe.ReplaceAllString(raw, "")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	ls := strings.Split(raw, "\n")
	if len(ls) > 0 && strings.Contains(ls[0], cmd) {
		ls = ls[1:]
	}
	if n := len(ls); n > 0 && promptRe.MatchString(strings.TrimRight(ls[n-1], " ")+" ") {
		ls = ls[:n-1]
	}
	return strings.Join(ls, "\n")
}
