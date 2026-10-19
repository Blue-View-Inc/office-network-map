package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"officemap/switchread"
)

// switchSource is the read-only view of a switch the web UI needs.
type switchSource interface {
	Missing() []string
	Fetch(ctx context.Context) (*switchread.Snapshot, error)
}

// sshSwitch adapts the SSH client to switchSource.
type sshSwitch struct {
	cfg switchread.Config
	cl  *switchread.Client
}

func newSSHSwitch(cfg switchread.Config) *sshSwitch {
	return &sshSwitch{cfg: cfg, cl: &switchread.Client{Cfg: cfg}}
}
func (s *sshSwitch) Missing() []string { return s.cfg.Missing() }
func (s *sshSwitch) Fetch(ctx context.Context) (*switchread.Snapshot, error) {
	return s.cl.Fetch(ctx)
}

// portCmp is one port of the map compared with the same port on the switch.
type portCmp struct {
	N          int
	Lane       string // map lane key
	State      string // map state
	Device     string // map port-table device, tags stripped
	Present    bool   // the switch reported this port
	Switch     switchread.IfStatus
	MACs       []string
	WantState  string // what the map should say, given the switch
	SuggestLn  string // lane the switch's access VLAN points to, if unambiguous
	DiffState  bool
	DiffDesc   bool
	DiffLane   bool
	WantDevice string
}

func (p portCmp) Differs() bool { return p.DiffState || p.DiffDesc || p.DiffLane }

