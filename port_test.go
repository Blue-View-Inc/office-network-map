package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := OpenStore(t.TempDir(), StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	return s, ts
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func TestRowPorts(t *testing.T) {
	cases := map[string][]int{
		"<b>Gi1/0/1</b>":     {1},
		"<b>Gi1/0/2 + 3</b>": {2, 3},
		"Gi1/0/12–18":        {12, 13, 14, 15, 16, 17, 18},
		"Gi1/0/19-20":        {19, 20},
		"Te1/0/1–2":          nil,
		"something else":     nil,
	}
	for in, want := range cases {
		got := rowPorts(in)
		if len(got) != len(want) {
			t.Errorf("%q: got %v want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q: got %v want %v", in, got, want)
			}
		}
	}
}

func TestPortFormRoundTrip(t *testing.T) {
	s, ts := testServer(t)
	code, body := get(t, ts.URL+"/edit/port/24")
	if code != 200 || !strings.Contains(body, "nas1") || !strings.Contains(body, "<details class=\"adv\">") {
		t.Fatalf("form: %d", code)
	}
	if c, _ := get(t, ts.URL+"/edit/port/49"); c != 404 {
		t.Errorf("port 49 should 404, got %d", c)
	}
	if c, _ := get(t, ts.URL+"/edit/port/te2"); c != 200 {
		t.Errorf("te2 should work, got %d", c)
	}
	form := url.Values{
		"role": {"cam"}, "state": {"used"}, "flag": {"1"},
		"device": {"<b>nas2</b>"}, "ip": {"172.16.1.41"}, "notes": {"access 1 · moved"},
		"label": {"NAS two"}, "sub": {"172.16.1.41"}, "dy": {"60"}, "dx": {"-10"}, "color": {"warn"},
		"summary": {"moved the NAS"},
	}
	resp, err := noRedirect().PostForm(ts.URL+"/edit/port/24", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 303 {
		t.Fatalf("post: %d", resp.StatusCode)
	}
	d, _ := s.st.Load()
	p := d.Switch.Ports[23]
	if p.Role != "cam" || p.State != "used" || !p.Flag {
		t.Errorf("port not updated: %+v", p)
	}
	i := d.Switch.calloutIndex(24)
	if i < 0 || d.Switch.Callouts[i].Label != "NAS two" || d.Switch.Callouts[i].Dy != 60 || d.Switch.Callouts[i].Color != "warn" {
		t.Errorf("callout not updated")
	}
	row := d.PortTable.Rows[func() int { i, _ := d.PortTable.findPortRow(24); return i }()]
	if row[1] != "<b>nas2</b>" || row[2] != "172.16.1.41" || row[3] != "access 1 · moved" || row[0] != "<b>Gi1/0/24</b>" {
		t.Errorf("table row not updated: %v", row)
	}
	cs, _ := s.st.History()
	if cs[0].Subject != "port 24: moved the NAS" {
		t.Errorf("commit subject %q", cs[0].Subject)
	}
	_, page := get(t, ts.URL+"/")
	if !strings.Contains(page, "NAS two") || !strings.Contains(page, "nas2") || !strings.Contains(page, `href="/edit/port/24"`) {
		t.Error("map page not updated")
	}
	out, err := exec.Command("git", "-C", s.st.dir, "log", "-1", "--format=%an <%ae>").Output()
	if err != nil || strings.TrimSpace(string(out)) != "Office Map <office-map@localhost>" {
		t.Errorf("committer %q %v", out, err)
	}

	// clearing the callout label removes the callout
	form.Set("label", "")
	if r, _ := noRedirect().PostForm(ts.URL+"/edit/port/24", form); r.StatusCode != 303 {
		t.Fatalf("post2: %d", r.StatusCode)
	}
	d, _ = s.st.Load()
	if d.Switch.calloutIndex(24) >= 0 {
		t.Error("callout should be gone")
	}
}

func TestPortFormValidation(t *testing.T) {
	s, ts := testServer(t)
	before, _ := s.st.Raw()
	form := url.Values{"role": {"nope"}, "state": {"used"}, "device": {"<script>"}, "label": {"x"}, "dy": {"9999"}, "dx": {"0"}}
	resp, err := noRedirect().PostForm(ts.URL+"/edit/port/5", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 422 {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
	after, _ := s.st.Raw()
	if string(before) != string(after) {
		t.Error("invalid form changed the data")
	}
}

func TestPortTableGroupAndNewRows(t *testing.T) {
	d := loadSeed(t)
	tb := &d.PortTable
	// unchanged values never touch the table
	n0 := len(tb.Rows)
	tb.setPortCells(12, tb.cellsFor(12), tb.cellsFor(12))
	if len(tb.Rows) != n0 {
		t.Fatal("unchanged edit changed table")
	}
	// port 12 lives only in the "12–18" group row: a real change creates its own row, in order
	old := tb.cellsFor(12)
	tb.setPortCells(12, old, portCells{"cam-12", "172.16.3.12", "access 30"})
	if len(tb.Rows) != n0+1 {
		t.Fatalf("expected one new row, have %d (was %d)", len(tb.Rows), n0)
	}
	i, exact := tb.findPortRow(12)
	if !exact || tb.Rows[i][0] != "<b>Gi1/0/12</b>" || tb.Rows[i][1] != "cam-12" {
		t.Errorf("new row wrong: %v exact=%v", tb.Rows[i], exact)
	}
	if prev := tb.Rows[i-1][0]; !strings.Contains(prev, "Gi1/0/11") {
		t.Errorf("new row not in port order, previous is %q", prev)
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	// the group row is left alone
	if g, _ := tb.findPortRow(13); !strings.Contains(tb.Rows[g][0], "12–18") {
		t.Error("group row should still cover 13")
	}
}

func TestTePortLabel(t *testing.T) {
	s, ts := testServer(t)
	resp, err := noRedirect().PostForm(ts.URL+"/edit/port/te1", url.Values{"te": {"Te1 uplink"}, "summary": {"renamed"}})
	if err != nil || resp.StatusCode != 303 {
		t.Fatalf("%v %v", err, resp)
	}
	d, _ := s.st.Load()
	if d.Switch.TeLabels[0] != "Te1 uplink" {
		t.Error("label not saved")
	}
	cs, _ := s.st.History()
	if cs[0].Subject != "port te1: renamed" {
		t.Error(cs[0].Subject)
	}
}

func TestSeedFlag(t *testing.T) {
	d := loadSeed(t)
	d.Header.Title = "Seeded office"
	st, err := OpenStore(t.TempDir(), StoreOptions{Seed: d.marshal(), GitName: "Tester", GitEmail: "t@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load()
	if got.Header.Title != "Seeded office" {
		t.Error("seed ignored")
	}
	if _, err := OpenStore(t.TempDir(), StoreOptions{Seed: []byte("{}")}); err == nil {
		t.Error("invalid seed should fail")
	}
}
