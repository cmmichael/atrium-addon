package main

// Alerts: Home Assistant's automations fire an `atrium_alert` event, and the add-on puts it on every phone as a
// notification (and, when asked, as a Live Activity in the Dynamic Island and on the Lock Screen), whether the
// app is open or not. The rules stay in Home Assistant, where they belong: a door unlocked after nine, bins not
// out on collection day, the water shut off. This file only delivers.
//
//	event: atrium_alert
//	event_data:
//	  id: front-door-night            # the same id again replaces the earlier notification; with clear: true it is withdrawn
//	  title: Front door unlocked
//	  body: Unlocked since 9:14 PM
//	  level: warning                  # info | warning | critical (critical and warning are time-sensitive: they show through Focus)
//	  icon: lock.open                 # an SF Symbol, for the Live Activity
//	  entity: lock.front_door         # the app opens it when the notification is tapped
//	  action: { title: Lock, service: lock.lock, entity: lock.front_door }   # one button on the notification
//	  live: true                      # a Live Activity too, until cleared or 8 hours
//	  to: [owner@example.com]         # these logins' phones only; default: every phone that has signed in
//
// The apps register their device with POST /atrium/devices (the APNs token, the Live Activity push-to-start
// token, the tokens of running activities). The owner's APNs key does the sending (apns.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// pushDevice is one app install that may receive alerts.
type pushDevice struct {
	Login      string            `json:"login"`
	Device     string            `json:"device"` // Tailscale's name for it, or "Home Assistant" through ingress
	Model      string            `json:"model"`
	Env        string            `json:"env"`                  // production (TestFlight, the App Store) or sandbox (a development build)
	Token      string            `json:"token"`                // the APNs device token
	LiveStart  string            `json:"liveStart,omitempty"`  // the push-to-start token for Live Activities (iOS 17.2 and later)
	Activities map[string]string `json:"activities,omitempty"` // alert id → the running Live Activity's push token
	Seen       time.Time         `json:"seen"`
}

// alert is what an automation sends.
type alert struct {
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	Body   string       `json:"body"`
	Level  string       `json:"level"`
	Icon   string       `json:"icon"`
	Entity string       `json:"entity"`
	Action *alertAction `json:"action,omitempty"`
	Live   bool         `json:"live"`
	Clear  bool         `json:"clear"`
	To     []string     `json:"to"`
	At     time.Time    `json:"at"`
}

type alertAction struct {
	Title   string         `json:"title"`
	Service string         `json:"service"` // "lock.lock"
	Entity  string         `json:"entity"`
	Data    map[string]any `json:"data,omitempty"`
}

type alerts struct {
	mu      sync.Mutex
	path    string
	devices []*pushDevice
	recent  []alert
	apns    *apnsClient
	home    string // the house's name, on the notification
}

func (a *alerts) load() {
	if a.path == "" {
		return
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		return
	}
	a.mu.Lock()
	json.Unmarshal(data, &a.devices)
	a.mu.Unlock()
}

func (a *alerts) save() {
	if a.path == "" {
		return
	}
	data, _ := json.MarshalIndent(a.devices, "", "  ")
	os.WriteFile(a.path, data, 0o600)
}

// register keeps a device's tokens: one record per login and device.
func (a *alerts) register(id Identity, body struct {
	Token      string            `json:"token"`
	LiveStart  string            `json:"liveStart"`
	Model      string            `json:"model"`
	Env        string            `json:"env"`
	Activities map[string]string `json:"activities"`
}) *pushDevice {
	a.mu.Lock()
	defer a.mu.Unlock()
	var d *pushDevice
	for _, x := range a.devices {
		// The same phone: the Live Activity token often arrives before the device token, so an empty token matches.
		if x.Login == id.Login && x.Device == id.Device && (x.Token == "" || x.Token == body.Token || body.Token == "") {
			d = x
		}
	}
	if d == nil {
		d = &pushDevice{Login: id.Login, Device: id.Device}
		a.devices = append(a.devices, d)
	}
	if body.Token != "" {
		d.Token = body.Token
	}
	if body.LiveStart != "" {
		d.LiveStart = body.LiveStart
	}
	if body.Model != "" {
		d.Model = body.Model
	}
	if body.Env != "" {
		d.Env = body.Env
	}
	for alertID, token := range body.Activities {
		if d.Activities == nil {
			d.Activities = map[string]string{}
		}
		if token == "" {
			delete(d.Activities, alertID)
		} else {
			d.Activities[alertID] = token
		}
	}
	d.Seen = time.Now()
	a.save()
	return d
}

