package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// The add-on's port: a Home Assistant token Home Assistant accepts opens the layout and nothing else; bad tokens
// are refused, and an address that keeps trying stops reaching Home Assistant.
func TestThePortServesTheLayoutToHomeAssistantUsers(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	var asked atomic.Int32
	checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path == "/api/" && r.Header.Get("Authorization") == "Bearer good.user.token" && r.Header.Get("X-Forwarded-For") == "" {
			w.Write([]byte(`{"message":"API running."}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer checker.Close()
	g := gateway(ha, Identity{})
	g.Core, _ = url.Parse(checker.URL)
	l := NewLocal(g)
	call := func(path, token string, header ...string) (int, map[string]any) {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.168.1.20:5555"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}

	if code, body := call("/atrium/hello", "good.user.token"); code != 200 || body["allowed"] != true || body["configures"] != false {
		t.Fatalf("hello with a good token: %d %v", code, body)
	}
	if code, body := call("/atrium/house", "good.user.token"); code != 200 || body["rooms"] == nil {
		t.Fatalf("house with a good token: %d %v", code, body)
	}
	for _, p := range []string{"/api/states", "/api/websocket", "/atrium/mapping", "/atrium/config", "/mcp", "/"} {
		if code, _ := call(p, "good.user.token"); code != 404 {
			t.Errorf("%s answered %d on the port; only the layout, hello and clips should", p, code)
		}
	}
	if code, _ := call("/atrium/house", "good.user.token", "Origin", "http://evil.example"); code != 403 {
		t.Errorf("a web page's request answered %d", code)
	}
	if code, body := call("/atrium/hello", ""); code != 200 || body["allowed"] != false {
		t.Errorf("hello without a token: %d %v", code, body)
	}
	before := asked.Load()
	if code, _ := call("/atrium/house", "not-a-jwt"); code != 403 || asked.Load() != before {
		t.Errorf("a token that is not a JWT: %d, Home Assistant asked %d times", code, asked.Load()-before)
	}
	for i := range 3 {
		if code, _ := call("/atrium/house", "bad.user.token"+strings.Repeat("x", i)); code != 403 {
			t.Fatalf("bad token %d answered %d", i, code)
		}
	}
	before = asked.Load()
	if code, _ := call("/atrium/house", "bad.user.tokenzzz"); code != 429 || asked.Load() != before {
		t.Errorf("after three refusals: %d, Home Assistant asked again: %v", code, asked.Load() != before)
	}
}
