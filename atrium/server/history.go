package main

// History for Home Assistant's own assistant: a second MCP server, read-only, at /mcp/history/<token> on the
// Home Assistant side. Home Assistant's MCP client integration offers every tool of a server to the model, so this
// one has only history tools, never the layout's. It answers only Home Assistant itself (its address on the
// Supervisor's network, asked every time) holding the token, and only about entities exposed to Assist: it cannot
// tell the assistant more than the owner already chose to show it. Answers are short plain text in the home's time
// zone; the model pays for every character.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// How far back history is looked for: Home Assistant's recorder keeps ten days unless told otherwise.
const historyDays = 10

type histEntity struct {
	ID, Name, Area, DeviceClass, Unit, State string
	names                                    []string // normalised: name, friendly name, aliases, area + name
	labelled                                 string   // the label with the entity id, where two found share a name
}

type histIndex struct {
	at   time.Time
	loc  *time.Location
	ents []*histEntity
}

type historyCache struct {
	mu    sync.Mutex
	index *histIndex
	token string
}

// historyToken is the secret in the endpoint's path, made once and kept in /data.
func (g *Gateway) historyToken() string {
	g.history.mu.Lock()
	defer g.history.mu.Unlock()
	if g.history.token != "" {
		return g.history.token
	}
	path := g.HistoryTokenPath
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		g.history.token = strings.TrimSpace(string(data))
		return g.history.token
	}
	b := make([]byte, 24)
	rand.Read(b)
	g.history.token = hex.EncodeToString(b)
	if path != "" {
		if err := os.WriteFile(path, []byte(g.history.token+"\n"), 0o600); err != nil {
			log.Printf("history: could not keep the token: %v", err)
		}
	}
	return g.history.token
}

// historyURL is what Home Assistant's MCP integration is given: the add-on's name on the Supervisor's network.
func (g *Gateway) historyURL() string {
	host := strings.ReplaceAll(firstNonEmpty(g.Slug, "local_atrium"), "_", "-")
	return "http://" + host + ":8099/mcp/history/" + g.historyToken()
}

// fromCore says whether the peer is Home Assistant itself: "homeassistant" must resolve to it on the Supervisor's
// network. Asked every time, as addresses are reused when containers restart.
func (g *Gateway) fromCore(ip string) bool {
	if g.isCore != nil {
		return g.isCore(ip)
	}
	ip = strings.TrimPrefix(ip, "::ffff:")
	addrs, err := net.LookupHost("homeassistant")
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if a == ip {
			return true
		}
	}
	return false
}