func (a *alerts) forget(token string) {
	a.mu.Lock()
	kept := a.devices[:0]
	for _, d := range a.devices {
		if d.Token != token {
			kept = append(kept, d)
		}
	}
	a.devices = kept
	a.save()
	a.mu.Unlock()
}

// serveDevices: POST registers the caller's device (anyone allowed in: it is their own phone); GET lists the
// devices for the owner and administrators, tokens shortened.
func (g *Gateway) serveDevices(w http.ResponseWriter, r *http.Request, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case "POST":
		var body struct {
			Token      string            `json:"token"`
			LiveStart  string            `json:"liveStart"`
			Model      string            `json:"model"`
			Env        string            `json:"env"`
			Activities map[string]string `json:"activities"`
		}
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err := json.Unmarshal(data, &body); err != nil {
			http.Error(w, "not a registration: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Token == "" && body.LiveStart == "" && len(body.Activities) == 0 {
			http.Error(w, "a registration carries a token", http.StatusBadRequest)
			return
		}
		d := g.alerts.register(id, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"registered": true, "device": d.Device, "pushes": g.alerts.apns != nil})
	case "GET":
		if !g.mayConfigure(id) {
			http.Error(w, "only the owner and administrators may list the devices", http.StatusForbidden)
			return
		}
		g.alerts.mu.Lock()
		out := make([]map[string]any, 0, len(g.alerts.devices))
		for _, d := range g.alerts.devices {
			out = append(out, map[string]any{"login": d.Login, "device": d.Device, "model": d.Model, "env": d.Env, "token": shortToken(d.Token) + "…",
				"liveActivities": d.LiveStart != "", "running": len(d.Activities), "seen": d.Seen})
		}
		g.alerts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"pushes": g.alerts.apns != nil, "devices": out})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

// serveAlerts: GET lists the latest alerts (anyone allowed in); POST raises one from outside Home Assistant's
// event bus (the owner and administrators; a test from the set-up page, say).
func (g *Gateway) serveAlerts(w http.ResponseWriter, r *http.Request, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case "GET":
		g.alerts.mu.Lock()
		recent := append([]alert{}, g.alerts.recent...)
		g.alerts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(recent)
	case "POST":
		if !g.mayConfigure(id) {
			http.Error(w, "only the owner and administrators may raise an alert here", http.StatusForbidden)
			return
		}
		var al alert
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err := json.Unmarshal(data, &al); err != nil {
			http.Error(w, "not an alert: "+err.Error(), http.StatusBadRequest)
			return
		}
		sent, errs := g.alerts.raise(al)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"sent": sent, "problems": errs})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

// watchAlerts keeps one WebSocket to Home Assistant for `atrium_alert` events, for as long as the add-on runs.
func (g *Gateway) watchAlerts() {
	pause := time.Second
	for {
		err := g.alertEvents(context.Background())
		if err != nil {
			log.Printf("alerts: %v; listening again in %s", err, pause)
		}
		time.Sleep(pause)
		if pause < 30*time.Second {
			pause *= 2
		}
	}
}

func (g *Gateway) alertEvents(ctx context.Context) error {
	c, _, err := websocket.Dial(ctx, g.Addon.WS, nil)
	if err != nil {
		return err
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	read := func() (map[string]any, error) {
		_, data, err := c.Read(ctx)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, json.Unmarshal(data, &m)
	}
	if _, err := read(); err != nil {
		return err
	}
	auth, _ := json.Marshal(map[string]any{"type": "auth", "access_token": g.Addon.Token})
	if err := c.Write(ctx, websocket.MessageText, auth); err != nil {
		return err
	}
	if m, err := read(); err != nil || m["type"] != "auth_ok" {
		return fmt.Errorf("Home Assistant refused the add-on's token")
	}
	sub, _ := json.Marshal(map[string]any{"id": 1, "type": "subscribe_events", "event_type": "atrium_alert"})
	if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
		return err
	}
	for {
		m, err := read()
		if err != nil {
			return err
		}
		if m["type"] != "event" {
			continue
		}
		ev, _ := m["event"].(map[string]any)
		data, _ := json.Marshal(ev["data"])
		var al alert
		if err := json.Unmarshal(data, &al); err != nil {
			log.Printf("alerts: an atrium_alert event the add-on could not read: %v", err)
			continue
		}
		sent, errs := g.alerts.raise(al)
		if al.Clear {
			log.Printf("cleared alert %s: %d sent%s", firstNonEmpty(al.ID, al.Title), sent, joinProblems(errs))
		} else {
			button := ""
			if al.Action != nil {
				button = fmt.Sprintf(" · button %s (%s)", category(al), al.Action.Service)
			}
			log.Printf("alert %q (%s, %s): %d sent%s%s", al.Title, al.ID, al.Level, sent, button, joinProblems(errs))
		}
	}
}

