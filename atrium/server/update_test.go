package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A Home Assistant that keeps an update entity for the add-on (or not), answers its state, and records what
// update.install was asked for.
func updateHA(t *testing.T, hasEntity bool, installed chan<- map[string]any) *httptest.Server {
	registry := `[{"entity_id":"light.den_lamp","platform":"demo"}]`
	if hasEntity {
		registry = `[{"entity_id":"light.den_lamp","platform":"demo"},{"entity_id":"update.atrium_update","platform":"hassio","unique_id":"df84f1fd_atrium_version_latest"}]`
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/websocket":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
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
				result := `[]`
				if m["type"] == "config/entity_registry/list" {
					result = registry
				}
				out, _ := json.Marshal(map[string]any{"id": m["id"], "type": "result", "success": true, "result": json.RawMessage(result)})
				c.Write(ctx, websocket.MessageText, out)
			}
		case r.URL.Path == "/api/states/update.atrium_update":
			io.WriteString(w, `{"entity_id":"update.atrium_update","state":"on","attributes":{"installed_version":"0.9.9","latest_version":"0.9.99","in_progress":false,"release_url":"https://github.com/cmmichael/atrium-addon/blob/main/atrium/CHANGELOG.md"}}`)
		case r.URL.Path == "/api/services/update/install" && r.Method == "POST":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			body["auth"] = r.Header.Get("Authorization")
			installed <- body
			io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTheAppUpdatesTheAddonThroughHomeAssistantsUpdateEntity(t *testing.T) {
	installed := make(chan map[string]any, 1)
	ha := updateHA(t, true, installed)
	defer ha.Close()
	owner, guest := Identity{Login: "owner@example.com", Owner: true}, Identity{Login: "guest@example.com"}

	g := gateway(ha, owner)
	g.Slug = "df84f1fd_atrium"
	code, body := get(t, g, "/atrium/update")
	if code != 200 || body["available"] != true || body["latest"] != "0.9.99" || body["installed"] != version || body["entity"] != "update.atrium_update" {
		t.Fatalf("GET /atrium/update answered %d %v", code, body)
	}

	post := func(g *Gateway) (int, string) {
		r := httptest.NewRequest("POST", "/atrium/update", nil)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		data, _ := io.ReadAll(w.Body)
		return w.Code, strings.TrimSpace(string(data))
	}
	// A guest may look but not update.
	if code, text := post(gateway(ha, guest)); code != 403 {
		t.Errorf("a guest's POST answered %d %s", code, text)
	}
	// The owner's POST is accepted at once; Home Assistant is asked to install, without a backup, as the add-on.
	code, text := post(g)
	if code != 202 || !strings.Contains(text, `"to":"0.9.99"`) {
		t.Fatalf("the owner's POST answered %d %s", code, text)
	}
	select {
	case call := <-installed:
		if call["entity_id"] != "update.atrium_update" || call["backup"] != false || call["auth"] != "Bearer addon-token" {
			t.Errorf("update.install was asked with %v", call)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Home Assistant was never asked to install the update")
	}

	// Without an update entity (no Supervisor, or an older Home Assistant), the status says so and a POST fails plainly.
	bare := updateHA(t, false, installed)
	defer bare.Close()
	g = gateway(bare, owner)
	g.Slug = "df84f1fd_atrium"
	if code, body := get(t, g, "/atrium/update"); code != 200 || body["available"] != false || body["unavailable"] == nil {
		t.Errorf("without an entity, GET answered %d %v", code, body)
	}
	if code, text := post(g); code != 502 {
		t.Errorf("without an entity, POST answered %d %s", code, text)
	}
	// A development run with no slug at all says why, and does not ask Home Assistant anything.
	g = gateway(bare, owner)
	if code, body := get(t, g, "/atrium/update"); code != 200 || body["unavailable"] == nil || !strings.Contains(body["unavailable"].(string), "slug") {
		t.Errorf("without a slug, GET answered %d %v", code, body)
	}
}
