package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var histLoc, _ = time.LoadLocation("America/Los_Angeles")

func testIndex() *histIndex {
	exposed := []string{"binary_sensor.front_door", "media_player.den_tv", "sensor.energy_today", "event.doorbell"}
	entities := []map[string]any{
		{"entity_id": "binary_sensor.front_door", "device_id": "d1"},
		{"entity_id": "media_player.den_tv", "area_id": "den"},
		{"entity_id": "lock.back_door", "area_id": "den"}, // not exposed
	}
	devices := []map[string]any{{"id": "d1", "area_id": "entry"}}
	areas := []map[string]any{{"area_id": "den", "name": "Den"}, {"area_id": "entry", "name": "Entry"}}
	states := []map[string]any{
		{"entity_id": "binary_sensor.front_door", "state": "off", "attributes": map[string]any{"friendly_name": "Front Door", "device_class": "door"}},
		{"entity_id": "media_player.den_tv", "state": "on", "attributes": map[string]any{"friendly_name": "TV"}},
		{"entity_id": "lock.back_door", "state": "locked", "attributes": map[string]any{"friendly_name": "Back Door"}},
		{"entity_id": "sensor.energy_today", "state": "12.3", "attributes": map[string]any{"friendly_name": "Energy today", "unit_of_measurement": "kWh"}},
		{"entity_id": "event.doorbell", "state": "2026-11-14T20:00:00Z", "attributes": map[string]any{"friendly_name": "Doorbell"}},
	}
	return buildHistIndex(exposed, entities, devices, areas, states, map[string][]string{"media_player.den_tv": {"Telly"}}, "America/Los_Angeles")
}

func TestHistoryFindsOnlyExposedEntitiesByNameAliasAndArea(t *testing.T) {
	ix := testIndex()
	cases := map[string]string{"front door": "binary_sensor.front_door", "The Front Door": "binary_sensor.front_door", "telly": "media_player.den_tv",
		"den tv": "media_player.den_tv", "media_player.den_tv": "media_player.den_tv", "doorbell": "event.doorbell"}
	for q, want := range cases {
		if got := ix.find(q, 4); len(got) != 1 || got[0].ID != want {
			t.Errorf("find(%q) = %v, want %s", q, ids(got), want)
		}
	}
	if got := ix.find("back door", 4); len(got) != 0 {
		t.Errorf("an unexposed lock was found: %v", ids(got))
	}
	if got := ix.find("door", 4); len(got) != 2 { // the front door and the doorbell, never the back door
		t.Errorf("find(door) = %v", ids(got))
	}
	if e := ix.find("front door", 1)[0]; e.label() != "Front Door (Entry)" || e.word("on") != "open" || e.word("off") != "closed" {
		t.Errorf("label %q, on %q, off %q", e.label(), e.word("on"), e.word("off"))
	}
}

func ids(es []*histEntity) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func row(s string, t time.Time) histRow { return histRow{S: s, LU: float64(t.UnixMilli()) / 1000} }

func TestHistoryAnswersWhenAndHowLong(t *testing.T) {
	ix := testIndex()
	door := ix.find("front door", 1)[0]
	now := time.Date(2026, 11, 14, 21, 30, 0, 0, histLoc)
	rows := []histRow{
		row("off", now.Add(-48*time.Hour)),
		row("on", time.Date(2026, 11, 14, 8, 0, 0, 0, histLoc)),
		row("off", time.Date(2026, 11, 14, 8, 2, 0, 0, histLoc)),
		row("off", time.Date(2026, 11, 14, 9, 0, 0, 0, histLoc)), // an attribute changed: not a change
		row("on", time.Date(2026, 11, 14, 20, 58, 0, 0, histLoc)),
		row("off", time.Date(2026, 11, 14, 21, 3, 0, 0, histLoc)),
	}
	if got, want := lastChangeText(door, rows, "", now), "Front Door (Entry): closed since 21:03 today (27 min); before that open for 5 min."; got != want {
		t.Errorf("last change:\n got %s\nwant %s", got, want)
	}
	if got, want := lastChangeText(door, rows, "open", now), "Front Door (Entry): last open 20:58 today until 21:03 today (5 min); closed now."; got != want {
		t.Errorf("last open:\n got %s\nwant %s", got, want)
	}
	from, to, _ := dayBounds("today", now)
	if got, want := timeInStateText(door, rows, "", from, to, now), "Front Door (Entry), today: open for 7 min, 2 times."; got != want {
		t.Errorf("time open:\n got %s\nwant %s", got, want)
	}
	if got, want := changesText(door, rows, from, to, now), "Front Door (Entry), today: from the start: closed, 08:00 open, 08:02 closed, 20:58 open, 21:03 closed."; got != want {
		t.Errorf("changes:\n got %s\nwant %s", got, want)
	}
	if got := lastChangeText(door, rows, "unlocked", now); !strings.Contains(got, "not unlocked in the last ten days") {
		t.Errorf("a state never seen: %s", got)
	}
}