// serveHistoryMCP answers the history endpoint on the ingress listener, before any other check there.
func (g *Gateway) serveHistoryMCP(w http.ResponseWriter, r *http.Request, peer string) {
	token := strings.TrimPrefix(r.URL.Path, "/mcp/history/")
	if subtle.ConstantTimeCompare([]byte(token), []byte(g.historyToken())) != 1 {
		http.NotFound(w, r)
		return
	}
	if !g.fromCore(peer) {
		log.Printf("history: refused %s, which is not Home Assistant", peer)
		http.Error(w, "only Home Assistant may use this", http.StatusForbidden)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "MCP over Streamable HTTP: POST JSON-RPC here", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "not JSON-RPC: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID == nil || strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	switch req.Method {
	case "initialize":
		out["result"] = map[string]any{
			"protocolVersion": mcpProtocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "atrium-history", "version": version},
			"instructions":    "The home's history from Home Assistant's recorder, for questions about the past: when something last changed, what happened over a day, how long something was on, daily totals. Also media_select_input, to change a receiver's or TV's input, which Assist's own tools cannot. Only entities exposed to Assist. Name the entity as people do (\"front door\", \"kitchen lights\").",
		}
	case "ping":
		out["result"] = map[string]any{}
	case "tools/list":
		out["result"] = map[string]any{"tools": haMCPTools()}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		text, err := g.historyTool(ctx, p.Name, p.Arguments, time.Now())
		cancel()
		isErr := err != nil
		if isErr {
			text = err.Error()
		}
		out["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
	default:
		out["error"] = &rpcError{Code: -32601, Message: "no such method: " + req.Method}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

var historyEntityArg = map[string]any{"type": "string", "description": "the device or sensor as people name it (\"front door\", \"living room TV\"), or its entity id"}
var historyDayArg = map[string]any{"type": "string", "description": "\"today\" (default), \"yesterday\", a weekday, or YYYY-MM-DD"}

var historyTools = []map[string]any{
	{"name": "history_last_change", "description": "When something last changed, and what it was before. With state, when it was last in that state (\"open\", \"on\", \"unlocked\", \"home\") and for how long. Looks back ten days.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"entity"}, "properties": map[string]any{
			"entity": historyEntityArg,
			"state":  map[string]any{"type": "string", "description": "optional: the state asked about"},
		}}},
	{"name": "history_changes", "description": "Every change of something over a day, with times: what happened, in order. For doorbells and motion, each event.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"entity"}, "properties": map[string]any{
			"entity": historyEntityArg, "day": historyDayArg,
		}}},
	{"name": "history_time_in_state", "description": "How long something was in a state over a day, and how many times: how long the TV was on, the garage open, the heating running. State defaults to the active one (on, open, unlocked, playing, home, heating).",
		"inputSchema": map[string]any{"type": "object", "required": []string{"entity"}, "properties": map[string]any{
			"entity": historyEntityArg, "day": historyDayArg,
			"state": map[string]any{"type": "string", "description": "optional: the state to count"},
		}}},
	{"name": "history_daily", "description": "A sensor's daily figures over recent days from long-term statistics: totals for meters (energy in kWh, water, gas), otherwise the day's average, low and high (temperature, humidity, power). For how much energy was made or used, ask the energy meter, not the power sensor: power gives averages.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"entity"}, "properties": map[string]any{
			"entity": historyEntityArg,
			"days":   map[string]any{"type": "integer", "description": "how many days back, today included (default 7, at most 31)"},
		}}},
	{"name": "history_find", "description": "Which exposed entities match a name, with their area and state now. Use when unsure which entity is meant.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "words of the name or the area"},
		}}},
}

func (g *Gateway) historyTool(ctx context.Context, name string, args map[string]any, now time.Time) (string, error) {
	ix, err := g.historyIndex(ctx)
	if err != nil {
		return "", fmt.Errorf("Home Assistant did not answer: %v", err)
	}
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	if name == "media_select_input" {
		return g.selectInput(ctx, ix, str("player"), str("input"))
	}
	if name == "history_find" {
		found := ix.find(str("query"), 15)
		if len(found) == 0 {
			return "Nothing exposed to Assist matches \"" + str("query") + "\".", nil
		}
		var b strings.Builder
		for _, e := range found {
			fmt.Fprintf(&b, "%s: %s\n", e.label(), e.word(e.State))
		}
		return strings.TrimSpace(b.String()), nil
	}
	ents := ix.find(str("entity"), 4)
	if len(ents) == 0 {
		return "", fmt.Errorf("nothing exposed to Assist is called %q; try history_find", str("entity"))
	}
	ents = distinctLabels(ents)
	now = now.In(ix.loc)
	switch name {
	case "history_last_change", "history_changes", "history_time_in_state":
		from, to := now.Add(-historyDays*24*time.Hour), now
		if name != "history_last_change" {
			var ok bool
			if from, to, ok = dayBounds(str("day"), now); !ok {
				return "", fmt.Errorf("day %q is not today, yesterday, a weekday or YYYY-MM-DD", str("day"))
			}
		}
		rows, err := g.historyRows(ctx, ents, from, to)
		if err != nil {
			return "", err
		}
		var out []string
		for _, e := range ents {
			r := rows[e.ID]
			switch name {
			case "history_last_change":
				out = append(out, lastChangeText(e, r, str("state"), now))
			case "history_changes":
				out = append(out, changesText(e, r, from, to, now))
			default:
				out = append(out, timeInStateText(e, r, str("state"), from, to, now))
			}
		}
		return strings.Join(out, "\n"), nil
	case "history_daily":
		days := 7
		if d, ok := args["days"].(float64); ok && d >= 1 {
			days = min(int(d), 31)
		}
		return g.dailyText(ctx, ents, days, now)
	}
	return "", fmt.Errorf("no such tool: %s", name)
}

