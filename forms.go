package main

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// formB builds the HTML of an edit form.
type formB struct {
	strings.Builder
	d *Data
}

func (b *formB) w(s string) { b.WriteString(s) }

func (b *formB) colorOpts() []string {
	ks := make([]string, 0, len(b.d.Palette))
	for k := range b.d.Palette {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (b *formB) field(label, control string) {
	b.w(`<label class="f"><span>` + esc(label) + `</span>` + control + `</label>`)
}

func input(name, val, extra string) string {
	return `<input name="` + esc(name) + `" value="` + esc(val) + `" ` + extra + `>`
}

func intInput(name string, val int) string {
	return input(name, strconv.Itoa(val), `type="number" step="1" inputmode="numeric"`)
}

func textarea(name, val string, rows int) string {
	return `<textarea name="` + esc(name) + `" rows="` + strconv.Itoa(rows) + `">` + esc(val) + `</textarea>`
}

func selectBox(name, val string, opts []string, blank bool) string {
	s := `<select name="` + esc(name) + `">`
	if blank {
		s += `<option value="">(default)</option>`
	}
	for _, o := range opts {
		sel := ""
		if o == val {
			sel = " selected"
		}
		s += `<option` + sel + `>` + esc(o) + `</option>`
	}
	return s + `</select>`
}

func checkbox(name string, on bool) string {
	c := ""
	if on {
		c = " checked"
	}
	return `<input type="checkbox" name="` + esc(name) + `" value="1"` + c + `>`
}

func (b *formB) text(label, name, val string)           { b.field(label, input(name, val, `type="text"`)) }
func (b *formB) area(label, name, val string, rows int) { b.field(label, textarea(name, val, rows)) }
func (b *formB) num(label, name string, val int)        { b.field(label, intInput(name, val)) }
func (b *formB) color(label, name, val string, blank bool) {
	b.field(label, selectBox(name, val, b.colorOpts(), blank))
}

func (b *formB) group(title string) { b.w(`<h3>` + esc(title) + `</h3>`) }

// rowStart/rowEnd wrap one repeatable row with its position and delete controls.
func (b *formB) rowStart(prefix string, i int, pos int, deletable bool) {
	b.w(`<div class="row"><div class="rc"><label class="f s"><span>Order</span>` +
		input(fmt.Sprintf("%s.%d.pos", prefix, i), strconv.Itoa(pos), `type="number" step="any"`) + `</label>`)
	if deletable {
		b.w(`<label class="chk"><span>Delete</span>` + checkbox(fmt.Sprintf("%s.%d.del", prefix, i), false) + `</label>`)
	} else {
		b.w(`<span class="muted">new</span>`)
	}
	b.w(`</div><div class="rf">`)
}
func (b *formB) rowEnd() { b.w(`</div></div>`) }

type col struct {
	Label string
	Opts  []string // non-nil: a select
	Rows  int
}

// rowsEditor renders a list of rows with order/delete controls plus blank rows to add.
func (b *formB) rowsEditor(prefix string, cols []col, rows [][]string, blank int) {
	n := len(rows) + blank
	b.w(`<input type="hidden" name="` + prefix + `.n" value="` + strconv.Itoa(n) + `">`)
	for i := 0; i < n; i++ {
		b.rowStart(prefix, i, i+1, i < len(rows))
		for j, c := range cols {
			v := ""
			if i < len(rows) && j < len(rows[i]) {
				v = rows[i][j]
			}
			name := fmt.Sprintf("%s.%d.%d", prefix, i, j)
			if c.Opts != nil {
				b.field(c.Label, selectBox(name, v, c.Opts, false))
			} else {
				r := c.Rows
				if r == 0 {
					r = 2
				}
				b.field(c.Label, textarea(name, v, r))
			}
		}
		b.rowEnd()
	}
}

// formP reads a posted form and collects validation errors.
type formP struct {
	v    url.Values
	errs []string
}

func (f *formP) str(name string) string {
	return strings.ReplaceAll(f.v.Get(name), "\r\n", "\n")
}

func (f *formP) trim(name string) string { return strings.TrimSpace(f.str(name)) }
func (f *formP) bool(name string) bool   { return f.v.Get(name) != "" }

func (f *formP) integer(name, label string, lo, hi, cur int) int {
	n, err := strconv.Atoi(strings.TrimSpace(f.v.Get(name)))
	if err != nil || n < lo || n > hi {
		f.errs = append(f.errs, fmt.Sprintf("%s: enter a whole number from %d to %d", label, lo, hi))
		return cur
	}
	return n
}

// order returns the indexes of non-deleted rows sorted by their Order value.
func (f *formP) order(prefix string) []int {
	n, _ := strconv.Atoi(f.v.Get(prefix + ".n"))
	type kv struct {
		i   int
		pos float64
	}
	var ks []kv
	for i := 0; i < n && i < 500; i++ {
		if f.v.Get(fmt.Sprintf("%s.%d.del", prefix, i)) != "" {
			continue
		}
		p, err := strconv.ParseFloat(strings.TrimSpace(f.v.Get(fmt.Sprintf("%s.%d.pos", prefix, i))), 64)
		if err != nil {
			p = float64(i + 1)
		}
		ks = append(ks, kv{i, p})
	}
	sort.SliceStable(ks, func(a, c int) bool { return ks[a].pos < ks[c].pos })
	out := make([]int, len(ks))
	for i, k := range ks {
		out[i] = k.i
	}
	return out
}

// rows returns the kept rows, dropping ones with every cell empty.
func (f *formP) rows(prefix string, ncol int) [][]string {
	var out [][]string
	for _, i := range f.order(prefix) {
		r := make([]string, ncol)
		empty := true
		for j := range r {
			r[j] = strings.TrimSpace(f.str(fmt.Sprintf("%s.%d.%d", prefix, i, j)))
			if r[j] != "" {
				empty = false
			}
		}
		if !empty {
			out = append(out, r)
		}
	}
	return out
}

func legendRows(l []Legend) [][]string {
	r := make([][]string, len(l))
	for i, x := range l {
		r[i] = []string{x.Color, x.Text}
	}
	return r
}

func toLegend(rows [][]string) []Legend {
	l := make([]Legend, 0, len(rows))
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		l = append(l, Legend{r[0], r[1]})
	}
	return l
}

func legendCols(b *formB) []col {
	return []col{{Label: "Colour", Opts: b.colorOpts()}, {Label: "Text", Rows: 1}}
}

// section ties a form renderer to its parser.
type section struct {
	title string
	form  func(b *formB)
	apply func(d *Data, f *formP)
}

func tableSec(get func(*Data) *Table) section {
	return section{
		form: func(b *formB) {
			t := get(b.d)
			b.text("Section title", "title", t.Title)
			b.group("Rows")
			cols := make([]col, len(t.Headers))
			for i, h := range t.Headers {
				cols[i] = col{Label: h}
			}
			b.rowsEditor("rows", cols, t.Rows, 3)
			b.area("Note under the table (optional)", "note", t.Note, 3)
			b.w(`<p class="muted">Cells may use <code>&lt;b&gt;</code>, <code>&lt;i&gt;</code> and <code>&lt;code&gt;</code>.</p>`)
		},
		apply: func(d *Data, f *formP) {
			t := get(d)
			t.Title = f.trim("title")
			t.Rows = f.rows("rows", len(t.Headers))
			t.Note = f.trim("note")
		},
	}
}

func listSec(get func(*Data) *List) section {
	return section{
		form: func(b *formB) {
			l := get(b.d)
			b.text("Section title", "title", l.Title)
			b.group("Items")
			rows := make([][]string, len(l.Items))
			for i, it := range l.Items {
				rows[i] = []string{it}
			}
			b.rowsEditor("items", []col{{Label: "Item", Rows: 3}}, rows, 3)
			b.w(`<p class="muted">Items may use <code>&lt;b&gt;</code>, <code>&lt;i&gt;</code> and <code>&lt;code&gt;</code>.</p>`)
		},
		apply: func(d *Data, f *formP) {
			l := get(d)
			l.Title = f.trim("title")
			l.Items = nil
			for _, r := range f.rows("items", 1) {
				l.Items = append(l.Items, r[0])
			}
		},
	}
}

func cardFields(b *formB, p string, c Card) {
	b.text("Title", p+".title", c.Title)
	b.text("Subtitle", p+".sub", c.Sub)
	b.color("Colour", p+".color", c.Color, false)
	b.num("X", p+".x", c.X)
	b.num("Y", p+".y", c.Y)
	b.num("Width", p+".w", c.W)
	var lines, warn []string
	for i, l := range c.Lines {
		lines = append(lines, l.Text)
		if l.Warn {
			warn = append(warn, strconv.Itoa(i+1))
		}
	}
	b.area("Lines (one per line; leading spaces kept)", p+".lines", strings.Join(lines, "\n"), 4)
	b.text("Warning lines (numbers, comma-separated)", p+".warn", strings.Join(warn, ","))
}

func readCard(f *formP, p string, c *Card) {
	c.Title = f.trim(p + ".title")
	c.Sub = f.trim(p + ".sub")
	c.Color = f.v.Get(p + ".color")
	c.X = f.integer(p+".x", c.Title+" X", 0, 1180, c.X)
	c.Y = f.integer(p+".y", c.Title+" Y", 0, 760, c.Y)
	c.W = f.integer(p+".w", c.Title+" width", 60, 1180, c.W)
	warn := map[int]bool{}
	for _, s := range strings.Split(f.v.Get(p+".warn"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				f.errs = append(f.errs, c.Title+": warning line numbers must be numbers")
				continue
			}
			warn[n] = true
		}
	}
	c.Lines = nil
	raw := strings.TrimRight(f.str(p+".lines"), "\n")
	if raw != "" {
		for i, ln := range strings.Split(raw, "\n") {
			c.Lines = append(c.Lines, Line{Text: strings.TrimRight(ln, " \t"), Warn: warn[i+1]})
		}
	}
}

