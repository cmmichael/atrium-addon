package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A stand-in Home Assistant: says which token each request carried.
func fakeHA(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/websocket" {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.CloseNow()
			ctx := r.Context()
			c.Write(ctx, websocket.MessageText, []byte(`{"type":"auth_required"}`))
			_, first, _ := c.Read(ctx)
			c.Write(ctx, websocket.MessageText, append([]byte(`{"type":"auth_ok","saw":`), append(first, '}')...))
			for { // registry requests get a tiny house; anything else is echoed untouched
				_, msg, err := c.Read(ctx)
				if err != nil {
					return
				}
				var m map[string]any
				json.Unmarshal(msg, &m)
				if result, ok := tinyHouse[m["type"].(string)]; ok {
					out, _ := json.Marshal(map[string]any{"id": m["id"], "type": "result", "success": true, "result": json.RawMessage(result)})
					c.Write(ctx, websocket.MessageText, out)
				} else {
					c.Write(ctx, websocket.MessageText, msg)
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path, "auth": r.Header.Get("Authorization"), "cookie": r.Header.Get("Cookie"), "location_name": "Test Home"})
	}))
}

var tinyHouse = map[string]string{
	"config/area_registry/list":   `[{"area_id":"den","name":"Den","floor_id":"g"}]`,
	"config/floor_registry/list":  `[{"floor_id":"g","name":"Ground","level":0}]`,
	"config/entity_registry/list": `[{"entity_id":"light.den_lamp","area_id":"den","platform":"demo"},{"entity_id":"light.den_settings","area_id":"den","platform":"demo","entity_category":"config"}]`,
	"config/device_registry/list": `[]`,
	"get_states":                  `[{"entity_id":"light.den_lamp","state":"on","attributes":{"friendly_name":"Den Lamp"}},{"entity_id":"light.den_settings","state":"off","attributes":{}}]`,
	"get_config":                  `{"location_name":"Test Home"}`,
	"config/auth/list":            `[{"id":"u1","name":"Someone","is_owner":false,"group_ids":["system-users"]},{"id":"u2","name":"The admin","is_owner":false,"group_ids":["system-admin"]},{"id":"u3","name":"The owner","is_owner":true,"group_ids":["system-admin"]}]`,
}

func gateway(ha *httptest.Server, caller Identity) *Gateway {
	base, _ := url.Parse(ha.URL)
	return &Gateway{
		Who:        func(context.Context, string) (Identity, error) { return caller, nil },
		Owner:      "owner@example.com",
		Opts:       Options{Allow: []string{"Guest@Example.com"}, People: []Person{{Login: "kid@example.com", Token: "kid-token"}}},
		Addon:      Upstream{Base: base, WS: "ws" + strings.TrimPrefix(ha.URL, "http") + "/api/websocket", Token: "addon-token", As: "home-assistant"},
		Core:       base,
		Names:      []string{"atrium", "atrium.tail1234.ts.net", "100.64.0.9", "127.0.0.1", "example.com"}, // httptest requests say example.com; real servers 127.0.0.1
		Supervisor: []string{"172.30.32.2"},
	}
}

func get(t *testing.T, g *Gateway, path string, header ...string) (int, map[string]any) {
	r := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	var body map[string]any
	data, _ := io.ReadAll(w.Body)
	json.Unmarshal(data, &body)
	return w.Code, body
}

func TestWhoIsAllowed(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	for _, c := range []struct {
		id   Identity
		want bool
	}{
		{Identity{Login: "owner@example.com"}, true},
		{Identity{Login: "OWNER@example.com"}, true},
		{Identity{Login: "renamed@example.com", Owner: true}, true}, // the owner by Tailscale user id, whatever the login reads
		{Identity{Login: "guest@example.com"}, true},
		{Identity{Login: "kid@example.com"}, true},
		{Identity{Login: "stranger@example.com"}, false},
		{Identity{Login: "owner@example.com", Tagged: true}, false},
		{Identity{}, false},
	} {
		code, hello := get(t, gateway(ha, c.id), "/atrium/hello")
		if code != 200 || hello["allowed"] != c.want || hello["home"] != "Test Home" {
			t.Errorf("%+v: hello %d %v", c.id, code, hello)
		}
		code, _ = get(t, gateway(ha, c.id), "/api/states")
		if (code == 200) != c.want {
			t.Errorf("%+v: /api/states answered %d", c.id, code)
		}
	}
}