var numTokenRe = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)*`)

// laneForVlan returns the one lane whose switch-legend text names this VLAN number, or "".
func (d *Data) laneForVlan(vlan string) string {
	if _, err := strconv.Atoi(vlan); err != nil {
		return ""
	}
	found := ""
	for _, l := range d.Switch.Legend {
		for _, tok := range numTokenRe.FindAllString(l.Text, -1) {
			if tok == vlan {
				if found != "" && found != l.Color {
					return "" // ambiguous: two lanes claim this VLAN
				}
				found = l.Color
			}
		}
	}
	return found
}

func plainText(s string) string {
	return strings.TrimSpace(strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#x27;", "'").Replace(inlineTag.ReplaceAllString(s, "")))
}

// compareSwitch builds the per-port comparison (ports 1-48).
func compareSwitch(d *Data, snap *switchread.Snapshot) []portCmp {
	by := map[int]switchread.IfStatus{}
	for _, s := range snap.Status {
		if n, te, ok := switchread.PortNumber(s.Port); ok && te == 0 {
			by[n] = s
		}
	}
	macs := snap.MACsByPort()
	out := make([]portCmp, 0, 48)
	for n := 1; n <= 48; n++ {
		mp := d.Switch.Ports[n-1]
		c := portCmp{N: n, Lane: mp.Role, State: mp.State, Device: plainText(d.PortTable.cellsFor(n).Device)}
		c.WantState = mp.State
		if s, ok := by[n]; ok {
			c.Present, c.Switch = true, s
			for port, ms := range macs {
				if pn, te, ok := switchread.PortNumber(port); ok && te == 0 && pn == n {
					c.MACs = ms
				}
			}
			switch {
			case s.Connected():
				c.WantState = "used"
			case mp.State == "used":
				c.WantState = "free"
			}
			c.DiffState = c.WantState != mp.State
			if s.Name != "" {
				a, b := strings.ToLower(c.Device), strings.ToLower(s.Name)
				c.DiffDesc = !(a != "" && (strings.Contains(a, b) || strings.Contains(b, a)))
				c.WantDevice = s.Name
			}
			if s.Vlan != "trunk" {
				c.SuggestLn = d.laneForVlan(s.Vlan)
				c.DiffLane = c.SuggestLn != "" && c.SuggestLn != mp.Role
			}
		}
		out = append(out, c)
	}
	return out
}

// applySwitch copies the switch's values for the chosen ports into d. It never touches the switch.
func applySwitch(d *Data, cmp []portCmp, ports []int) {
	for _, n := range ports {
		c := cmp[n-1]
		if !c.Present {
			continue
		}
		d.Switch.Ports[n-1].State = c.WantState
		if c.DiffLane {
			d.Switch.Ports[n-1].Role = c.SuggestLn
		}
		if c.DiffDesc && c.WantDevice != "" {
			old := d.PortTable.cellsFor(n)
			now := old
			now.Device = esc(c.WantDevice)
			d.PortTable.setPortCells(n, old, now)
		}
	}
}

func (s *Server) switchConfigured() bool { return s.sw != nil && len(s.sw.Missing()) == 0 }

func (s *Server) notConfigured(w http.ResponseWriter) {
	var missing []string
	if s.sw != nil {
		missing = s.sw.Missing()
	} else {
		missing = []string{"-switch-host", "-switch-user", "-switch-key", "-switch-known-hosts"}
	}
	var b strings.Builder
	b.WriteString(`<header><a href="/">&larr; Back to the map</a><h1>Switch not configured</h1>` +
		`<p class="muted">This page compares the map with a live switch. It is read-only: only four <code>show</code> commands are ever sent, and nothing is ever written to the switch. Start office-map with the flags below to turn it on.</p></header>`)
	b.WriteString(`<div class="tw"><table><thead><tr><th>Flag</th><th>Meaning</th><th></th></tr></thead><tbody>`)
	rows := [][3]string{
		{"-switch-host", "host or host:port of the switch (SSH)", "required"},
		{"-switch-user", "SSH user (a read-only account is plenty)", "required"},
		{"-switch-key", "path to the private key file", "required"},
		{"-switch-known-hosts", "OpenSSH known_hosts file holding the switch's host key", "required"},
		{"-switch-hostkey-alias", "name to look up in known_hosts instead of the host", "optional"},
		{"-switch-jump", "user@host to hop through first (same key)", "optional"},
	}
	for _, r := range rows {
		mark := r[2]
		for _, m := range missing {
			if m == r[0] {
				mark = "<strong>missing</strong>"
			}
		}
		if r[2] == "required" && mark == "required" {
			mark = "set"
		}
		b.WriteString(`<tr><td><code>` + r[0] + `</code></td><td>` + esc(r[1]) + `</td><td>` + mark + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	b.WriteString(`<p class="muted">Host keys are checked strictly and never auto-accepted: add the switch's key to the known_hosts file yourself after verifying its fingerprint (for example with <code>ssh-keyscan -t rsa HOST</code>). Example:</p>` +
		`<pre><code>./office-map -switch-host sw1.example.test -switch-user reader \` + "\n" + `  -switch-key ~/.ssh/id_rsa -switch-known-hosts ~/.ssh/known_hosts</code></pre>`)
	html(w, 200, page("Switch not configured", b.String()))
}

func laneLabel(d *Data, k string) string {
	return `<span class="sw" style="background:` + esc(d.color(k)) + `"></span>` + esc(d.laneName(k))
}

func (s *Server) renderSwitch(w http.ResponseWriter, code int, d *Data, cmp []portCmp, at time.Time, notes []string) {
	diffs := 0
	for _, c := range cmp {
		if c.Differs() {
			diffs++
		}
	}
	var b strings.Builder
	b.WriteString(`<header><a href="/">&larr; Back to the map</a><h1>Map vs switch</h1><p class="muted">Read from the switch at ` +
		esc(at.Format("2006-01-02 15:04:05")) + `. Read-only: nothing is written to the switch. ` +
		fmt.Sprintf("%d of 48 ports differ.", diffs) + `</p></header>`)
	if len(notes) > 0 {
		b.WriteString(`<div class="err"><ul>`)
		for _, n := range notes {
			b.WriteString("<li>" + esc(n) + "</li>")
		}
		b.WriteString("</ul></div>")
	}
	b.WriteString(`<form method="post" action="/switch/sync"><div class="tw"><table class="cmp"><thead><tr><th></th><th>Port</th><th>Map</th><th>Switch</th><th>MACs seen</th><th>Differences</th></tr></thead><tbody>`)
	for _, c := range cmp {
		cls := ""
		if c.Differs() {
			cls = ` class="diff"`
		}
		b.WriteString(`<tr` + cls + `><td>`)
		if c.Present && c.Differs() {
			b.WriteString(`<input type="checkbox" name="port" value="` + strconv.Itoa(c.N) + `" aria-label="Use switch values for port ` + strconv.Itoa(c.N) + `">`)
		}
		b.WriteString(`</td><td><a href="/edit/port/` + strconv.Itoa(c.N) + `"><strong>` + strconv.Itoa(c.N) + `</strong></a></td>`)
		b.WriteString(`<td>` + laneLabel(d, c.Lane) + `<br>` + esc(c.State))
		if c.Device != "" {
			b.WriteString(` · ` + esc(c.Device))
		}
		b.WriteString(`</td><td>`)
		if !c.Present {
			b.WriteString(`<span class="muted">not reported</span>`)
		} else {
			b.WriteString(esc(c.Switch.Status) + ` · ` + esc(c.Switch.Vlan))
			if c.Switch.Name != "" {
				b.WriteString(`<br>` + esc(c.Switch.Name))
			}
			if c.SuggestLn != "" {
				b.WriteString(`<br><span class="muted">VLAN points to ` + esc(d.laneName(c.SuggestLn)) + `</span>`)
			}
		}
		b.WriteString(`</td><td class="macs">`)
		for i, m := range c.MACs {
			if i == 4 {
				b.WriteString(fmt.Sprintf(" +%d more", len(c.MACs)-4))
				break
			}
			if i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(`<code>` + esc(m) + `</code>`)
		}
		b.WriteString(`</td><td>`)
		var tags []string
		if c.DiffState {
			tags = append(tags, "state: map "+c.State+" → switch says "+c.WantState)
		}
		if c.DiffDesc {
			tags = append(tags, "description differs")
		}
		if c.DiffLane {
			tags = append(tags, "lane: VLAN "+c.Switch.Vlan+" → "+d.laneName(c.SuggestLn))
		}
		for i, t := range tags {
			if i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(`<span class="tag">` + esc(t) + `</span>`)
		}
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	b.WriteString(`<div class="bar"><input type="text" name="summary" maxlength="72" placeholder="Note (optional)"><button type="submit">Use switch values for selected ports</button>` +
		`<button type="button" class="alt" onclick="document.querySelectorAll('input[name=port]').forEach(function(c){c.checked=true})">Select all differences</button><a href="/">Cancel</a></div></form>`)
	b.WriteString(`<p class="muted">“Use switch values” updates the map (and the port table) for the ticked ports: state follows link status, description is copied into the port table, and the lane follows the access VLAN when exactly one lane names it. Every sync is a normal commit in History.</p>`)
	html(w, code, page("Map vs switch", b.String()))
}

func (s *Server) switchView(w http.ResponseWriter, r *http.Request) {
	if !s.switchConfigured() {
		s.notConfigured(w)
		return
	}
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	snap, err := s.sw.Fetch(ctx)
	if err != nil {
		b := `<header><a href="/">&larr; Back to the map</a><h1>Could not read the switch</h1></header><div class="err">` + esc(err.Error()) + `</div>`
		html(w, http.StatusBadGateway, page("Switch error", b))
		return
	}
	s.renderSwitch(w, 200, d, compareSwitch(d, snap), time.Now(), nil)
}

func (s *Server) switchSync(w http.ResponseWriter, r *http.Request) {
	if !s.switchConfigured() {
		s.notConfigured(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var ports []int
	seen := map[int]bool{}
	for _, v := range r.PostForm["port"] {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 48 {
			http.Error(w, "bad port "+v, http.StatusBadRequest)
			return
		}
		if !seen[n] {
			seen[n] = true
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)
	// read the switch again: apply what it says now, not what the page showed minutes ago
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	snap, err := s.sw.Fetch(ctx)
	if err != nil {
		b := `<header><a href="/">&larr; Back to the map</a><h1>Could not read the switch</h1></header><div class="err">` + esc(err.Error()) + `</div>`
		html(w, http.StatusBadGateway, page("Switch error", b))
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	d, err := s.st.Load()
	if err != nil {
		s.fail(w, err)
		return
	}
	cmp := compareSwitch(d, snap)
	if len(ports) == 0 {
		s.renderSwitch(w, http.StatusUnprocessableEntity, d, cmp, time.Now(), []string{"Nothing selected: tick at least one port."})
		return
	}
	applySwitch(d, cmp, ports)
	strs := make([]string, len(ports))
	for i, n := range ports {
		strs[i] = strconv.Itoa(n)
	}
	msg := "switch sync: ports " + strings.Join(strs, ", ")
	if note := strings.Join(strings.Fields(r.PostForm.Get("summary")), " "); note != "" {
		msg += " (" + note + ")"
	}
	if errs := d.Validate(); len(errs) > 0 {
		s.renderSwitch(w, http.StatusUnprocessableEntity, d, cmp, time.Now(), errs)
		return
	}
	if err := s.st.Save(d, msg); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/switch", http.StatusSeeOther)
}