// historyIndex is what the assistant may ask about: the entities exposed to Assist, with their names, aliases and
// areas. Kept two minutes: a conversation asks several times in a row.
func (g *Gateway) historyIndex(ctx context.Context) (*histIndex, error) {
	g.history.mu.Lock()
	if ix := g.history.index; ix != nil && time.Since(ix.at) < 2*time.Minute {
		g.history.mu.Unlock()
		return ix, nil
	}
	g.history.mu.Unlock()
	res, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "homeassistant/expose_entity/list"}, {"type": "config/entity_registry/list"},
		{"type": "config/device_registry/list"}, {"type": "config/area_registry/list"}, {"type": "get_states"}, {"type": "get_config"}})
	if err != nil {
		return nil, err
	}
	for i, e := range errs {
		if e != nil && i < 5 {
			return nil, e
		}
	}
	var exposed []string
	if m, _ := res[0].(map[string]any); m != nil {
		list, _ := m["exposed_entities"].(map[string]any)
		for id, v := range list {
			if a, _ := v.(map[string]any); a != nil && a["conversation"] == true {
				exposed = append(exposed, id)
			}
		}
	}
	sort.Strings(exposed)
	aliases := map[string][]string{}
	if len(exposed) > 0 {
		if more, merrs, err := g.wsBatch(ctx, []map[string]any{{"type": "config/entity_registry/get_entries", "entity_ids": exposed}}); err == nil && merrs[0] == nil {
			if m, _ := more[0].(map[string]any); m != nil {
				for id, v := range m {
					if e, _ := v.(map[string]any); e != nil {
						aliases[id] = strs(e["aliases"])
					}
				}
			}
		}
	}
	zone := ""
	if cfg, _ := res[5].(map[string]any); cfg != nil {
		zone, _ = cfg["time_zone"].(string)
	}
	ix := buildHistIndex(exposed, objects(res[1]), objects(res[2]), objects(res[3]), objects(res[4]), aliases, zone)
	g.history.mu.Lock()
	g.history.index = ix
	g.history.mu.Unlock()
	return ix, nil
}

func buildHistIndex(exposed []string, entities, devices, areas, states []map[string]any, aliases map[string][]string, zone string) *histIndex {
	ix := &histIndex{at: time.Now(), loc: time.Local}
	if l, err := time.LoadLocation(zone); zone != "" && err == nil {
		ix.loc = l
	}
	areaName := map[string]string{}
	for _, a := range areas {
		id, _ := a["area_id"].(string)
		areaName[id], _ = a["name"].(string)
	}
	deviceArea := map[string]string{}
	for _, d := range devices {
		id, _ := d["id"].(string)
		deviceArea[id], _ = d["area_id"].(string)
	}
	reg := map[string]map[string]any{}
	for _, e := range entities {
		id, _ := e["entity_id"].(string)
		reg[id] = e
	}
	state := map[string]map[string]any{}
	for _, s := range states {
		id, _ := s["entity_id"].(string)
		state[id] = s
	}
	for _, id := range exposed {
		e := &histEntity{ID: id}
		var attrs map[string]any
		if s := state[id]; s != nil {
			e.State, _ = s["state"].(string)
			attrs, _ = s["attributes"].(map[string]any)
		}
		friendly, _ := attrs["friendly_name"].(string)
		e.DeviceClass, _ = attrs["device_class"].(string)
		e.Unit, _ = attrs["unit_of_measurement"].(string)
		area := ""
		if r := reg[id]; r != nil {
			area, _ = r["area_id"].(string)
			if area == "" {
				dev, _ := r["device_id"].(string)
				area = deviceArea[dev]
			}
			if n, _ := r["name"].(string); n != "" {
				e.Name = n
			}
		}
		e.Area = areaName[area]
		e.Name = firstNonEmpty(friendly, e.Name, strings.ReplaceAll(id[strings.Index(id, ".")+1:], "_", " "))
		for _, n := range append([]string{e.Name, id, strings.ReplaceAll(id[strings.Index(id, ".")+1:], "_", " ")}, aliases[id]...) {
			e.names = append(e.names, norm(n))
			if e.Area != "" && !strings.Contains(norm(n), norm(e.Area)) {
				e.names = append(e.names, norm(e.Area+" "+n))
			}
		}
		ix.ents = append(ix.ents, e)
	}
	return ix
}

