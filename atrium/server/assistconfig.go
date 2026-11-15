package main

// AI Assist's settings, for the owner and administrators in Atrium's web app: which agent the apps' pipeline uses,
// its model, options and prompt (through Home Assistant's own settings form for that agent, so any agent's
// fields appear, not only Anthropic's), its tool sets, and connecting Atrium's history tools. Everything goes
// through Home Assistant's config flows as its own settings page would; the add-on only ever starts the flow for
// the pipeline's agent (or the MCP integration, for history) and answers steps of flows it started. Each change is
// logged with who made it, and each prompt replaced is kept to restore.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type assistAgent struct {
	Pipeline     string `json:"pipeline"`     // its id
	PipelineName string `json:"pipelineName"` // as Home Assistant shows it
	Preferred    bool   `json:"preferred"`    // Home Assistant's preferred pipeline (the apps use it unless the layout names an agent)
	Entity       string `json:"entity"`       // conversation.*
	Name         string `json:"name"`
	Domain       string `json:"domain"` // the integration: anthropic, openai_conversation, ollama…
	Entry        string `json:"entry"`
	Subentry     string `json:"subentry,omitempty"`
	EntryTitle   string `json:"entryTitle"`
	EntryState   string `json:"entryState"`
}

type assistConfigState struct {
	mu      sync.Mutex
	flows   map[string]assistFlow // flows this add-on started, by id
	prompts string                // where replaced prompts are kept (/data/assist-prompts.jsonl)
}

type assistFlow struct {
	kind   string // "agent" or "history"
	agent  assistAgent
	prompt string // the prompt when the flow started, to keep if it is replaced
	login  string
	at     time.Time
	set    map[string]any // what the steps so far set, for the log line at the end
}

// assistAgentNow finds the agent the apps' pipeline uses: the preferred pipeline's conversation engine, its
// registry entry, and the integration entry (and sub-entry) that holds its settings.
func (g *Gateway) assistAgentNow(ctx context.Context) (assistAgent, error) {
	var a assistAgent
	res, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "assist_pipeline/pipeline/list"}, {"type": "config_entries/get"}})
	if err != nil {
		return a, err
	}
	if errs[0] != nil {
		return a, fmt.Errorf("pipelines: %v", errs[0])
	}
	list, _ := res[0].(map[string]any)
	preferred, _ := list["preferred_pipeline"].(string)
	for _, p := range objects(list["pipelines"]) {
		if id, _ := p["id"].(string); id == preferred {
			a.Pipeline, a.Preferred = id, true
			a.PipelineName, _ = p["name"].(string)
			a.Entity, _ = p["conversation_engine"].(string)
		}
	}
	if a.Entity == "" {
		return a, errors.New("Home Assistant's preferred voice pipeline has no conversation agent")
	}
	reg, rerrs, err := g.wsBatch(ctx, []map[string]any{{"type": "config/entity_registry/get", "entity_id": a.Entity}})
	if err != nil {
		return a, err
	}
	if rerrs[0] != nil {
		return a, fmt.Errorf("%s is not in the entity registry (Home Assistant's built-in agent has no settings to change here)", a.Entity)
	}
	e, _ := reg[0].(map[string]any)
	a.Entry, _ = e["config_entry_id"].(string)
	a.Subentry, _ = e["config_subentry_id"].(string)
	a.Name = firstNonEmpty(str(e["name"]), str(e["original_name"]), a.Entity)
	for _, entry := range objects(res[1]) {
		if id, _ := entry["entry_id"].(string); id == a.Entry {
			a.Domain, _ = entry["domain"].(string)
			a.EntryTitle, _ = entry["title"].(string)
			a.EntryState, _ = entry["state"].(string)
		}
	}
	if a.Entry == "" {
		return a, fmt.Errorf("%s belongs to no integration entry", a.Entity)
	}
	return a, nil
}

func str(v any) string { s, _ := v.(string); return s }

