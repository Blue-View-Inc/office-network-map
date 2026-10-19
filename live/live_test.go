package live

import (
	"context"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"officemap/switchread"
)

func TestExtractIPs(t *testing.T) {
	got := ExtractIPs(
		"<b>host</b> 192.0.2.10 and 192.0.2.11",
		"192.0.2.20-30",                // range
		"192.0.2.0/24 network",         // network
		"192.0.2.x",                    // wildcard
		"DHCP pool 192.0.2.100",        // DHCP text
		"will be 192.0.2.12",           // plain host
		"bad 999.1.1.1 and 1192.0.2.5", // not addresses
		"https://192.0.2.10 again",
	)
	want := []string{"192.0.2.10", "192.0.2.11", "192.0.2.12"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if u := Union([]string{"b", "a"}, []string{"a", ""}); !reflect.DeepEqual(u, []string{"a", "b"}) {
		t.Fatalf("union %v", u)
	}
}

func TestSpeedMbps(t *testing.T) {
	for in, want := range map[string]int{"a-1000": 1000, "100": 100, "10G": 10000, "a-100": 100} {
		if n, ok := SpeedMbps(in); !ok || n != want {
			t.Errorf("%s: %d %v", in, n, ok)
		}
	}
	if _, ok := SpeedMbps("auto"); ok {
		t.Error("auto is unknown")
	}
}

type fakeMap struct{}

func (fakeMap) Lane(n int) string {
	if n == 3 {
		return "cam"
	}
	return "desk"
}
func (fakeMap) Expects(int) bool { return true }
func (fakeMap) LaneForVlan(v string) string {
	if v == "30" {
		return "cam"
	}
	if v == "20" {
		return "desk"
	}
	return ""
}
func (fakeMap) PortForIP(ip string) int {
	if ip == "192.0.2.5" {
		return 5
	}
	return 0
}

func sw(ports ...Port) *Snapshot {
	return &Snapshot{SwitchOK: true, Ports: ports}
}

func kinds(as []Alert) map[string]int {
	m := map[string]int{}
	for _, a := range as {
		m[a.Kind]++
	}
	return m
}

func TestDiffSwitchAlerts(t *testing.T) {
	t0 := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	prev := sw(
		Port{N: 1, Status: "connected", Vlan: "20", MACs: []string{"aaaa.0000.0001", "aaaa.0000.0009"}},
		Port{N: 2, Status: "connected", Vlan: "20", MACs: []string{"aaaa.0000.0002"}},
		Port{N: 3, Status: "notconnect", Vlan: "30"},
	)
	prev.Time = t0
	cur := sw(
		Port{N: 1, Status: "connected", Vlan: "20", MACs: []string{"aaaa.0000.0001"}},                   // 0009 gone
		Port{N: 2, Status: "notconnect", Vlan: "20"},                                                    // link down, 0002 moved
		Port{N: 3, Status: "connected", Vlan: "30", MACs: []string{"aaaa.0000.0002", "aaaa.0000.0003"}}, // link up, moved in, new mac
		Port{N: 4, Status: "connected", Vlan: "30", MACs: nil},                                          // vlan mismatch (map lane desk)
	)
	cur.Time = t0.Add(30 * time.Second)
	got := Diff(prev, cur, fakeMap{}, time.Minute)
	k := kinds(got)
	for _, want := range []string{KindNewMAC, KindMACGone, KindMACMoved, KindLinkChange, KindVlanMismatch} {
		if k[want] == 0 {
			t.Errorf("missing %s in %v", want, k)
		}
	}
	if k[KindLinkChange] != 2 {
		t.Errorf("want two link changes: %v", k)
	}
	for _, a := range got {
		if a.Kind == KindVlanMismatch && (a.Port != 4 || a.Suggest["role"] != "cam") {
			t.Errorf("mismatch alert %+v", a)
		}
		if a.Kind == KindMACMoved && (a.Port != 3 || a.Status != StatusOpen) {
			t.Errorf("moved alert %+v", a)
		}
	}
	// first cycle: no edge alerts, but the level condition (VLAN) still fires
	first := Diff(nil, cur, fakeMap{}, time.Minute)
	if k := kinds(first); len(k) != 1 || k[KindVlanMismatch] != 1 {
		t.Errorf("first cycle: %v", k)
	}
	// switch unreadable now: no edge alerts at all
	bad := &Snapshot{Time: cur.Time}
	if len(Diff(cur, bad, fakeMap{}, time.Minute)) != 0 {
		t.Error("unreadable switch must not raise alerts")
	}
}

func TestDedupAndPersistence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "alerts.json")
	a, err := OpenAlerts(p)
	if err != nil {
		t.Fatal(err)
	}
	x := newAlert(time.Now(), "k|1", KindNewMAC, 1, "d", nil)
	if !a.Add(x) || a.Add(x) {
		t.Fatal("second identical alert must be dropped")
	}
	if !a.SetStatus(x.ID, StatusIgnored) || a.SetStatus("nope", StatusIgnored) || a.SetStatus(x.ID, "weird") {
		t.Fatal("set status")
	}
	if a.Add(x) {
		t.Fatal("an ignored alert must not come back")
	}
	if len(a.Open()) != 0 || len(a.All()) != 1 {
		t.Fatal("open/all")
	}
	b, err := OpenAlerts(p)
	if err != nil || len(b.All()) != 1 || b.All()[0].Status != StatusIgnored {
		t.Fatalf("reload: %v %+v", err, b.All())
	}
}

