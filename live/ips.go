// Package live adds read-only live status to the map: a poller that reads the switch
// (through the switchread connector) and pings the devices named in the map, and change
// alerts derived from comparing successive snapshots. Nothing here ever writes to the
// switch or to the map data.
package live

import (
	"net"
	"regexp"
	"sort"
	"strings"
)

var (
	tagRe  = regexp.MustCompile(`<[^>]*>`)
	quadRe = regexp.MustCompile(`\d{1,3}(?:\.\d{1,3}){3}`)
	// a quad followed by "-20", ".x", "/24" or ".." is a range or a network, not a host
	rangeAfterRe = regexp.MustCompile(`^\s*(?:[-–—~]\s*\d|\.\.|\.[xX*]|/\d)`)
	dhcpRe       = regexp.MustCompile(`(?i)dhcp`)
)

// ExtractIPs returns the distinct IPv4 host addresses written in the given table cells
// (HTML tags are ignored). Ranges ("10.0.0.5-20", "192.0.2.0/24", "192.0.2.x") and any
// cell that talks about DHCP are skipped. The result is sorted.
func ExtractIPs(cells ...string) []string {
	set := map[string]bool{}
	for _, c := range cells {
		txt := tagRe.ReplaceAllString(c, " ")
		if dhcpRe.MatchString(txt) {
			continue
		}
		for _, loc := range quadRe.FindAllStringIndex(txt, -1) {
			q := txt[loc[0]:loc[1]]
			if loc[0] > 0 && (txt[loc[0]-1] >= '0' && txt[loc[0]-1] <= '9') {
				continue
			}
			if rangeAfterRe.MatchString(txt[loc[1]:]) {
				continue
			}
			ip := net.ParseIP(q)
			if ip == nil || ip.To4() == nil {
				continue // octet above 255, or leading zeros the parser refuses
			}
			set[ip.To4().String()] = true
		}
	}
	out := make([]string, 0, len(set))
	for ip := range set {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}

// Union merges address lists, sorted and de-duplicated.
func Union(lists ...[]string) []string {
	set := map[string]bool{}
	for _, l := range lists {
		for _, ip := range l {
			set[strings.TrimSpace(ip)] = true
		}
	}
	delete(set, "")
	out := make([]string, 0, len(set))
	for ip := range set {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}
