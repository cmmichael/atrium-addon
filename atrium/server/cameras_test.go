package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
)

// A camera that is switched off refuses camera/capabilities, and Home Assistant logs an error for each asking:
// after the first refusal it is left alone, while a camera that answers is asked every time.
func TestACameraThatIsOffIsAskedHowItStreamsOnce(t *testing.T) {
	var askedOff, askedOn atomic.Int32
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
			reply := map[string]any{"id": m["id"], "type": "result", "success": true}
			switch kind, _ := m["type"].(string); {
			case kind == "get_states":
				reply["result"] = json.RawMessage(`[{"entity_id":"camera.tablet","state":"idle","attributes":{}},{"entity_id":"camera.door","state":"recording","attributes":{}}]`)
			case kind == "camera/capabilities" && m["entity_id"] == "camera.tablet":
				askedOff.Add(1)
				reply["success"], reply["error"] = false, map[string]any{"code": "home_assistant_error", "message": "Camera is off"}
			case kind == "camera/capabilities":
				askedOn.Add(1)
				reply["result"] = map[string]any{"frontend_stream_types": []string{"hls", "web_rtc"}}
			case tinyHouse[kind] != "":
				reply["result"] = json.RawMessage(tinyHouse[kind])
			default:
				reply["result"] = []any{}
			}
			out, _ := json.Marshal(reply)
			c.Write(ctx, websocket.MessageText, out)
		}
	}))
	defer ha.Close()
	base, _ := url.Parse(ha.URL)
	g := &Gateway{Addon: Upstream{Base: base, WS: "ws" + ha.URL[4:] + "/api/websocket", Token: "t"}}
	for range 3 {
		d, err := g.dump(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(d.CameraStreams["camera.door"]) != 2 || d.CameraStreams["camera.tablet"] != nil {
			t.Fatalf("streams: %v", d.CameraStreams)
		}
	}
	if askedOff.Load() != 1 || askedOn.Load() != 3 {
		t.Errorf("the camera that is off was asked %d times (want 1), the other %d (want 3)", askedOff.Load(), askedOn.Load())
	}
}
