// Package switchread reads a Cisco IOS 15.x switch, strictly read-only: it parses the
// output of four show commands and can fetch them over SSH using a hard allowlist.
package switchread

import (
	"regexp"
	"strconv"
	"strings"
)

// IfStatus is one row of "show interfaces status".
type IfStatus struct {
	Port   string // as printed, e.g. Gi1/0/12
	Name   string // the interface description (IOS truncates it to 18 characters)
	Status string // connected, notconnect, suspended, disabled, err-disabled ...
	Vlan   string // a VLAN number, "trunk" or "routed"
	Duplex string
	Speed  string
	Type   string
}

// Connected reports whether the link is up.
func (s IfStatus) Connected() bool { return s.Status == "connected" }

// VLAN is one row of "show vlan brief".
type VLAN struct {
	ID     int
	Name   string
	Status string
	Ports  []string
}

// Trunk is one trunk port from "show interfaces trunk".
type Trunk struct {
	Port       string
	Mode       string
	Encap      string
	Status     string
	Native     string
	Allowed    string // VLANs allowed on trunk
	Active     string // allowed and active in management domain
	Forwarding string // in spanning-tree forwarding state and not pruned
}

// MACEntry is one row of "show mac address-table dynamic".
type MACEntry struct {
	Vlan int
	MAC  string
	Type string
	Port string
}

func lines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// ifaceRe matches an interface name such as Gi1/0/1, Te1/0/2, Po3 or Fa0.
var ifaceRe = regexp.MustCompile(`^[A-Za-z]{2,}[0-9][0-9/.]*$`)

// ParseInterfacesStatus parses "show interfaces status". Columns are located from the header
// line because descriptions contain spaces.
func ParseInterfacesStatus(out string) []IfStatus {
	var res []IfStatus
	nameAt, statusAt := -1, -1
	for _, ln := range lines(out) {
		if nameAt < 0 {
			if strings.HasPrefix(ln, "Port") && strings.Contains(ln, "Name") && strings.Contains(ln, "Status") {
				nameAt, statusAt = strings.Index(ln, "Name"), strings.Index(ln, "Status")
			}
			continue
		}
		if len(ln) <= statusAt {
			continue
		}
		port := strings.TrimSpace(ln[:nameAt])
		if !ifaceRe.MatchString(port) {
			continue
		}
		f := strings.Fields(ln[statusAt:])
		if len(f) < 4 {
			continue
		}
		res = append(res, IfStatus{
			Port: port, Name: strings.TrimSpace(ln[nameAt:statusAt]),
			Status: f[0], Vlan: f[1], Duplex: f[2], Speed: f[3], Type: strings.Join(f[4:], " "),
		})
	}
	return res
}

var vlanRowRe = regexp.MustCompile(`^(\d+)\s+(\S+)\s+(\S+)(?:\s+(.*))?$`)

func splitPorts(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseVLANBrief parses "show vlan brief", including wrapped port lists.
func ParseVLANBrief(out string) []VLAN {
	var res []VLAN
	started := false
	for _, ln := range lines(out) {
		if strings.HasPrefix(ln, "---- ") {
			started = true
			continue
		}
		if !started || strings.TrimSpace(ln) == "" {
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' { // continuation of the previous port list
			if len(res) > 0 {
				res[len(res)-1].Ports = append(res[len(res)-1].Ports, splitPorts(ln)...)
			}
			continue
		}
		m := vlanRowRe.FindStringSubmatch(strings.TrimRight(ln, " "))
		if m == nil {
			continue
		}
		id, _ := strconv.Atoi(m[1])
		res = append(res, VLAN{ID: id, Name: m[2], Status: m[3], Ports: splitPorts(m[4])})
	}
	return res
}

// ParseTrunks parses "show interfaces trunk" (all four tables).
func ParseTrunks(out string) []Trunk {
	var res []Trunk
	idx := map[string]int{}
	section := 0
	last := ""
	for _, ln := range lines(out) {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if strings.HasPrefix(ln, "Port") {
			section++
			last = ""
			continue
		}
		if section == 0 {
			continue
		}
		cont := ln[0] == ' ' || ln[0] == '\t'
		f := strings.Fields(ln)
		if section == 1 {
			if len(f) >= 5 && ifaceRe.MatchString(f[0]) {
				idx[f[0]] = len(res)
				res = append(res, Trunk{Port: f[0], Mode: f[1], Encap: f[2], Status: f[3], Native: f[4]})
			}
			continue
		}
		var port, vlans string
		if cont {
			port, vlans = last, strings.Join(f, "")
		} else if len(f) >= 2 && ifaceRe.MatchString(f[0]) {
			port, vlans = f[0], strings.Join(f[1:], "")
			last = port
		} else {
			continue
		}
		i, ok := idx[port]
		if !ok {
			continue
		}
		t := &res[i]
		switch section {
		case 2:
			t.Allowed += vlans
		case 3:
			t.Active += vlans
		case 4:
			t.Forwarding += vlans
		}
	}
	return res
}

var macRowRe = regexp.MustCompile(`^\s*(\d+)\s+([0-9a-fA-F]{4}\.[0-9a-fA-F]{4}\.[0-9a-fA-F]{4})\s+(\S+)\s+(\S+)\s*$`)

// ParseMACTable parses "show mac address-table dynamic".
func ParseMACTable(out string) []MACEntry {
	var res []MACEntry
	for _, ln := range lines(out) {
		m := macRowRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		v, _ := strconv.Atoi(m[1])
		res = append(res, MACEntry{Vlan: v, MAC: strings.ToLower(m[2]), Type: m[3], Port: m[4]})
	}
	return res
}

// PortNumber maps an interface name to a front-panel position: Gi1/0/N is port N (1-48);
// Te1/0/N is ten-gig N. Anything else (Po1, Fa0, uplink SFPs beyond 48) is not mapped.
func PortNumber(name string) (n int, te int, ok bool) {
	var kind string
	var a, b, c int
	switch {
	case strings.HasPrefix(name, "Gi"):
		kind = "Gi"
	case strings.HasPrefix(name, "Te"):
		kind = "Te"
	default:
		return 0, 0, false
	}
	parts := strings.Split(name[2:], "/")
	if len(parts) != 3 {
		return 0, 0, false
	}
	var err error
	if a, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, false
	}
	if b, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, false
	}
	if c, err = strconv.Atoi(parts[2]); err != nil {
		return 0, 0, false
	}
	_, _ = a, b
	if kind == "Gi" {
		if c < 1 || c > 48 {
			return 0, 0, false
		}
		return c, 0, true
	}
	if c < 1 || c > 2 {
		return 0, 0, false
	}
	return 0, c, true
}

// Snapshot is everything read from the switch in one session.
type Snapshot struct {
	Status []IfStatus
	VLANs  []VLAN
	Trunks []Trunk
	MACs   []MACEntry
}

// MACsByPort groups learned MAC addresses (deduplicated across VLANs) by interface name.
func (s *Snapshot) MACsByPort() map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, m := range s.MACs {
		k := m.Port + "|" + m.MAC
		if seen[k] {
			continue
		}
		seen[k] = true
		out[m.Port] = append(out[m.Port], m.MAC)
	}
	return out
}
