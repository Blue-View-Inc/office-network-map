package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"officemap/switchread"
)

// fixtureSwitch serves the synthetic fixtures through the same interface the SSH client implements.
type fixtureSwitch struct {
	miss []string
	err  error
}

func (f fixtureSwitch) Missing() []string { return f.miss }
func (f fixtureSwitch) Fetch(context.Context) (*switchread.Snapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	rd := func(n string) string {
		b, err := os.ReadFile("switchread/testdata/" + n)
		if err != nil {
			panic(err)
		}
		return string(b)
	}
	return &switchread.Snapshot{
		Status: switchread.ParseInterfacesStatus(rd("show_interfaces_status.txt")),
		VLANs:  switchread.ParseVLANBrief(rd("show_vlan_brief.txt")),
		Trunks: switchread.ParseTrunks(rd("show_interfaces_trunk.txt")),
		MACs:   switchread.ParseMACTable(rd("show_mac_address-table_dynamic.txt")),
	}, nil
}

func switchServer(t *testing.T, sw switchSource) (*Server, *httptest.Server) {
	t.Helper()
	s, _ := testServer(t)
	s.sw = sw
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestSwitchNotConfigured(t *testing.T) {
	_, ts := testServer(t)
	code, body := get(t, ts.URL+"/switch")
	if code != 200 || !strings.Contains(body, "Switch not configured") || !strings.Contains(body, "-switch-known-hosts") {
		t.Fatalf("%d %.200s", code, body)
	}
	_, ts2 := switchServer(t, fixtureSwitch{miss: []string{"-switch-key"}})
	_, body = get(t, ts2.URL+"/switch")
	if !strings.Contains(body, "Switch not configured") || !strings.Contains(body, "<strong>missing</strong>") {
		t.Error("partially configured should list what is missing")
	}
	// the sync endpoint is equally closed
	resp, _ := http.PostForm(ts.URL+"/switch/sync", url.Values{"port": {"25"}})
	if resp.StatusCode != 200 { // same friendly page, no change made
		t.Errorf("sync unconfigured: %d", resp.StatusCode)
	}
}

func TestSwitchLinkOnMap(t *testing.T) {
	_, ts := testServer(t)
	_, body := get(t, ts.URL+"/")
	if !strings.Contains(body, `href="/switch">Switch</a>`) || !strings.Contains(body, `href="/history">History</a>`) {
		t.Error("map page should link to History and Switch")
	}
}

func TestCompare(t *testing.T) {
	d := loadSeed(t)
	snap, _ := fixtureSwitch{}.Fetch(context.Background())
	cmp := compareSwitch(d, snap)
	if len(cmp) != 48 {
		t.Fatal(len(cmp))
	}
	p25, p22, p11, p1 := cmp[24], cmp[21], cmp[10], cmp[0]
	if !p25.DiffState || p25.WantState != "used" || p25.Switch.Name != "ex-laptop" || len(p25.MACs) != 2 {
		t.Errorf("port 25: %+v", p25)
	}
	if !p22.DiffState || p22.WantState != "free" {
		t.Errorf("port 22: %+v", p22)
	}
	if p11.DiffState || p11.DiffLane || p11.SuggestLn != "cam" {
		t.Errorf("port 11 should agree: %+v", p11)
	}
	if p1.SuggestLn != "" || p1.DiffLane || !p1.Present {
		t.Errorf("trunk port gets no lane suggestion: %+v", p1)
	}
	if got := d.laneForVlan("50"); got != "" {
		t.Errorf("VLAN 50 is claimed by two lanes, want no suggestion, got %q", got)
	}
	if got := d.laneForVlan("30"); got != "cam" {
		t.Errorf("VLAN 30: %q", got)
	}
	if got := d.laneForVlan("1"); got != "" {
		t.Errorf("VLAN 1 must not match 172.16.1.x: %q", got)
	}
}

func TestSwitchPageAndSync(t *testing.T) {
	s, ts := switchServer(t, fixtureSwitch{})
	code, body := get(t, ts.URL+"/switch")
	if code != 200 || !strings.Contains(body, "Map vs switch") || !strings.Contains(body, `value="25"`) || !strings.Contains(body, "0200.5e00.0025") {
		t.Fatalf("%d", code)
	}
	if strings.Contains(body, `name="port" value="11"`) {
		t.Error("agreeing port should have no checkbox")
	}
	// nothing ticked
	resp, _ := noRedirect().PostForm(ts.URL+"/switch/sync", url.Values{})
	if resp.StatusCode != 422 {
		t.Errorf("empty sync: %d", resp.StatusCode)
	}
	if resp, _ = noRedirect().PostForm(ts.URL+"/switch/sync", url.Values{"port": {"99"}}); resp.StatusCode != 400 {
		t.Errorf("bad port: %d", resp.StatusCode)
	}
	before, _ := s.st.History()
	resp, err := noRedirect().PostForm(ts.URL+"/switch/sync", url.Values{"port": {"25", "22"}, "summary": {"audit"}})
	if err != nil || resp.StatusCode != 303 {
		t.Fatalf("sync: %v %v", err, resp)
	}
	d, _ := s.st.Load()
	if d.Switch.Ports[24].State != "used" || d.Switch.Ports[21].State != "free" {
		t.Errorf("states: %s %s", d.Switch.Ports[24].State, d.Switch.Ports[21].State)
	}
	if c := d.PortTable.cellsFor(25); c.Device != "ex-laptop" {
		t.Errorf("table device for 25: %q", c.Device)
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Error(errs)
	}
	after, _ := s.st.History()
	if len(after) != len(before)+1 || after[0].Subject != "switch sync: ports 22, 25 (audit)" {
		t.Errorf("commit: %v", after[0])
	}
	// now they agree, so the page offers nothing for them
	_, body = get(t, ts.URL+"/switch")
	if strings.Contains(body, `name="port" value="25"`) || strings.Contains(body, `name="port" value="22"`) {
		t.Error("synced ports should no longer differ")
	}
}

func TestSwitchReadError(t *testing.T) {
	_, ts := switchServer(t, fixtureSwitch{err: errors.New("boom <b>")})
	code, body := get(t, ts.URL+"/switch")
	if code != http.StatusBadGateway || !strings.Contains(body, "boom &lt;b&gt;") {
		t.Errorf("%d %s", code, body)
	}
}
