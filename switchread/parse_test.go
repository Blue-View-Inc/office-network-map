package switchread

import (
	"os"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseInterfacesStatus(t *testing.T) {
	st := ParseInterfacesStatus(fixture(t, "show_interfaces_status.txt"))
	if len(st) != 48+1+2+3+1 {
		t.Fatalf("got %d interfaces", len(st))
	}
	by := map[string]IfStatus{}
	for _, s := range st {
		by[s.Port] = s
	}
	g1 := by["Gi1/0/1"]
	if g1.Name != "fw1 LAN trunk" || g1.Status != "connected" || g1.Vlan != "trunk" || g1.Duplex != "a-full" || g1.Speed != "a-1000" || g1.Type != "10/100/1000BaseTX" || !g1.Connected() {
		t.Errorf("Gi1/0/1: %+v", g1)
	}
	if g := by["Gi1/0/22"]; g.Name != "" || g.Status != "notconnect" || g.Vlan != "20" || g.Connected() {
		t.Errorf("Gi1/0/22 (empty description): %+v", g)
	}
	if g := by["Gi1/0/5"]; g.Speed != "a-100" || g.Status != "suspended" {
		t.Errorf("Gi1/0/5: %+v", g)
	}
	if g := by["Gi1/0/49"]; g.Type != "1000BaseSX SFP" {
		t.Errorf("SFP type: %+v", g)
	}
	if g := by["Te1/0/1"]; g.Status != "disabled" || g.Name != "reserved 10G" || g.Type != "SFP-10GBase-SR" {
		t.Errorf("Te1/0/1: %+v", g)
	}
	if g := by["Po1"]; g.Status != "notconnect" || g.Type != "" {
		t.Errorf("Po1: %+v", g)
	}
	if g := by["Fa0"]; g.Vlan != "routed" || g.Name != "Management" {
		t.Errorf("Fa0: %+v", g)
	}
	// a description exactly as wide as the column must not bleed into the status
	if g := by["Gi1/0/44"]; g.Name != "BMC lane 50: host" || g.Status != "notconnect" {
		t.Errorf("truncated description: %+v", g)
	}
}

func TestParseVLANBrief(t *testing.T) {
	vs := ParseVLANBrief(fixture(t, "show_vlan_brief.txt"))
	by := map[int]VLAN{}
	for _, v := range vs {
		by[v.ID] = v
	}
	if len(vs) != 14+4 {
		t.Fatalf("got %d vlans", len(vs))
	}
	v1 := by[1]
	if v1.Name != "default" || v1.Status != "active" || len(v1.Ports) != 21 {
		t.Errorf("vlan 1: %+v", v1)
	}
	if v1.Ports[0] != "Gi1/0/24" || v1.Ports[len(v1.Ports)-1] != "Gi1/0/49" {
		t.Errorf("wrapped ports lost: %v", v1.Ports)
	}
	if v := by[200]; v.Name != "Sandbox" || len(v.Ports) != 0 {
		t.Errorf("vlan 200: %+v", v)
	}
	if v := by[1002]; v.Status != "act/unsup" || v.Name != "fddi-default" {
		t.Errorf("vlan 1002: %+v", v)
	}
	if v := by[30]; len(v.Ports) != 8 || v.Ports[0] != "Gi1/0/11" {
		t.Errorf("vlan 30: %+v", v)
	}
}

func TestParseTrunks(t *testing.T) {
	ts := ParseTrunks(fixture(t, "show_interfaces_trunk.txt"))
	if len(ts) != 4 {
		t.Fatalf("got %d trunks", len(ts))
	}
	if g := ts[0]; g.Port != "Gi1/0/1" || g.Mode != "on" || g.Encap != "802.1q" || g.Status != "trunking" || g.Native != "1" ||
		g.Allowed != "1,10-106,90,110-204,200" || g.Active != g.Allowed || g.Forwarding != g.Allowed {
		t.Errorf("Gi1/0/1: %+v", g)
	}
	if g := ts[1]; g.Native != "50" || g.Allowed != "50-106,90" {
		t.Errorf("Gi1/0/8: %+v", g)
	}
	if g := ts[3]; g.Allowed != "1-4094" || g.Active != "1,99-106,90,110-204,200" {
		t.Errorf("Gi1/0/36: %+v", g)
	}
}

func TestParseTrunksWrappedList(t *testing.T) {
	out := "Port Mode Encapsulation Status Native vlan\nGi1/0/1 on 802.1q trunking 1\n\nPort Vlans allowed on trunk\nGi1/0/1 1,2,3,\n        4,5\n"
	ts := ParseTrunks(out)
	if len(ts) != 1 || ts[0].Allowed != "1,2,3,4,5" {
		t.Errorf("%+v", ts)
	}
}

func TestParseMACTable(t *testing.T) {
	ms := ParseMACTable(fixture(t, "show_mac_address-table_dynamic.txt"))
	if len(ms) != 22 {
		t.Fatalf("got %d macs", len(ms))
	}
	if m := ms[0]; m.Vlan != 1 || m.MAC != "0200.5e00.0001" || m.Type != "DYNAMIC" || m.Port != "Gi1/0/1" {
		t.Errorf("first: %+v", m)
	}
	snap := &Snapshot{MACs: ms}
	by := snap.MACsByPort()
	if len(by["Gi1/0/1"]) != 1 { // same MAC on four VLANs counts once
		t.Errorf("Gi1/0/1: %v", by["Gi1/0/1"])
	}
	if len(by["Gi1/0/25"]) != 2 || len(by["Gi1/0/36"]) != 2 {
		t.Errorf("macs by port: %v", by)
	}
}

func TestParsersTolerateShellNoise(t *testing.T) {
	out := "sw1#show interfaces status\r\n\r\nPort      Name               Status       Vlan       Duplex  Speed Type \r\nGi1/0/1   uplink             connected    trunk      a-full a-1000 10/100/1000BaseTX\r\n\r\nsw1#"
	st := ParseInterfacesStatus(out)
	if len(st) != 1 || st[0].Name != "uplink" {
		t.Errorf("%+v", st)
	}
	if got := ParseMACTable("garbage\n\nsw1#"); len(got) != 0 {
		t.Error(got)
	}
	if got := ParseVLANBrief(""); len(got) != 0 {
		t.Error(got)
	}
}

func TestPortNumber(t *testing.T) {
	cases := []struct {
		in    string
		n, te int
		ok    bool
	}{
		{"Gi1/0/1", 1, 0, true}, {"Gi1/0/48", 48, 0, true}, {"Gi1/0/49", 0, 0, false},
		{"Te1/0/1", 0, 1, true}, {"Te1/0/2", 0, 2, true}, {"Te1/0/3", 0, 0, false},
		{"Po1", 0, 0, false}, {"Fa0", 0, 0, false}, {"Gi1/0/x", 0, 0, false},
	}
	for _, c := range cases {
		n, te, ok := PortNumber(c.in)
		if n != c.n || te != c.te || ok != c.ok {
			t.Errorf("%s: got %d %d %v", c.in, n, te, ok)
		}
	}
}
