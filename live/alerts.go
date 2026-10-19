package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const maxAlerts = 500

// Alerts is the persistent alert list. It lives outside the git-tracked data directory.
type Alerts struct {
	path string
	mu   sync.Mutex
	list []Alert
}

// OpenAlerts loads (or starts) the alert file at path. An empty path keeps alerts in memory only.
func OpenAlerts(path string) (*Alerts, error) {
	a := &Alerts{path: path}
	if path == "" {
		return a, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &a.list); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (a *Alerts) saveLocked() {
	if a.path == "" {
		return
	}
	b, err := json.MarshalIndent(a.list, "", " ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(a.path), 0o755) != nil {
		return
	}
	tmp := a.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, a.path)
	}
}

// Add stores x unless an alert with the same key already exists (open, accepted or
// ignored: a repeat is never raised twice). It reports whether x was new.
func (a *Alerts) Add(x Alert) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.list {
		if e.Key == x.Key {
			return false
		}
	}
	a.list = append(a.list, x)
	if len(a.list) > maxAlerts { // drop the oldest ones that are no longer open
		kept := a.list[:0]
		drop := len(a.list) - maxAlerts
		for _, e := range a.list {
			if drop > 0 && e.Status != StatusOpen {
				drop--
				continue
			}
			kept = append(kept, e)
		}
		a.list = kept
	}
	a.saveLocked()
	return true
}

// All returns a copy, newest first.
func (a *Alerts) All() []Alert {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := append([]Alert(nil), a.list...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// Open returns the open alerts, newest first.
func (a *Alerts) Open() []Alert {
	var out []Alert
	for _, x := range a.All() {
		if x.Status == StatusOpen {
			out = append(out, x)
		}
	}
	return out
}

func (a *Alerts) Get(id string) (Alert, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, x := range a.list {
		if x.ID == id {
			return x, true
		}
	}
	return Alert{}, false
}

// SetStatus changes one alert's status; false if the id is unknown or the status invalid.
func (a *Alerts) SetStatus(id, status string) bool {
	if status != StatusOpen && status != StatusAccepted && status != StatusIgnored {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.list {
		if a.list[i].ID == id {
			a.list[i].Status = status
			a.saveLocked()
			return true
		}
	}
	return false
}
