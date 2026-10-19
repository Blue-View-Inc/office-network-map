package switchread

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestCheckCommandAllowlist(t *testing.T) {
	for _, c := range AllowedCommands() {
		if err := CheckCommand(c); err != nil {
			t.Errorf("%q should be allowed: %v", c, err)
		}
	}
	if err := CheckCommand("  show   interfaces  status "); err != nil {
		t.Errorf("whitespace should be normalised: %v", err)
	}
	for _, c := range []string{
		"", "configure terminal", "conf t", "write memory", "reload", "enable", "show running-config",
		"show startup-config", "show interfaces", "show interfaces status; reload", "show interfaces status\nreload",
		"show interfaces status\r", "SHOW INTERFACES STATUS", "sh int status", "show vlan", "terminal length 24",
		"terminal length 0 ", "no shutdown", "show mac address-table", "show mac address-table dynamic | include 0200",
		"show interfaces status | begin Gi", "clear mac address-table dynamic", "copy running-config tftp:", "\x00",
	} {
		err := CheckCommand(c)
		// "terminal length 0 " has only a trailing space, which normalisation removes
		if c == "terminal length 0 " {
			if err != nil {
				t.Errorf("trailing space should normalise: %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q must be refused", c)
		}
	}
	if len(AllowedCommands()) != 5 {
		t.Error("allowlist must be exactly five commands")
	}
}

func TestRunRefusesBeforeConnecting(t *testing.T) {
	c := &Client{Cfg: Config{Host: "127.0.0.1:1", User: "u", KeyFile: "k", KnownHosts: "kh"}}
	_, err := c.Run(context.Background(), []string{"show running-config"})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("expected allowlist refusal, got %v", err)
	}
	_, err = (&Client{}).Run(context.Background(), []string{CmdStatus})
	if err == nil || !strings.Contains(err.Error(), "-switch-host") {
		t.Errorf("expected not-configured error naming the flags, got %v", err)
	}
}

// ---- in-process fake switch ----

func rsaSigner(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s, pemBytes
}

type fakeSwitch struct {
	addr     string
	hostKey  ssh.PublicKey
	mu       sync.Mutex
	received []string
	stall    bool // never answer commands
	l        net.Listener
}

func (f *fakeSwitch) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.received...)
}

// startSwitch runs an SSH server that behaves like IOS 15.2: RSA host key offered only as
// ssh-rsa, SHA-1 RSA user signatures, group14-sha1 key exchange, PTY shell with a prompt.
func startSwitch(t *testing.T, clientPub ssh.PublicKey, legacyOnly bool) *fakeSwitch {
	t.Helper()
	hostSigner, _ := rsaSigner(t)
	legacy, err := ssh.NewSignerWithAlgorithms(hostSigner.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if string(k.Marshal()) == string(clientPub.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, io.ErrUnexpectedEOF
		},
	}
	if legacyOnly {
		cfg.KeyExchanges = []string{ssh.InsecureKeyExchangeDH14SHA1}
		cfg.PublicKeyAuthAlgorithms = []string{ssh.KeyAlgoRSA}
	}
	cfg.AddHostKey(legacy)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSwitch{addr: l.Addr().String(), hostKey: hostSigner.PublicKey(), l: l}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(c, cfg)
		}
	}()
	return f
}

var fixtures = map[string]string{
	CmdStatus: "show_interfaces_status.txt", CmdVLAN: "show_vlan_brief.txt",
	CmdTrunk: "show_interfaces_trunk.txt", CmdMAC: "show_mac_address-table_dynamic.txt",
}

func (f *fakeSwitch) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	defer nc.Close()
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			ch, creqs, err := nch.Accept()
			if err != nil {
				return
			}
			go f.session(ch, creqs)
		case "direct-tcpip": // lets one fake switch act as the jump host
			var d struct {
				Host string
				Port uint32
				OA   string
				OP   uint32
			}
			if ssh.Unmarshal(nch.ExtraData(), &d) != nil {
				nch.Reject(ssh.ConnectionFailed, "bad")
				continue
			}
			up, err := net.Dial("tcp", net.JoinHostPort(d.Host, itoa(d.Port)))
			if err != nil {
				nch.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, creqs, _ := nch.Accept()
			go ssh.DiscardRequests(creqs)
			go func() { io.Copy(up, ch); up.Close() }()
			go func() { io.Copy(ch, up); ch.Close() }()
		default:
			nch.Reject(ssh.UnknownChannelType, "no")
		}
	}
}

