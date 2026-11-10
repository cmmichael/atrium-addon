package main

// Health: one answer that says how the add-on and its house are doing, for the app's status sheet and for a
// person reading it with curl. Nothing here is new information; it is the add-on's own state in one place, so a
// Waze outage, a stale layout or a dropped Home Assistant socket is seen in the app and not only in the log.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
)

type healthReport struct {
	Version       string    `json:"version"`
	Latest        string    `json:"latest,omitempty"` // a newer add-on, when Home Assistant knows of one
	Uptime        string    `json:"uptime"`
	Started       time.Time `json:"started"`
	HomeAssistant struct {
		LastMessage *time.Time `json:"lastMessage,omitempty"` // the last word from Home Assistant on any socket or request
		AgeSeconds  int        `json:"ageSeconds"`
		Reachable   bool       `json:"reachable"` // heard from within the last two minutes
	} `json:"homeAssistant"`
	Tailnet struct {
		State   string `json:"state"` // starting, needs-login, up
		Address string `json:"address,omitempty"`
		Owner   string `json:"owner,omitempty"`
	} `json:"tailnet"`
	Mapping struct {
		Exists   bool       `json:"exists"`
		Custom   bool       `json:"custom"`
		Rooms    int        `json:"rooms"`
		Problems []string   `json:"problems"`
		Stale    int        `json:"stale"` // entities Home Assistant still lists but no integration provides
		ReadAt   *time.Time `json:"readAt,omitempty"`
	} `json:"mapping"`
	House struct {
		BuiltAt     *time.Time `json:"builtAt,omitempty"` // nil before the first build
		Stale       bool       `json:"stale"`             // the layout served is an older one: Home Assistant did not answer the last build
		Unavailable []string   `json:"unavailable"`       // entities the layout shows that Home Assistant reports unavailable
	} `json:"house"`
	Alerts struct {
		Pushes  bool `json:"pushes"` // an APNs key is in
		Devices int  `json:"devices"`
		// The socket that hears atrium_alert: subscribed now, since when, and how many times it had to connect
		// again since the add-on started. Not listening means alerts are being missed.
		Listening  bool       `json:"listening"`
		Since      *time.Time `json:"since,omitempty"`
		Reconnects int        `json:"reconnects"`
	} `json:"alerts"`
}

// serveHealth answers anyone allowed in.
func (g *Gateway) serveHealth(w http.ResponseWriter, r *http.Request, id Identity) {
	var h healthReport
	h.Version = version
	h.Started = g.started
	h.Uptime = spanWords(time.Since(g.started))
	g.mu.Lock()
	if !g.haSeen.IsZero() {
		t := g.haSeen
		h.HomeAssistant.LastMessage = &t
		h.HomeAssistant.AgeSeconds = int(time.Since(t).Seconds())
		h.HomeAssistant.Reachable = time.Since(t) < 2*time.Minute
	}
	h.Tailnet.State, h.Tailnet.Address, h.Tailnet.Owner = g.setup.State, g.setup.Address, g.Owner
	h.Mapping.Exists, h.Mapping.Custom, h.Mapping.Rooms, h.Mapping.Stale = !g.mappingAt.IsZero(), g.mapping.Custom(), g.rooms, len(g.stale)
	if !g.mappingAt.IsZero() {
		at := g.mappingAt
		h.Mapping.ReadAt = &at
	}
	h.Mapping.Problems = append([]string{}, g.problems...)
	if g.mappingErr != "" {
		h.Mapping.Problems = append([]string{g.mappingErr}, h.Mapping.Problems...)
	}
	if !g.house.at.IsZero() {
		at := g.house.at
		h.House.BuiltAt = &at
	}
	h.House.Stale = g.house.stale
	h.Alerts.Listening = g.alertsListening
	if g.alertsListening {
		since := g.alertsSince
		h.Alerts.Since = &since
	}
	h.Alerts.Reconnects = max(0, g.alertsConnects-1)
	h.House.Unavailable = append([]string{}, g.unavailable...)
	if g.updateLatest != "" && olderThan(version, g.updateLatest) {
		h.Latest = g.updateLatest
	}
	g.mu.Unlock()
	sort.Strings(h.House.Unavailable)
	if g.alerts != nil {
		h.Alerts.Pushes = g.alerts.apns != nil
		h.Alerts.Devices = len(g.alerts.list())
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(h)
}

// spanWords words a duration the way the app's diagnostics do: "4 s", "12 min", "3 h 05 min", "2 d".
func spanWords(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min", s/60)
	case s < 86400:
		return fmt.Sprintf("%d h %02d min", s/3600, s%3600/60)
	}
	return fmt.Sprintf("%d d", s/86400)
}

// heard notes a word from Home Assistant, on any path.
func (g *Gateway) heard() {
	g.mu.Lock()
	g.haSeen = time.Now()
	g.mu.Unlock()
}

// unavailableIn lists the layout's entities Home Assistant reports unavailable: the rooms' controls and
// readouts, the security and energy sensors, the remotes' players.
func unavailableIn(h house.House, states map[string]*house.State) []string {
	var ids []string
	for _, r := range h.Rooms {
		ids = append(ids, r.Entities...)
		ids = append(ids, r.Temperature, r.Humidity)
	}
	ids = append(ids, h.Security.Cameras...)
	ids = append(ids, h.Security.Locks...)
	ids = append(ids, h.Security.Entries...)
	ids = append(ids, h.Security.Hazards...)
	ids = append(ids, h.Energy.LoadPower...)
	ids = append(ids, h.Energy.Consumers...)
	ids = append(ids, h.Energy.SolarPower, h.Energy.GridPower, h.Energy.BatteryPower, h.Energy.WaterFlow, h.Energy.WaterToday)
	for _, r := range h.Remotes {
		ids = append(ids, r.Player)
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if s := states[id]; s != nil && s.State == "unavailable" {
			out = append(out, id)
		}
	}
	return out
}
