package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A stand-in Supervisor: a few installed add-ons, this add-on's options, and a record of what was posted.
type fakeSupervisor struct {
	*httptest.Server
	mu      sync.Mutex
	options map[string]any // this add-on's options, as the Supervisor keeps them
	listing bool           // whether /supervisor/info carries the addons list (older Supervisors do)
	posted  []map[string]any
}

func newFakeSupervisor(t *testing.T) *fakeSupervisor {
	f := &fakeSupervisor{listing: true, options: map[string]any{
		"hostname": "atrium", "allow": []any{"guest@example.com"}, "people": []any{map[string]any{"login": "kid@example.com", "token": "kid-token"}},
		"admins": []any{}, "writers": []any{"11111111_first"},
	}}
	installed := map[string]AddonInfo{
		"d7e97e69_claudecode": {Name: "Claude Code", Slug: "d7e97e69_claudecode", Repository: "d7e97e69", State: "started", Version: "1.2.3"},
		"core_ssh":            {Name: "Terminal & SSH", Slug: "core_ssh", Repository: "core", State: "started", Version: "9.0"},
		"11111111_first":      {Name: "Another app", Slug: "11111111_first", Repository: "11111111", State: "stopped", Version: "0.1"},
		"df84f1fd_atrium":     {Name: "Atrium", Slug: "df84f1fd_atrium", Repository: "df84f1fd", State: "started", Version: version},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer addon-token" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/supervisor/info":
			data := map[string]any{"version": "2026.11.0"}
			if f.listing {
				var list []AddonInfo
				for _, a := range installed {
					list = append(list, a)
				}
				data["addons"] = list
			}
			json.NewEncoder(w).Encode(map[string]any{"result": "ok", "data": data})
		case r.Method == "GET" && r.URL.Path == "/addons/self/info":
			json.NewEncoder(w).Encode(map[string]any{"result": "ok", "data": map[string]any{"slug": "df84f1fd_atrium", "options": f.options}})
		case r.Method == "POST" && r.URL.Path == "/addons/self/options":
			body, _ := io.ReadAll(r.Body)
			var m map[string]any
			if json.Unmarshal(body, &m) != nil || m["options"] == nil {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"result":"error","message":"options required"}`)
				return
			}
			f.posted = append(f.posted, m)
			f.options = m["options"].(map[string]any)
			fmt.Fprint(w, `{"result":"ok","data":{}}`)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/addons/") && strings.HasSuffix(r.URL.Path, "/info"):
			slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/addons/"), "/info")
			a, ok := installed[slug]
			if !ok {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"result":"error","message":"Addon does not exist"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"result": "ok", "data": a})
		default:
			w.WriteHeader(403)
			fmt.Fprint(w, `{"result":"error","message":"Forbidden"}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// An administrator, through Home Assistant's sidebar, lists the installed add-ons and allows one to write the
// layout: Atrium adds the slug to its own options (the whole object, since /addons/self/options replaces them)
// and applies it at once.
func TestAnAdministratorGrantsWritersFromTheSetupPage(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	sup := newFakeSupervisor(t)
	g := gateway(ha, Identity{})
	g.SupervisorURL = sup.URL
	g.Slug = "df84f1fd_atrium"
	g.Opts.Writers = []string{"11111111_first"}
	g.MappingPath = filepath.Join(t.TempDir(), "atrium.yaml")
	admin := []string{"X-Remote-User-Name", "admin@example.com", "X-Remote-User-Id", "u2", "X-Ingress-Path", "/api/hassio_ingress/tok"}
	ingress := func(method, path, body string, header ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host, r.RemoteAddr = "172.30.33.5:8099", "172.30.32.2:41000"
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		Ingress{g}.ServeHTTP(w, r)
		return w
	}
	// The list: every installed add-on, by name, with slug and repository; nothing is pre-selected (the page's job).
	w := ingress("GET", "/atrium/addons", "", admin...)
	var listed struct{ Addons []AddonInfo }
	json.Unmarshal(w.Body.Bytes(), &listed)
	if w.Code != 200 || len(listed.Addons) != 4 || listed.Addons[0].Name != "Another app" || listed.Addons[1].Slug != "df84f1fd_atrium" || listed.Addons[2].Repository != "d7e97e69" {
		t.Errorf("addons: %d %s", w.Code, w.Body.String())
	}
	// A slug typed in is checked with the Supervisor.
	w = ingress("GET", "/atrium/addons?slug=core_ssh", "", admin...)
	var one AddonInfo
	json.Unmarshal(w.Body.Bytes(), &one)
	if w.Code != 200 || one.Name != "Terminal & SSH" || one.Repository != "core" {
		t.Errorf("one add-on: %d %s", w.Code, w.Body.String())
	}
	if w := ingress("GET", "/atrium/addons?slug=nope_nothing", "", admin...); w.Code != 502 || !strings.Contains(w.Body.String(), "Supervisor status 404") {
		t.Errorf("an unknown slug: %d %s", w.Code, w.Body.String())
	}
	// Granting: the Supervisor gets the whole options object back with the slug added, and Atrium applies it now.
	w = ingress("POST", "/atrium/writers", `{"slug":"d7e97e69_claudecode"}`, append(admin, "Content-Type", "application/json")...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"writers":["11111111_first","d7e97e69_claudecode"]`) || !strings.Contains(w.Body.String(), `"name":"Claude Code"`) {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	sup.mu.Lock()
	if len(sup.posted) != 1 {
		t.Fatalf("posted %d times", len(sup.posted))
	}
	opts := sup.posted[0]["options"].(map[string]any)
	sup.mu.Unlock()
	if fmt.Sprint(opts["writers"]) != "[11111111_first d7e97e69_claudecode]" || opts["hostname"] != "atrium" || fmt.Sprint(opts["people"]) != "[map[login:kid@example.com token:kid-token]]" {
		t.Errorf("the Supervisor got: %v", opts)
	}
	if got := g.writers(); fmt.Sprint(got) != "[11111111_first d7e97e69_claudecode]" {
		t.Errorf("in memory: %v", got)
	}
	w = ingress("GET", "/atrium/setup", "", admin...)
	if !strings.Contains(w.Body.String(), `"writers":["11111111_first","d7e97e69_claudecode"]`) {
		t.Errorf("setup after the grant: %s", w.Body.String())
	}
	// …so the add-on writes without a restart: its address is now a listed slug's.
	g.ResolveAddon = func(slug string) ([]string, error) {
		if slug == "d7e97e69_claudecode" {
			return []string{"172.30.33.3"}, nil
		}
		return nil, fmt.Errorf("no such host")
	}
	g.CheckAddon = func(slug, ip string) error { return nil }
	r := httptest.NewRequest("PUT", "/atrium/mapping", strings.NewReader("rooms: []\n"))
	r.RemoteAddr = "172.30.33.3:40000"
	w = httptest.NewRecorder()
	Ingress{g}.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("the newly listed add-on could not write: %d %s", w.Code, w.Body.String())
	}
	// Granting twice adds nothing twice; Atrium itself, an unknown slug and a bad body are refused.
	if w := ingress("POST", "/atrium/writers", `{"slug":"d7e97e69_claudecode"}`, admin...); w.Code != 200 || strings.Count(w.Body.String(), "d7e97e69_claudecode") != 2 {
		t.Errorf("granting twice: %d %s", w.Code, w.Body.String())
	}
	if w := ingress("POST", "/atrium/writers", `{"slug":"df84f1fd_atrium"}`, admin...); w.Code != 400 {
		t.Errorf("Atrium itself: %d %s", w.Code, w.Body.String())
	}
	if w := ingress("POST", "/atrium/writers", `{"slug":"nope_nothing"}`, admin...); w.Code != 502 || !strings.Contains(w.Body.String(), "404") {
		t.Errorf("an unknown slug: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{`{}`, `{"slug":"../self"}`, `{"slug":"a b"}`, `not json`} {
		if w := ingress("POST", "/atrium/writers", bad, admin...); w.Code != 400 {
			t.Errorf("%q: %d", bad, w.Code)
		}
	}
	// Through ingress only, and only administrators: not a non-admin, not the tailnet owner, not a writer, not the
	// home-network port.
	if w := ingress("POST", "/atrium/writers", `{"slug":"core_ssh"}`, "X-Remote-User-Name", "someone@example.com", "X-Remote-User-Id", "u1"); w.Code != 403 {
		t.Errorf("a non-admin granted a writer: %d", w.Code)
	}
	if w := ingress("GET", "/atrium/addons", "", "X-Remote-User-Name", "someone@example.com", "X-Remote-User-Id", "u1"); w.Code != 403 {
		t.Errorf("a non-admin listed add-ons: %d", w.Code)
	}
	owner := gateway(ha, Identity{Login: "owner@example.com"})
	owner.SupervisorURL = sup.URL
	for _, c := range []struct{ method, path string }{{"GET", "/atrium/addons"}, {"POST", "/atrium/writers"}} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(`{"slug":"core_ssh"}`))
		w := httptest.NewRecorder()
		owner.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("the tailnet owner reached %s: %d", c.path, w.Code)
		}
	}
	r = httptest.NewRequest("POST", "/atrium/writers", strings.NewReader(`{"slug":"core_ssh"}`))
	r.RemoteAddr = "172.30.33.3:40000"
	w = httptest.NewRecorder()
	Ingress{g}.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("a writer granted a writer: %d", w.Code)
	}
	r = httptest.NewRequest("POST", "/atrium/writers", strings.NewReader(`{"slug":"core_ssh"}`))
	w = httptest.NewRecorder()
	NewLocal(g).ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("the home-network port answered: %d", w.Code)
	}
	if !writerPath("/atrium/mapping") || writerPath("/atrium/writers") || writerPath("/atrium/addons") {
		t.Error("writerPath lets a writer at the granting routes")
	}
	// A Supervisor that no longer lists add-ons in /supervisor/info: an empty list, and the page asks for a slug.
	sup.mu.Lock()
	sup.listing = false
	sup.mu.Unlock()
	if w := ingress("GET", "/atrium/addons", "", admin...); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"addons":[]}` {
		t.Errorf("without a list: %d %s", w.Code, w.Body.String())
	}
}
