package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppsHandTheirDiagnosticsToTheAddonAlone(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	owner, guest := Identity{Login: "owner@example.com", Owner: true, Device: "owners-iphone"}, Identity{Login: "guest@example.com", Device: "guests-iphone"}
	g := gateway(ha, guest)
	post := func(g *Gateway, body string) int {
		r := httptest.NewRequest("POST", "/atrium/telemetry", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w.Code
	}
	// A guest may send theirs; the batch is kept under their login and device.
	if code := post(g, `{"device":"iPhone17,1","os":"26.1","app":"0.9","events":[{"id":1,"time":"2026-11-09T12:00:00Z","kind":"launch","detail":"after iOS ended the previous run"},{"id":2,"time":"2026-11-09T12:00:02Z","kind":"connected","detail":"in 1800 ms"}]}`); code != 202 {
		t.Fatalf("a guest's batch answered %d", code)
	}
	if code := post(g, `not json`); code != 400 {
		t.Errorf("a bad batch answered %d", code)
	}
	// Reading them is for the owner and administrators.
	if code, _ := get(t, g, "/atrium/telemetry"); code != 403 {
		t.Errorf("a guest reading the diagnostics answered %d", code)
	}
	g.Who = func(context.Context, string) (Identity, error) { return owner, nil }
	r := httptest.NewRequest("GET", "/atrium/telemetry", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `"login":"guest@example.com"`) || !strings.Contains(body, `"kind":"connected"`) || !strings.Contains(body, `"model":"iPhone17,1"`) {
		t.Errorf("the owner's GET answered %d %s", w.Code, body)
	}
}
