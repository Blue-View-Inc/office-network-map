package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"officemap/live"
	"officemap/switchread"
)

// liveServer is a test server with a poller whose probe and switch reads are fakes.
func liveServer(t *testing.T, probe func(string) bool, sw func() *switchread.Snapshot) (*Server, *httptest.Server) {
	t.Helper()
	s, _ := testServer(t)
	al, _ := live.OpenAlerts(filepath.Join(t.TempDir(), "alerts.json"))
	s.alerts = al
	cfg := live.Config{
		OfflineAfter: time.Nanosecond,
		Probe:        func(_ context.Context, ip string) bool { return probe(ip) },
		Targets: func() []string {
			d, _ := s.st.Load()
			return liveTargets(d)
		},
		Map:    func() live.MapView { d, _ := s.st.Load(); return mapView{d} },
		Alerts: al,
	}
	if sw != nil {
		cfg.Switch = func(context.Context) (*switchread.Snapshot, error) { return sw(), nil }
	}
	s.lv = live.NewPoller(cfg)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestLiveTargetsFromSample(t *testing.T) {
	d := loadSeed(t)
	ips := liveTargets(d)
	if len(ips) < 5 {
		t.Fatalf("too few targets: %v", ips)
	}
	found := false
	for _, ip := range ips {
		if ip == "172.16.0.1" {
			found = true
		}
	}
	if !found {
		t.Errorf("fw address missing: %v", ips)
	}
}

func TestLiveJSONAndPageBadge(t *testing.T) {
	s, ts := liveServer(t, func(string) bool { return false }, nil)
	s.lv.Cycle(context.Background())
	code, body := get(t, ts.URL+"/live.json")
	if code != 200 {
		t.Fatal(code)
	}
	var v struct {
		Enabled bool
		Devices []struct{ IP string }
		Alerts  []live.Alert
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil || !v.Enabled || len(v.Devices) == 0 {
		t.Fatalf("live.json: %v %.200s", err, body)
	}
	_, page := get(t, ts.URL+"/")
	if !strings.Contains(page, `data-live="1"`) || !strings.Contains(page, `id="om-dlg"`) {
		t.Error("live badge / dialog missing from the map page")
	}
	_, off := get(t, func() string { _, o := testServer(t); return o.URL }()+"/")
	if !strings.Contains(off, `data-live="0"`) {
		t.Error("badge should say live off")
	}
	// never on history or print views
	cs, _ := s.st.History()
	_, hv := get(t, ts.URL+"/history/"+cs[0].Hash)
	if strings.Contains(hv, `id="om-dlg"`) || strings.Contains(hv, "<script>") || strings.Contains(PrintHTML(loadSeed(t)), "om-badge") {
		t.Error("live UI leaked into history/print")
	}
}

func TestEventsStreamsSnapshots(t *testing.T) {
	s, ts := liveServer(t, func(string) bool { return true }, nil)
	s.lv.Cycle(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	rd := bufio.NewReader(resp.Body)
	readData := func() string {
		for {
			ln, err := rd.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(ln, "data: ") {
				return strings.TrimPrefix(ln, "data: ")
			}
		}
	}
	var v liveView
	if err := json.Unmarshal([]byte(readData()), &v); err != nil || !v.Enabled || len(v.Devices) == 0 {
		t.Fatalf("first event: %v", err)
	}
	first := v.Time
	time.Sleep(10 * time.Millisecond)
	s.lv.Cycle(ctx) // a new cycle pushes a new event
	var v2 liveView
	if err := json.Unmarshal([]byte(readData()), &v2); err != nil || !v2.Time.After(first) {
		t.Fatalf("second event: %v %v %v", err, v2.Time, first)
	}
}

func TestEventsNotFoundWithoutLive(t *testing.T) {
	_, ts := testServer(t)
	if c, _ := get(t, ts.URL+"/events"); c != 404 {
		t.Errorf("/events without -live: %d", c)
	}
	c, body := get(t, ts.URL+"/live.json")
	if c != 200 || !strings.Contains(body, `"enabled":false`) {
		t.Errorf("live.json without live: %d %s", c, body)
	}
}

func TestOfflineAlertsEndpointsAndPrefill(t *testing.T) {
	s, ts := liveServer(t, func(string) bool { return false }, nil)
	s.lv.Cycle(context.Background())
	time.Sleep(2 * time.Millisecond)
	s.lv.Cycle(context.Background())
	_, body := get(t, ts.URL+"/alerts")
	var as []live.Alert
	if err := json.Unmarshal([]byte(body), &as); err != nil || len(as) == 0 {
		t.Fatalf("alerts: %v %s", err, body)
	}
	var withPort *live.Alert
	for i := range as {
		if as[i].Kind == live.KindDeviceOff && as[i].Port > 0 {
			withPort = &as[i]
		}
	}
	if withPort == nil {
		t.Fatalf("no offline alert mapped to a port: %v", as)
	}
	n := len(as)
	s.lv.Cycle(context.Background())
	_, body = get(t, ts.URL+"/alerts")
	json.Unmarshal([]byte(body), &as)
	if len(as) != n {
		t.Errorf("repeat cycles duplicated alerts: %d -> %d", n, len(as))
	}
	// ignore
	resp, err := http.Post(ts.URL+"/alerts/"+as[0].ID+"/ignore", "", nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("ignore: %v %v", err, resp)
	}
	if a, _ := s.alerts.Get(as[0].ID); a.Status != live.StatusIgnored {
		t.Error("not ignored")
	}
	if r, _ := http.Post(ts.URL+"/alerts/nope/ignore", "", nil); r.StatusCode != 404 {
		t.Error("unknown id should 404")
	}

	// a VLAN alert pre-fills the port form but changes nothing until saved
	a := live.Alert{ID: "abc", Key: "k", Kind: live.KindVlanMismatch, Port: 6, Status: live.StatusOpen,
		Suggest: map[string]string{"role": "cam", "device": "New device (aaaa.bbbb.cccc)"}}
	s.alerts.Add(a)
	before, _ := s.st.Raw()
	_, form := get(t, ts.URL+"/edit/port/6?fragment=1&alert=abc")
	if !strings.Contains(form, `<option value="cam" selected>`) || !strings.Contains(form, `name="alert" value="abc"`) {
		t.Errorf("prefill missing: %.600s", form)
	}
	after, _ := s.st.Raw()
	if string(before) != string(after) {
		t.Error("prefill must not change the map")
	}
	// saving through the dialog flow commits and marks the alert accepted
	f := url.Values{"role": {"cam"}, "state": {"used"}, "device": {"dev6"}, "alert": {"abc"}, "summary": {"from alert"}}
	r, err := http.PostForm(ts.URL+"/edit/port/6?fragment=1", f)
	if err != nil || r.StatusCode != 204 {
		t.Fatalf("dialog save: %v %v", err, r)
	}
	if x, _ := s.alerts.Get("abc"); x.Status != live.StatusAccepted {
		t.Error("alert should be accepted after the save")
	}
}

func TestSwitchAlertsFromFakeReads(t *testing.T) {
	step := 0
	read := func() *switchread.Snapshot {
		st := []switchread.IfStatus{{Port: "Gi1/0/6", Status: "connected", Vlan: "30", Speed: "a-100"}}
		macs := []switchread.MACEntry{{Vlan: 30, MAC: "aaaa.0000.0001", Port: "Gi1/0/6"}}
		if step > 0 {
			macs = append(macs, switchread.MACEntry{Vlan: 30, MAC: "aaaa.0000.0002", Port: "Gi1/0/6"})
		}
		return &switchread.Snapshot{Status: st, MACs: macs}
	}
	s, ts := liveServer(t, func(string) bool { return true }, read)
	s.lv.Cycle(context.Background())
	step = 1
	s.lv.Cycle(context.Background())
	k := map[string]bool{}
	for _, a := range s.alerts.All() {
		k[a.Kind] = true
	}
	if !k[live.KindNewMAC] {
		t.Errorf("new MAC alert missing: %v", k)
	}
	_, body := get(t, ts.URL+"/live.json")
	if !strings.Contains(body, `"slow"`) {
		t.Errorf("port view lacks slow flag: %.300s", body)
	}
}

func TestFragmentEndpoints(t *testing.T) {
	_, ts := testServer(t)
	_, frag := get(t, ts.URL+"/edit/port/24?fragment=1")
	if strings.Contains(frag, "<html") || strings.Contains(frag, "Back to the map") || !strings.Contains(frag, `<form method="post" action="/edit/port/24"`) {
		t.Errorf("port fragment wrong: %.300s", frag)
	}
	_, full := get(t, ts.URL+"/edit/port/24")
	if !strings.Contains(full, "<html") || !strings.Contains(full, "Back to the map") {
		t.Error("full page must still work without JS")
	}
	_, mf := get(t, ts.URL+"/?fragment=1")
	if strings.Contains(mf, "<style") || strings.Contains(mf, "<header>") || !strings.Contains(mf, "<svg") || !strings.Contains(mf, `/edit/port/24`) {
		t.Errorf("map fragment wrong: %.200s", mf)
	}
}

func TestDialogPostValidationReturnsFragment(t *testing.T) {
	s, ts := testServer(t)
	before, _ := s.st.Raw()
	f := url.Values{"role": {"nope"}, "state": {"used"}, "device": {"x"}, "label": {"x"}, "dy": {"9999"}, "dx": {"0"}, "summary": {"keep me"}}
	resp, err := http.PostForm(ts.URL+"/edit/port/5?fragment=1", f)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		sb.WriteString(sc.Text())
	}
	b := sb.String()
	if resp.StatusCode != 422 || !strings.Contains(b, "Not saved") || strings.Contains(b, "<html") || !strings.Contains(b, `value="keep me"`) {
		t.Fatalf("%d %.400s", resp.StatusCode, b)
	}
	after, _ := s.st.Raw()
	if string(before) != string(after) {
		t.Error("invalid dialog post changed the data")
	}
}

func TestThemeServing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b.css"), []byte(":root{--om-accent:#000}"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.css"), []byte("/*a*/"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.woff2"), []byte("w"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("no"), 0o644)
	themeDir = dir
	t.Cleanup(func() { themeDir = "" })
	_, ts := testServer(t)
	_, page := get(t, ts.URL+"/")
	ia, ib := strings.Index(page, `href="/theme/a.css"`), strings.Index(page, `href="/theme/b.css"`)
	if ia < 0 || ib < ia || ia < strings.Index(page, "</style>") {
		t.Errorf("theme links missing, unsorted or before built-ins")
	}
	for _, p := range []string{"/edit/port/3", "/edit/header", "/history", "/switch"} {
		if _, b := get(t, ts.URL+p); !strings.Contains(b, `/theme/a.css`) {
			t.Errorf("%s lacks theme link", p)
		}
	}
	if c, b := get(t, ts.URL+"/theme/b.css"); c != 200 || !strings.Contains(b, "--om-accent") {
		t.Error("css not served")
	}
	if c, _ := get(t, ts.URL+"/theme/x.woff2"); c != 200 {
		t.Error("font not served")
	}
	for _, p := range []string{"/theme/secret.txt", "/theme/..%2Fgo.mod", "/theme/nope.css"} {
		if c, _ := get(t, ts.URL+p); c != 404 {
			t.Errorf("%s: %d", p, c)
		}
	}
	if !strings.Contains(PrintHTML(loadSeed(t)), `/theme/a.css`) {
		t.Error("print html lacks theme")
	}
}