func TestTheCredentialIsSetHereAndNothingOfTheCallersIsForwarded(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	_, body := get(t, gateway(ha, Identity{Login: "owner@example.com"}), "/api/states", "Authorization", "Bearer from-the-app", "Cookie", "x=1")
	if body["auth"] != "Bearer addon-token" || body["cookie"] != "" || body["path"] != "/api/states" {
		t.Errorf("as the add-on: %v", body)
	}
	_, body = get(t, gateway(ha, Identity{Login: "kid@example.com"}), "/api/states")
	if body["auth"] != "Bearer kid-token" {
		t.Errorf("as a person with a token: %v", body)
	}
	_, body = get(t, gateway(ha, Identity{Login: "owner@example.com"}), "/local/x.json", "Authorization", "Bearer from-the-app")
	if body["auth"] != "" || body["path"] != "/local/x.json" {
		t.Errorf("/local: %v", body)
	}
}

func TestOnlyTheAPIAndLocalFilesAreReachable(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com"})
	for path, want := range map[string]int{"/auth/token": 404, "/": 404, "/lovelace": 404, "/api/../auth/token": 400, "/api/states": 200} {
		if code, _ := get(t, g, path); code != want {
			t.Errorf("%s answered %d, want %d", path, code, want)
		}
	}
}

func TestTheWebSocketSignsInWithTheRealToken(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	front := httptest.NewServer(gateway(ha, Identity{Login: "kid@example.com"}))
	defer front.Close()
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+"/api/websocket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, m, _ := c.Read(ctx); !strings.Contains(string(m), "auth_required") {
		t.Fatalf("first message: %s", m)
	}
	c.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","access_token":"placeholder"}`))
	_, ok, _ := c.Read(ctx)
	if !strings.Contains(string(ok), `"access_token":"kid-token"`) || strings.Contains(string(ok), "placeholder") {
		t.Errorf("Home Assistant saw: %s", ok)
	}
	later := `{"id":1,"type":"auth","access_token":"placeholder"}`
	c.Write(ctx, websocket.MessageText, []byte(later))
	if _, echo, _ := c.Read(ctx); string(echo) != later {
		t.Errorf("a later message was changed: %s", echo)
	}

	refused := httptest.NewServer(gateway(ha, Identity{Login: "stranger@example.com"}))
	defer refused.Close()
	if _, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(refused.URL, "http")+"/api/websocket", nil); err == nil || res == nil || res.StatusCode != 403 {
		t.Errorf("a stranger's WebSocket was not refused: %v", err)
	}
}

// A web page in a browser on an allowed person's device must get nothing: the browser marks its requests.
func TestWebPagesAreRefused(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com"})
	for _, c := range []struct {
		name   string
		header []string
		want   int
	}{
		{"a native app", nil, 200},
		{"an Origin", []string{"Origin", "https://evil.example"}, 403},
		{"a null Origin", []string{"Origin", "null"}, 403},
		{"the add-on's own page", []string{"Origin", "https://atrium.tail1234.ts.net", "Sec-Fetch-Site", "same-origin"}, 200},
		{"a page on another machine of the tailnet", []string{"Origin", "https://other.tail1234.ts.net", "Sec-Fetch-Site", "same-site"}, 403},
		{"Sec-Fetch-Site cross-site", []string{"Sec-Fetch-Site", "cross-site"}, 403},
		{"Sec-Fetch-Site same-site without Origin", []string{"Sec-Fetch-Site", "same-site"}, 403},
		{"Sec-Fetch-Site same-origin", []string{"Sec-Fetch-Site", "same-origin"}, 200},
		{"Sec-Fetch-Site none (typed in the address bar)", []string{"Sec-Fetch-Site", "none"}, 200},
	} {
		for _, path := range []string{"/api/states", "/local/x.json", "/atrium/hello"} {
			if code, _ := get(t, g, path, c.header...); code != c.want {
				t.Errorf("%s, %s: answered %d, want %d", c.name, path, code, c.want)
			}
		}
	}
	r := httptest.NewRequest("POST", "/api/services/light/turn_on", strings.NewReader(`{"entity_id":"light.x"}`))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("a no-cors POST from a page answered %d", w.Code)
	}

	front := httptest.NewServer(g)
	defer front.Close()
	ctx := context.Background()
	opts := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}}}
	if _, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+"/api/websocket", opts); err == nil || res == nil || res.StatusCode != 403 {
		t.Errorf("a page's WebSocket was not refused: %v", err)
	}
	if c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+"/api/websocket", nil); err != nil {
		t.Errorf("an app's WebSocket was refused: %v", err)
	} else {
		c.CloseNow()
	}
}

