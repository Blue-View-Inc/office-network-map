package main

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed assets/style.css
var styleCSS string

//go:embed assets/edit_links.css
var editLinksCSS string

// num mirrors Python's int/float distinction so coordinates print exactly as the
// original generator did ("85.0" for a float, "85" for an int).
type num struct {
	v float64
	f bool
}

func I(v int) num           { return num{float64(v), false} }
func F(v float64) num       { return num{v, true} }
func (a num) add(b num) num { return num{a.v + b.v, a.f || b.f} }
func (a num) String() string {
	if !a.f {
		return strconv.Itoa(int(a.v))
	}
	s := strconv.FormatFloat(a.v, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func pmax(a, b num) num {
	if b.v > a.v {
		return b
	}
	return a
}
func pmin(a, b num) num {
	if b.v < a.v {
		return b
	}
	return a
}

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

// esc matches Python's html.escape (quote=True).
func esc(s string) string { return escaper.Replace(s) }

func (d *Data) color(k string) string {
	if c, ok := d.Palette[k]; ok {
		return c
	}
	return k
}

const (
	swW, swH       = 1180, 640
	x0, yTop, yBot = 70, 232, 272
	pw, ph         = 30, 30
	tW, tH         = 1180, 760
)

func px(c int) int { return x0 + c*36 + (c/6)*20 }

// SwitchSVG renders the front-panel diagram exactly as the original generator did.
func SwitchSVG(d *Data) string { return switchSVG(d, false) }

// SwitchSVGLinks is SwitchSVG with every port wrapped in an <a href="/edit/port/N">
// (Te1/Te2 -> /edit/port/te1, te2). The look is unchanged; only the wrapper is added.
func SwitchSVGLinks(d *Data) string { return switchSVG(d, true) }

func switchSVG(d *Data, links bool) string {
	sw := &d.Switch
	var s []string
	a := func(f string, args ...any) { s = append(s, fmt.Sprintf(f, args...)) }
	a(`<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg" font-family="Inter,Manrope,Arial,sans-serif" role="img" aria-label="%s">`, swW, swH, esc(sw.Title))
	a(`<defs><linearGradient id="bg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#eef3f9"/><stop offset="1" stop-color="#dfe8f3"/></linearGradient>` +
		`<radialGradient id="o1" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="#bcd3f0" stop-opacity=".9"/><stop offset="1" stop-color="#bcd3f0" stop-opacity="0"/></radialGradient>` +
		`<linearGradient id="chassis" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#ffffff" stop-opacity=".92"/><stop offset="1" stop-color="#e8eef6" stop-opacity=".92"/></linearGradient>` +
		`<filter id="sh" x="-5%%" y="-20%%" width="110%%" height="150%%"><feDropShadow dx="0" dy="6" stdDeviation="9" flood-color="#1e3a5f" flood-opacity=".16"/></filter></defs>`)
	a(`<rect width="%d" height="%d" rx="22" fill="url(#bg)"/>`, swW, swH)
	a(`<ellipse cx="300" cy="180" rx="380" ry="200" fill="url(#o1)"/><ellipse cx="880" cy="430" rx="380" ry="190" fill="url(#o1)"/>`)
	a(`<text x="40" y="52" font-size="26" font-weight="700" fill="#1e2b3c" font-family="Manrope,Inter,Arial">%s</text>`, esc(sw.Title))
	a(`<text x="40" y="78" font-size="14" fill="#52637a">%s</text>`, esc(sw.Subtitle))
	cx0, cy0, cw, ch := 40, 196, 1100, 124
	a(`<rect x="%d" y="%d" width="%d" height="%d" rx="14" fill="url(#chassis)" stroke="#ffffff" stroke-width="1.5" filter="url(#sh)"/>`, cx0, cy0, cw, ch)
	a(`<rect x="%d" y="%d" width="%d" height="%d" rx="14" fill="none" stroke="#9fb3cc" stroke-opacity=".6"/>`, cx0, cy0, cw, ch)
	for c := 0; c < 24; c++ {
		for _, pp := range [2]struct{ p, y int }{{2*c + 1, yTop}, {2*c + 2, yBot}} {
			p, y := pp.p, pp.y
			port := sw.Ports[p-1]
			col := d.color(port.Role)
			x := px(c)
			if links {
				a(`<a href="/edit/port/%d" class="pl"><title>Edit port %d</title>`, p, p)
			}
			var tc string
			switch port.State {
			case "used":
				a(`<rect x="%d" y="%d" width="%d" height="%d" rx="5" fill="%s"/>`, x, y, pw, ph, col)
				tc = "#fff"
			case "reserved":
				a(`<rect x="%d" y="%d" width="%d" height="%d" rx="5" fill="%s" fill-opacity=".18" stroke="%s" stroke-width="1.6" stroke-dasharray="4 3"/>`, x, y, pw, ph, col, col)
				tc = col
			default:
				a(`<rect x="%d" y="%d" width="%d" height="%d" rx="5" fill="%s" fill-opacity=".10" stroke="%s" stroke-opacity=".55"/>`, x, y, pw, ph, col, col)
				tc = "#52637a"
			}
			a(`<text x="%s" y="%d" font-size="12" font-weight="700" text-anchor="middle" fill="%s">%d</text>`, F(float64(x)+pw/2.0), y+20, tc, p)
			if port.Flag {
				a(`<circle cx="%d" cy="%d" r="6" fill="#f59e0b" stroke="#fff" stroke-width="1.5"/><text x="%d" y="%d" font-size="9" font-weight="800" text-anchor="middle" fill="#fff">!</text>`, x+pw-2, y+2, x+pw-2, y+6)
			}
			if links {
				a(`</a>`)
			}
		}
	}
	for i, lbl := range sw.TeLabels {
		x := px(23) + 60
		y := yTop
		if i == 1 {
			y = yBot
		}
		if links {
			a(`<a href="/edit/port/te%d" class="pl"><title>Edit %s</title>`, i+1, esc(lbl))
		}
		a(`<rect x="%d" y="%d" width="34" height="%d" rx="4" fill="#e2e8f0" stroke="#94a3b8" stroke-dasharray="3 3"/>`, x, y, ph)
		a(`<text x="%d" y="%d" font-size="10" text-anchor="middle" fill="#64748b">%s</text>`, x+17, y+20, esc(lbl))
		if links {
			a(`</a>`)
		}
	}
	a(`<text x="%d" y="%d" font-size="9" text-anchor="middle" fill="#64748b">%s</text>`, px(23)+77, cy0+ch-10, esc(sw.TeNote))
	a(`<text x="%d" y="%d" font-size="11" fill="#52637a" font-weight="600">%s</text>`, cx0+16, cy0+22, esc(sw.Chassis))
	for _, co := range sw.Callouts {
		c := (co.Port - 1) / 2
		x := F(float64(px(c)) + pw/2.0)
		col := d.color(sw.Ports[co.Port-1].Role)
		if co.Color != "" {
			col = d.color(co.Color)
		}
		ys := yBot + ph
		if co.Top {
			ys = yTop
		}
		ye := ys + co.Dy
		tx := x.add(I(co.Dx))
		mid := F(float64(ys+ye) / 2)
		a(`<path d="M%s,%d L%s,%s L%s,%s L%s,%d" fill="none" stroke="%s" stroke-width="1.6"/>`, x, ys, x, mid, tx, mid, tx, ye, col)
		a(`<circle cx="%s" cy="%d" r="3.5" fill="%s"/>`, tx, ye, col)
		ty, y1, y2 := ye+18, ye+18, ye+18+15
		if co.Top {
			ty = ye - 10
			y1, y2 = ty-14, ty
		}
		a(`<text x="%s" y="%d" font-size="13" font-weight="700" text-anchor="middle" fill="#1e2b3c">%s</text>`, tx, y1, esc(co.Label))
		a(`<text x="%s" y="%d" font-size="11.5" text-anchor="middle" fill="%s" font-weight="600">%s</text>`, tx, y2, col, esc(co.Sub))
	}
	yb := yBot + ph
	for _, b := range sw.Brackets {
		col := d.color(b.Color)
		bx1, bx2 := px(b.ColFrom), px(b.ColTo)+pw
		a(`<path d="M%d,%d L%d,%d L%d,%d L%d,%d" fill="none" stroke="%s" stroke-width="1.6"/>`, bx1, yb+8, bx1, yb+14, bx2, yb+14, bx2, yb+8, col)
		stem := I(bx1 + b.StemDx)
		if b.StemMid {
			stem = F(float64(bx1+bx2) / 2)
		}
		dash := ""
		if b.Dashed {
			dash = ` stroke-dasharray="4 3"`
		}
		a(`<line x1="%s" y1="%d" x2="%s" y2="%d" stroke="%s" stroke-width="1.6"%s/>`, stem, yb+14, stem, yb+14+b.StemLen, col, dash)
		tx, anchor := I(bx1+b.TextDx), ""
		if b.Anchor == "middle" {
			tx, anchor = stem, ` text-anchor="middle"`
			if !b.StemMid {
				tx = F(float64(bx1+bx2) / 2)
			}
		}
		a(`<text x="%s" y="%d" font-size="13" font-weight="700"%s fill="#1e2b3c">%s</text>`, tx, yb+b.TitleDy, anchor, esc(b.Title))
		a(`<text x="%s" y="%d" font-size="11.5" font-weight="600"%s fill="%s">%s</text>`, tx, yb+b.SubDy, anchor, col, esc(b.Sub))
	}
	ly := swH - 60
	a(`<rect x="28" y="%d" width="1124" height="64" rx="12" fill="#ffffff" fill-opacity=".6" stroke="#ffffff"/>`, ly-24)
	x := I(40)
	for _, it := range sw.Legend {
		a(`<rect x="%s" y="%d" width="14" height="14" rx="3" fill="%s"/><text x="%s" y="%d" font-size="11.5" fill="#1e2b3c">%s</text>`, x, ly-8, d.color(it.Color), x.add(I(20)), ly+4, esc(it.Text))
		x = x.add(F(30 + float64(utf8.RuneCountInString(it.Text))*5.7))
	}
	a(`<text x="40" y="%d" font-size="11" fill="#52637a">%s</text>`, ly+28, esc(sw.LegendNote))
	a(`</svg>`)
	return strings.Join(s, "\n")
}

// card draws one topology card and returns its height.
func topoCard(d *Data, s *[]string, c Card) int {
	a := func(f string, args ...any) { *s = append(*s, fmt.Sprintf(f, args...)) }
	col := d.color(c.Color)
	h := 34 + len(c.Lines)*17 + 10
	if c.Sub != "" {
		h += 18
	}
	a(`<rect x="%d" y="%d" width="%d" height="%d" rx="14" fill="#ffffff" fill-opacity=".9" stroke="#ffffff" filter="url(#tsh)"/>`, c.X, c.Y, c.W, h)
	a(`<rect x="%d" y="%d" width="%d" height="6" rx="3" fill="%s"/>`, c.X, c.Y, c.W, col)
	a(`<text x="%d" y="%d" font-size="14" font-weight="700" fill="#1e2b3c" font-family="Manrope,Inter,Arial">%s</text>`, c.X+14, c.Y+28, esc(c.Title))
	yy := c.Y + 28
	if c.Sub != "" {
		yy += 18
		a(`<text x="%d" y="%d" font-size="11.5" font-weight="600" fill="%s">%s</text>`, c.X+14, yy, col, esc(c.Sub))
	}
	for _, ln := range c.Lines {
		yy += 17
		fill := "#3b4a5e"
		if ln.Warn {
			fill = "#b45309"
		}
		a(`<text x="%d" y="%d" font-size="11.5" fill="%s">%s</text>`, c.X+14, yy, fill, esc(ln.Text))
	}
	return h
}

func topoLink(s *[]string, x1, y1, x2, y2 num, col string, label string, dash bool, w string) {
	my := F(float64(y1.v+y2.v) / 2)
	ds := ""
	if dash {
		ds = ` stroke-dasharray="6 5"`
	}
	*s = append(*s, fmt.Sprintf(`<path d="M%s,%s C%s,%s %s,%s %s,%s" fill="none" stroke="%s" stroke-width="%s"%s stroke-linecap="round"/>`, x1, y1, x1, my, x2, my, x2, y2, col, w, ds))
	if label != "" {
		*s = append(*s, fmt.Sprintf(`<text x="%s" y="%s" font-size="10.5" font-weight="600" fill="%s">%s</text>`, x2.add(I(6)), y2.add(I(-8)), col, esc(label)))
	}
}

// TopoSVG renders the topology diagram.
func TopoSVG(d *Data) string {
	t := &d.Topology
	var s []string
	a := func(f string, args ...any) { s = append(s, fmt.Sprintf(f, args...)) }
	a(`<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg" font-family="Inter,Manrope,Arial,sans-serif" role="img" aria-label="%s">`, tW, tH, esc(t.Title))
	a(`<defs><linearGradient id="tbg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#eef3f9"/><stop offset="1" stop-color="#dde7f2"/></linearGradient>` +
		`<radialGradient id="tor" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="#bcd3f0" stop-opacity=".85"/><stop offset="1" stop-color="#bcd3f0" stop-opacity="0"/></radialGradient>` +
		`<filter id="tsh" x="-10%%" y="-10%%" width="120%%" height="140%%"><feDropShadow dx="0" dy="5" stdDeviation="8" flood-color="#1e3a5f" flood-opacity=".14"/></filter></defs>`)
	a(`<rect width="%d" height="%d" rx="22" fill="url(#tbg)"/>`, tW, tH)
	a(`<ellipse cx="260" cy="560" rx="420" ry="220" fill="url(#tor)"/><ellipse cx="930" cy="200" rx="380" ry="200" fill="url(#tor)"/>`)
	a(`<text x="40" y="50" font-size="26" font-weight="700" fill="#1e2b3c" font-family="Manrope,Inter,Arial">%s</text>`, esc(t.Title))
	a(`<text x="40" y="76" font-size="14" fill="#52637a">%s</text>`, esc(t.Subtitle))

	cx, cy := 590, 128
	a(`<g filter="url(#tsh)"><ellipse cx="%d" cy="%d" rx="54" ry="30" fill="#fff"/><ellipse cx="%d" cy="%d" rx="60" ry="32" fill="#fff"/><ellipse cx="%d" cy="%d" rx="58" ry="34" fill="#fff"/></g>`, cx-46, cy+6, cx+40, cy+4, cx, cy-14)
	a(`<text x="%d" y="%d" font-size="15" font-weight="700" text-anchor="middle" fill="#1e2b3c" font-family="Manrope,Inter,Arial">%s</text>`, cx, cy-4, esc(t.Internet.Name))
	a(`<text x="%d" y="%d" font-size="11" text-anchor="middle" fill="#52637a">%s</text>`, cx, cy+14, esc(t.Internet.Sub))

	vpn, dmz := d.color(t.Remote.Color), d.color(t.DMZ.Color)
	h := topoCard(d, &s, t.Remote)
	topoLink(&s, I(t.Remote.X+t.Remote.W/2), I(t.Remote.Y+h), I(700), I(262), vpn, "", true, "1.8")
	hd := topoCard(d, &s, t.DMZ)
	fx, fy, fw, fh := 440, 214, 300, 74
	topoLink(&s, I(cx), I(cy+34), I(cx), I(fy), d.color("wan"), "", false, "3")
	a(`<rect x="%d" y="%d" width="%d" height="%d" rx="16" fill="#1e2b3c" filter="url(#tsh)"/>`, fx, fy, fw, fh)
	a(`<text x="%d" y="%d" font-size="16" font-weight="800" text-anchor="middle" fill="#fff" font-family="Manrope,Inter,Arial">%s</text>`, cx, fy+30, esc(t.Firewall.Title))
	a(`<text x="%d" y="%d" font-size="11.5" text-anchor="middle" fill="#c7d2e0">%s</text>`, cx, fy+51, esc(t.Firewall.Line1))
	a(`<text x="%d" y="%d" font-size="11" text-anchor="middle" fill="#93a4bb">%s</text>`, cx, fy+66, esc(t.Firewall.Line2))
	topoLink(&s, I(t.DMZ.X+t.DMZ.W/2), I(t.DMZ.Y+hd), I(fx), I(fy+fh-14), dmz, "", false, "2.2")
	sx, sy, sw, sh := 240, 352, 700, 62
	topoLink(&s, I(cx), I(fy+fh), I(cx), I(sy), d.color("fw"), t.Switch.TrunkLabel, false, "3.2")
	a(`<rect x="%d" y="%d" width="%d" height="%d" rx="16" fill="#ffffff" stroke="#9fb3cc" filter="url(#tsh)"/>`, sx, sy, sw, sh)
	for i := 0; i < 24; i++ {
		a(`<rect x="%d" y="%d" width="18" height="12" rx="3" fill="#cbd5e1"/>`, sx+70+i*24, sy+36)
	}
	a(`<text x="%d" y="%d" font-size="15" font-weight="800" text-anchor="middle" fill="#1e2b3c" font-family="Manrope,Inter,Arial">%s</text>`, cx, sy+26, esc(t.Switch.Title))

	for _, g := range t.Groups {
		hh := topoCard(d, &s, g)
		_ = hh
		mid := F(float64(g.X) + float64(g.W)/2)
		x1 := pmin(pmax(mid, I(sx+30)), I(sx+sw-30))
		topoLink(&s, x1, I(sy+sh), mid, I(g.Y), d.color(g.Color), "", false, "2.4")
	}
	ly := tH - 34
	x := I(40)
	for _, it := range t.Legend {
		a(`<rect x="%s" y="%d" width="14" height="14" rx="3" fill="%s"/><text x="%s" y="%d" font-size="11.5" fill="#1e2b3c">%s</text>`, x, ly-10, d.color(it.Color), x.add(I(20)), ly+2, esc(it.Text))
		x = x.add(F(34 + float64(utf8.RuneCountInString(it.Text))*6.2))
	}
	a(`</svg>`)
	return strings.Join(s, "\n")
}

// Opts controls the page chrome around the map; the zero value is the plain print/PDF page.
type Opts struct {
	Edit   bool   // show Edit/History links
	Live   bool   // the live poller is running (screen UI only)
	Banner string // trusted HTML shown above the map (history view)
}

func ol(items []string, sep string) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString("<li>" + it + "</li>" + sep)
	}
	return b.String()
}

