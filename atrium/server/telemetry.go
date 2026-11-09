package main

// The apps' diagnostics: when an app launched and why (iOS having ended the previous run, say), when it went to
// the background and came back, how long each connection took and why it dropped. Each app keeps its own lines and
// hands the new ones to this add-on (POST /atrium/telemetry) and nowhere else. The add-on logs them, so a reconnect
// that takes too long can be read about in Settings → Apps → Atrium → Log, and keeps the latest per device for
// GET /atrium/telemetry (owner and administrators). Nothing about the house is in them.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type telemetryEvent struct {
	ID     int       `json:"id"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

type telemetryBatch struct {
	Device string           `json:"device"` // the hardware model ("iPhone17,1")
	OS     string           `json:"os"`
	App    string           `json:"app"`
	Events []telemetryEvent `json:"events"`
}

// telemetryDevice is one app install as the add-on has seen it: who, on what, and its latest lines.
type telemetryDevice struct {
	Login  string           `json:"login"`
	Device string           `json:"device"`
	Model  string           `json:"model"`
	OS     string           `json:"os"`
	App    string           `json:"app"`
	Seen   time.Time        `json:"seen"`
	Events []telemetryEvent `json:"events"`
}

type telemetry struct {
	mu      sync.Mutex
	devices map[string]*telemetryDevice
}

const telemetryKeep = 200 // lines per device

// serveTelemetry: POST takes a batch from an app (any allowed caller: it is about their own app); GET lists what
// the add-on holds, for the owner and administrators.
func (g *Gateway) serveTelemetry(w http.ResponseWriter, r *http.Request, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case "POST":
		data, err := io.ReadAll(io.LimitReader(r.Body, 256<<10+1))
		if err != nil || len(data) > 256<<10 {
			http.Error(w, "a batch is at most 256 KiB", http.StatusRequestEntityTooLarge)
			return
		}
		var b telemetryBatch
		if err := json.Unmarshal(data, &b); err != nil {
			http.Error(w, "not a batch: "+err.Error(), http.StatusBadRequest)
			return
		}
		g.telemetry.take(id, b)
		w.WriteHeader(http.StatusAccepted)
	case "GET":
		if !g.mayConfigure(id) {
			http.Error(w, "only the owner and administrators may read the diagnostics", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g.telemetry.all())
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

func (t *telemetry) take(id Identity, b telemetryBatch) {
	key := id.Login + " · " + firstNonEmpty(id.Device, b.Device)
	t.mu.Lock()
	if t.devices == nil {
		t.devices = map[string]*telemetryDevice{}
	}
	d := t.devices[key]
	if d == nil {
		d = &telemetryDevice{Login: id.Login, Device: id.Device}
		t.devices[key] = d
	}
	d.Model, d.OS, d.App, d.Seen = b.Device, b.OS, b.App, time.Now()
	d.Events = append(d.Events, b.Events...)
	if len(d.Events) > telemetryKeep {
		d.Events = d.Events[len(d.Events)-telemetryKeep:]
	}
	t.mu.Unlock()
	// One line each in the add-on's log: the place to read them after the fact.
	for _, e := range b.Events {
		line := e.Kind
		if e.Detail != "" {
			line += ": " + e.Detail
		}
		log.Printf("app %s (%s, %s, app %s) %s · %s", id.Login, firstNonEmpty(id.Device, b.Device), b.OS, b.App, e.Time.Local().Format("15:04:05"), strings.TrimSpace(line))
	}
}

func (t *telemetry) all() []*telemetryDevice {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*telemetryDevice, 0, len(t.devices))
	for _, d := range t.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seen.After(out[j].Seen) })
	return out
}

func (d telemetryDevice) String() string {
	return fmt.Sprintf("%s on %s (%s)", d.Login, d.Model, d.Device)
}
