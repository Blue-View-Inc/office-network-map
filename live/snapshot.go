package live

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"officemap/switchread"
)

// Port is one front-panel switch port as last read from the switch.
type Port struct {
	Name   string   `json:"name"`
	N      int      `json:"n"`
	Status string   `json:"status"`
	Vlan   string   `json:"vlan"`
	Speed  string   `json:"speed"`
	Desc   string   `json:"desc,omitempty"`
	MACs   []string `json:"macs,omitempty"`
}

// Device is one probed IPv4 address.
type Device struct {
	IP           string     `json:"ip"`
	Online       bool       `json:"online"`
	LastSeen     *time.Time `json:"lastSeen,omitempty"`
	OfflineSince *time.Time `json:"offlineSince,omitempty"`
}

// MACSeen is a MAC address the switch has learned, with when it was last present.
type MACSeen struct {
	MAC      string    `json:"mac"`
	Port     string    `json:"port"` // "" once the MAC is gone
	N        int       `json:"n,omitempty"`
	LastSeen time.Time `json:"lastSeen"`
}

// Snapshot is the poller's latest picture.
type Snapshot struct {
	Time             time.Time `json:"time"`
	Enabled          bool      `json:"enabled"`
	SwitchConfigured bool      `json:"switchConfigured"`
	SwitchOK         bool      `json:"switchOk"`
	SwitchError      string    `json:"switchError,omitempty"`
	Ports            []Port    `json:"ports"`
	Devices          []Device  `json:"devices"`
	MACs             []MACSeen `json:"macs"`
}

// MapView is what the poller needs to know about the map; the main package implements it.
type MapView interface {
	Lane(n int) string           // lane key of port n
	Expects(n int) bool          // the map says port n should have a link
	LaneForVlan(v string) string // the one lane that names VLAN v, or ""
	PortForIP(ip string) int     // port whose table row mentions ip, or 0
}

// Config wires a Poller. Only Probe and Now have defaults.
type Config struct {
	Every        time.Duration
	OfflineAfter time.Duration
	Switch       func(context.Context) (*switchread.Snapshot, error) // nil: no switch
	Targets      func() []string
	Map          func() MapView // may be nil
	Probe        func(ctx context.Context, ip string) bool
	Now          func() time.Time
	Alerts       *Alerts // may be nil
}

// Poller periodically refreshes a Snapshot and raises alerts.
type Poller struct {
	cfg Config

	mu       sync.RWMutex
	cur      *Snapshot
	prev     *Snapshot
	lastSeen map[string]time.Time // ip -> last time it answered
	offline  map[string]time.Time // ip -> when it was first seen offline (this episode)
	macs     map[string]*MACSeen
	subs     map[chan struct{}]bool
}

func NewPoller(cfg Config) *Poller {
	if cfg.Probe == nil {
		cfg.Probe = PingProbe
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Every <= 0 {
		cfg.Every = 30 * time.Second
	}
	if cfg.OfflineAfter <= 0 {
		cfg.OfflineAfter = 10 * time.Minute
	}
	return &Poller{cfg: cfg, lastSeen: map[string]time.Time{}, offline: map[string]time.Time{},
		macs: map[string]*MACSeen{}, subs: map[chan struct{}]bool{}}
}

// PingProbe sends one ICMP echo with a one second wait (the system ping, no privileges needed).
func PingProbe(ctx context.Context, ip string) bool {
	if !validIP(ip) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ping", "-c1", "-W1", ip).Run() == nil
}

func validIP(ip string) bool { return len(ExtractIPs(ip)) == 1 && !strings.ContainsAny(ip, " -/") }

// Snapshot returns the latest snapshot, or an empty one before the first cycle.
func (p *Poller) Snapshot() *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cur == nil {
		return &Snapshot{Enabled: true, SwitchConfigured: p.cfg.Switch != nil}
	}
	return p.cur
}

// Subscribe returns a channel that receives a signal after every cycle, and a cancel func.
func (p *Poller) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	p.mu.Lock()
	p.subs[ch] = true
	p.mu.Unlock()
	return ch, func() { p.mu.Lock(); delete(p.subs, ch); p.mu.Unlock() }
}

// Run cycles until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	p.Cycle(ctx)
	t := time.NewTicker(p.cfg.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Cycle(ctx)
		}
	}
}

// Cycle runs one poll: read the switch, probe devices (16 at a time), diff, publish.
func (p *Poller) Cycle(ctx context.Context) {
	now := p.cfg.Now()
	snap := &Snapshot{Time: now, Enabled: true, SwitchConfigured: p.cfg.Switch != nil}
	var sw *switchread.Snapshot
	if p.cfg.Switch != nil {
		var err error
		if sw, err = p.cfg.Switch(ctx); err != nil {
			snap.SwitchError = err.Error()
			sw = nil
		} else {
			snap.SwitchOK = true
		}
	}
	var ips []string
	if p.cfg.Targets != nil {
		ips = p.cfg.Targets()
	}
	online := make([]bool, len(ips))
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i, ip := range ips {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			online[i] = p.cfg.Probe(ctx, ip)
		}()
	}
	wg.Wait()

	p.mu.Lock()
	for i, ip := range ips {
		d := Device{IP: ip, Online: online[i]}
		if d.Online {
			p.lastSeen[ip] = now
			delete(p.offline, ip)
		} else if _, ok := p.offline[ip]; !ok {
			p.offline[ip] = now
		}
		if t, ok := p.lastSeen[ip]; ok {
			t := t
			d.LastSeen = &t
		}
		if t, ok := p.offline[ip]; ok && !d.Online {
			t := t
			d.OfflineSince = &t
		}
		snap.Devices = append(snap.Devices, d)
	}
	if sw != nil {
		byMAC := map[string]string{}
		macs := sw.MACsByPort()
		for _, s := range sw.Status {
			n, te, ok := switchread.PortNumber(s.Port)
			if !ok || te != 0 {
				continue
			}
			snap.Ports = append(snap.Ports, Port{Name: s.Port, N: n, Status: s.Status, Vlan: s.Vlan,
				Speed: s.Speed, Desc: s.Name, MACs: macs[s.Port]})
		}
		for port, ms := range macs {
			if n, te, ok := switchread.PortNumber(port); ok && te == 0 {
				_ = n
				for _, m := range ms {
					byMAC[m] = port
				}
			}
		}
		for m, port := range byMAC {
			n, _, _ := switchread.PortNumber(port)
			p.macs[m] = &MACSeen{MAC: m, Port: port, N: n, LastSeen: now}
		}
		for m, e := range p.macs {
			if _, here := byMAC[m]; !here {
				e.Port, e.N = "", 0
			}
		}
	}
	for _, e := range p.macs {
		snap.MACs = append(snap.MACs, *e)
	}
	sortMACs(snap.MACs)
	prev := p.cur
	p.prev, p.cur = prev, snap
	subs := make([]chan struct{}, 0, len(p.subs))
	for c := range p.subs {
		subs = append(subs, c)
	}
	p.mu.Unlock()

	if p.cfg.Alerts != nil {
		var m MapView
		if p.cfg.Map != nil {
			m = p.cfg.Map()
		}
		for _, a := range Diff(prev, snap, m, p.cfg.OfflineAfter) {
			p.cfg.Alerts.Add(a)
		}
	}
	for _, c := range subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

func sortMACs(m []MACSeen) {
	sort.Slice(m, func(i, j int) bool { return m[i].MAC < m[j].MAC })
}