func joinProblems(errs []string) string {
	if len(errs) == 0 {
		return ""
	}
	return "; " + strings.Join(errs, "; ")
}

// raise sends one alert to every phone it is for: a notification (or its withdrawal), and a Live Activity when
// asked. Returns how many pushes went out and what went wrong.
func (a *alerts) raise(al alert) (sent int, problems []string) {
	if al.ID == "" {
		al.ID = strings.ToLower(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return '-'
		}, al.Title))
	}
	if al.ID == "" {
		return 0, []string{"an alert needs a title or an id"}
	}
	al.At = time.Now()
	a.mu.Lock()
	a.recent = append(a.recent, al)
	if len(a.recent) > 50 {
		a.recent = a.recent[len(a.recent)-50:]
	}
	var to []*pushDevice
	for _, d := range a.devices {
		if len(al.To) > 0 && !containsFold(al.To, d.Login) {
			continue
		}
		if d.Token != "" || d.LiveStart != "" || d.Activities[al.ID] != "" {
			to = append(to, d)
		}
	}
	home := a.home
	a.mu.Unlock()
	if a.apns == nil {
		return 0, []string{"no APNs key in the add-on's options: alerts are logged only"}
	}
	if len(to) == 0 {
		return 0, []string{"no phone has registered for alerts yet"}
	}
	var gone []string
	for _, d := range to {
		note := func(err error) {
			if err == nil {
				sent++
				return
			}
			var ae *apnsError
			if ok := asAPNSError(err, &ae); ok && ae.status == 410 {
				gone = append(gone, d.Token)
			}
			problems = append(problems, fmt.Sprintf("%s (%s): %v", d.Login, d.Device, err))
		}
		if d.Token != "" {
			if al.Clear {
				note(a.apns.send(push{token: d.Token, env: d.Env, pushType: "background", collapse: al.ID, priority: 5,
					payload: map[string]any{"aps": map[string]any{"content-available": 1}, "atrium": map[string]any{"clear": al.ID}}}))
			} else {
				note(a.apns.send(push{token: d.Token, env: d.Env, pushType: "alert", collapse: al.ID, payload: notificationPayload(al, home)}))
			}
		}
		if al.Live || al.Clear {
			if running := d.Activities[al.ID]; running != "" {
				note(a.apns.send(push{token: running, env: d.Env, pushType: "liveactivity", payload: activityPayload(al, home, al.Clear)}))
			} else if al.Live && !al.Clear && d.LiveStart != "" {
				note(a.apns.send(push{token: d.LiveStart, env: d.Env, pushType: "liveactivity", payload: activityStartPayload(al, home)}))
			}
		}
	}
	for _, t := range gone {
		a.forget(t) // Apple says the app is gone from that phone
	}
	return sent, problems
}