func norm(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' {
			return r
		}
		return ' '
	}, s)
	words := strings.Fields(s)
	if len(words) > 1 && words[0] == "the" {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// find: an exact name, alias or entity id first; otherwise every entity whose name and area hold all the words.
func (ix *histIndex) find(q string, limit int) []*histEntity {
	q = norm(q)
	if q == "" {
		return nil
	}
	var exact, loose []*histEntity
	words := strings.Fields(q)
	for _, e := range ix.ents {
		hit := false
		for _, n := range e.names {
			if n == q {
				hit = true
				break
			}
		}
		if hit {
			exact = append(exact, e)
			continue
		}
		hay := " " + strings.Join(e.names, " ") + " "
		all := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				all = false
				break
			}
		}
		if all {
			loose = append(loose, e)
		}
	}
	found := exact
	if len(found) == 0 {
		found = loose
	}
	if len(found) > limit {
		found = found[:limit]
	}
	return found
}

func (e *histEntity) label() string {
	if e.labelled != "" {
		return e.labelled
	}
	if e.Area != "" && !strings.Contains(strings.ToLower(e.Name), strings.ToLower(e.Area)) {
		return e.Name + " (" + e.Area + ")"
	}
	return e.Name
}

// distinctLabels: where two entities found go by the same name (a garage door's cover and its sensor), each
// answer line says which is which. Copies, so the cached index keeps its plain labels.
func distinctLabels(ents []*histEntity) []*histEntity {
	count := map[string]int{}
	for _, e := range ents {
		count[e.label()]++
	}
	out := make([]*histEntity, len(ents))
	for i, e := range ents {
		c := *e
		if count[e.label()] > 1 {
			c.labelled = e.label() + " [" + e.ID + "]"
		}
		out[i] = &c
	}
	return out
}

func (e *histEntity) domain() string { return e.ID[:strings.Index(e.ID, ".")] }

// word: the state as people say it. A door's binary sensor records "on"; it means open.
func (e *histEntity) word(s string) string {
	if e.domain() == "binary_sensor" && (s == "on" || s == "off") {
		pairs := map[string][2]string{
			"door": {"open", "closed"}, "window": {"open", "closed"}, "garage_door": {"open", "closed"}, "opening": {"open", "closed"},
			"motion": {"motion", "clear"}, "occupancy": {"occupied", "clear"}, "presence": {"present", "away"}, "moisture": {"wet", "dry"},
			"smoke": {"smoke", "clear"}, "lock": {"unlocked", "locked"}, "connectivity": {"connected", "disconnected"}, "plug": {"plugged in", "unplugged"},
		}
		if p, ok := pairs[e.DeviceClass]; ok {
			if s == "on" {
				return p[0]
			}
			return p[1]
		}
	}
	if e.domain() == "event" {
		return "event"
	}
	s = strings.ReplaceAll(s, "_", " ")
	if e.Unit != "" && s != "unavailable" && s != "unknown" {
		return s + " " + e.Unit
	}
	return s
}

// active: the state worth counting when none is named.
func (e *histEntity) active(s string) bool {
	switch s {
	case "on", "open", "opening", "unlocked", "playing", "home", "heat", "cool", "heating", "cooling", "heat_cool", "triggered", "armed_away", "armed_home", "cleaning":
		return true
	}
	return false
}

// matches: the state asked about, in the recorder's words or people's.
func (e *histEntity) matches(asked, s string) bool {
	if asked == "" {
		return e.active(s)
	}
	asked = strings.ToLower(asked)
	return strings.EqualFold(s, asked) || strings.EqualFold(e.word(s), asked) || (asked == "opened" && e.word(s) == "open") || (asked == "closed" && e.word(s) == "closed")
}

func (g *Gateway) historyRows(ctx context.Context, ents []*histEntity, from, to time.Time) (map[string][]histRow, error) {
	ids := make([]string, len(ents))
	for i, e := range ents {
		ids[i] = e.ID
	}
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	res, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "history/history_during_period", "start_time": stamp(from), "end_time": stamp(to),
		"entity_ids": ids, "minimal_response": true, "no_attributes": true, "significant_changes_only": false}})
	if err != nil {
		return nil, err
	}
	if errs[0] != nil {
		return nil, errs[0]
	}
	data, _ := json.Marshal(res[0])
	out := map[string][]histRow{}
	json.Unmarshal(data, &out)
	return out, nil
}

// changesOf drops repeats: the recorder keeps a row when only an attribute changed.
func changesOf(rows []histRow) []histRow {
	var out []histRow
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].S != r.S {
			out = append(out, r)
		}
	}
	return out
}

