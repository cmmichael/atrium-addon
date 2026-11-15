package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
	"github.com/coder/websocket"
)

func TestInputsAreMatchedLooselyButNeverGuessed(t *testing.T) {
	avr := []string{"CBL/SAT", "DVD", "Blu-ray", "GAME", "AUX1", "MEDIA PLAYER", "TV AUDIO", "VIDEO", "Phono", "Tuner"}
	for asked, want := range map[string]string{"video": "VIDEO", "Video": "VIDEO", "tv": "TV AUDIO", "phono": "Phono", "the tuner": "Tuner",
		"bluray": "Blu-ray", "blu ray": "Blu-ray", "aux 1": "AUX1", "media player": "MEDIA PLAYER"} {
		if got, why := pickSource(avr, asked); got != want {
			t.Errorf("pickSource(%q) = %q (%s), want %q", asked, got, why, want)
		}
	}
	tv := []string{"HDMI 1", "HDMI 2", "Live TV", "Netflix"}
	if got, why := pickSource(tv, "hdmi"); got != "" || !strings.Contains(why, "HDMI 1 or HDMI 2") {
		t.Errorf("an ambiguous input was guessed: %q %s", got, why)
	}
	if got, _ := pickSource(tv, "hdmi 2"); got != "HDMI 2" {
		t.Errorf("hdmi 2 = %q", got)
	}
	if got, why := pickSource(tv, "phono"); got != "" || !strings.Contains(why, "no input like") {
		t.Errorf("a missing input: %q %s", got, why)
	}
}