func TestOfflineTimerAndRecovery(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	var up atomic.Bool
	alerts, _ := OpenAlerts("")
	p := NewPoller(Config{
		OfflineAfter: 10 * time.Minute,
		Targets:      func() []string { return []string{"192.0.2.5"} },
		Map:          func() MapView { return fakeMap{} },
		Probe:        func(context.Context, string) bool { return up.Load() },
		Now:          func() time.Time { return now },
		Alerts:       alerts,
	})
	ctx := context.Background()
	p.Cycle(ctx)
	if len(alerts.All()) != 0 {
		t.Fatal("no alert before the timer")
	}
	now = now.Add(9 * time.Minute)
	p.Cycle(ctx)
	if len(alerts.All()) != 0 {
		t.Fatal("no alert at 9 minutes")
	}
	now = now.Add(2 * time.Minute)
	p.Cycle(ctx)
	p.Cycle(ctx) // same episode: still one alert
	as := alerts.All()
	if len(as) != 1 || as[0].Kind != KindDeviceOff || as[0].Port != 5 {
		t.Fatalf("offline alert: %+v", as)
	}
	d := p.Snapshot().Devices[0]
	if d.Online || d.LastSeen != nil {
		t.Fatalf("device %+v", d)
	}
	up.Store(true)
	now = now.Add(time.Minute)
	p.Cycle(ctx)
	d = p.Snapshot().Devices[0]
	if !d.Online || d.LastSeen == nil || !d.LastSeen.Equal(now) {
		t.Fatalf("last seen: %+v", d)
	}
	if kinds(alerts.All())[KindDeviceOn] != 1 {
		t.Fatalf("back-online alert missing: %v", kinds(alerts.All()))
	}
	// a second outage is a new episode and may alert again
	up.Store(false)
	now = now.Add(time.Minute)
	p.Cycle(ctx)
	now = now.Add(11 * time.Minute)
	p.Cycle(ctx)
	if kinds(alerts.All())[KindDeviceOff] != 2 {
		t.Fatalf("second episode: %v", kinds(alerts.All()))
	}
}

func TestPollerSwitchAndConcurrency(t *testing.T) {
	var cur, peak atomic.Int32
	ips := make([]string, 40)
	for i := range ips {
		ips[i] = "192.0.2." + string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	status := "connected"
	p := NewPoller(Config{
		Switch: func(context.Context) (*switchread.Snapshot, error) {
			return &switchread.Snapshot{
				Status: []switchread.IfStatus{{Port: "Gi1/0/1", Status: status, Vlan: "20", Speed: "a-100"}, {Port: "Po1", Status: "connected"}},
				MACs:   []switchread.MACEntry{{Vlan: 20, MAC: "aaaa.0000.0001", Port: "Gi1/0/1"}},
			}, nil
		},
		Targets: func() []string { return ips },
		Probe: func(context.Context, string) bool {
			n := cur.Add(1)
			for {
				o := peak.Load()
				if n <= o || peak.CompareAndSwap(o, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			cur.Add(-1)
			return true
		},
	})
	ch, cancel := p.Subscribe()
	defer cancel()
	p.Cycle(context.Background())
	select {
	case <-ch:
	default:
		t.Fatal("subscriber not signalled")
	}
	if peak.Load() > 16 || peak.Load() < 2 {
		t.Fatalf("probe concurrency %d", peak.Load())
	}
	s := p.Snapshot()
	if !s.SwitchOK || len(s.Ports) != 1 || s.Ports[0].N != 1 || len(s.Ports[0].MACs) != 1 || len(s.Devices) != 40 {
		t.Fatalf("snapshot %+v", s)
	}
	if len(s.MACs) != 1 || s.MACs[0].Port != "Gi1/0/1" {
		t.Fatalf("macs %+v", s.MACs)
	}
}
