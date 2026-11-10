package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
	"github.com/coder/websocket"
)

// mayConfigure says who may change the layout for everyone: the login that signed the machine in, and the
// logins listed under admins (the only way when the machine is tagged and has no owner).
func (g *Gateway) mayConfigure(id Identity) bool {
	if id.Login == "" || id.Tagged {
		return false
	}
	if id.Local {
		return false // the add-on's port serves the layout, never changes to it
	}
	if id.Addon {
		return true // listed by the owner for exactly this
	}
	if id.HomeAssistant {
		return g.isHAAdmin(id.UserID)
	}
	if id.Owner {
		return true
	}
	if owner := g.owner(); owner != "" && strings.EqualFold(id.Login, owner) {
		return true
	}
	for _, a := range g.Opts.Admins {
		if strings.EqualFold(strings.TrimSpace(a), id.Login) {
			return true
		}
	}
	return false
}

// dump asks Home Assistant for everything the renderer needs: the four registries and every state, over one
// WebSocket session, and the house's name.
func (g *Gateway) dump(ctx context.Context) (house.Dump, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	up := g.Addon
	c, _, err := websocket.Dial(ctx, up.WS, nil)
	if err != nil {
		return house.Dump{}, fmt.Errorf("Home Assistant's WebSocket: %w", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(-1)
	read := func() (map[string]any, error) {
		_, data, err := c.Read(ctx)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, json.Unmarshal(data, &m)
	}
	write := func(m map[string]any) error {
		data, _ := json.Marshal(m)
		return c.Write(ctx, websocket.MessageText, data)
	}
	if first, err := read(); err != nil || first["type"] != "auth_required" {
		return house.Dump{}, fmt.Errorf("Home Assistant did not ask for a token: %v", err)
	}
	if err := write(map[string]any{"type": "auth", "access_token": up.Token}); err != nil {
		return house.Dump{}, err
	}
	if ok, err := read(); err != nil || ok["type"] != "auth_ok" {
		return house.Dump{}, fmt.Errorf("Home Assistant refused the add-on's token: %v %v", ok["message"], err)
	}
	kinds := []string{"config/area_registry/list", "config/floor_registry/list", "config/entity_registry/list", "config/device_registry/list", "get_states", "get_config", "config_entries/get", "recorder/list_statistic_ids"}
	for i, kind := range kinds {
		if err := write(map[string]any{"id": i + 1, "type": kind}); err != nil {
			return house.Dump{}, err
		}
	}
	results := map[int]any{}
	for len(results) < len(kinds) {
		m, err := read()
		if err != nil {
			return house.Dump{}, err
		}
		id, _ := m["id"].(float64)
		if m["type"] != "result" || id == 0 {
			continue
		}
		if ok, _ := m["success"].(bool); !ok {
			// Only the stale list and the meters' check need these; a Home Assistant without them still has a house.
			if k := kinds[int(id)-1]; k == "config_entries/get" || k == "recorder/list_statistic_ids" {
				results[int(id)] = nil
				continue
			}
			return house.Dump{}, fmt.Errorf("%s: %v", kinds[int(id)-1], m["error"])
		}
		results[int(id)] = m["result"]
	}
	d := house.Dump{States: map[string]*house.State{}}
	d.Areas, d.Floors, d.Entities, d.Devices = objects(results[1]), objects(results[2]), objects(results[3]), objects(results[4])
	if results[7] != nil {
		d.ConfigEntries = objects(results[7])
	}
	if results[8] != nil {
		d.Statistics = map[string]bool{}
		for _, s := range objects(results[8]) {
			if id, _ := s["statistic_id"].(string); id != "" {
				d.Statistics[id] = true
			}
		}
	}
	for _, s := range objects(results[5]) {
		id, _ := s["entity_id"].(string)
		state, _ := s["state"].(string)
		attrs, _ := s["attributes"].(map[string]any)
		if id != "" {
			st := &house.State{State: state, Attributes: attrs}
			if lc, _ := s["last_changed"].(string); lc != "" {
				st.LastChanged, _ = time.Parse(time.RFC3339Nano, lc)
			}
			d.States[id] = st
		}
	}
	if cfg, _ := results[6].(map[string]any); cfg != nil {
		d.Name, _ = cfg["location_name"].(string)
		state, _ := cfg["state"].(string)
		d.Running = state == "RUNNING"
	}
	d.RunningFor = runningFor(d, time.Now())
	// How each camera streams (HLS through the API, or WebRTC only): the apps choose the player by it, and a
	// WebRTC-only camera (Nest) has no still image, only a placeholder.
	d.CameraStreams = map[string][]string{}
	var cams []string
	for id := range d.States {
		if strings.HasPrefix(id, "camera.") {
			cams = append(cams, id)
		}
	}
	sort.Strings(cams)
	for i, id := range cams {
		if err := write(map[string]any{"id": 100 + i, "type": "camera/capabilities", "entity_id": id}); err != nil {
			return d, nil
		}
	}
	for got := 0; got < len(cams); {
		m, err := read()
		if err != nil {
			break
		}
		id, _ := m["id"].(float64)
		if m["type"] != "result" || int(id) < 100 || int(id) >= 100+len(cams) {
			continue
		}
		got++
		if r, _ := m["result"].(map[string]any); r != nil {
			for _, t := range strs(r["frontend_stream_types"]) {
				d.CameraStreams[cams[int(id)-100]] = append(d.CameraStreams[cams[int(id)-100]], t)
			}
		}
	}
	return d, nil
}

// runningFor is how long Home Assistant has been running, read off the dump: at start Home Assistant writes a
// restored placeholder for every registered entity its integrations have not provided yet, so the oldest of those
// still standing dates the start. With none left there is nothing a settling time would decide, and the house
// counts as settled. Restored placeholders that carry no readable time cannot date the start: not settled.
func runningFor(d house.Dump, now time.Time) time.Duration {
	if !d.Running {
		return 0
	}
	var oldest time.Time
	restored := false
	for _, st := range d.States {
		if r, _ := st.Attributes["restored"].(bool); r {
			restored = true
			if !st.LastChanged.IsZero() && (oldest.IsZero() || st.LastChanged.Before(oldest)) {
				oldest = st.LastChanged
			}
		}
	}
	switch {
	case !restored:
		return house.SettleAfter
	case oldest.IsZero():
		return 0
	}
	return now.Sub(oldest)
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func objects(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// built is one house.json with when it was built.
type built struct {
	data  []byte
	etag  string
	at    time.Time
	stale bool // Home Assistant did not answer the last time; this is the previous layout
}

const houseTTL = 30 * time.Second

// staleRetry is how long a layout served stale (Home Assistant did not answer) stands before another attempt.
const staleRetry = 10 * time.Second

// houseJSON is the current layout: rebuilt from Home Assistant when older than houseTTL or when the
// configuration changed, otherwise served from memory. Concurrent callers share one rebuild, and a rebuild
// that started before the configuration changed is not kept.
func (g *Gateway) houseJSON(ctx context.Context) (built, error) {
	g.mu.Lock()
	cached, fresh := g.house, time.Since(g.house.at) < houseTTL
	g.mu.Unlock()
	if fresh && cached.data != nil {
		return cached, nil
	}
	g.mu.Lock()
	key := fmt.Sprint(g.generation) // a build for an older configuration is never joined
	g.mu.Unlock()
	v, err, _ := g.building.Do(key, func() (any, error) {
		g.mu.Lock()
		cfg, mapping, gen, before := g.config, g.mapping, g.generation, g.house
		g.mu.Unlock()
		d, err := g.dump(context.WithoutCancel(ctx))
		if err != nil {
			if before.data != nil {
				log.Printf("Home Assistant did not answer; serving the layout built at %s: %v", before.at.Format(time.RFC3339), err)
				// Better the last layout than none, and not another minute-long attempt on every request while
				// Home Assistant restarts: the stale copy stands for a little while.
				before.stale = true
				before.at = time.Now().Add(staleRetry - houseTTL)
				g.mu.Lock()
				if g.generation == gen {
					g.house = before
				}
				g.mu.Unlock()
				return before, nil
			}
			return built{}, err
		}
		h, problems := house.Render(mapping, d, cfg)
		h.BuiltAt = time.Now().UTC().Format(time.RFC3339)
		stale := house.Stale(d)
		unavailable := unavailableIn(h, d.States)
		g.mu.Lock()
		changed := !g.rendered || strings.Join(problems, "\n") != strings.Join(g.problems, "\n") || len(h.Rooms) != g.rooms
		g.problems, g.rendered, g.rooms, g.stale, g.unavailable = problems, true, len(h.Rooms), stale, unavailable
		g.haSeen = time.Now() // Home Assistant answered the registries just now
		g.mu.Unlock()
		if changed {
			for _, p := range problems {
				log.Printf("mapping: %s", p)
			}
			if len(problems) == 0 {
				log.Printf("mapping: %d rooms, no problems", len(h.Rooms))
			}
		}
		data, err := json.Marshal(h)
		if err != nil {
			return built{}, err
		}
		b := built{data: data, etag: fmt.Sprintf(`"%x"`, fnv(data)), at: time.Now()}
		g.mu.Lock()
		if g.generation == gen { // the configuration did not change meanwhile
			g.house = b
		}
		g.mu.Unlock()
		return b, nil
	})
	if err != nil {
		return built{}, err
	}
	return v.(built), nil
}

// defaultHouseJSON is the house as the add-on draws it with no atrium.yaml (the owner's preferences still
// apply), cached like the real one.
func (g *Gateway) defaultHouseJSON(ctx context.Context) (built, error) {
	g.mu.Lock()
	cached, cfg := g.houseDefault, g.config
	g.mu.Unlock()
	if cached.data != nil && time.Since(cached.at) < houseTTL {
		return cached, nil
	}
	v, err, _ := g.building.Do("default", func() (any, error) {
		d, err := g.dump(context.WithoutCancel(ctx))
		if err != nil {
			return built{}, err
		}
		h, _ := house.Render(house.Mapping{}, d, cfg)
		h.BuiltAt = time.Now().UTC().Format(time.RFC3339)
		data, err := json.Marshal(h)
		if err != nil {
			return built{}, err
		}
		b := built{data: data, etag: fmt.Sprintf(`"d%x"`, fnv(data)), at: time.Now()}
		g.mu.Lock()
		g.houseDefault = b
		g.mu.Unlock()
		return b, nil
	})
	if err != nil {
		return built{}, err
	}
	return v.(built), nil
}

func fnv(data []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range data {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

// serveHouse answers GET /atrium/house.
func (g *Gateway) serveHouse(w http.ResponseWriter, r *http.Request) {
	get := g.houseJSON
	if r.URL.Query().Get("layout") == "default" { // the defaults alone, to compare with the file's layout
		get = g.defaultHouseJSON
	}
	b, err := get(r.Context())
	if err != nil {
		http.Error(w, "Home Assistant did not answer: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", b.etag)
	if b.stale {
		w.Header().Set("X-Atrium-Stale", "1")
	}
	if r.Header.Get("If-None-Match") == b.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(b.data)
}

// serveConfig answers GET and PUT /atrium/config: the owner's overrides.
// serveMapping reads and writes atrium.yaml itself, so whoever makes the layout (the atrium-mapping skill, run
// by a Claude on the tailnet or on the Home Assistant machine) can deliver it here, with no file access to
// anything. A PUT is checked before it is written; the answer says what the new layout could not use.
func (g *Gateway) serveMapping(w http.ResponseWriter, r *http.Request, id Identity) {
	switch r.Method {
	case "GET":
		data, err := os.ReadFile(g.MappingPath)
		if err != nil {
			http.Error(w, "there is no layout yet", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
	case "PUT":
		if !g.mayConfigure(id) {
			http.Error(w, "only the owner and administrators may change the layout", http.StatusForbidden)
			return
		}
		const limit = 1 << 20
		data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(data) > limit {
			http.Error(w, "a layout is at most 1 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		text, failed, _ := g.putMapping(r.Context(), id, data)
		switch {
		case failed && strings.HasPrefix(text, "Refused"):
			http.Error(w, strings.ToLower(text[:1])+text[1:], http.StatusBadRequest)
		case failed && strings.HasPrefix(text, "Saved"):
			http.Error(w, text, http.StatusBadGateway)
		case failed:
			http.Error(w, strings.ToLower(text[:1])+text[1:], http.StatusInternalServerError)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(text))
		}
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "GET or PUT", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) serveConfig(w http.ResponseWriter, r *http.Request, id Identity) {
	switch r.Method {
	case "GET":
		g.mu.Lock()
		cfg := g.config
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
	case "PUT":
		if !g.mayConfigure(id) {
			http.Error(w, "only the owner may change the configuration", http.StatusForbidden)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var cfg house.Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			http.Error(w, "not a configuration: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := cfg.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if g.ConfigPath != "" {
			if err := house.Save(g.ConfigPath, cfg); err != nil {
				http.Error(w, "could not save: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		g.mu.Lock()
		g.config = cfg
		g.generation++
		g.house, g.houseDefault = built{}, built{} // rebuild on the next request
		g.mu.Unlock()
		log.Printf("configuration changed by %s", id.Login)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "GET or PUT", http.StatusMethodNotAllowed)
	}
}

// isHAAdmin asks Home Assistant whether a user is an administrator (config/auth/list), remembered for a while.
func (g *Gateway) isHAAdmin(userID string) bool {
	if userID == "" {
		log.Printf("ingress request without X-Remote-User-Id: cannot tell whether the user is an administrator")
		return false
	}
	g.mu.Lock()
	admins, at := g.admins, g.adminsAt
	g.mu.Unlock()
	if admins == nil || time.Since(at) > 5*time.Minute {
		admins = map[string]bool{}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if users, err := g.wsRequest(ctx, "config/auth/list"); err == nil {
			for _, u := range objects(users) {
				if id, _ := u["id"].(string); id != "" {
					admins[id] = isAdminUser(u)
				}
			}
			g.mu.Lock()
			g.admins, g.adminsAt = admins, time.Now()
			g.mu.Unlock()
		} else {
			g.mu.Lock()
			if g.admins != nil {
				// Home Assistant is not answering (restarting, say): the list it gave before still holds, and it
				// is asked again in a minute rather than on every request.
				admins = g.admins
				g.adminsAt = time.Now().Add(time.Minute - 5*time.Minute)
				log.Printf("could not list Home Assistant's users; keeping the list of %d from before: %v", len(admins), err)
			} else {
				log.Printf("could not list Home Assistant's users (so nobody is an administrator): %v", err)
			}
			g.mu.Unlock()
		}
	}
	if !admins[userID] {
		log.Printf("Home Assistant user %s is not an administrator, by its own list of %d users", userID, len(admins))
	}
	return admins[userID]
}

// isAdminUser reads Home Assistant's own description of a user: an owner, or a member of the system-admin
// group (config/auth/list has no is_admin field; it is kept as a fallback should one appear).
func isAdminUser(u map[string]any) bool {
	if active, ok := u["is_active"].(bool); ok && !active {
		return false
	}
	if owner, _ := u["is_owner"].(bool); owner {
		return true
	}
	if admin, _ := u["is_admin"].(bool); admin {
		return true
	}
	groups, _ := u["group_ids"].([]any)
	for _, g := range groups {
		if g == "system-admin" {
			return true
		}
	}
	return false
}

// wsRequest makes one request on a fresh signed-in WebSocket session.
func (g *Gateway) wsRequest(ctx context.Context, kind string) (any, error) {
	results, errs, err := g.wsBatch(ctx, []map[string]any{{"type": kind}})
	if err != nil {
		return nil, err
	}
	return results[0], errs[0]
}

// wsBatch makes several requests on one fresh signed-in WebSocket session, each a message without its id.
// An answer Home Assistant refused is that request's error; the others still stand.
func (g *Gateway) wsBatch(ctx context.Context, reqs []map[string]any) ([]any, []error, error) {
	c, _, err := websocket.Dial(ctx, g.Addon.WS, nil)
	if err != nil {
		return nil, nil, err
	}
	defer c.CloseNow()
	c.SetReadLimit(-1)
	read := func() (map[string]any, error) {
		_, data, err := c.Read(ctx)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, json.Unmarshal(data, &m)
	}
	if _, err := read(); err != nil {
		return nil, nil, err
	}
	auth, _ := json.Marshal(map[string]any{"type": "auth", "access_token": g.Addon.Token})
	if err := c.Write(ctx, websocket.MessageText, auth); err != nil {
		return nil, nil, err
	}
	if ok, err := read(); err != nil || ok["type"] != "auth_ok" {
		return nil, nil, fmt.Errorf("refused: %v", err)
	}
	for i, r := range reqs {
		m := map[string]any{"id": i + 1}
		for k, v := range r {
			m[k] = v
		}
		req, _ := json.Marshal(m)
		if err := c.Write(ctx, websocket.MessageText, req); err != nil {
			return nil, nil, err
		}
	}
	results, errs := make([]any, len(reqs)), make([]error, len(reqs))
	for left := len(reqs); left > 0; {
		m, err := read()
		if err != nil {
			return nil, nil, err
		}
		id, _ := m["id"].(float64)
		if m["type"] != "result" || id < 1 || int(id) > len(reqs) {
			continue
		}
		if ok, _ := m["success"].(bool); !ok {
			errs[int(id)-1] = fmt.Errorf("%v: %v", reqs[int(id)-1]["type"], m["error"])
		} else {
			results[int(id)-1] = m["result"]
		}
		left--
	}
	return results, errs, nil
}