// A name that is not this machine's (an attacker's domain pointed at the gateway's address) is refused.
func TestOnlyThisMachinesNamesAreAnswered(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com"})
	for host, want := range map[string]int{
		"atrium": 200, "Atrium.tail1234.ts.net": 200, "atrium.tail1234.ts.net.": 200, "atrium.tail1234.ts.net:443": 200,
		"100.64.0.9": 200, "100.64.0.9:80": 200,
		"evil.example": 421, "atrium.tail1234.ts.net.evil.example": 421, "": 421,
	} {
		for _, path := range []string{"/api/states", "/atrium/hello"} {
			r := httptest.NewRequest("GET", path, nil)
			r.Host = host
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code != want {
				t.Errorf("Host %q, %s: answered %d, want %d", host, path, w.Code, want)
			}
		}
	}
}

func TestTheSupervisorIsNotReachable(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com"})
	for path, want := range map[string]int{"/api/hassio/supervisor/info": 403, "/api/hassio": 403, "/api/hassio_ingress/x": 403, "/api/hassio_app/x": 403, "/api/states": 200, "/api/history/period": 200} {
		if code, _ := get(t, g, path); code != want {
			t.Errorf("%s answered %d, want %d", path, code, want)
		}
	}
}

func TestTheHouseIsBuiltHereAndTheConfigurationApplies(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com"})
	// No mapping yet: an empty house, and the set-up page is told.
	code, h := get(t, g, "/atrium/house")
	if code != 200 || len(h["rooms"].([]any)) != 0 {
		t.Fatalf("house without a mapping: %d %v", code, h)
	}
	if _, setup := get(t, g, "/atrium/setup"); setup["layout"].(map[string]any)["exists"] != false {
		t.Errorf("setup should say there is no layout: %v", setup)
	}
	// The mapping appears (the skill wrote it): the next house.json renders it, and a missing entity is reported.
	g.MappingPath = filepath.Join(t.TempDir(), "atrium.yaml")
	os.WriteFile(g.MappingPath, []byte("rooms:\n  - id: den\n    entities: [light.den_lamp, light.nowhere]\n"), 0o644)
	if !g.readMapping() {
		t.Fatal("the mapping was not read")
	}
	code, h = get(t, g, "/atrium/house")
	if code != 200 || h["name"] != "Test Home" {
		t.Fatalf("house: %d %v", code, h)
	}
	rooms := h["rooms"].([]any)
	den := rooms[0].(map[string]any)
	if len(rooms) != 1 || den["name"] != "Den" || len(den["entities"].([]any)) != 1 {
		t.Errorf("rooms: %v", rooms)
	}
	if names := h["names"].(map[string]any); names["light.den_lamp"] != "Lamp" {
		t.Errorf("names: %v", names)
	}
	if _, setup := get(t, g, "/atrium/setup"); fmt.Sprint(setup["layout"].(map[string]any)["problems"]) != "[room den: Home Assistant has no light.nowhere]" {
		t.Errorf("setup should report the missing entity: %v", setup["layout"])
	}
	// The layout can be delivered to the add-on itself: checked, saved, rendered, and what it lacks is reported.
	put := func(body string, header ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/atrium/mapping", strings.NewReader(body))
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	if w := put("rooms: [\n"); w.Code != 400 || !strings.Contains(w.Body.String(), "refused") {
		t.Errorf("a broken layout was not refused: %d %s", w.Code, w.Body.String())
	}
	w := put("rooms:\n  - id: den\n    entities: [light.den_lamp, light.elsewhere]\n")
	var answer map[string]any
	json.Unmarshal(w.Body.Bytes(), &answer)
	if w.Code != 200 || answer["rooms"] != 1.0 || fmt.Sprint(answer["problems"]) != "[room den: Home Assistant has no light.elsewhere]" {
		t.Errorf("PUT mapping: %d %v", w.Code, answer)
	}
	if saved, _ := os.ReadFile(g.MappingPath); !strings.Contains(string(saved), "light.elsewhere") {
		t.Error("the layout was not saved")
	}
	if code, body := get(t, g, "/atrium/setup"); code != 200 || fmt.Sprint(body["layout"].(map[string]any)["problems"]) != "[room den: Home Assistant has no light.elsewhere]" {
		t.Errorf("setup after PUT: %v", body["layout"])
	}
	r := httptest.NewRequest("GET", "/atrium/mapping", nil)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "light.elsewhere") {
		t.Errorf("GET mapping: %d", w.Code)
	}
	if w := put("rooms: []\n", "Origin", "https://evil.example"); w.Code != 403 {
		t.Errorf("another site's page wrote the layout: %d", w.Code)
	}
	// A save that fails must say so, not report the old layout as saved.
	g.MappingPath = filepath.Join(t.TempDir(), "no-such-folder", "atrium.yaml")
	if w := put("rooms: []\n"); w.Code != 500 || !strings.Contains(w.Body.String(), "could not save") {
		t.Errorf("a failed save answered %d %s", w.Code, w.Body.String())
	}
	g.MappingPath = filepath.Join(t.TempDir(), "atrium.yaml")
	if w := put(strings.Repeat("# padding\n", 1<<17) + "rooms: []\n"); w.Code != 413 {
		t.Errorf("an oversized layout answered %d", w.Code)
	}
	stranger := gateway(ha, Identity{Login: "guest@example.com"})
	stranger.MappingPath = g.MappingPath
	r = httptest.NewRequest("PUT", "/atrium/mapping", strings.NewReader("rooms: []\n"))
	w = httptest.NewRecorder()
	stranger.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("an allowed non-owner changed the layout: %d", w.Code)
	}
	// A broken file is refused and the last good layout stays.
	os.WriteFile(g.MappingPath, []byte("rooms: [\n"), 0o644)
	os.Chtimes(g.MappingPath, time.Now(), time.Now().Add(time.Second))
	g.readMapping()
	if _, h := get(t, g, "/atrium/house"); len(h["rooms"].([]any)) != 1 {
		t.Errorf("a refused mapping should keep the previous layout: %v", h)
	}
	if _, setup := get(t, g, "/atrium/setup"); setup["layout"].(map[string]any)["error"] == nil {
		t.Errorf("setup should show the file's error: %v", setup["layout"])
	}

	r = httptest.NewRequest("PUT", "/atrium/config", strings.NewReader(`{"rooms":{"den":{"name":"Study"}},"entities":{"light.den_lamp":{"name":"Reading lamp"}}}`))
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("put config: %d %s", w.Code, w.Body)
	}
	_, h = get(t, g, "/atrium/house")
	if h["rooms"].([]any)[0].(map[string]any)["name"] != "Study" || h["names"].(map[string]any)["light.den_lamp"] != "Reading lamp" {
		t.Errorf("the configuration did not apply: %v", h)
	}
	code, cfg := get(t, g, "/atrium/config")
	if code != 200 || cfg["rooms"] == nil {
		t.Errorf("config: %d %v", code, cfg)
	}
	r = httptest.NewRequest("PUT", "/atrium/config", strings.NewReader(`{"entities":{"nope":{"hidden":true}}}`))
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("a bad configuration was accepted: %d", w.Code)
	}
	if code, _ := get(t, gateway(ha, Identity{Login: "stranger@example.com"}), "/atrium/house"); code != 403 {
		t.Errorf("a stranger got the house: %d", code)
	}
	if h["builtAt"] == nil {
		t.Error("house.json does not say when it was built")
	}
	// Only the owner (or an admin) may change the layout for everyone.
	for login, want := range map[string]int{"guest@example.com": 403, "kid@example.com": 403, "owner@example.com": 204, "admin@example.com": 204} {
		g := gateway(ha, Identity{Login: login})
		g.Opts.Admins = []string{"Admin@example.com"}
		r := httptest.NewRequest("PUT", "/atrium/config", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("PUT by %s answered %d, want %d", login, w.Code, want)
		}
	}
}

