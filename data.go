package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Data is the whole document stored in data/network.json.
type Data struct {
	Palette   map[string]string `json:"palette"`
	Header    Header            `json:"header"`
	Topology  Topology          `json:"topology"`
	Switch    Switch            `json:"switch"`
	PortTable Table             `json:"portTable"`
	VLANs     Table             `json:"vlans"`
	Devices   Table             `json:"devices"`
	Before    List              `json:"before"`
	Hardware  List              `json:"hardware"`
	After     List              `json:"after"`
	Power     List              `json:"power"`
}

type Header struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
}

type Line struct {
	Text string `json:"text"`
	Warn bool   `json:"warn,omitempty"`
}

type Card struct {
	Title string `json:"title"`
	Sub   string `json:"sub,omitempty"`
	Color string `json:"color"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	Lines []Line `json:"lines"`
}

type Legend struct {
	Color string `json:"color"`
	Text  string `json:"text"`
}

type Topology struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Internet struct {
		Name string `json:"name"`
		Sub  string `json:"sub"`
	} `json:"internet"`
	Firewall struct {
		Title string `json:"title"`
		Line1 string `json:"line1"`
		Line2 string `json:"line2"`
	} `json:"firewall"`
	Switch struct {
		Title      string `json:"title"`
		TrunkLabel string `json:"trunkLabel"`
	} `json:"switch"`
	Remote Card     `json:"remote"`
	DMZ    Card     `json:"dmz"`
	Groups []Card   `json:"groups"`
	Legend []Legend `json:"legend"`
}

type Port struct {
	N     int    `json:"n"`
	Role  string `json:"role"`
	State string `json:"state"` // used | reserved | free
	Flag  bool   `json:"flag,omitempty"`
}

type Callout struct {
	Port  int    `json:"port"`
	Label string `json:"label"`
	Sub   string `json:"sub"`
	Dy    int    `json:"dy"`
	Top   bool   `json:"top"`
	Dx    int    `json:"dx"`
	Color string `json:"color,omitempty"`
}

type Bracket struct {
	ColFrom int    `json:"colFrom"`
	ColTo   int    `json:"colTo"`
	Color   string `json:"color"`
	StemMid bool   `json:"stemMid"`
	StemDx  int    `json:"stemDx"`
	StemLen int    `json:"stemLen"`
	Dashed  bool   `json:"dashed"`
	Anchor  string `json:"anchor"` // start | middle
	TextDx  int    `json:"textDx"`
	TitleDy int    `json:"titleDy"`
	SubDy   int    `json:"subDy"`
	Title   string `json:"title"`
	Sub     string `json:"sub"`
}

type Switch struct {
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle"`
	Chassis    string    `json:"chassis"`
	TeLabels   [2]string `json:"teLabels"`
	TeNote     string    `json:"teNote"`
	Ports      []Port    `json:"ports"`
	Callouts   []Callout `json:"callouts"`
	Brackets   []Bracket `json:"brackets"`
	Legend     []Legend  `json:"legend"`
	LegendNote string    `json:"legendNote"`
}

type Table struct {
	Title   string     `json:"title"`
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
	Note    string     `json:"note,omitempty"`
}

type List struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

func parseData(b []byte) (*Data, error) {
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if errs := d.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return &d, nil
}

func (d *Data) clone() *Data {
	b, _ := json.Marshal(d)
	var c Data
	_ = json.Unmarshal(b, &c)
	return &c
}

// marshal is the canonical on-disk form (stable key order, no HTML escaping).
func (d *Data) marshal() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(d)
	return buf.Bytes()
}

var inlineTag = regexp.MustCompile(`<[^<>]*>`)
var allowedTag = regexp.MustCompile(`^</?(b|i|code)>$`)