func TestHistoryDaysAndSpans(t *testing.T) {
	now := time.Date(2026, 11, 14, 21, 30, 0, 0, histLoc) // a Saturday
	for day, want := range map[string]string{"yesterday": "2026-11-13", "thursday": "2026-11-12", "2026-11-10": "2026-11-10", "": "2026-11-14"} {
		from, _, ok := dayBounds(day, now)
		if !ok || from.Format("2006-01-02") != want {
			t.Errorf("dayBounds(%q) = %s %v, want %s", day, from, ok, want)
		}
	}
	if _, _, ok := dayBounds("someday", now); ok {
		t.Error("someday was taken for a day")
	}
	for d, want := range map[time.Duration]string{40 * time.Second: "40 s", 5 * time.Minute: "5 min", 192 * time.Minute: "3 h 12 min", 52 * time.Hour: "2 d 4 h"} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %s, want %s", d, got, want)
		}
	}
}

// The endpoint end to end: the token, Home Assistant's address, the tools, and an answer from the recorder.
func TestHistoryEndpointAnswersOnlyHomeAssistantWithTheToken(t *testing.T) {
	opened := time.Now().Add(-10 * time.Minute)
	closed := time.Now().Add(-8 * time.Minute)
	answers := map[string]any{
		"homeassistant/expose_entity/list": map[string]any{"exposed_entities": map[string]any{
			"binary_sensor.front_door": map[string]any{"conversation": true}, "lock.back_door": map[string]any{"conversation": false}}},
		"config/entity_registry/list": []any{map[string]any{"entity_id": "binary_sensor.front_door", "area_id": "entry"}},
		"config/device_registry/list": []any{},
		"config/area_registry/list":   []any{map[string]any{"area_id": "entry", "name": "Entry"}},
		"get_states": []any{map[string]any{"entity_id": "binary_sensor.front_door", "state": "off",
			"attributes": map[string]any{"friendly_name": "Front Door", "device_class": "door"}}},
		"get_config":                         map[string]any{"time_zone": "America/Los_Angeles"},
		"config/entity_registry/get_entries": map[string]any{"binary_sensor.front_door": map[string]any{"aliases": []any{"main door"}}},
		"history/history_during_period": map[string]any{"binary_sensor.front_door": []any{
			map[string]any{"s": "off", "lu": float64(time.Now().Add(-48 * time.Hour).Unix())},
			map[string]any{"s": "on", "lu": float64(opened.Unix())},
			map[string]any{"s": "off", "lu": float64(closed.Unix())},
		}},
	}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		c.Write(ctx, websocket.MessageText, []byte(`{"type":"auth_required"}`))
		c.Read(ctx)
		c.Write(ctx, websocket.MessageText, []byte(`{"type":"auth_ok"}`))
		for {
			_, msg, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(msg, &m)
			kind, _ := m["type"].(string)
			if kind == "history/history_during_period" && fmt.Sprint(m["entity_ids"]) != "[binary_sensor.front_door]" {
				t.Errorf("history asked for %v", m["entity_ids"])
			}
			out, _ := json.Marshal(map[string]any{"id": m["id"], "type": "result", "success": true, "result": answers[kind]})
			c.Write(ctx, websocket.MessageText, out)
		}
	}))
	defer ha.Close()
	g := gateway(ha, Identity{})
	g.isCore = func(ip string) bool { return ip == "172.30.32.1" }
	in := Ingress{g}
	call := func(path, peer string, body any) (int, map[string]any) {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(data))
		r.RemoteAddr = peer + ":40000"
		w := httptest.NewRecorder()
		in.ServeHTTP(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	list := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	if code, _ := call("/mcp/history/wrong", "172.30.32.1", list); code != 404 {
		t.Errorf("a wrong token got %d, want 404", code)
	}
	good := "/mcp/history/" + g.historyToken()
	if code, _ := call(good, "172.30.33.5", list); code != 403 {
		t.Errorf("another container with the token got %d, want 403", code)
	}
	code, out := call(good, "172.30.32.1", list)
	tools, _ := out["result"].(map[string]any)["tools"].([]any)
	if code != 200 || len(tools) != len(historyTools) {
		t.Fatalf("tools/list: %d %v", code, out)
	}
	for _, tool := range tools {
		if name := tool.(map[string]any)["name"].(string); !strings.HasPrefix(name, "history_") {
			t.Errorf("a tool that is not history's: %s", name)
		}
	}
	code, out = call(good, "172.30.32.1", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "history_last_change", "arguments": map[string]any{"entity": "main door", "state": "open"}}})
	content, _ := out["result"].(map[string]any)["content"].([]any)
	if code != 200 || len(content) != 1 {
		t.Fatalf("tools/call: %d %v", code, out)
	}
	text := content[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "Front Door (Entry): last open ") || !strings.Contains(text, "(2 min); closed now.") {
		t.Errorf("answer: %s", text)
	}
	_, out = call(good, "172.30.32.1", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "history_last_change", "arguments": map[string]any{"entity": "back door"}}})
	if res := out["result"].(map[string]any); res["isError"] != true {
		t.Errorf("an unexposed lock was answered: %v", res)
	}
}