func TestIngressTakesHomeAssistantsWordForWhoIsSignedIn(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{}) // Tailscale knows nothing about these connections
	in := Ingress{g}
	askFrom := func(peer, path string, header ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "172.30.33.5:8099" // the Supervisor's view of the add-on: not one of the tailnet names
		r.RemoteAddr = peer + ":41000"
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		in.ServeHTTP(w, r)
		return w
	}
	ask := func(path string, header ...string) *httptest.ResponseRecorder {
		return askFrom("172.30.32.2", path, header...)
	}
	admin := []string{"X-Remote-User-Name", "admin@example.com", "X-Remote-User-Display-Name", "The admin", "X-Remote-User-Id", "u2", "X-Ingress-Path", "/api/hassio_ingress/tok"}
	// Another container on the add-on network claiming to be the Supervisor gets nothing, whatever it sends.
	if w := askFrom("172.30.33.9", "/api/states", admin...); w.Code != 403 {
		t.Errorf("a request from another container answered %d", w.Code)
	}
	if w := ask("/atrium/hello"); w.Code != 403 {
		t.Errorf("no user header, answered %d", w.Code)
	}
	w := ask("/atrium/hello", admin...)
	var hello map[string]any
	json.Unmarshal(w.Body.Bytes(), &hello)
	if w.Code != 200 || hello["allowed"] != true || hello["configures"] != true || hello["login"] != "admin@example.com" || hello["name"] != "The admin" {
		t.Errorf("hello through ingress: %d %v", w.Code, hello)
	}
	if w := ask("/api/states", admin...); w.Code != 200 {
		t.Errorf("the API through ingress answered %d", w.Code)
	}
	if w := ask("/api/states", append(admin, "Sec-Fetch-Site", "cross-site")...); w.Code != 403 {
		t.Errorf("a cross-site request through ingress answered %d", w.Code)
	}
	// A Home Assistant user who is not an administrator is not let in: through ingress everyone would get the
	// add-on's own access. (The Supervisor refuses them first, as panel_admin is on; this does not rely on that.)
	w = ask("/atrium/hello", "X-Remote-User-Name", "someone@example.com", "X-Remote-User-Id", "u1")
	json.Unmarshal(w.Body.Bytes(), &hello)
	if hello["allowed"] != false || hello["configures"] != false {
		t.Errorf("a non-admin through ingress: %v", hello)
	}
	if w := ask("/api/states", "X-Remote-User-Name", "someone@example.com", "X-Remote-User-Id", "u1"); w.Code != 403 {
		t.Errorf("a non-admin got the API through ingress: %d", w.Code)
	}
	// Setting up: the panel works before the tailnet does, and shows the administrator the sign-in link.
	g.mu.Lock()
	g.setup = Setup{State: "needs-login", AuthURL: "https://login.tailscale.com/a/abc"}
	g.mu.Unlock()
	var setup Setup
	w = ask("/atrium/setup", admin...)
	json.Unmarshal(w.Body.Bytes(), &setup)
	if w.Code != 200 || setup.State != "needs-login" || setup.AuthURL != "https://login.tailscale.com/a/abc" {
		t.Errorf("setup through ingress: %d %+v", w.Code, setup)
	}
	if w := ask("/atrium/setup", "X-Remote-User-Name", "someone@example.com", "X-Remote-User-Id", "u1"); w.Code != 403 {
		t.Errorf("a non-admin saw the sign-in link: %d", w.Code)
	}
}