func tableSection(t *Table, class, edit string) string {
	var b strings.Builder
	b.WriteString(`<section` + class + `>` + edit + `<h2>` + esc(t.Title) + `</h2><div class="tw"><table><thead><tr>`)
	for _, h := range t.Headers {
		b.WriteString("<th>" + esc(h) + "</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, r := range t.Rows {
		b.WriteString("<tr>")
		for _, c := range r {
			b.WriteString("<td>" + c + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
	if t.Note != "" {
		b.WriteString("\n" + `<p class="note">` + t.Note + `</p>`)
	}
	return b.String() + "</section>"
}

// Body renders the map exactly as build.py's office_network_map.html (plus optional edit chrome).
func Body(d *Data, o Opts) string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s) }
	w("<title>" + esc(d.Header.Title) + "</title>\n")
	w(`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Manrope:wght@600;700;800&family=Inter:wght@400;500;600&display=swap">` + "\n")
	w("<style>\n" + styleCSS + "\n</style>\n")
	if o.Edit {
		w("<style>\n" + editLinksCSS + "\n" + uiCSS + "\n</style>\n")
	}
	w(themeLinks())
	w(`<div class="wrap">` + "\n")
	w(o.Banner)
	bar := ""
	if o.Edit {
		badge := `<span id="om-badge" class="om-badge" data-live="0"><i></i>Live off</span>`
		if o.Live {
			badge = `<span id="om-badge" class="om-badge" data-live="1"><i></i>Live · starting</span>`
		}
		bar = `<div class="edbar">` + badge + `<a class="ed" href="/edit/header">Edit</a><a class="ed" href="/history">History</a><a class="ed" href="/switch">Switch</a></div>`
	}
	w("<header>" + bar + "<h1>" + esc(d.Header.Title) + `</h1><p class="sub">` + esc(d.Header.Subtitle) + "</p></header>\n")
	if o.Edit {
		w(`<div id="om-alerts" class="om-alerts" hidden></div>` + "\n")
		w(`<div id="om-main" style="display:contents">`)
	}
	w(bodyMain(d, o))
	if o.Edit {
		w("</div>\n")
	}
	w("</div>")
	if o.Edit {
		w("\n" + `<dialog id="om-dlg" class="om-dlg" aria-label="Edit port"><button type="button" class="x" aria-label="Close">&times;</button><div class="om-body"></div></dialog>` + "\n")
		w("<script>\n" + uiJS + "\n</script>\n")
	}
	return b.String()
}

// bodyMain renders everything below the header: the two diagrams and all sections. The live
// page re-fetches exactly this (GET /?fragment=1) to refresh the map in place.
func bodyMain(d *Data, o Opts) string {
	link := func(sec string) string {
		if !o.Edit {
			return ""
		}
		return `<a class="ed" href="/edit/` + sec + `">Edit</a>`
	}
	corner := func(sec string) string {
		if !o.Edit {
			return ""
		}
		return `<a class="ed corner" href="/edit/` + sec + `">Edit</a>`
	}
	var b strings.Builder
	w := func(s string) { b.WriteString(s) }
	w(`<div class="diagram">` + corner("topology") + TopoSVG(d) + "</div>\n")
	svg := SwitchSVG
	if o.Edit {
		svg = SwitchSVGLinks
	}
	w(`<div class="diagram pb">` + corner("ports") + svg(d) + "</div>\n")
	w(`<section>` + link("before") + `<h2>` + esc(d.Before.Title) + `</h2><ol>` + "\n" + ol(d.Before.Items, "\n") + "</ol></section>\n")
	w(`<section>` + link("hardware") + `<h2>` + esc(d.Hardware.Title) + `</h2><ul class="todo">` + "\n" + ol(d.Hardware.Items, "\n") + "</ul></section>\n")
	w(`<section>` + link("after") + `<h2>` + esc(d.After.Title) + `</h2><ol>` + "\n" + ol(d.After.Items, "\n") + "</ol></section>\n")
	w(tableSection(&d.PortTable, ` class="pb"`, link("porttable")) + "\n")
	w(tableSection(&d.VLANs, ` class="pb"`, link("vlans")) + "\n")
	w(tableSection(&d.Devices, ` class="pb"`, link("devices")) + "\n")
	w(`<section>` + link("power") + `<h2>` + esc(d.Power.Title) + `</h2><ol>` + ol(d.Power.Items, "") + "</ol></section>\n")
	return b.String()
}

// PrintHTML wraps the body in a doctype page for headless Chrome, as build.py does.
func PrintHTML(d *Data) string {
	return `<!doctype html><html><head><meta charset="utf-8"></head><body>` + Body(d, Opts{}) + `</body></html>`
}