// checkInline allows only <b>, <i>, <code> in trusted-HTML cells.
func checkInline(where, s string) string {
	for _, t := range inlineTag.FindAllString(s, -1) {
		if !allowedTag.MatchString(t) {
			return fmt.Sprintf("%s: tag %s not allowed (only <b>, <i>, <code>)", where, t)
		}
	}
	if strings.ContainsAny(inlineTag.ReplaceAllString(s, ""), "<>") {
		return where + ": stray < or >"
	}
	return ""
}

func (d *Data) key(where, k string, errs *[]string) {
	if _, ok := d.Palette[k]; !ok {
		*errs = append(*errs, fmt.Sprintf("%s: unknown colour %q", where, k))
	}
}

// Validate returns every problem found; empty means the document renders safely.
func (d *Data) Validate() []string {
	var errs []string
	add := func(s string) {
		if s != "" {
			errs = append(errs, s)
		}
	}
	if len(d.Palette) == 0 {
		return []string{"palette is empty"}
	}
	for k, v := range d.Palette {
		if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(v) {
			errs = append(errs, "palette "+k+": bad colour "+v)
		}
	}
	if strings.TrimSpace(d.Header.Title) == "" {
		errs = append(errs, "header: title is required")
	}
	t := &d.Topology
	cards := append([]Card{t.Remote, t.DMZ}, t.Groups...)
	for i, c := range cards {
		w := fmt.Sprintf("topology card %d (%s)", i+1, c.Title)
		d.key(w, c.Color, &errs)
		if c.X < 0 || c.X > 1180 || c.Y < 0 || c.Y > 760 || c.W < 60 || c.W > 1180 {
			errs = append(errs, w+": position/size out of range")
		}
	}
	for _, l := range t.Legend {
		d.key("topology legend", l.Color, &errs)
	}
	s := &d.Switch
	if len(s.Ports) != 48 {
		errs = append(errs, fmt.Sprintf("switch: need 48 ports, have %d", len(s.Ports)))
	}
	for i, p := range s.Ports {
		w := fmt.Sprintf("port %d", i+1)
		if p.N != i+1 {
			errs = append(errs, w+": number out of order")
		}
		d.key(w, p.Role, &errs)
		if p.State != "used" && p.State != "reserved" && p.State != "free" {
			errs = append(errs, w+": state must be used, reserved or free")
		}
	}
	for _, c := range s.Callouts {
		w := fmt.Sprintf("callout port %d", c.Port)
		if c.Port < 1 || c.Port > 48 {
			errs = append(errs, w+": port must be 1-48")
		}
		if c.Color != "" {
			d.key(w, c.Color, &errs)
		}
		if c.Dy < -400 || c.Dy > 400 || c.Dx < -400 || c.Dx > 400 {
			errs = append(errs, w+": dx/dy must be within -400..400")
		}
	}
	for i, b := range s.Brackets {
		w := fmt.Sprintf("bracket %d", i+1)
		d.key(w, b.Color, &errs)
		if b.ColFrom < 0 || b.ColTo > 23 || b.ColFrom > b.ColTo {
			errs = append(errs, w+": columns must be 0-23 with from <= to")
		}
		if b.Anchor != "start" && b.Anchor != "middle" {
			errs = append(errs, w+": anchor must be start or middle")
		}
	}
	for _, l := range s.Legend {
		d.key("switch legend", l.Color, &errs)
	}
	for _, tb := range []*Table{&d.PortTable, &d.VLANs, &d.Devices} {
		for i, r := range tb.Rows {
			if len(r) != len(tb.Headers) {
				errs = append(errs, fmt.Sprintf("%s row %d: need %d cells", tb.Title, i+1, len(tb.Headers)))
			}
			for _, c := range r {
				add(checkInline(tb.Title, c))
			}
		}
		add(checkInline(tb.Title+" note", tb.Note))
	}
	for _, l := range []*List{&d.Before, &d.Hardware, &d.After, &d.Power} {
		for _, it := range l.Items {
			add(checkInline(l.Title, it))
		}
	}
	return errs
}