func TestTheWebAppIsServedWithItsBase(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><head><title>Atrium</title></head><body></body></html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "setup.html"), []byte("<!doctype html><html><head><title>Atrium</title></head><body>set-up</body></html>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644)
	g := gateway(ha, Identity{Login: "owner@example.com"})
	g.WebDir = dir
	for path, want := range map[string]string{"/": `<base href="/">`, "/rooms": `<base href="/">`, "/assets/app.js": "console.log(1)"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %q", path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "172.30.33.5:8099"
	r.RemoteAddr = "172.30.32.2:41000"
	r.Header.Set("X-Remote-User-Name", "admin@example.com")
	r.Header.Set("X-Remote-User-Id", "u2")
	r.Header.Set("X-Ingress-Path", "/api/hassio_ingress/tok")
	w := httptest.NewRecorder()
	Ingress{g}.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `<base href="/api/hassio_ingress/tok/">`) || !strings.Contains(w.Body.String(), "set-up") {
		t.Errorf("through ingress (should be the set-up page): %q", w.Body.String())
	}
	if code, _ := get(t, gateway(ha, Identity{Login: "stranger@example.com"}), "/"); code != 403 {
		t.Errorf("a stranger got the web app: %d", code)
	}
}

// Through ingress the browser's Origin is Home Assistant's and the Host is the Supervisor's view of the add-on,
// so the WebSocket must not insist that they match (it did, and the panel never connected).
func TestTheIngressWebSocketConnectsWithHomeAssistantsOrigin(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{})
	g.Supervisor = []string{"127.0.0.1", "::1"} // the test's own connection stands in for the Supervisor's
	front := httptest.NewServer(Ingress{g})
	defer front.Close()
	ctx := context.Background()
	opts := &websocket.DialOptions{HTTPHeader: http.Header{
		"Origin": {"http://192.168.1.106:8123"}, "Sec-Fetch-Site": {"same-origin"},
		"X-Remote-User-Name": {"admin@example.com"}, "X-Remote-User-Id": {"u2"}, "X-Ingress-Path": {"/api/hassio_ingress/tok"},
	}}
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+"/api/websocket", opts)
	if err != nil {
		t.Fatalf("the panel's WebSocket was refused: %v", err)
	}
	defer c.CloseNow()
	if _, m, _ := c.Read(ctx); !strings.Contains(string(m), "auth_required") {
		t.Fatalf("first message: %s", m)
	}
}

