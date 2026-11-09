package main

import (
	"testing"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
)

func TestHealthSaysHowTheHouseIsDoing(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com", Owner: true})
	g.started = time.Now().Add(-90 * time.Minute)
	g.setup = Setup{State: "up", Address: "https://atrium.tail1234.ts.net"}
	g.heard()
	g.mu.Lock()
	g.rooms, g.problems, g.rendered = 3, []string{"security: no camera.x"}, true
	g.unavailable = []string{"sensor.waze_school"}
	g.updateLatest = "9.9.9"
	g.mu.Unlock()
	code, body := get(t, g, "/atrium/health")
	if code != 200 || body["version"] != version || body["latest"] != "9.9.9" || body["uptime"] != "1 h 30 min" {
		t.Fatalf("health answered %d %v", code, body)
	}
	haPart := body["homeAssistant"].(map[string]any)
	if haPart["reachable"] != true {
		t.Errorf("Home Assistant not reachable: %v", haPart)
	}
	if m := body["mapping"].(map[string]any); m["rooms"] != 3.0 || len(m["problems"].([]any)) != 1 {
		t.Errorf("mapping: %v", m)
	}
	if h := body["house"].(map[string]any); len(h["unavailable"].([]any)) != 1 {
		t.Errorf("house: %v", h)
	}
	if tn := body["tailnet"].(map[string]any); tn["state"] != "up" || tn["owner"] != "owner@example.com" {
		t.Errorf("tailnet: %v", tn)
	}
	// The collector: a room's control reported unavailable is listed once; others are not.
	h := house.House{Rooms: []house.Room{{ID: "den", Entities: []string{"light.a", "light.b", "light.a"}, Temperature: "sensor.t"}}}
	states := map[string]*house.State{"light.a": {State: "unavailable"}, "light.b": {State: "on"}, "sensor.t": {State: "unavailable"}}
	if got := unavailableIn(h, states); len(got) != 2 || got[0] != "light.a" || got[1] != "sensor.t" {
		t.Errorf("unavailable: %v", got)
	}
	if spanWords(45*time.Second) != "45 s" || spanWords(26*time.Hour) != "1 d" || spanWords(5*time.Minute) != "5 min" {
		t.Error("the duration words")
	}
}
