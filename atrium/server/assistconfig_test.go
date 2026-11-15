package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// A Home Assistant with one pipeline whose agent keeps its settings on a sub-entry, and its settings form.
func assistHA(t *testing.T, steps *[]string, mu *sync.Mutex) *httptest.Server {
	ws := map[string]any{
		"assist_pipeline/pipeline/list": map[string]any{"preferred_pipeline": "p1", "pipelines": []any{map[string]any{"id": "p1", "name": "Home", "conversation_engine": "conversation.claude"}}},
		"config_entries/get":            []any{map[string]any{"entry_id": "e1", "domain": "anthropic", "title": "Claude", "state": "loaded"}},
		"config/entity_registry/get":    map[string]any{"entity_id": "conversation.claude", "config_entry_id": "e1", "config_subentry_id": "s1", "original_name": "Claude"},
		"conversation/process":          map[string]any{"response": map[string]any{"speech": map[string]any{"plain": map[string]any{"speech": "It is 10:00."}}}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/websocket" {
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
				out, _ := json.Marshal(map[string]any{"id": m["id"], "type": "result", "success": true, "result": ws[m["type"].(string)]})
				c.Write(ctx, websocket.MessageText, out)
			}
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*steps = append(*steps, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/config/config_entries/subentries/flow":
			if h, _ := body["handler"].([]any); len(h) != 2 || h[0] != "e1" || body["subentry_id"] != "s1" {
				t.Errorf("the flow was started for %v", body)
			}
			w.Write([]byte(`{"flow_id":"f1","type":"form","step_id":"init","data_schema":[{"name":"prompt","description":{"suggested_value":"the old prompt"}},{"name":"chat_model"}]}`))
		case r.URL.Path == "/api/config/config_entries/subentries/flow/f1":
			w.Write([]byte(`{"type":"abort","reason":"reconfigure_successful"}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestAIAssistSettingsGoThroughTheAgentsOwnForm(t *testing.T) {
	var steps []string
	var mu sync.Mutex
	ha := assistHA(t, &steps, &mu)
	defer ha.Close()
	base, _ := url.Parse(ha.URL)
	make := func(id Identity) *Gateway {
		g := gateway(ha, id)
		g.Addon.Base = base
		g.assistCfg.prompts = filepath.Join(t.TempDir(), "prompts.jsonl")
		return g
	}
	call := func(g *Gateway, method, path, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	for _, who := range []Identity{{Login: "guest@example.com"}, {Login: "d7e97e69_claudecode", Addon: true}} {
		if code, _ := call(make(who), "GET", "/atrium/assist/config", ""); code != 403 {
			t.Errorf("%s read AI Assist's settings: %d", who.Login, code)
		}
	}
	g := make(Identity{Login: "owner@example.com", Owner: true})
	code, cfg := call(g, "GET", "/atrium/assist/config", "")
	agent, _ := cfg["agent"].(map[string]any)
	if code != 200 || agent["entity"] != "conversation.claude" || agent["domain"] != "anthropic" || agent["subentry"] != "s1" {
		t.Fatalf("GET: %d %v", code, cfg)
	}
	if code, _ := call(g, "POST", "/atrium/assist/config/flow/somebody-elses", `{}`); code != 502 {
		t.Errorf("a flow Atrium did not start was answered: %d", code)
	}
	code, form := call(g, "POST", "/atrium/assist/config/flow", "")
	if code != 200 || form["flow_id"] != "f1" {
		t.Fatalf("start: %d %v", code, form)
	}
	code, done := call(g, "POST", "/atrium/assist/config/flow/f1", `{"prompt":"a better prompt","chat_model":"claude-sonnet-5-5"}`)
	if code != 200 || done["reason"] != "reconfigure_successful" {
		t.Fatalf("step: %d %v", code, done)
	}
	if code, _ := call(g, "POST", "/atrium/assist/config/flow/f1", `{}`); code != 502 {
		t.Errorf("a finished flow was answered again: %d", code)
	}
	r := httptest.NewRequest("GET", "/atrium/assist/config/prompts", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	var kept []keptPrompt
	json.Unmarshal(w.Body.Bytes(), &kept)
	if len(kept) != 1 || kept[0].Prompt != "the old prompt" || kept[0].Login != "owner@example.com" {
		t.Errorf("kept prompts: %+v", kept)
	}
	if code, ready := call(g, "POST", "/atrium/assist/config/ready", ""); code != 200 || ready["ready"] != true {
		t.Errorf("ready: %d %v", code, ready)
	}
}