func lastChangeText(e *histEntity, rows []histRow, asked string, now time.Time) string {
	rows = changesOf(rows)
	if len(rows) == 0 {
		return e.label() + ": no history in the last ten days."
	}
	if asked == "" {
		cur := rows[len(rows)-1]
		if len(rows) == 1 {
			return fmt.Sprintf("%s: %s, unchanged for at least ten days.", e.label(), e.word(cur.S))
		}
		prev := rows[len(rows)-2]
		return fmt.Sprintf("%s: %s since %s (%s); before that %s for %s.", e.label(), e.word(cur.S), when(cur.at(), now), span(now.Sub(cur.at())),
			e.word(prev.S), span(cur.at().Sub(prev.at())))
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if !e.matches(asked, rows[i].S) {
			continue
		}
		if i == len(rows)-1 {
			if i == 0 {
				return fmt.Sprintf("%s: %s now, for at least ten days.", e.label(), e.word(rows[i].S))
			}
			return fmt.Sprintf("%s: %s now, since %s (%s).", e.label(), e.word(rows[i].S), when(rows[i].at(), now), span(now.Sub(rows[i].at())))
		}
		end := rows[i+1].at()
		from := "at least ten days ago"
		if i > 0 {
			from = when(rows[i].at(), now)
		}
		return fmt.Sprintf("%s: last %s %s until %s (%s); %s now.", e.label(), e.word(rows[i].S), from, when(end, now), span(end.Sub(rows[i].at())), e.word(rows[len(rows)-1].S))
	}
	what := asked
	if what == "" {
		what = "on"
	}
	return fmt.Sprintf("%s: not %s in the last ten days; %s now.", e.label(), what, e.word(rows[len(rows)-1].S))
}

func changesText(e *histEntity, rows []histRow, from, to, now time.Time) string {
	rows = changesOf(rows)
	var lines []string
	for i, r := range rows {
		if i == 0 && !r.at().After(from) {
			lines = append(lines, "from the start: "+e.word(r.S))
			continue
		}
		at := r.at().In(now.Location()).Format("15:04")
		if e.domain() == "event" {
			lines = append(lines, at)
		} else {
			lines = append(lines, at+" "+e.word(r.S))
		}
	}
	day := dayName(from, now)
	if len(lines) == 0 {
		return e.label() + ", " + day + ": no history."
	}
	if len(lines) == 1 && strings.HasPrefix(lines[0], "from the start") {
		return fmt.Sprintf("%s, %s: no changes; %s all along.", e.label(), day, e.word(rows[0].S))
	}
	more := ""
	if len(lines) > 40 {
		more = fmt.Sprintf(" (%d earlier left out)", len(lines)-40)
		lines = lines[len(lines)-40:]
	}
	return fmt.Sprintf("%s, %s%s: %s.", e.label(), day, more, strings.Join(lines, ", "))
}

