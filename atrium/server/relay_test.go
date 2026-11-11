package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// With no key, pushes go to the relay as JSON, and Apple's answer comes back as the same apnsError, so a phone that
// deleted the app (410) is still forgotten.
func TestPushesGoThroughTheRelayWithNoKey(t *testing.T) {
	var got map[string]any
	status := 200
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/push" || r.Method != "POST" {
			t.Errorf("relay asked %s %s", r.Method, r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &got)
		w.WriteHeader(status)
		if status != 200 {
			w.Write([]byte(`{"ok":false,"status":410,"reason":"Unregistered"}`))
		}
	}))
	defer relay.Close()
	c := &apnsClient{relay: relay.URL, client: relay.Client()}
	if err := c.send(push{token: "ab12", pushType: "background", collapse: "water-valve", priority: 5, payload: map[string]any{"atrium": map[string]any{"clear": "water-valve"}}}); err != nil {
		t.Fatal(err)
	}
	if got["token"] != "ab12" || got["env"] != "production" || got["pushType"] != "background" || got["collapse"] != "water-valve" || got["priority"] != 5.0 {
		t.Errorf("sent %v", got)
	}
	if p, _ := got["payload"].(map[string]any); p["atrium"] == nil {
		t.Errorf("payload %v", got["payload"])
	}
	status = 410
	var ae *apnsError
	if err := c.send(push{token: "ab12", env: "sandbox", pushType: "alert", payload: map[string]any{}}); !asAPNSError(err, &ae) || ae.status != 410 || ae.reason != "Unregistered" {
		t.Errorf("410: %v", err)
	}
	if got["env"] != "sandbox" || got["priority"] != 10.0 {
		t.Errorf("sent %v", got)
	}
	if _, err := newAPNS(Options{PushRelay: "http://push.example.com"}); err == nil {
		t.Error("a plain-http relay must be refused")
	}
	if c, err := newAPNS(Options{PushRelay: "https://push.example.com/"}); err != nil || c == nil || c.relay != "https://push.example.com" {
		t.Errorf("relay: %+v %v", c, err)
	}
}