// The add-on is an MCP server: a Claude on the tailnet (or an administrator through ingress) makes the layout
// with its tools, no token anywhere.
func TestTheMCPServerMakesTheLayout(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{Login: "owner@example.com"})
	g.MappingPath = filepath.Join(t.TempDir(), "atrium.yaml")
	g.SkillDir = t.TempDir()
	os.WriteFile(filepath.Join(g.SkillDir, "mapping.md"), []byte("# The contract\n"), 0o644)
	os.WriteFile(filepath.Join(g.SkillDir, "SKILL.md"), []byte("# The skill\n"), 0o644)
	rpc := func(g *Gateway, body string) (int, map[string]any) {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	call := func(g *Gateway, tool string, args string) (string, bool) {
		_, m := rpc(g, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+args+`}}`)
		res, _ := m["result"].(map[string]any)
		if res == nil {
			t.Fatalf("%s: %v", tool, m)
		}
		content := res["content"].([]any)[0].(map[string]any)
		isErr, _ := res["isError"].(bool)
		return content["text"].(string), isErr
	}
	if code, m := rpc(g, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); code != 200 || m["result"].(map[string]any)["protocolVersion"] != mcpProtocol {
		t.Fatalf("initialize: %d %v", code, m)
	}
	if code, _ := rpc(g, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Errorf("a notification answered %d", code)
	}
	_, m := rpc(g, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if tools := m["result"].(map[string]any)["tools"].([]any); len(tools) != len(mcpTools) {
		t.Errorf("tools/list: %d tools", len(tools))
	}
	if text, _ := call(g, "atrium_contract", `{}`); text != "# The contract\n" {
		t.Errorf("contract: %q", text)
	}
	if text, _ := call(g, "atrium_home", `{"domain":"light"}`); !strings.Contains(text, "light.den_lamp") || !strings.Contains(text, "Den") {
		t.Errorf("home: %q", text)
	}
	if text, isErr := call(g, "atrium_mapping_get", `{}`); !isErr || !strings.Contains(text, "no layout") {
		t.Errorf("get before any: %q %v", text, isErr)
	}
	if text, isErr := call(g, "atrium_mapping_put", `{"yaml":"rooms: [\n"}`); !isErr || !strings.HasPrefix(text, "Refused") {
		t.Errorf("a broken layout: %q %v", text, isErr)
	}
	text, isErr := call(g, "atrium_mapping_put", `{"yaml":"rooms:\n  - id: den\n    entities: [light.den_lamp, light.ghost]\n"}`)
	if isErr || !strings.Contains(text, `"rooms":1`) || !strings.Contains(text, "light.ghost") {
		t.Errorf("put: %q %v", text, isErr)
	}
	if text, _ := call(g, "atrium_mapping_get", `{}`); !strings.Contains(text, "light.ghost") {
		t.Errorf("get after put: %q", text)
	}
	if text, _ := call(g, "atrium_layout_status", `{}`); !strings.Contains(text, `"rooms": 1`) {
		t.Errorf("status: %q", text)
	}
	guest := gateway(ha, Identity{Login: "guest@example.com"})
	guest.MappingPath = g.MappingPath
	if text, isErr := call(guest, "atrium_mapping_put", `{"yaml":"rooms: []\n"}`); !isErr || !strings.Contains(text, "Only the owner") {
		t.Errorf("a guest changed the layout: %q", text)
	}
	if _, m := rpc(g, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`); m["error"] == nil {
		t.Error("an unknown tool was not an error")
	}
}

// An add-on the owner lists under writers (the Claude Code add-on) writes the layout from inside Home Assistant:
// known by its address on the Supervisor's network, no token, no file access to anything.
func TestAListedAddonWritesTheLayoutFromInsideHomeAssistant(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	g := gateway(ha, Identity{})
	g.Opts.Writers = []string{"d7e97e69_claudecode"}
	g.ResolveAddon = func(slug string) []string {
		if slug == "d7e97e69_claudecode" {
			return []string{"fd0c:ac1e:2100::3", "172.30.33.3"}
		}
		return nil
	}
	running := true
	g.CheckAddon = func(slug, ip string) bool { return running && slug == "d7e97e69_claudecode" && ip == "172.30.33.3" }
	g.MappingPath = filepath.Join(t.TempDir(), "atrium.yaml")
	in := Ingress{g}
	from := func(peer, method, path, body string, header ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "df84f1fd-atrium:8099"
		r.RemoteAddr = peer + ":40000"
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		in.ServeHTTP(w, r)
		return w
	}
	if w := from("172.30.33.9", "PUT", "/atrium/mapping", "rooms: []\n"); w.Code != 403 {
		t.Errorf("an unlisted container wrote the layout: %d", w.Code)
	}
	if w := from("172.30.33.3", "PUT", "/atrium/mapping", "rooms: []\n", "Origin", "http://evil.example"); w.Code != 403 {
		t.Errorf("a web page from the listed container's address wrote the layout: %d", w.Code)
	}
	w := from("172.30.33.3", "PUT", "/atrium/mapping", "rooms:\n  - id: den\n    entities: [light.den_lamp]\n")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rooms":1`) {
		t.Errorf("the listed add-on could not write the layout: %d %s", w.Code, w.Body.String())
	}
	w = from("172.30.33.3", "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atrium_layout_status","arguments":{}}}`, "Content-Type", "application/json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `\"rooms\": 1`) {
		t.Errorf("MCP from the listed add-on: %d %s", w.Code, w.Body.String())
	}
	var hello map[string]any
	json.Unmarshal(from("172.30.33.3", "GET", "/atrium/hello", "").Body.Bytes(), &hello)
	if hello["login"] != "d7e97e69_claudecode" || hello["configures"] != true {
		t.Errorf("hello as the add-on: %v", hello)
	}
	// The writer gets the layout, not Home Assistant's API through Atrium's token.
	if w := from("172.30.33.3", "GET", "/api/states", ""); w.Code != 403 {
		t.Errorf("the writer reached the API: %d", w.Code)
	}
	// An address the DNS still has but the Supervisor says is not the running add-on (reused after a restart).
	running = false
	if w := from("172.30.33.3", "PUT", "/atrium/mapping", "rooms: []\n"); w.Code != 403 {
		t.Errorf("a stale address wrote the layout: %d", w.Code)
	}
	running = true
	// IPv6 peers are not matched (the Supervisor reports one IPv4 address).
	r := httptest.NewRequest("PUT", "/atrium/mapping", strings.NewReader("rooms: []\n"))
	r.RemoteAddr = "[fd0c:ac1e:2100::3]:40000"
	w = httptest.NewRecorder()
	in.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("an IPv6 peer wrote the layout: %d", w.Code)
	}
}