func timeInStateText(e *histEntity, rows []histRow, asked string, from, to, now time.Time) string {
	rows = changesOf(rows)
	var total time.Duration
	times := 0
	for i, r := range rows {
		if !e.matches(asked, r.S) {
			continue
		}
		start := r.at()
		if start.Before(from) {
			start = from
		} else {
			times++
		}
		end := to
		if i+1 < len(rows) {
			end = rows[i+1].at()
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	what := asked
	if what == "" {
		what = "on"
		for _, r := range rows {
			if e.active(r.S) {
				what = e.word(r.S)
				break
			}
		}
	}
	day := dayName(from, now)
	if total == 0 {
		return fmt.Sprintf("%s, %s: never %s.", e.label(), day, what)
	}
	count := ""
	switch times {
	case 0:
	case 1:
		count = ", once"
	default:
		count = fmt.Sprintf(", %d times", times)
	}
	return fmt.Sprintf("%s, %s: %s for %s%s.", e.label(), day, what, span(total), count)
}

func (g *Gateway) dailyText(ctx context.Context, ents []*histEntity, days int, now time.Time) (string, error) {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from := today.AddDate(0, 0, -(days - 1))
	ids := make([]string, len(ents))
	for i, e := range ents {
		ids[i] = e.ID
	}
	res, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "recorder/statistics_during_period", "start_time": from.UTC().Format(time.RFC3339),
		"end_time": now.UTC().Format(time.RFC3339), "statistic_ids": ids, "period": "day", "types": []string{"change", "mean", "min", "max"},
		"units": map[string]any{"energy": "kWh", "power": "kW"}}}) // in kWh whatever an entity displays (MWh rounds a day to 0.04)
	if err != nil {
		return "", err
	}
	if errs[0] != nil {
		return "", errs[0]
	}
	data, _ := json.Marshal(res[0])
	var stats map[string][]struct {
		Start  float64  `json:"start"`
		Change *float64 `json:"change"`
		Mean   *float64 `json:"mean"`
		Min    *float64 `json:"min"`
		Max    *float64 `json:"max"`
	}
	json.Unmarshal(data, &stats)
	var out []string
	for _, e := range ents {
		rows := stats[e.ID]
		unit := e.Unit
		switch strings.ToLower(unit) {
		case "wh", "kwh", "mwh", "gwh":
			unit = "kWh"
		case "w", "kw", "mw":
			unit = "kW"
		}
		if len(rows) == 0 {
			out = append(out, e.label()+": no long-term statistics (only sensors with a state class keep them).")
			continue
		}
		var parts []string
		var sum float64
		meter := false
		for _, r := range rows {
			day := time.UnixMilli(int64(r.Start)).In(loc).Format("Mon 2 Jan")
			switch {
			case r.Change != nil:
				meter = true
				sum += *r.Change
				parts = append(parts, fmt.Sprintf("%s %s", day, amountUnit(*r.Change, unit)))
			case r.Mean != nil:
				p := fmt.Sprintf("%s avg %s", day, amountUnit(*r.Mean, unit))
				if r.Min != nil && r.Max != nil {
					p += fmt.Sprintf(" (%s to %s)", trimFloat(*r.Min), trimFloat(*r.Max))
				}
				parts = append(parts, p)
			}
		}
		line := e.label() + ": " + strings.Join(parts, "; ")
		if meter && len(rows) > 1 {
			line += "; total " + amountUnit(sum, unit)
		}
		out = append(out, line+".")
	}
	return strings.Join(out, "\n"), nil
}

func trimFloat(v float64) string {
	switch {
	case v >= 100 || v <= -100:
		return fmt.Sprintf("%.0f", v)
	case v >= 10 || v <= -10:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func amountUnit(v float64, unit string) string {
	if unit == "" {
		return trimFloat(v)
	}
	return trimFloat(v) + " " + unit
}

// dayBounds: midnight to midnight in the home's zone, today up to now.
func dayBounds(day string, now time.Time) (time.Time, time.Time, bool) {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	d := strings.ToLower(strings.TrimSpace(day))
	var start time.Time
	switch d {
	case "", "today":
		return today, now, true
	case "yesterday":
		start = today.AddDate(0, 0, -1)
	default:
		if t, err := time.ParseInLocation("2006-01-02", d, loc); err == nil {
			start = t
			break
		}
		found := false
		for back := 1; back <= 7; back++ {
			c := today.AddDate(0, 0, -back)
			if strings.HasPrefix(strings.ToLower(c.Weekday().String()), d) && len(d) >= 3 {
				start, found = c, true
				break
			}
		}
		if !found {
			return time.Time{}, time.Time{}, false
		}
	}
	end := start.AddDate(0, 0, 1)
	if end.After(now) {
		end = now
	}
	return start, end, true
}

func dayName(from, now time.Time) string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !from.Before(today):
		return "today"
	case !from.Before(today.AddDate(0, 0, -1)):
		return "yesterday"
	}
	return from.In(now.Location()).Format("Mon 2 Jan")
}

// when: "21:03 today", "21:03 yesterday", "Thu 21:03", "3 Nov 21:03".
func when(t, now time.Time) string {
	t = t.In(now.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(today):
		return t.Format("15:04") + " today"
	case !t.Before(today.AddDate(0, 0, -1)):
		return t.Format("15:04") + " yesterday"
	case !t.Before(today.AddDate(0, 0, -6)):
		return t.Format("Mon 15:04")
	}
	return t.Format("2 Jan 15:04")
}

// span: "under a second", "40 s", "5 min", "3 h 12 min", "2 d 4 h".
func span(d time.Duration) string {
	switch {
	case d < time.Second:
		return "under a second"
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	}
	days, h := int(d.Hours())/24, int(d.Hours())%24
	if h == 0 {
		return fmt.Sprintf("%d d", days)
	}
	return fmt.Sprintf("%d d %d h", days, h)
}