func asAPNSError(err error, out **apnsError) bool {
	e, ok := err.(*apnsError)
	if ok {
		*out = e
	}
	return ok
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// interruption is how loudly iOS shows the alert: critical and warning break through Focus (time-sensitive, an
// entitlement the app has); info does not. (Apple's "critical" level that overrides silence needs an approval
// Atrium does not have.)
func interruption(level string) string {
	switch level {
	case "critical", "warning":
		return "time-sensitive"
	case "info":
		return "active"
	}
	return "active"
}

// category picks the button the app shows under the notification, by what the action does; the app registered
// these names at launch and runs the action itself when pressed.
func category(al alert) string {
	if al.Action == nil {
		return "atrium.alert"
	}
	svc := al.Action.Service
	// A door kept unlocked by a rule (UniFi Access: select.front_door_lock_rule) is locked again by choosing the
	// locking option; the button should say so.
	if svc == "select.select_option" {
		if opt, _ := al.Action.Data["option"].(string); strings.Contains(strings.ToLower(opt), "lock") || opt == "reset" {
			return "atrium.lock"
		}
	}
	entity := strings.ToLower(al.Action.Entity)
	switch {
	case svc == "input_boolean.turn_on", svc == "input_boolean.turn_off", svc == "todo.update_item":
		return "atrium.done" // a chore or a flag: "Done"
	// Opening a way into the house from a notification: its own category, which the app marks destructive and
	// behind Face ID, so a phone in the wrong hands cannot open the door from the Lock Screen.
	case strings.HasSuffix(svc, ".unlock"), svc == "button.press" && strings.Contains(entity, "unlock"), strings.HasSuffix(svc, ".open_cover"), strings.HasSuffix(svc, ".open_valve"):
		return "atrium.unlock"
	case strings.HasSuffix(svc, ".lock"):
		return "atrium.lock"
	case strings.HasSuffix(svc, ".close_cover"), strings.HasSuffix(svc, ".close_valve"):
		return "atrium.close"
	case strings.Contains(svc, "alarm_arm"):
		return "atrium.arm"
	case strings.HasSuffix(svc, ".turn_off"):
		return "atrium.off"
	case strings.HasSuffix(svc, ".turn_on"):
		return "atrium.on"
	}
	return "atrium.act"
}

func notificationPayload(al alert, home string) map[string]any {
	aps := map[string]any{
		"alert":              map[string]any{"title": al.Title, "body": al.Body, "subtitle": home},
		"sound":              "default",
		"interruption-level": interruption(al.Level),
		"thread-id":          al.ID,
		"category":           category(al),
	}
	if al.Level == "critical" {
		aps["relevance-score"] = 1
	}
	return map[string]any{"aps": aps, "atrium": alertExtras(al)}
}

func alertExtras(al alert) map[string]any {
	x := map[string]any{"id": al.ID, "level": al.Level, "entity": al.Entity, "icon": al.Icon}
	if al.Action != nil {
		x["action"] = al.Action
	}
	return x
}

// The Live Activity: attributes fixed for its life, a content state that updates. The names match the app's
// AlertActivityAttributes.
func activityAttributes(al alert, home string) (map[string]any, map[string]any) {
	attrs := map[string]any{"id": al.ID, "title": al.Title, "icon": firstNonEmpty(al.Icon, "exclamationmark.triangle.fill"), "level": firstNonEmpty(al.Level, "warning"), "entity": al.Entity, "house": home}
	state := map[string]any{"body": al.Body, "since": al.At.Unix(), "cleared": al.Clear}
	return attrs, state
}

func activityStartPayload(al alert, home string) map[string]any {
	attrs, state := activityAttributes(al, home)
	return map[string]any{"aps": map[string]any{
		"timestamp": time.Now().Unix(), "event": "start", "content-state": state,
		"attributes-type": "AlertActivityAttributes", "attributes": attrs,
		"alert":          map[string]any{"title": al.Title, "body": al.Body},
		"stale-date":     time.Now().Add(8 * time.Hour).Unix(),
		"dismissal-date": time.Now().Add(8 * time.Hour).Unix(),
	}}
}

func activityPayload(al alert, home string, end bool) map[string]any {
	_, state := activityAttributes(al, home)
	aps := map[string]any{"timestamp": time.Now().Unix(), "event": "update", "content-state": state}
	if end {
		aps["event"] = "end"
		aps["dismissal-date"] = time.Now().Add(30 * time.Second).Unix()
	} else {
		aps["alert"] = map[string]any{"title": al.Title, "body": al.Body}
	}
	return map[string]any{"aps": aps}
}

// For the set-up page and tests: the devices, oldest first.
func (a *alerts) list() []*pushDevice {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := append([]*pushDevice{}, a.devices...)
	sort.Slice(out, func(i, j int) bool { return out[i].Seen.Before(out[j].Seen) })
	return out
}
