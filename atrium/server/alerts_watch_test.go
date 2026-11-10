package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A Home Assistant that takes the subscription and then goes silent without closing: no pongs, no events. The
// listener must give the connection up within alertSilence and connect again, and health must say so.
func TestTheAlertsListenerNoticesASilentHomeAssistant(t *testing.T) {
	var connects, pings atomic.Int32
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		send := func(v any) { b, _ := json.Marshal(v); c.Write(ctx, websocket.MessageText, b) }
		send(map[string]any{"type": "auth_required"})
		c.Read(ctx)
		send(map[string]any{"type": "auth_ok"})
		connects.Add(1)
		for { // read everything (the subscription, the pings), answer nothing, never close
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if strings.Contains(string(data), `"ping"`) {
				pings.Add(1)
			}
		}
	}))
	defer ha.Close()
	g := gateway(ha, Identity{})
	g.alertPing, g.alertSilence = 20*time.Millisecond, 150*time.Millisecond
	go g.watchAlerts()

	deadline := time.Now().Add(4 * time.Second)
	for connects.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if connects.Load() < 2 {
		t.Fatalf("connected %d times: a silent Home Assistant was never given up", connects.Load())
	}
	if pings.Load() == 0 {
		t.Error("no ping was sent on the alerts socket")
	}
	g.mu.Lock()
	connected := g.alertsConnects
	g.mu.Unlock()
	if connected < 2 {
		t.Errorf("health counts %d connections", connected)
	}
}

// A quiet Home Assistant that answers the pings keeps the one connection, and is heard from all along.
func TestPongsKeepTheAlertsListenerConnected(t *testing.T) {
	var connects atomic.Int32
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		send := func(v any) { b, _ := json.Marshal(v); c.Write(ctx, websocket.MessageText, b) }
		send(map[string]any{"type": "auth_required"})
		c.Read(ctx)
		send(map[string]any{"type": "auth_ok"})
		connects.Add(1)
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			if m["type"] == "ping" {
				send(map[string]any{"id": m["id"], "type": "pong"})
			}
		}
	}))
	defer ha.Close()
	g := gateway(ha, Identity{})
	g.alertPing, g.alertSilence = 20*time.Millisecond, 150*time.Millisecond
	go g.watchAlerts()
	time.Sleep(600 * time.Millisecond)
	g.mu.Lock()
	listening, seen := g.alertsListening, g.haSeen
	g.mu.Unlock()
	if n := connects.Load(); n != 1 || !listening {
		t.Errorf("connected %d times, listening %v", n, listening)
	}
	if time.Since(seen) > 100*time.Millisecond {
		t.Errorf("last heard %s ago", time.Since(seen))
	}
}
