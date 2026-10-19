package main

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	giPrefix  = regexp.MustCompile(`^Gi\d+/\d+/`)
	rangeTok  = regexp.MustCompile(`^(\d+)\s*[-–—]\s*(\d+)$`)
	numTok    = regexp.MustCompile(`^\d+$`)
	splitToks = regexp.MustCompile(`[+,&]`)
)

// rowPorts lists the front-panel ports named by a port-table first cell such as
// "<b>Gi1/0/2 + 3</b>" or "Gi1/0/12–18". Non-Gi rows (Te1/0/1–2) yield nothing.
func rowPorts(first string) []int {
	txt := strings.TrimSpace(inlineTag.ReplaceAllString(first, ""))
	loc := giPrefix.FindStringIndex(txt)
	if loc == nil {
		return nil
	}
	var out []int
	for _, tok := range splitToks.Split(txt[loc[1]:], -1) {
		tok = strings.TrimSpace(tok)
		if m := rangeTok.FindStringSubmatch(tok); m != nil {
			a, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[2])
			for i := a; i <= b && i-a < 64; i++ {
				out = append(out, i)
			}
		} else if numTok.MatchString(tok) {
			n, _ := strconv.Atoi(tok)
			out = append(out, n)
		}
	}
	return out
}

// findPortRow returns the table row that describes port n: the row naming only that
// port if there is one (exact), else the narrowest group row containing it.
func (t *Table) findPortRow(n int) (idx int, exact bool) {
	best, bestSize := -1, 1<<30
	for i, r := range t.Rows {
		if len(r) == 0 {
			continue
		}
		ps := rowPorts(r[0])
		for _, p := range ps {
			if p == n && len(ps) < bestSize {
				best, bestSize = i, len(ps)
			}
		}
	}
	return best, bestSize == 1
}

// portCells are the table columns the port form edits: device, address, notes.
type portCells struct{ Device, IP, Notes string }

func (t *Table) cellsFor(n int) portCells {
	i, _ := t.findPortRow(n)
	if i < 0 {
		return portCells{}
	}
	r := t.Rows[i]
	get := func(j int) string {
		if j < len(r) {
			return r[j]
		}
		return ""
	}
	return portCells{get(1), get(2), get(3)}
}

func (t *Table) gigPrefix() string {
	for _, r := range t.Rows {
		if len(r) > 0 {
			if m := giPrefix.FindString(strings.TrimSpace(inlineTag.ReplaceAllString(r[0], ""))); m != "" {
				return m
			}
		}
	}
	return "Gi1/0/"
}

// setPortCells makes the port table agree with the port form. The old cells are what the
// form showed; an unchanged form never touches the table. A port that only appears inside a
// group row ("Gi1/0/12–18") gets its own row when its values are changed.
func (t *Table) setPortCells(n int, old, now portCells) {
	if old == now {
		return
	}
	width := len(t.Headers)
	if width < 4 {
		return // not the standard port table; leave it alone
	}
	i, exact := t.findPortRow(n)
	if i >= 0 && exact {
		r := t.Rows[i]
		r[1], r[2], r[3] = now.Device, now.IP, now.Notes
		return
	}
	if now == (portCells{}) {
		return
	}
	row := make([]string, width)
	row[0] = "<b>" + t.gigPrefix() + strconv.Itoa(n) + "</b>"
	row[1], row[2], row[3] = now.Device, now.IP, now.Notes
	// keep numeric order: before the first Gi row whose lowest port is above n; Te rows stay last
	at := len(t.Rows)
	for j, r := range t.Rows {
		ps := rowPorts(r[0])
		if len(ps) == 0 {
			if strings.HasPrefix(strings.TrimSpace(inlineTag.ReplaceAllString(r[0], "")), "Te") {
				at = j
				break
			}
			continue
		}
		lo := ps[0]
		for _, p := range ps {
			lo = min(lo, p)
		}
		if lo > n {
			at = j
			break
		}
	}
	t.Rows = append(t.Rows, nil)
	copy(t.Rows[at+1:], t.Rows[at:])
	t.Rows[at] = row
}
