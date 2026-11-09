package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// An APNs that records what it is sent: the path (device token), the headers that matter, the payload.
type fakeAPNS struct {
	mu    sync.Mutex
	calls []map[string]any
	gone  string // a token Apple says is unregistered
}

func (f *fakeAPNS) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.URL.Path, "/3/device/")
		var payload map[string]any
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &payload)
		f.mu.Lock()
		f.calls = append(f.calls, map[string]any{"token": token, "topic": r.Header.Get("apns-topic"), "type": r.Header.Get("apns-push-type"),
			"collapse": r.Header.Get("apns-collapse-id"), "auth": r.Header.Get("authorization"), "payload": payload})
		f.mu.Unlock()
		if token == f.gone {
			w.WriteHeader(410)
			io.WriteString(w, `{"reason":"Unregistered"}`)
			return
		}
		w.WriteHeader(200)
	}))
}

func testKey(t *testing.T) string {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestAnAlertEventReachesEveryRegisteredPhoneThroughApple(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	apple := &fakeAPNS{gone: "oldphone"}
	srv := apple.server(t)
	defer srv.Close()

	g := gateway(ha, Identity{Login: "owner@example.com", Owner: true, Device: "owners-iphone"})
	apns, err := newAPNS(Options{APNSTeamID: "TEAM123456", APNSKeyID: "KEY1234567", APNSKey: testKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	apns.production, apns.sandbox = srv.URL, srv.URL
	g.alerts.apns = apns
	g.alerts.path = filepath.Join(t.TempDir(), "devices.json")
	g.alerts.home = "Test Home"

	// Two phones register: the owner's (production, with Live Activities) and a guest's (a development build).
	post := func(g *Gateway, path, body string) (int, string) {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		data, _ := io.ReadAll(w.Body)
		return w.Code, string(data)
	}
	if code, body := post(g, "/atrium/devices", `{"token":"ownertoken","liveStart":"ownerlivestart","model":"iPhone17,1","env":"production"}`); code != 202 || !strings.Contains(body, `"pushes":true`) {
		t.Fatalf("the owner's registration answered %d %s", code, body)
	}
	guest := gateway(ha, Identity{Login: "guest@example.com", Device: "guests-iphone"})
	guest.alerts = g.alerts // the same house
	if code, _ := post(guest, "/atrium/devices", `{"token":"guesttoken","model":"iPhone16,1","env":"sandbox"}`); code != 202 {
		t.Fatalf("the guest's registration answered %d", code)
	}
	g.alerts.register(Identity{Login: "old@example.com", Device: "old"}, struct {
		Token      string            `json:"token"`
		LiveStart  string            `json:"liveStart"`
		Model      string            `json:"model"`
		Env        string            `json:"env"`
		Activities map[string]string `json:"activities"`
	}{Token: "oldphone", Env: "production"})

	// The event, as an automation fires it.
	al := alert{ID: "front-door-night", Title: "Front door unlocked", Body: "Unlocked since 9:14 PM", Level: "warning", Icon: "lock.open", Entity: "lock.front_door",
		Action: &alertAction{Title: "Lock", Service: "lock.lock", Entity: "lock.front_door"}, Live: true}
	sent, problems := g.alerts.raise(al)
	// Owner: a notification and a Live Activity start; guest: a notification; the old phone: refused and forgotten.
	if sent != 3 || len(problems) != 1 || !strings.Contains(problems[0], "410") {
		t.Fatalf("sent %d, problems %v", sent, problems)
	}
	byToken := map[string]map[string]any{}
	for _, c := range apple.calls {
		byToken[c["token"].(string)] = c
	}
	n := byToken["ownertoken"]
	if n == nil || n["type"] != "alert" || n["topic"] != "com.cmmichael.atrium" || n["collapse"] != "front-door-night" || !strings.HasPrefix(n["auth"].(string), "bearer ey") {
		t.Errorf("the owner's notification: %v", n)
	}
	aps := n["payload"].(map[string]any)["aps"].(map[string]any)
	if aps["interruption-level"] != "time-sensitive" || aps["category"] != "atrium.lock" || aps["thread-id"] != "front-door-night" {
		t.Errorf("the notification's aps: %v", aps)
	}
	if extras := n["payload"].(map[string]any)["atrium"].(map[string]any); extras["entity"] != "lock.front_door" || extras["action"] == nil {
		t.Errorf("the notification's extras: %v", extras)
	}
	live := byToken["ownerlivestart"]
	if live == nil || live["type"] != "liveactivity" || live["topic"] != "com.cmmichael.atrium.push-type.liveactivity" {
		t.Errorf("the Live Activity start: %v", live)
	} else if laps := live["payload"].(map[string]any)["aps"].(map[string]any); laps["event"] != "start" || laps["attributes-type"] != "AlertActivityAttributes" {
		t.Errorf("the Live Activity start's aps: %v", laps)
	}
	if byToken["guesttoken"] == nil || byToken["guesttoken"]["type"] != "alert" {
		t.Errorf("the guest's notification: %v", byToken["guesttoken"])
	}
	for _, d := range g.alerts.list() {
		if d.Token == "oldphone" {
			t.Errorf("the unregistered phone was kept")
		}
	}

	// The phone reports its running activity's token; clearing the alert ends it and withdraws the notification.
	post(g, "/atrium/devices", `{"activities":{"front-door-night":"activitytoken"}}`)
	apple.calls = nil
	sent, problems = g.alerts.raise(alert{ID: "front-door-night", Clear: true, To: []string{"Owner@Example.com"}})
	if sent != 2 || len(problems) != 0 {
		t.Fatalf("clearing sent %d, problems %v", sent, problems)
	}
	kinds := map[string]string{}
	for _, c := range apple.calls {
		kinds[c["token"].(string)] = c["type"].(string)
	}
	if kinds["ownertoken"] != "background" || kinds["activitytoken"] != "liveactivity" || kinds["guesttoken"] != "" {
		t.Errorf("clearing went to %v", kinds)
	}

	// The devices list is for administrators, tokens shortened.
	code, body := get(t, g, "/atrium/devices")
	if code != 200 || strings.Contains(body["devices"].([]any)[0].(map[string]any)["token"].(string), "ownertoken") {
		t.Errorf("GET /atrium/devices answered %d %v", code, body)
	}
	if code, _ := get(t, guest, "/atrium/devices"); code != 403 {
		t.Errorf("a guest listing the devices answered %d", code)
	}
	if code, body := get(t, g, "/atrium/alerts"); code != 200 || body != nil && len(body) == 0 {
		_ = body // a list, not an object: get() decodes an object; the code is what matters here
		if code != 200 {
			t.Errorf("GET /atrium/alerts answered %d", code)
		}
	}
}

func TestWithoutAnAPNSKeyAlertsAreLoggedOnly(t *testing.T) {
	apns, err := newAPNS(Options{})
	if err != nil || apns != nil {
		t.Fatalf("no key: %v %v", apns, err)
	}
	if _, err := newAPNS(Options{APNSKey: "not a key", APNSTeamID: "T", APNSKeyID: "K"}); err == nil {
		t.Error("a bad key was accepted")
	}
	a := &alerts{}
	if sent, problems := a.raise(alert{Title: "Bins out"}); sent != 0 || len(problems) != 1 || !strings.Contains(problems[0], "APNs") {
		t.Errorf("without a key: %d %v", sent, problems)
	}
	if a.recent[0].ID != "bins-out" {
		t.Errorf("the id from the title: %q", a.recent[0].ID)
	}
	for svc, want := range map[string]string{"lock.lock": "atrium.lock", "cover.close_cover": "atrium.close", "alarm_control_panel.alarm_arm_away": "atrium.arm", "switch.turn_off": "atrium.off", "script.turn_on": "atrium.on", "script.bins": "atrium.act", "lock.unlock": "atrium.unlock", "cover.open_cover": "atrium.unlock"} {
		if got := category(alert{Action: &alertAction{Service: svc}}); got != want {
			t.Errorf("%s → %s, want %s", svc, got, want)
		}
	}
	if got := category(alert{Action: &alertAction{Service: "select.select_option", Entity: "select.front_door_lock_rule", Data: map[string]any{"option": "reset"}}}); got != "atrium.lock" {
		t.Errorf("a lock rule reset → %s", got)
	}
	if got := category(alert{Action: &alertAction{Service: "button.press", Entity: "button.front_door_unlock"}}); got != "atrium.unlock" {
		t.Errorf("an unlock button → %s", got)
	}
	if category(alert{}) != "atrium.alert" || interruption("critical") != "time-sensitive" || interruption("info") != "active" {
		t.Error("the plain alert's category or the levels")
	}
}
