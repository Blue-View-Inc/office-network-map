package live

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Alert kinds.
const (
	KindNewMAC       = "new_mac"
	KindMACGone      = "mac_gone"
	KindMACMoved     = "mac_moved"
	KindLinkChange   = "link_change"
	KindDeviceOff    = "device_offline"
	KindDeviceOn     = "device_online"
	KindVlanMismatch = "vlan_mismatch"
)

// Alert statuses.
const (
	StatusOpen     = "open"
	StatusAccepted = "accepted"
	StatusIgnored  = "ignored"
)

// Alert is one detected change. Suggest holds form values ("role", "state", "device",
// "ip", "notes") that "Accept into map" pre-fills; it is only ever a suggestion.
type Alert struct {
	ID      string            `json:"id"`
	Key     string            `json:"key"`
	Time    time.Time         `json:"time"`
	Kind    string            `json:"kind"`
	Port    int               `json:"port,omitempty"`
	Details string            `json:"details"`
	Suggest map[string]string `json:"suggest,omitempty"`
	Status  string            `json:"status"`
}

func newAlert(t time.Time, key, kind string, port int, details string, suggest map[string]string) Alert {
	h := sha256.Sum256([]byte(key))
	return Alert{ID: hex.EncodeToString(h[:6]), Key: key, Time: t, Kind: kind, Port: port,
		Details: details, Suggest: suggest, Status: StatusOpen}
}

func portMap(s *Snapshot) map[int]Port {
	m := map[int]Port{}
	if s != nil {
		for _, p := range s.Ports {
			m[p.N] = p
		}
	}
	return m
}

func macOwners(ps map[int]Port) map[string]int {
	m := map[string]int{}
	for n, p := range ps {
		for _, mac := range p.MACs {
			m[mac] = n
		}
	}
	return m
}

// SpeedMbps reads an IOS speed column ("a-1000", "100", "10G", "auto"); ok is false when unknown.
func SpeedMbps(s string) (int, bool) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "a-")
	mult := 1
	if strings.HasSuffix(s, "g") {
		mult, s = 1000, strings.TrimSuffix(s, "g")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n * mult, true
}

// Diff compares the snapshot with the previous one and with the map and returns the alerts
// that follow. prev may be nil (first cycle: only level conditions can fire). m may be nil.
func Diff(prev, cur *Snapshot, m MapView, offlineAfter time.Duration) []Alert {
	var out []Alert
	t := cur.Time

	if cur.SwitchOK && prev != nil && prev.SwitchOK {
		pp, cp := portMap(prev), portMap(cur)
		po, co := macOwners(pp), macOwners(cp)
		macs := make([]string, 0, len(co)+len(po))
		for mac := range co {
			macs = append(macs, mac)
		}
		for mac := range po {
			if _, ok := co[mac]; !ok {
				macs = append(macs, mac)
			}
		}
		sort.Strings(macs)
		for _, mac := range macs {
			a, was := po[mac]
			b, is := co[mac]
			switch {
			case is && !was:
				out = append(out, newAlert(t, fmt.Sprintf("%s|%d|%s", KindNewMAC, b, mac), KindNewMAC, b,
					fmt.Sprintf("New MAC %s appeared on port %d.", mac, b),
					map[string]string{"state": "used", "device": "New device (" + mac + ")"}))
			case was && !is:
				out = append(out, newAlert(t, fmt.Sprintf("%s|%d|%s", KindMACGone, a, mac), KindMACGone, a,
					fmt.Sprintf("MAC %s is no longer seen on port %d.", mac, a), nil))
			case was && is && a != b:
				out = append(out, newAlert(t, fmt.Sprintf("%s|%d>%d|%s", KindMACMoved, a, b, mac), KindMACMoved, b,
					fmt.Sprintf("MAC %s moved from port %d to port %d.", mac, a, b),
					map[string]string{"state": "used", "device": "Moved device (" + mac + ")"}))
			}
		}
		ns := make([]int, 0, len(cp))
		for n := range cp {
			ns = append(ns, n)
		}
		sort.Ints(ns)
		for _, n := range ns {
			was, ok := pp[n]
			if !ok {
				continue
			}
			up0, up1 := was.Status == "connected", cp[n].Status == "connected"
			if up0 != up1 {
				word, sug := "went down", map[string]string(nil)
				if up1 {
					word, sug = "came up", map[string]string{"state": "used"}
				}
				out = append(out, newAlert(t, fmt.Sprintf("%s|%d|%s|%d", KindLinkChange, n, word, t.Unix()), KindLinkChange, n,
					fmt.Sprintf("Port %d link %s (%s).", n, word, cp[n].Status), sug))
			}
		}
	}

	if cur.SwitchOK && m != nil {
		for _, p := range cur.Ports {
			if p.Status != "connected" || p.Vlan == "trunk" || p.Vlan == "routed" {
				continue
			}
			want := m.LaneForVlan(p.Vlan)
			if want != "" && want != m.Lane(p.N) {
				out = append(out, newAlert(t, fmt.Sprintf("%s|%d|%s", KindVlanMismatch, p.N, p.Vlan), KindVlanMismatch, p.N,
					fmt.Sprintf("Port %d is on VLAN %s but the map shows lane %q (VLAN suggests %q).", p.N, p.Vlan, m.Lane(p.N), want),
					map[string]string{"role": want}))
			}
		}
	}

	prevDev := map[string]Device{}
	if prev != nil {
		for _, d := range prev.Devices {
			prevDev[d.IP] = d
		}
	}
	for _, d := range cur.Devices {
		port := 0
		if m != nil {
			port = m.PortForIP(d.IP)
		}
		if !d.Online && d.OfflineSince != nil && t.Sub(*d.OfflineSince) >= offlineAfter {
			out = append(out, newAlert(t, fmt.Sprintf("%s|%s|%d", KindDeviceOff, d.IP, d.OfflineSince.Unix()), KindDeviceOff, port,
				fmt.Sprintf("%s has not answered ping since %s.", d.IP, d.OfflineSince.Format("15:04:05")), nil))
		}
		if pd, ok := prevDev[d.IP]; ok && d.Online && !pd.Online && pd.OfflineSince != nil &&
			prev.Time.Sub(*pd.OfflineSince) >= offlineAfter {
			out = append(out, newAlert(t, fmt.Sprintf("%s|%s|%d", KindDeviceOn, d.IP, t.Unix()), KindDeviceOn, port,
				fmt.Sprintf("%s is back online.", d.IP), nil))
		}
	}
	return out
}