func itoa(u uint32) string { return strconv.Itoa(int(u)) }

func (f *fakeSwitch) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	shell := make(chan struct{})
	go func() {
		for r := range reqs {
			switch r.Type {
			case "pty-req":
				r.Reply(true, nil)
			case "shell":
				r.Reply(true, nil)
				close(shell)
			default: // "exec" and everything else: refused, like a locked-down switch
				r.Reply(false, nil)
			}
		}
	}()
	<-shell
	io.WriteString(ch, "\r\nUnauthorized use prohibited.\r\n\r\nsw1#")
	var line strings.Builder
	b := make([]byte, 1)
	for {
		if _, err := ch.Read(b); err != nil {
			return
		}
		if b[0] != '\r' && b[0] != '\n' {
			line.WriteByte(b[0])
			continue
		}
		cmd := line.String()
		line.Reset()
		if cmd == "" {
			continue
		}
		f.mu.Lock()
		f.received = append(f.received, cmd)
		stall := f.stall
		f.mu.Unlock()
		if stall {
			continue
		}
		io.WriteString(ch, cmd+"\r\n")
		switch {
		case cmd == CmdPagerOff:
		case fixtures[cmd] != "":
			data, _ := os.ReadFile("testdata/" + fixtures[cmd])
			io.WriteString(ch, strings.ReplaceAll(string(data), "\n", "\r\n"))
		default:
			io.WriteString(ch, "% Invalid input detected at '^' marker.\r\n")
		}
		io.WriteString(ch, "sw1#")
	}
}

type env struct {
	cfg      Config
	sw       *fakeSwitch
	keyPath  string
	khPath   string
	signer   ssh.Signer
	dir      string
	hostLine func(host string, key ssh.PublicKey) string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	signer, pemBytes := rsaSigner(t)
	keyPath := filepath.Join(dir, "id_rsa")
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sw := startSwitch(t, signer.PublicKey(), true)
	e := &env{sw: sw, keyPath: keyPath, khPath: filepath.Join(dir, "known_hosts"), signer: signer, dir: dir}
	e.hostLine = func(host string, key ssh.PublicKey) string { return knownhosts.Line([]string{host}, key) }
	e.cfg = Config{Host: sw.addr, User: "reader", KeyFile: keyPath, KnownHosts: e.khPath, Timeout: 5 * time.Second}
	return e
}