// End to end through the MCP endpoint: only an exposed player, only its own sources, turned on first when off.
func TestAssistCanChangeAReceiversInput(t *testing.T) {
	inputWarmup = 0
	var mu sync.Mutex
	var calls []string
	state := "off"
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
			var result any
			mu.Lock()
			switch m["type"] {
			case "homeassistant/expose_entity/list":
				result = map[string]any{"exposed_entities": map[string]any{
					"media_player.den_receiver": map[string]any{"conversation": true}, "media_player.attic_tv": map[string]any{"conversation": false}}}
			case "config/entity_registry/list":
				result = []any{map[string]any{"entity_id": "media_player.den_receiver", "area_id": "den"}}
			case "config/area_registry/list":
				result = []any{map[string]any{"area_id": "den", "name": "Den"}}
			case "get_config":
				result = map[string]any{"time_zone": "UTC"}
			case "config/entity_registry/get_entries":
				result = map[string]any{"media_player.den_receiver": map[string]any{"aliases": []any{"AVR"}}}
			case "get_states":
				result = []any{
					map[string]any{"entity_id": "media_player.den_receiver", "state": state, "attributes": map[string]any{"friendly_name": "Receiver",
						"supported_features": float64(2048 | 128 | 256), "source": "TV AUDIO", "source_list": []any{"TV AUDIO", "VIDEO", "Phono"}}},
					map[string]any{"entity_id": "media_player.attic_tv", "state": "on", "attributes": map[string]any{"friendly_name": "Attic TV",
						"supported_features": float64(2048), "source_list": []any{"VIDEO"}}},
				}
			case "call_service":
				calls = append(calls, fmt.Sprintf("%s %v %v", m["service"], m["target"], m["service_data"]))
				if m["service"] == "turn_on" {
					state = "on"
				}
			default:
				result = []any{}
			}
			mu.Unlock()
			out, _ := json.Marshal(map[string]any{"id": m["id"], "type": "result", "success": true, "result": result})
			c.Write(ctx, websocket.MessageText, out)
		}
	}))
	defer ha.Close()
	g := gateway(ha, Identity{})
	g.isCore = func(ip string) bool { return ip == "172.30.32.1" }
	call := func(args map[string]any) (string, bool) {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "media_select_input", "arguments": args}})
		r := httptest.NewRequest("POST", "/mcp/history/"+g.historyToken(), bytes.NewReader(data))
		r.RemoteAddr = "172.30.32.1:40000"
		w := httptest.NewRecorder()
		Ingress{g}.ServeHTTP(w, r)
		var out struct {
			Result struct {
				Content []struct{ Text string } `json:"content"`
				IsError bool                    `json:"isError"`
			} `json:"result"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		if len(out.Result.Content) == 0 {
			t.Fatalf("no answer: %s", w.Body.String())
		}
		return out.Result.Content[0].Text, out.Result.IsError
	}
	if text, isErr := call(map[string]any{"player": "AVR", "input": "video"}); isErr || text != "Receiver (Den): input set to VIDEO (was TV AUDIO)." {
		t.Errorf("set video: %q %v", text, isErr)
	}
	mu.Lock()
	if strings.Join(calls, "|") != "turn_on map[entity_id:media_player.den_receiver] <nil>|select_source map[entity_id:media_player.den_receiver] map[source:VIDEO]" {
		t.Errorf("calls: %q", calls)
	}
	calls = nil
	mu.Unlock()
	if text, isErr := call(map[string]any{"player": "receiver"}); isErr || !strings.Contains(text, "TV AUDIO, VIDEO, Phono") {
		t.Errorf("list: %q %v", text, isErr)
	}
	if text, isErr := call(map[string]any{"player": "receiver", "input": "game"}); !isErr || !strings.Contains(text, "its inputs are TV AUDIO, VIDEO, Phono") {
		t.Errorf("a missing input: %q %v", text, isErr)
	}
	if text, isErr := call(map[string]any{"player": "attic tv", "input": "video"}); !isErr || !strings.Contains(text, "no media player exposed") {
		t.Errorf("an unexposed player was used: %q %v", text, isErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("refused asks still called services: %q", calls)
	}
	_ = time.Second
}

// Where the layout gives the receiver its own inputs (scripts, because the integration lists only some), those are
// what Assist picks from and runs.
func TestTheLayoutsInputsComeFirst(t *testing.T) {
	g := &Gateway{}
	h := house.House{Remotes: []house.Remote{{ID: "family", Name: "Family room AVR", Player: "media_player.family_avr", Inputs: &house.Inputs{Options: []house.InputOption{
		{Name: "PS5", Action: house.Action{Domain: "script", Service: "turn_on", Entity: "script.activity_ps5"}, Entity: "sensor.avr_input", Value: "PS5"},
		{Name: "Apple TV", Action: house.Action{Domain: "script", Service: "turn_on", Entity: "script.activity_apple_tv"}},
	}}}}}
	data, _ := json.Marshal(h)
	g.house.data = data
	players := []*histEntity{{ID: "media_player.family_avr", Name: "Family Room AVR"}}
	states := []map[string]any{{"entity_id": "sensor.avr_input", "state": "PS5"}}
	text, done, err := g.layoutInput(context.Background(), players, states, "")
	if !done || err != nil || text != "Family room AVR inputs: PS5, Apple TV. Now: PS5." {
		t.Errorf("list: %q %v %v", text, done, err)
	}
	if _, done, err := g.layoutInput(context.Background(), players, states, "video"); !done || err == nil || !strings.Contains(err.Error(), "its inputs are PS5, Apple TV") {
		t.Errorf("an input the layout does not name: %v %v", done, err)
	}
	if _, done, _ := g.layoutInput(context.Background(), []*histEntity{{ID: "media_player.other"}}, states, "ps5"); done {
		t.Error("a player without layout inputs was taken")
	}
}

// A release that changes the tools reloads Home Assistant's MCP entry once, so the agent sees them; the next start
// with the same tools does nothing.
func TestChangedToolsReloadTheMCPEntryOnce(t *testing.T) {
	var mu sync.Mutex
	var reloads []string
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			mu.Lock()
			reloads = append(reloads, r.Method+" "+r.URL.Path)
			mu.Unlock()
			w.Write([]byte(`{"require_restart":false}`))
			return
		}
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
			out, _ := json.Marshal(map[string]any{"id": m["id"], "type": "result", "success": true, "result": []any{
				map[string]any{"entry_id": "e1", "domain": "mcp", "title": "atrium-history"}, map[string]any{"entry_id": "e2", "domain": "hue", "title": "Hue"}}})
			c.Write(ctx, websocket.MessageText, out)
		}
	}))
	defer ha.Close()
	g := gateway(ha, Identity{})
	g.HistoryTokenPath = filepath.Join(t.TempDir(), "history-token")
	g.refreshToolsInHA(context.Background())
	g.refreshToolsInHA(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(reloads, "|") != "POST /api/config/config_entries/entry/e1/reload" {
		t.Errorf("reloads: %q", reloads)
	}
}
