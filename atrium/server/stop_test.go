package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Told to stop, the add-on closes an app's WebSocket with 1001 (going away), so the app reconnects at once, and
// stop returns without waiting out the grace.
func TestStoppingClosesTheAppsWebSocketsAsGoingAway(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "kid@example.com"})
	front := httptest.NewServer(g)
	defer front.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+"/api/websocket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, m, _ := c.Read(ctx); !strings.Contains(string(m), "auth_required") {
		t.Fatalf("first message: %s", m)
	}
	if n := g.open.Load(); n != 1 {
		t.Fatalf("%d sockets counted open, want 1", n)
	}
	hooked := false
	g.onStop(func() { hooked = true })
	done := make(chan struct{})
	start := time.Now()
	go func() { g.stop("test"); close(done) }()
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("the app's socket ended with %v, want 1001 going away", err)
	}
	<-done
	if took := time.Since(start); took >= socketGrace {
		t.Errorf("stop took %s, the whole grace", took)
	}
	if !hooked {
		t.Error("the onStop work was not run")
	}
	if n := g.open.Load(); n != 0 {
		t.Errorf("%d sockets still counted open", n)
	}
}
