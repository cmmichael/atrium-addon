package main

import (
	"context"
	"net"
	"net/http"
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

// Tailscale's close at the end of stop closes the listeners under http.Serve. That must not count as a failure
// (in 0.9.37 it was fatal, and the add-on exited 1 before logging "Stopped"); before stopping it still does.
func TestAListenerClosedByStoppingIsNotAnError(t *testing.T) {
	g := &Gateway{}
	broken, _ := net.Listen("tcp", "127.0.0.1:0")
	broken.Close()
	if g.serveUntilStopped(broken, http.NotFoundHandler()) == nil {
		t.Error("a listener that failed before stopping was not an error")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g.onStop(func() { ln.Close() })
	served := make(chan error, 1)
	go func() { served <- g.serveUntilStopped(ln, http.NotFoundHandler()) }()
	g.stop("test")
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serving ended with %v after stopping", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("serving did not end when the listener closed")
	}
}

// An app that is gone sends nothing and no reset arrives: the add-on's ping goes unanswered and the socket is
// closed, so apps.open counts only apps that are there. One that answers stays.
func TestASocketThatDoesNotAnswerPingsIsClosed(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "kid@example.com"})
	g.pings = [2]time.Duration{50 * time.Millisecond, 100 * time.Millisecond}
	front := httptest.NewServer(g)
	defer front.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(front.URL, "http") + "/api/websocket"

	alive, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer alive.CloseNow()
	go func() { // reading answers pings, as an app or a browser does
		for {
			if _, _, err := alive.Read(ctx); err != nil {
				return
			}
		}
	}()
	gone, _, err := websocket.Dial(ctx, url, nil) // never reads again: pings go unanswered
	if err != nil {
		t.Fatal(err)
	}
	defer gone.CloseNow()

	deadline := time.Now().Add(3 * time.Second)
	for g.open.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := g.open.Load(); n != 1 {
		t.Fatalf("%d sockets counted open, want 1 (the one that answers)", n)
	}
	time.Sleep(300 * time.Millisecond) // several more pings: the live one is still there
	if n := g.open.Load(); n != 1 {
		t.Errorf("%d sockets counted open after more pings, want 1", n)
	}
}
