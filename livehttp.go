package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"officemap/live"
)

// mapView adapts the map data to what the live poller compares against.
type mapView struct{ d *Data }

func (m mapView) Lane(n int) string {
	if n < 1 || n > len(m.d.Switch.Ports) {
		return ""
	}
	return m.d.Switch.Ports[n-1].Role
}
func (m mapView) Expects(n int) bool {
	return n >= 1 && n <= len(m.d.Switch.Ports) && m.d.Switch.Ports[n-1].State == "used"
}
func (m mapView) LaneForVlan(v string) string { return m.d.laneForVlan(v) }
func (m mapView) PortForIP(ip string) int {
	for _, r := range m.d.PortTable.Rows {
		if len(r) > 2 && strings.Contains(r[2], ip) {
			if ps := rowPorts(r[0]); len(ps) > 0 {
				return ps[0]
			}
		}
	}
	return 0
}

// colIndex finds the table column whose header mentions one of the words.
func colIndex(t *Table, fallback int, words ...string) int {
	for i, h := range t.Headers {
		l := strings.ToLower(h)
		for _, w := range words {
			if strings.Contains(l, w) {
				return i
			}
		}
	}
	return fallback
}

// liveTargets lists the IPv4 addresses in the port table and devices table address columns.
func liveTargets(d *Data) []string {
	var cells []string
	for _, t := range []*Table{&d.PortTable, &d.Devices} {
		c := colIndex(t, 1, "address", "ip")
		for _, r := range t.Rows {
			if c < len(r) {
				cells = append(cells, r[c])
			}
		}
	}
	return live.ExtractIPs(cells...)
}

type vport struct {
	live.Port
	Lane     string `json:"lane"`
	Expects  bool   `json:"expects"`
	Slow     bool   `json:"slow"`
	Mismatch bool   `json:"mismatch"`
	Suggest  string `json:"suggest,omitempty"`
}

// liveView is the JSON the page and /events work from: the snapshot plus what the current map says.
type liveView struct {
	live.Snapshot
	Ports  []vport      `json:"ports"`
	Alerts []live.Alert `json:"alerts"`
	Now    time.Time    `json:"now"`
}

func (s *Server) liveView() (*liveView, error) {
	if s.lv == nil {
		return &liveView{Snapshot: live.Snapshot{}, Ports: []vport{}, Alerts: []live.Alert{}, Now: time.Now()}, nil
	}
	d, err := s.st.Load()
	if err != nil {
		return nil, err
	}
	snap := s.lv.Snapshot()
	m := mapView{d}
	v := &liveView{Snapshot: *snap, Ports: []vport{}, Alerts: []live.Alert{}, Now: time.Now()}
	if snap.Devices == nil {
		v.Devices = []live.Device{}
	}
	for _, p := range snap.Ports {
		vp := vport{Port: p, Lane: m.Lane(p.N), Expects: m.Expects(p.N)}
		if mb, ok := live.SpeedMbps(p.Speed); ok && p.Status == "connected" && vp.Expects && mb < 1000 {
			vp.Slow = true
		}
		if p.Status == "connected" && p.Vlan != "trunk" && p.Vlan != "routed" {
			if want := m.LaneForVlan(p.Vlan); want != "" && want != vp.Lane {
				vp.Mismatch, vp.Suggest = true, want
			}
		}
		v.Ports = append(v.Ports, vp)
	}
	if s.alerts != nil {
		v.Alerts = append(v.Alerts, s.alerts.Open()...)
	}
	return v, nil
}

func (s *Server) liveJSON(w http.ResponseWriter, r *http.Request) {
	v, err := s.liveView()
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

// events streams the live view as Server-Sent Events: once on connect, then after every cycle.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if s.lv == nil || !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.lv.Subscribe()
	defer cancel()
	send := func() bool {
		v, err := s.liveView()
		if err != nil {
			return false
		}
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", b)
		fl.Flush()
		return true
	}
	if !send() {
		return
	}
	keep := time.NewTicker(25 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if !send() {
				return
			}
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) alertsJSON(w http.ResponseWriter, r *http.Request) {
	out := []live.Alert{}
	if s.alerts != nil {
		out = s.alerts.All()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) alertAct(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if s.alerts == nil || !s.alerts.SetStatus(id, status) {
			http.NotFound(w, r)
			return
		}
		a, _ := s.alerts.Get(id)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "status": status, "port": a.Port})
	}
}

// prefillFromAlert overlays an alert's suggested values on the port form. The suggestion is only
// shown; nothing changes until the form is saved through the normal commit flow.
func (s *Server) prefillFromAlert(f *portForm, id string) {
	if s.alerts == nil || id == "" {
		return
	}
	a, ok := s.alerts.Get(id)
	if !ok {
		return
	}
	if v := a.Suggest["role"]; v != "" {
		f.Role = v
	}
	if v := a.Suggest["state"]; v != "" {
		f.State = v
	}
	if v := a.Suggest["device"]; v != "" && strings.TrimSpace(f.Cells.Device) == "" {
		f.Cells.Device = esc(v)
	}
	if v := a.Suggest["ip"]; v != "" {
		f.Cells.IP = esc(v)
	}
	if v := a.Suggest["notes"]; v != "" {
		f.Cells.Notes = esc(v)
	}
}

// startLive runs the poller until ctx ends.
func (s *Server) startLive(ctx context.Context, cfg live.Config) {
	cfg.Targets = func() []string {
		d, err := s.st.Load()
		if err != nil {
			return nil
		}
		return liveTargets(d)
	}
	cfg.Map = func() live.MapView {
		d, err := s.st.Load()
		if err != nil {
			return nil
		}
		return mapView{d}
	}
	if s.sw != nil && s.switchConfigured() {
		cfg.Switch = s.sw.Fetch
	}
	cfg.Alerts = s.alerts
	s.lv = live.NewPoller(cfg)
	go s.lv.Run(ctx)
}