func (e *env) trust(t *testing.T, lines ...string) {
	t.Helper()
	if err := os.WriteFile(e.khPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFetchAgainstLegacyServer(t *testing.T) {
	e := newEnv(t)
	e.trust(t, e.hostLine(e.sw.addr, e.sw.hostKey))
	snap, err := (&Client{Cfg: e.cfg}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Status) != 55 || len(snap.VLANs) != 18 || len(snap.Trunks) != 4 || len(snap.MACs) != 22 {
		t.Errorf("snapshot sizes: %d %d %d %d", len(snap.Status), len(snap.VLANs), len(snap.Trunks), len(snap.MACs))
	}
	if snap.Status[0].Name != "fw1 LAN trunk" {
		t.Errorf("first interface: %+v", snap.Status[0])
	}
	want := []string{CmdPagerOff, CmdStatus, CmdVLAN, CmdTrunk, CmdMAC}
	got := e.sw.commands()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands sent: %v", got)
	}
	for _, c := range got {
		if err := CheckCommand(c); err != nil {
			t.Errorf("a non-allowlisted command reached the wire: %q", c)
		}
	}
}

func TestStrictHostKeyChecking(t *testing.T) {
	e := newEnv(t)
	other, _ := rsaSigner(t)
	// wrong key on file for this address
	e.trust(t, e.hostLine(e.sw.addr, other.PublicKey()))
	if _, err := (&Client{Cfg: e.cfg}).Fetch(context.Background()); err == nil {
		t.Fatal("a changed host key must be refused")
	}
	// host not in known_hosts at all: never auto-accepted
	e.trust(t, e.hostLine("192.0.2.77:22", e.sw.hostKey))
	if _, err := (&Client{Cfg: e.cfg}).Fetch(context.Background()); err == nil {
		t.Fatal("an unknown host must be refused")
	}
	if n := len(e.sw.commands()); n != 0 {
		t.Errorf("no command may be sent on a refused connection, got %d", n)
	}
	if got, _ := os.ReadFile(e.khPath); strings.Contains(string(got), e.sw.addr) {
		t.Error("known_hosts must never be modified")
	}
}

func TestHostKeyAlias(t *testing.T) {
	e := newEnv(t)
	_, port, _ := net.SplitHostPort(e.sw.addr)
	e.trust(t, e.hostLine("sw1.example.test:"+port, e.sw.hostKey))
	if _, err := (&Client{Cfg: e.cfg}).Fetch(context.Background()); err == nil {
		t.Fatal("without the alias the address is unknown")
	}
	e.cfg.HostKeyAlias = "sw1.example.test"
	if _, err := (&Client{Cfg: e.cfg}).Fetch(context.Background()); err != nil {
		t.Fatalf("alias should match: %v", err)
	}
}

func TestWrongUserKeyRefused(t *testing.T) {
	e := newEnv(t)
	e.trust(t, e.hostLine(e.sw.addr, e.sw.hostKey))
	_, pemBytes := rsaSigner(t)
	if err := os.WriteFile(e.keyPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Client{Cfg: e.cfg}).Fetch(context.Background()); err == nil {
		t.Fatal("an unauthorised key must fail")
	}
}

func TestTimeoutWhenSwitchStalls(t *testing.T) {
	e := newEnv(t)
	e.trust(t, e.hostLine(e.sw.addr, e.sw.hostKey))
	e.sw.stall = true
	e.cfg.Timeout = 400 * time.Millisecond
	start := time.Now()
	_, err := (&Client{Cfg: e.cfg}).Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("took far too long")
	}
}

func TestJumpHost(t *testing.T) {
	e := newEnv(t)
	// a second server plays the jump host: same key, forwards direct-tcpip
	jump := startSwitch(t, e.signer.PublicKey(), false)
	e.trust(t, e.hostLine(e.sw.addr, e.sw.hostKey), e.hostLine(jump.addr, jump.hostKey))
	e.cfg.Jump = "hop@" + jump.addr
	snap, err := (&Client{Cfg: e.cfg}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Status) == 0 {
		t.Error("no data through the jump host")
	}
	if len(jump.commands()) != 0 {
		t.Error("the jump host must never receive commands")
	}
	// an untrusted jump host is refused too
	e.trust(t, e.hostLine(e.sw.addr, e.sw.hostKey))
	if _, err := (&Client{Cfg: e.cfg}).Fetch(context.Background()); err == nil {
		t.Error("unknown jump host must be refused")
	}
	e.cfg.Jump = "nonsense"
	if _, err := (&Client{Cfg: e.cfg}).Fetch(context.Background()); err == nil {
		t.Error("malformed jump must be refused")
	}
}

func TestConfigMissing(t *testing.T) {
	if (Config{}).Configured() {
		t.Error("empty config is not configured")
	}
	m := Config{Host: "h"}.Missing()
	if len(m) != 3 {
		t.Errorf("missing: %v", m)
	}
	if !(Config{Host: "h", User: "u", KeyFile: "k", KnownHosts: "kh"}).Configured() {
		t.Error("complete config")
	}
}

func TestCleanOutput(t *testing.T) {
	raw := "show vlan brief\r\nVLAN Name\r\n1 default\r\nsw1#"
	if got := cleanOutput(raw, "show vlan brief"); got != "VLAN Name\n1 default" {
		t.Errorf("%q", got)
	}
}