func sortedStrings(list []string) []string {
	out := append([]string(nil), list...)
	sort.Strings(out)
	return out
}

// lastHouse is the last house built, if any.
func (g *Gateway) lastHouse() ([]byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.house.data, g.house.data != nil
}

// historyConnected: an MCP integration entry for Atrium's history server (it names itself atrium-history).
func (g *Gateway) historyConnected(ctx context.Context) (string, error) {
	res, err := g.wsRequest(ctx, "config_entries/get")
	if err != nil {
		return "", err
	}
	for _, entry := range objects(res) {
		if entry["domain"] == "mcp" && strings.Contains(strings.ToLower(str(entry["title"])), "atrium") {
			return str(entry["entry_id"]), nil
		}
	}
	return "", nil
}

// serveAssistConfig answers /atrium/assist/config…: for the owner and administrators, never an add-on.
func (g *Gateway) serveAssistConfig(w http.ResponseWriter, r *http.Request, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	if id.Addon || !g.mayConfigure(id) {
		http.Error(w, "only the owner and administrators may change AI Assist", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	reply := func(v any, err error) {
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	rest := strings.TrimPrefix(r.URL.Path, "/atrium/assist/config")
	switch {
	case rest == "" && r.Method == "GET":
		a, err := g.assistAgentNow(ctx)
		if err != nil {
			reply(nil, err)
			return
		}
		history, _ := g.historyConnected(ctx)
		reply(map[string]any{"agent": a, "history": map[string]any{"connected": history != "", "entry": history, "url": g.historyURL()},
			"appsAsk": g.layoutAsk()}, nil)
	case rest == "/flow" && r.Method == "POST": // start the agent's settings form
		reply(g.startAssistFlow(ctx, id, "agent"))
	case rest == "/history" && r.Method == "POST": // connect Atrium's history tools
		reply(g.startAssistFlow(ctx, id, "history"))
	case strings.HasPrefix(rest, "/flow/") && (r.Method == "POST" || r.Method == "DELETE"):
		flowID := strings.TrimPrefix(rest, "/flow/")
		var data map[string]any
		if r.Method == "POST" {
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&data); err != nil {
				http.Error(w, "not JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		reply(g.stepAssistFlow(ctx, id, flowID, r.Method, data))
	case rest == "/ready" && r.Method == "POST":
		reply(g.assistReady(ctx))
	case rest == "/prompts" && r.Method == "GET":
		reply(g.keptPrompts(20))
	default:
		http.NotFound(w, r)
	}
}

// layoutAsk is the agent the layout names for the apps (home.ask), "" for the preferred pipeline.
func (g *Gateway) layoutAsk() string {
	b, ok := g.lastHouse()
	if !ok {
		return ""
	}
	var h struct {
		Summary struct {
			Ask string `json:"ask"`
		} `json:"summary"`
	}
	json.Unmarshal(b, &h)
	return h.Summary.Ask
}

func (g *Gateway) startAssistFlow(ctx context.Context, id Identity, kind string) (map[string]any, error) {
	var out map[string]any
	var flow assistFlow
	switch kind {
	case "agent":
		a, err := g.assistAgentNow(ctx)
		if err != nil {
			return nil, err
		}
		if a.Subentry == "" {
			return nil, fmt.Errorf("%s keeps its settings on the integration itself; change them in Home Assistant", a.Name)
		}
		body := map[string]any{"handler": []string{a.Entry, "conversation"}, "subentry_id": a.Subentry}
		if err := g.haREST(ctx, "POST", "/api/config/config_entries/subentries/flow", body, &out); err != nil {
			return nil, err
		}
		flow = assistFlow{kind: kind, agent: a, prompt: suggested(out, "prompt"), login: id.Login, at: time.Now()}
	case "history":
		if entry, _ := g.historyConnected(ctx); entry != "" {
			return nil, errors.New("Domiel's history tools are connected already")
		}
		if err := g.haREST(ctx, "POST", "/api/config/config_entries/flow", map[string]any{"handler": "mcp"}, &out); err != nil {
			return nil, err
		}
		flow = assistFlow{kind: kind, login: id.Login, at: time.Now()}
		if fid := str(out["flow_id"]); fid != "" && str(out["type"]) == "form" { // the one field: the server's URL
			g.rememberFlow(fid, flow)
			return g.stepAssistFlow(ctx, id, fid, "POST", map[string]any{"url": g.historyURL()})
		}
	}
	if fid := str(out["flow_id"]); fid != "" {
		g.rememberFlow(fid, flow)
	}
	return out, nil
}

// rememberFlow keeps a flow this add-on started. Forms left open (a page closed half-way) are given up in Home
// Assistant too, where they would otherwise linger until it restarts: after fifteen minutes, or at once when a
// new form for the same thing starts.
func (g *Gateway) rememberFlow(id string, f assistFlow) {
	g.assistCfg.mu.Lock()
	if g.assistCfg.flows == nil {
		g.assistCfg.flows = map[string]assistFlow{}
	}
	var stale []string
	for k, old := range g.assistCfg.flows {
		if time.Since(old.at) > 15*time.Minute || old.kind == f.kind {
			stale = append(stale, k)
			delete(g.assistCfg.flows, k)
		}
	}
	g.assistCfg.flows[id] = f
	g.assistCfg.mu.Unlock()
	for _, k := range stale {
		go func(k string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Either kind of flow: one of the two paths is the right one, the other answers 404.
			g.haREST(ctx, "DELETE", "/api/config/config_entries/subentries/flow/"+k, nil, nil)
			g.haREST(ctx, "DELETE", "/api/config/config_entries/flow/"+k, nil, nil)
		}(k)
	}
}

// stepAssistFlow answers a step of a flow this add-on started (POST: the step's data; DELETE: give up).
func (g *Gateway) stepAssistFlow(ctx context.Context, id Identity, flowID, method string, data map[string]any) (map[string]any, error) {
	g.assistCfg.mu.Lock()
	flow, ok := g.assistCfg.flows[flowID]
	g.assistCfg.mu.Unlock()
	if !ok || strings.ContainsAny(flowID, "/?#") {
		return nil, errors.New("not a settings form Domiel started")
	}
	path := "/api/config/config_entries/subentries/flow/" + flowID
	if flow.kind == "history" {
		path = "/api/config/config_entries/flow/" + flowID
	}
	if method == "DELETE" {
		g.forgetFlow(flowID)
		return map[string]any{"type": "abort", "reason": "cancelled"}, g.haREST(ctx, "DELETE", path, nil, nil)
	}
	if p, ok := data["prompt"].(string); ok && flow.kind == "agent" && p != flow.prompt && flow.prompt != "" {
		g.keepPrompt(id.Login, flow.agent.Name, flow.prompt)
	}
	var out map[string]any
	if err := g.haREST(ctx, "POST", path, data, &out); err != nil {
		return nil, err
	}
	if flow.set == nil {
		flow.set = map[string]any{}
	}
	for k, v := range data {
		flow.set[k] = v
	}
	switch str(out["type"]) {
	case "form":
		// The next step, or the same one with errors: still ours.
		g.assistCfg.mu.Lock()
		g.assistCfg.flows[flowID] = flow
		g.assistCfg.mu.Unlock()
	default:
		g.forgetFlow(flowID)
		what := describeAssistChange(flow, flow.set)
		log.Printf("AI Assist: %s changed %s (%s: %s)", id.Login, what, str(out["type"]), firstNonEmpty(str(out["reason"]), str(out["title"])))
	}
	return out, nil
}

func (g *Gateway) forgetFlow(id string) {
	g.assistCfg.mu.Lock()
	delete(g.assistCfg.flows, id)
	g.assistCfg.mu.Unlock()
}

// describeAssistChange names what a step set, for the log: the keys, and the model if one was chosen.
func describeAssistChange(f assistFlow, data map[string]any) string {
	if f.kind == "history" {
		return "the history tools' connection"
	}
	var keys []string
	for k := range data {
		keys = append(keys, k)
	}
	what := f.agent.Name + " " + strings.Join(sortedStrings(keys), ", ")
	if m := str(data["chat_model"]); m != "" {
		what += " (model " + m + ")"
	}
	return what
}

// suggested is a form field's current value, as Home Assistant fills it in for a reconfigure.
func suggested(form map[string]any, field string) string {
	list, _ := form["data_schema"].([]any)
	for _, f := range list {
		m, _ := f.(map[string]any)
		if m["name"] != field {
			continue
		}
		if d, ok := m["description"].(map[string]any); ok {
			if v, ok := d["suggested_value"].(string); ok {
				return v
			}
		}
		if v, ok := m["default"].(string); ok {
			return v
		}
	}
	return ""
}

// assistReady waits for the agent's integration to load again after a change, then asks it one short question:
// the first question after a reload has been seen to hang for minutes while its tools reconnect.
func (g *Gateway) assistReady(ctx context.Context) (map[string]any, error) {
	start := time.Now()
	a, err := g.assistAgentNow(ctx)
	for err == nil && a.EntryState != "loaded" && time.Since(start) < 40*time.Second {
		time.Sleep(time.Second)
		a, err = g.assistAgentNow(ctx)
	}
	if err != nil {
		return nil, err
	}
	if a.EntryState != "loaded" {
		return map[string]any{"ready": false, "state": a.EntryState}, nil
	}
	warm := time.Now()
	wctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, errs, err := g.wsBatch(wctx, []map[string]any{{"type": "conversation/process", "text": "What time is it?", "agent_id": a.Entity}})
	if err == nil && errs[0] != nil {
		err = errs[0]
	}
	return map[string]any{"ready": err == nil, "state": a.EntryState, "reloadMs": warm.Sub(start).Milliseconds(),
		"warmMs": time.Since(warm).Milliseconds(), "error": errString(err)}, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type keptPrompt struct {
	At     time.Time `json:"at"`
	Login  string    `json:"login"`
	Agent  string    `json:"agent"`
	Prompt string    `json:"prompt"`
}

func (g *Gateway) keepPrompt(login, agent, prompt string) {
	if g.assistCfg.prompts == "" {
		return
	}
	data, _ := json.Marshal(keptPrompt{At: time.Now(), Login: login, Agent: agent, Prompt: prompt})
	f, err := os.OpenFile(g.assistCfg.prompts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("AI Assist: could not keep the previous prompt: %v", err)
		return
	}
	f.Write(append(data, '\n'))
	f.Close()
	// The last fifty are enough to restore from; the file holds the house's prompts, so it does not grow forever.
	if kept, err := g.keptPrompts(1000); err == nil && len(kept) > 50 {
		var b strings.Builder
		for i := 49; i >= 0; i-- {
			line, _ := json.Marshal(kept[i])
			b.Write(append(line, '\n'))
		}
		tmp := g.assistCfg.prompts + ".new"
		if os.WriteFile(tmp, []byte(b.String()), 0o600) == nil {
			os.Rename(tmp, g.assistCfg.prompts)
		}
	}
}

// keptPrompts are the prompts replaced, newest first.
func (g *Gateway) keptPrompts(n int) ([]keptPrompt, error) {
	data, err := os.ReadFile(g.assistCfg.prompts)
	if errors.Is(err, os.ErrNotExist) || g.assistCfg.prompts == "" {
		return []keptPrompt{}, nil
	} else if err != nil {
		return nil, err
	}
	var all []keptPrompt
	for _, line := range strings.Split(string(data), "\n") {
		var p keptPrompt
		if json.Unmarshal([]byte(line), &p) == nil && p.Prompt != "" {
			all = append(all, p)
		}
	}
	out := []keptPrompt{}
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out, nil
}
