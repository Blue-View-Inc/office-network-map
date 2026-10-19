package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// builtinLanes names the standard palette keys when the diagram legend does not.
var builtinLanes = map[string]string{
	"ap": "Wi-Fi access points", "cam": "Cameras", "desk": "Desktops", "dmz": "DMZ",
	"fw": "Firewall", "legacy": "Legacy network", "media": "Media", "mgmt": "Management",
	"pve": "Servers", "vmw": "Hypervisor trunk", "vpn": "Remote access", "wan": "Internet / WAN",
	"warn": "Warning",
}

// laneName is the readable name of a palette key: the switch legend text if the legend
// has one, else a built-in name, else the key itself.
func (d *Data) laneName(k string) string {
	for _, l := range d.Switch.Legend {
		if l.Color == k && strings.TrimSpace(l.Text) != "" {
			return l.Text
		}
	}
	if n, ok := builtinLanes[k]; ok {
		return n
	}
	return k
}

type laneOpt struct{ Key, Name string }

func (d *Data) laneOpts() []laneOpt {
	ks := make([]string, 0, len(d.Palette))
	for k := range d.Palette {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	out := make([]laneOpt, len(ks))
	for i, k := range ks {
		out[i] = laneOpt{k, d.laneName(k)}
	}
	return out
}

func laneSelect(name, val string, opts []laneOpt, blank string) string {
	s := `<select name="` + esc(name) + `">`
	if blank != "" {
		s += `<option value="">` + esc(blank) + `</option>`
	}
	for _, o := range opts {
		sel := ""
		if o.Key == val {
			sel = " selected"
		}
		s += `<option value="` + esc(o.Key) + `"` + sel + `>` + esc(o.Name+" ("+o.Key+")") + `</option>`
	}
	return s + `</select>`
}

// calloutIndex finds the first callout on a port, or -1.
func (sw *Switch) calloutIndex(n int) int {
	for i, c := range sw.Callouts {
		if c.Port == n {
			return i
		}
	}
	return -1
}

// portForm is everything the compact port form edits.
type portForm struct {
	Role, State string
	Flag        bool
	Cells       portCells
	Callout     Callout
}

func portFormFor(d *Data, n int) portForm {
	p := d.Switch.Ports[n-1]
	f := portForm{Role: p.Role, State: p.State, Flag: p.Flag, Cells: d.PortTable.cellsFor(n)}
	if i := d.Switch.calloutIndex(n); i >= 0 {
		f.Callout = d.Switch.Callouts[i]
	}
	return f
}

// parsePortNum accepts 1-48; (0, "te1"/"te2") for the 10G labels.
func parsePortNum(s string) (n int, te int, ok bool) {
	switch s {
	case "te1":
		return 0, 1, true
	case "te2":
		return 0, 2, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 48 || strconv.Itoa(n) != s {
		return 0, 0, false
	}
	return n, 0, true
}

func (s *Server) renderPort(w http.ResponseWriter, code int, n, te int, d *Data, f portForm, errs []string, summary string) {
	b := &formB{d: d}
	name := strconv.Itoa(n)
	title := "Port " + name
	if te > 0 {
		name = fmt.Sprintf("te%d", te)
		title = d.Switch.TeLabels[te-1]
		if title == "" {
			title = "10G port " + strconv.Itoa(te)
		}
	}
	b.w(`<header><a href="/">&larr; Back to the map</a><h1>` + esc(title) + `</h1>`)
	if te == 0 {
		prev, next := "", ""
		if n > 1 {
			prev = `<a href="/edit/port/` + strconv.Itoa(n-1) + `">&larr; port ` + strconv.Itoa(n-1) + `</a>`
		}
		if n < 48 {
			next = `<a href="/edit/port/` + strconv.Itoa(n+1) + `">port ` + strconv.Itoa(n+1) + ` &rarr;</a>`
		}
		b.w(`<p class="muted pn">` + prev + ` ` + next + `</p>`)
	}
	b.w(`</header>`)
	if len(errs) > 0 {
		b.w(`<div class="err"><strong>Not saved. Please fix:</strong><ul>`)
		for _, e := range errs {
			b.w("<li>" + esc(e) + "</li>")
		}
		b.w("</ul></div>")
	}
	b.w(`<form method="post" action="/edit/port/` + name + `" class="pf">`)
	if te > 0 {
		b.text("Label shown on the diagram", "te", d.Switch.TeLabels[te-1])
	} else {
		lanes := d.laneOpts()
		b.w(`<div class="rf">`)
		b.field("Lane / role", laneSelect("role", f.Role, lanes, ""))
		b.field("State", selectBox("state", f.State, []string{"used", "reserved", "free"}, false))
		b.w(`</div>`)
		b.w(`<label class="chk big"><span>Needs attention (orange !)</span>` + checkbox("flag", f.Flag) + `</label>`)
		b.group("What is plugged in")
		b.area("Device", "device", f.Cells.Device, 2)
		b.area("Address", "ip", f.Cells.IP, 2)
		b.area("Notes (VLAN / mode)", "notes", f.Cells.Notes, 2)
		b.w(`<p class="muted">These three fill this port's row in the port table. Cells may use <code>&lt;b&gt;</code>, <code>&lt;i&gt;</code> and <code>&lt;code&gt;</code>.</p>`)
		b.group("Callout on the diagram")
		b.text("Label (leave empty for no callout)", "label", f.Callout.Label)
		b.text("Detail line", "sub", f.Callout.Sub)
		b.w(`<details class="adv"><summary>Advanced</summary><div class="rf">`)
		b.num("Leader length dy (negative = up)", "dy", f.Callout.Dy)
		b.num("Sideways shift dx", "dx", f.Callout.Dx)
		b.field("Above the panel", checkbox("top", f.Callout.Top))
		b.field("Callout colour", laneSelect("color", f.Callout.Color, lanes, "(lane colour)"))
		b.w(`</div></details>`)
	}
	b.w(`<div class="bar"><input type="text" name="summary" maxlength="72" placeholder="What changed? (commit note)" value="` + esc(summary) + `"><button type="submit">Save</button><a href="/">Cancel</a></div></form>`)
	html(w, code, page("Edit - "+title, b.String()))
}

func (s *Server) portForm(w http.ResponseWriter, r *http.Request) {
	n, te, ok := parsePortNum(r.PathValue("n"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	var f portForm
	if te == 0 {
		f = portFormFor(d, n)
	}
	s.renderPort(w, 200, n, te, d, f, nil, "")
}

func (s *Server) portSave(w http.ResponseWriter, r *http.Request) {
	n, te, ok := parsePortNum(r.PathValue("n"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	p := &formP{v: r.PostForm}
	summary := strings.Join(strings.Fields(p.str("summary")), " ")
	label := ""
	var shown portForm
	if te > 0 {
		label = fmt.Sprintf("port te%d", te)
		d.Switch.TeLabels[te-1] = p.trim("te")
	} else {
		label = "port " + strconv.Itoa(n)
		shown = applyPortForm(d, n, p)
	}
	errs := append(p.errs, d.Validate()...)
	if len(errs) > 0 {
		s.renderPort(w, http.StatusUnprocessableEntity, n, te, d, shown, errs, summary)
		return
	}
	if summary == "" {
		summary = "updated"
	}
	if err := s.st.Save(d, label+": "+summary); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// applyPortForm writes a posted port form into d (ports, callout, port-table row) and
// returns what was posted so an invalid form can be shown again unchanged.
func applyPortForm(d *Data, n int, p *formP) portForm {
	old := portFormFor(d, n)
	now := portForm{
		Role: p.v.Get("role"), State: p.v.Get("state"), Flag: p.bool("flag"),
		Cells: portCells{p.trim("device"), p.trim("ip"), p.trim("notes")},
	}
	c := Callout{Port: n, Label: p.trim("label"), Sub: p.trim("sub"), Top: p.bool("top"), Color: p.v.Get("color")}
	if c.Label != "" {
		c.Dy = p.integer("dy", "dy", -400, 400, old.Callout.Dy)
		c.Dx = p.integer("dx", "dx", -400, 400, old.Callout.Dx)
	} else {
		c.Dy, _ = strconv.Atoi(strings.TrimSpace(p.v.Get("dy")))
		c.Dx, _ = strconv.Atoi(strings.TrimSpace(p.v.Get("dx")))
	}
	now.Callout = c
	sw := &d.Switch
	sw.Ports[n-1].Role, sw.Ports[n-1].State, sw.Ports[n-1].Flag = now.Role, now.State, now.Flag
	i := sw.calloutIndex(n)
	switch {
	case c.Label == "" && i >= 0:
		sw.Callouts = append(sw.Callouts[:i], sw.Callouts[i+1:]...)
	case c.Label != "" && i >= 0:
		sw.Callouts[i] = c
	case c.Label != "":
		sw.Callouts = append(sw.Callouts, c)
	}
	d.PortTable.setPortCells(n, old.Cells, now.Cells)
	return now
}