var sections = map[string]section{
	"header": {
		form: func(b *formB) {
			b.text("Page title", "title", b.d.Header.Title)
			b.area("Subtitle", "subtitle", b.d.Header.Subtitle, 3)
		},
		apply: func(d *Data, f *formP) {
			d.Header.Title = f.trim("title")
			d.Header.Subtitle = f.trim("subtitle")
		},
	},
	"topology": {
		form: func(b *formB) {
			t := &b.d.Topology
			b.text("Diagram title", "title", t.Title)
			b.area("Diagram subtitle", "subtitle", t.Subtitle, 2)
			b.group("Internet")
			b.text("Name", "inet.name", t.Internet.Name)
			b.text("Detail", "inet.sub", t.Internet.Sub)
			b.group("Firewall box")
			b.text("Title", "fw.title", t.Firewall.Title)
			b.text("Line 1", "fw.line1", t.Firewall.Line1)
			b.text("Line 2", "fw.line2", t.Firewall.Line2)
			b.group("Switch box")
			b.text("Title", "sw.title", t.Switch.Title)
			b.text("Trunk link label", "sw.trunk", t.Switch.TrunkLabel)
			b.group("Remote access card")
			cardFields(b, "remote", t.Remote)
			b.group("DMZ card")
			cardFields(b, "dmz", t.DMZ)
			b.group("Group cards (bottom row)")
			n := len(t.Groups) + 1
			b.w(`<input type="hidden" name="grp.n" value="` + strconv.Itoa(n) + `">`)
			for i := 0; i < n; i++ {
				b.rowStart("grp", i, i+1, i < len(t.Groups))
				c := Card{Color: "ap", Y: 478, W: 176}
				if i < len(t.Groups) {
					c = t.Groups[i]
				}
				cardFields(b, fmt.Sprintf("grp.%d", i), c)
				b.rowEnd()
			}
			b.group("Legend")
			b.rowsEditor("legend", legendCols(b), legendRows(t.Legend), 2)
		},
		apply: func(d *Data, f *formP) {
			t := &d.Topology
			t.Title, t.Subtitle = f.trim("title"), f.trim("subtitle")
			t.Internet.Name, t.Internet.Sub = f.trim("inet.name"), f.trim("inet.sub")
			t.Firewall.Title, t.Firewall.Line1, t.Firewall.Line2 = f.trim("fw.title"), f.trim("fw.line1"), f.trim("fw.line2")
			t.Switch.Title, t.Switch.TrunkLabel = f.trim("sw.title"), f.trim("sw.trunk")
			readCard(f, "remote", &t.Remote)
			readCard(f, "dmz", &t.DMZ)
			var groups []Card
			for _, i := range f.order("grp") {
				p := fmt.Sprintf("grp.%d", i)
				if f.trim(p+".title") == "" {
					continue
				}
				c := Card{Y: 478, W: 176}
				readCard(f, p, &c)
				groups = append(groups, c)
			}
			t.Groups = groups
			t.Legend = toLegend(f.rows("legend", 2))
		},
	},
	"ports": {
		form: func(b *formB) {
			sw := &b.d.Switch
			b.text("Diagram title", "title", sw.Title)
			b.area("Diagram subtitle", "subtitle", sw.Subtitle, 2)
			b.text("Chassis label", "chassis", sw.Chassis)
			b.text("Te1 label", "te0", sw.TeLabels[0])
			b.text("Te2 label", "te1", sw.TeLabels[1])
			b.text("10G note", "tenote", sw.TeNote)
			b.group("Ports 1-48")
			b.w(`<p class="muted">Role sets the lane colour. A callout appears when its label is filled; dy is the leader length (negative = upward), dx the sideways shift.</p>`)
			roles := b.colorOpts()
			for i, p := range sw.Ports {
				var co Callout
				for _, c := range sw.Callouts {
					if c.Port == p.N {
						co = c
						break
					}
				}
				k := fmt.Sprintf("p.%d.", p.N)
				b.w(`<div class="row"><div class="rc"><strong>Port ` + strconv.Itoa(p.N) + `</strong></div><div class="rf">`)
				b.field("Role", selectBox(k+"role", p.Role, roles, false))
				b.field("State", selectBox(k+"state", p.State, []string{"used", "reserved", "free"}, false))
				b.field("Needs attention", checkbox(k+"flag", p.Flag))
				b.text("Callout label", k+"label", co.Label)
				b.text("Callout detail", k+"sub", co.Sub)
				b.num("dy", k+"dy", co.Dy)
				b.num("dx", k+"dx", co.Dx)
				b.field("Above the panel", checkbox(k+"top", co.Top))
				b.color("Callout colour", k+"color", co.Color, true)
				b.rowEnd()
				_ = i
			}
			b.group("Brackets")
			n := len(sw.Brackets) + 1
			b.w(`<input type="hidden" name="br.n" value="` + strconv.Itoa(n) + `">`)
			for i := 0; i < n; i++ {
				br := Bracket{Color: "pve", Anchor: "start", StemLen: 26, TitleDy: 58, SubDy: 74}
				if i < len(sw.Brackets) {
					br = sw.Brackets[i]
				}
				k := fmt.Sprintf("br.%d.", i)
				b.rowStart("br", i, i+1, i < len(sw.Brackets))
				b.text("Title", k+"title", br.Title)
				b.text("Detail", k+"sub", br.Sub)
				b.color("Colour", k+"color", br.Color, false)
				b.num("From column (0-23)", k+"from", br.ColFrom)
				b.num("To column (0-23)", k+"to", br.ColTo)
				b.field("Stem at middle", checkbox(k+"mid", br.StemMid))
				b.num("Stem offset", k+"sdx", br.StemDx)
				b.num("Stem length", k+"slen", br.StemLen)
				b.field("Dashed stem", checkbox(k+"dash", br.Dashed))
				b.field("Text anchor", selectBox(k+"anchor", br.Anchor, []string{"start", "middle"}, false))
				b.num("Text offset", k+"tdx", br.TextDx)
				b.num("Title dy", k+"tdy", br.TitleDy)
				b.num("Detail dy", k+"sdy", br.SubDy)
				b.rowEnd()
			}
			b.group("Legend")
			b.rowsEditor("legend", legendCols(b), legendRows(sw.Legend), 2)
			b.area("Legend note", "legendnote", sw.LegendNote, 2)
		},
		apply: func(d *Data, f *formP) {
			sw := &d.Switch
			sw.Title, sw.Subtitle, sw.Chassis = f.trim("title"), f.trim("subtitle"), f.trim("chassis")
			sw.TeLabels = [2]string{f.trim("te0"), f.trim("te1")}
			sw.TeNote = f.trim("tenote")
			sw.LegendNote = f.trim("legendnote")
			edited := map[int]Callout{}
			for i := range sw.Ports {
				n := i + 1
				k := fmt.Sprintf("p.%d.", n)
				sw.Ports[i].Role = f.v.Get(k + "role")
				sw.Ports[i].State = f.v.Get(k + "state")
				sw.Ports[i].Flag = f.bool(k + "flag")
				if lbl := f.trim(k + "label"); lbl != "" {
					edited[n] = Callout{Port: n, Label: lbl, Sub: f.trim(k + "sub"),
						Dy:    f.integer(k+"dy", fmt.Sprintf("port %d dy", n), -400, 400, 0),
						Dx:    f.integer(k+"dx", fmt.Sprintf("port %d dx", n), -400, 400, 0),
						Top:   f.bool(k + "top"),
						Color: f.v.Get(k + "color")}
				}
			}
			// keep existing order; extra callouts on the same port are left untouched
			var out []Callout
			seen := map[int]bool{}
			for _, c := range sw.Callouts {
				if seen[c.Port] {
					out = append(out, c)
					continue
				}
				seen[c.Port] = true
				if e, ok := edited[c.Port]; ok {
					out = append(out, e)
					delete(edited, c.Port)
				}
			}
			for n := 1; n <= 48; n++ {
				if e, ok := edited[n]; ok {
					out = append(out, e)
				}
			}
			sw.Callouts = out
			var brs []Bracket
			for _, i := range f.order("br") {
				k := fmt.Sprintf("br.%d.", i)
				if f.trim(k+"title") == "" {
					continue
				}
				nm := "bracket " + f.trim(k+"title")
				brs = append(brs, Bracket{
					Title: f.trim(k + "title"), Sub: f.trim(k + "sub"), Color: f.v.Get(k + "color"),
					ColFrom: f.integer(k+"from", nm+" from", 0, 23, 0), ColTo: f.integer(k+"to", nm+" to", 0, 23, 0),
					StemMid: f.bool(k + "mid"), StemDx: f.integer(k+"sdx", nm+" stem offset", -400, 400, 0),
					StemLen: f.integer(k+"slen", nm+" stem length", 0, 400, 0), Dashed: f.bool(k + "dash"),
					Anchor: f.v.Get(k + "anchor"), TextDx: f.integer(k+"tdx", nm+" text offset", -400, 400, 0),
					TitleDy: f.integer(k+"tdy", nm+" title dy", 0, 400, 0), SubDy: f.integer(k+"sdy", nm+" detail dy", 0, 400, 0),
				})
			}
			sw.Brackets = brs
			sw.Legend = toLegend(f.rows("legend", 2))
		},
	},
	"porttable": tableSec(func(d *Data) *Table { return &d.PortTable }),
	"vlans":     tableSec(func(d *Data) *Table { return &d.VLANs }),
	"devices":   tableSec(func(d *Data) *Table { return &d.Devices }),
	"before":    listSec(func(d *Data) *List { return &d.Before }),
	"hardware":  listSec(func(d *Data) *List { return &d.Hardware }),
	"after":     listSec(func(d *Data) *List { return &d.After }),
	"power":     listSec(func(d *Data) *List { return &d.Power }),
}

func init() {
	names := map[string]string{"header": "Header", "topology": "Topology diagram", "ports": "Switch ports and callouts",
		"porttable": "Port table", "vlans": "VLANs", "devices": "Key devices", "before": "Before you power off",
		"hardware": "Hardware checklist", "after": "After power-up", "power": "Power-up order"}
	for k, s := range sections {
		s.title = names[k]
		sections[k] = s
	}
}
