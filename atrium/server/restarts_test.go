package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// A link to Atrium clicked on another site is a top-level navigation, and the web app opens; Home Assistant's
// API and files are still not for other pages, navigation or not.
func TestALinkFromAnotherSiteOpensTheWebApp(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com"})
	nav := []string{"Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "navigate", "Sec-Fetch-Dest", "document"}
	if code, _ := get(t, g, "/atrium/hello", nav...); code != 200 {
		t.Errorf("a navigation to the app answered %d", code)
	}
	for _, p := range []string{"/api/states", "/local/x.json"} {
		if code, _ := get(t, g, p, nav...); code != 403 {
			t.Errorf("a navigation to %s answered %d, want 403", p, code)
		}
	}
	if code, _ := get(t, g, "/atrium/hello", "Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "cors", "Sec-Fetch-Dest", "empty"); code != 403 {
		t.Errorf("a page's fetch answered %d, want 403", code)
	}
}

// Just after a start Tailscale may not have said yet who signed the machine in. A caller it would refuse is then
// told to try again, never refused: the app takes a refusal as a sign-out. Listed people are let in meanwhile.
func TestWhileTheOwnerIsUnknownNobodyIsRefused(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "stranger@example.com", Device: "phone"})
	g.Owner = ""
	g.ownerPending = true
	if code, _ := get(t, g, "/atrium/hello"); code != 503 {
		t.Errorf("a stranger while the owner is unknown: %d, want 503", code)
	}
	if code, _ := get(t, g, "/api/states"); code != 503 {
		t.Errorf("a stranger's request while the owner is unknown: %d, want 503", code)
	}
	g.Who = func(ctxArg context.Context, _ string) (Identity, error) {
		return Identity{Login: "guest@example.com"}, nil
	}
	if code, body := get(t, g, "/atrium/hello"); code != 200 || body["allowed"] != true {
		t.Errorf("a listed guest while the owner is unknown: %d %v", code, body)
	}
	g.Who = func(ctxArg context.Context, _ string) (Identity, error) {
		return Identity{Login: "stranger@example.com"}, nil
	}
	g.ownerPending = false
	if code, body := get(t, g, "/atrium/hello"); code != 200 || body["allowed"] != false {
		t.Errorf("a stranger once the owner is known: %d %v", code, body)
	}
}

// While Home Assistant is down the last layout is served, and it stands for a little while: not a minute-long
// attempt on every request.
func TestTheStaleLayoutStandsWhileHomeAssistantIsDown(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	var down atomic.Bool
	var dials atomic.Int32
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/websocket" {
			dials.Add(1)
			if down.Load() {
				http.Error(w, "restarting", http.StatusBadGateway)
				return
			}
		}
		ha.Config.Handler.ServeHTTP(w, r)
	}))
	defer flaky.Close()
	g := gateway(flaky, Identity{Login: "owner@example.com"})
	base, _ := url.Parse(flaky.URL)
	g.Addon.WS = "ws" + flaky.URL[len("http"):] + "/api/websocket"
	g.Addon.Base, g.Core = base, base
	house := func() (int, string) {
		r := httptest.NewRequest("GET", "/atrium/house", nil)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w.Code, w.Header().Get("X-Atrium-Stale")
	}
	if code, stale := house(); code != 200 || stale != "" {
		t.Fatalf("first layout: %d stale=%q", code, stale)
	}
	down.Store(true)
	g.mu.Lock()
	g.house.at = time.Now().Add(-houseTTL) // old enough to rebuild
	g.mu.Unlock()
	before := dials.Load()
	for i := range 3 {
		if code, stale := house(); code != 200 || stale != "1" {
			t.Errorf("request %d while down: %d stale=%q", i, code, stale)
		}
	}
	if n := dials.Load() - before; n != 1 {
		t.Errorf("Home Assistant was dialled %d times for three requests while down; the stale layout should stand", n)
	}
}

// The port takes a token on trust for a minute, then asks Home Assistant again, so a revoked token stops working
// here soon after. The house's name is not told to whoever reaches the port without one.
func TestThePortAsksAgainAfterAMinute(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	var asked atomic.Int32
	checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.Header.Get("Authorization") == "Bearer good.user.token" {
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer checker.Close()
	g := gateway(ha, Identity{})
	g.Core, _ = url.Parse(checker.URL)
	l := NewLocal(g)
	clock := time.Now()
	l.tokens.now = func() time.Time { return clock }
	hello := func(token string) (int, map[string]any) {
		r := httptest.NewRequest("GET", "/atrium/hello", nil)
		r.RemoteAddr = "192.168.1.20:5555"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	if _, body := hello("good.user.token"); body["allowed"] != true || body["home"] != "Test Home" {
		t.Fatalf("with a token: %v", body)
	}
	hello("good.user.token")
	if asked.Load() != 1 {
		t.Errorf("Home Assistant asked %d times within the minute, want 1", asked.Load())
	}
	clock = clock.Add(61 * time.Second)
	hello("good.user.token")
	if asked.Load() != 2 {
		t.Errorf("Home Assistant asked %d times after the minute, want 2", asked.Load())
	}
	if _, body := hello(""); body["allowed"] != false || body["home"] != "" {
		t.Errorf("without a token the house's name is told: %v", body)
	}
}
