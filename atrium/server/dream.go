package main

// Dreaming: once a night the add-on consolidates AI Assist's memory. The memory is a to-do list in Home Assistant
// ("AI Memory", one short fact per item) that the assistant reads and adds to during the day. At 03:30 home time,
// if anything was asked since the last night, the add-on hands the list and the day's questions and answers (from
// AI Assist's log, assistlog.go) to Home Assistant's AI task (ai_task.generate_data, which has no tools to change
// the home) and asks for the memory as it should be: duplicates merged, what changed updated, what has passed
// dropped, durable new facts added. The list is then brought to that, item by item, through the to-do services.
// Each night's before and after is kept in /data/memory/, and the result is a line in the add-on's log.
//
// It never wipes the memory: an answer that would drop most of a list of any size is not applied.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type memItem struct {
	UID     string `json:"uid"`
	Summary string `json:"summary"`
}

type dreamResult struct {
	At        time.Time `json:"at"`
	List      string    `json:"list,omitempty"`
	Task      string    `json:"task,omitempty"`
	Questions int       `json:"questions"`
	Before    []string  `json:"before,omitempty"`
	After     []string  `json:"after,omitempty"`
	Added     int       `json:"added"`
	Removed   int       `json:"removed"`
	Applied   bool      `json:"applied"`
	Note      string    `json:"note,omitempty"` // why nothing was done, or what went wrong
}

type dreamer struct {
	mu   sync.Mutex
	dir  string // /data/memory: one file per night, and last.json
	last *dreamResult
}

const dreamAt = 3*time.Hour + 30*time.Minute // home time

// watchDreams runs the consolidation once a night, at dreamAt in Home Assistant's time zone.
func (g *Gateway) watchDreams() {
	g.dreams.load()
	for {
		loc := g.homeZone(context.Background())
		now := time.Now().In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Add(dreamAt)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		time.Sleep(time.Until(next))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		r := g.dream(ctx, false)
		cancel()
		log.Print(r.line())
	}
}

func (g *Gateway) homeZone(ctx context.Context) *time.Location {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if cfg, err := g.wsRequest(ctx, "get_config"); err == nil {
		if m, _ := cfg.(map[string]any); m != nil {
			if zone, _ := m["time_zone"].(string); zone != "" {
				if l, err := time.LoadLocation(zone); err == nil {
					return l
				}
			}
		}
	}
	return time.Local
}

// serveDream: GET the last night's result; POST runs it now (?dry=1: say what it would do, change nothing). The owner
// and administrators only.
func (g *Gateway) serveDream(w http.ResponseWriter, r *http.Request, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	if !g.mayConfigure(id) {
		http.Error(w, "only the owner and administrators may see AI Assist's memory", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case "GET":
		g.dreams.mu.Lock()
		last := g.dreams.last
		g.dreams.mu.Unlock()
		json.NewEncoder(w).Encode(last)
	case "POST":
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		res := g.dream(ctx, r.URL.Query().Get("dry") == "1")
		log.Print(res.line())
		json.NewEncoder(w).Encode(res)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

// dream consolidates the memory once. dry: work it out, change nothing, keep nothing.
func (g *Gateway) dream(ctx context.Context, dry bool) dreamResult {
	res := dreamResult{At: time.Now()}
	g.dreams.mu.Lock()
	since := time.Time{}
	if g.dreams.last != nil && g.dreams.last.Applied {
		since = g.dreams.last.At
	}
	g.dreams.mu.Unlock()
	if since.IsZero() || time.Since(since) > 7*24*time.Hour {
		since = time.Now().Add(-24 * time.Hour)
	}

	list, task, err := g.memoryEntities(ctx)
	res.List, res.Task = list, task
	if err != nil {
		res.Note = err.Error()
		return res
	}
	turns, err := g.assist.newest(0)
	if err != nil {
		res.Note = "could not read AI Assist's log: " + err.Error()
		return res
	}
	var day []assistTurn
	for i := len(turns) - 1; i >= 0; i-- { // oldest first
		if turns[i].Time.After(since) {
			day = append(day, turns[i])
		}
	}
	if len(day) > 300 {
		day = day[len(day)-300:]
	}
	res.Questions = len(day)
	if len(day) == 0 {
		res.Note = "nothing asked since the last night"
		return res
	}
	items, err := g.memoryItems(ctx, list)
	if err != nil {
		res.Note = "could not read the memory: " + err.Error()
		return res
	}
	for _, it := range items {
		res.Before = append(res.Before, it.Summary)
	}
	loc := g.homeZone(ctx)
	facts, err := g.consolidate(ctx, task, dreamInstructions(res.Before, day, time.Now().In(loc)))
	if err != nil {
		res.Note = "the AI task did not answer: " + err.Error()
		return res
	}
	add, remove, ok, why := planDream(items, facts)
	res.After, res.Added, res.Removed = facts, len(add), len(remove)
	if !ok {
		res.Note = why
		return res
	}
	if dry {
		res.Note = "dry run: nothing changed"
		return res
	}
	if err := g.applyMemory(ctx, list, add, remove); err != nil {
		res.Note = "changing the list failed part way: " + err.Error()
	} else {
		res.Applied = true
	}
	g.dreams.keep(res)
	return res
}

// planDream works out the changes that bring the list to the facts: items not among them go, facts not yet on the
// list are added. Not ok when the answer would empty a list or drop most of a list of any size.
func planDream(items []memItem, facts []string) (add []string, remove []memItem, ok bool, why string) {
	want := map[string]bool{}
	var clean []string
	for _, f := range facts {
		f = strings.Join(strings.Fields(f), " ")
		if f == "" || want[strings.ToLower(f)] {
			continue
		}
		want[strings.ToLower(f)] = true
		clean = append(clean, f)
	}
	have := map[string]bool{}
	for _, it := range items {
		k := strings.ToLower(strings.Join(strings.Fields(it.Summary), " "))
		if !want[k] {
			remove = append(remove, it)
		}
		have[k] = true
	}
	for _, f := range clean {
		if !have[strings.ToLower(f)] {
			add = append(add, f)
		}
	}
	if len(items) > 0 && len(clean) == 0 {
		return nil, nil, false, "the answer would empty the memory; left as it is"
	}
	if len(items) >= 6 && len(clean) < len(items)/2 {
		return nil, nil, false, fmt.Sprintf("the answer would cut %d memories to %d; left as it is", len(items), len(clean))
	}
	return add, remove, true, ""
}

func dreamInstructions(memory []string, day []assistTurn, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You keep the long-term memory of a home's AI assistant. Today is %s.
Below are the facts it remembers now, and the household's conversations with it since the last time this was done.
Return the memory as it should be from now on:
- keep facts that are still true and useful; merge duplicates and near-duplicates into one;
- update a fact the conversations changed; remove a fact that is no longer true, or whose date has passed;
- add durable facts the household stated or asked to be remembered: preferences, names they use for things,
  routines, plans with their dates (write dates out, e.g. "until Sunday 22 November");
- do not add device states, readings, one-off questions or anything the home's sensors already report;
- never invent anything; when unsure, keep an existing fact as it is;
- each fact is one short sentence; at most 60 facts.
Also say how many of the existing facts you removed or merged ("dropped").

Facts remembered now:
`, now.Format("Monday 2 January 2006, 15:04"))
	if len(memory) == 0 {
		b.WriteString("(none)\n")
	}
	for _, m := range memory {
		fmt.Fprintf(&b, "- %s\n", m)
	}
	b.WriteString("\nConversations, oldest first:\n")
	for _, t := range day {
		fmt.Fprintf(&b, "[%s] Asked: %s\n  Answered: %s\n", t.Time.In(now.Location()).Format("Mon 15:04"), clip(t.Question, 400), clip(t.Answer, 400))
	}
	return b.String()
}

// memoryEntities finds the memory list (todo.ai_memory, or a to-do list named like "memory") and the AI task to
// consolidate with (Claude's where there is one).
func (g *Gateway) memoryEntities(ctx context.Context) (list, task string, err error) {
	states, err := g.wsRequest(ctx, "get_states")
	if err != nil {
		return "", "", fmt.Errorf("could not read Home Assistant's states: %w", err)
	}
	var lists, tasks []string
	for _, s := range objects(states) {
		id, _ := s["entity_id"].(string)
		attrs, _ := s["attributes"].(map[string]any)
		name, _ := attrs["friendly_name"].(string)
		switch {
		case id == "todo.ai_memory":
			lists = append([]string{id}, lists...)
		case strings.HasPrefix(id, "todo.") && strings.Contains(strings.ToLower(name+" "+id), "memory"):
			lists = append(lists, id)
		case strings.HasPrefix(id, "ai_task."):
			tasks = append(tasks, id)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		return strings.Contains(tasks[i], "claude") && !strings.Contains(tasks[j], "claude")
	})
	if len(lists) == 0 {
		return "", "", fmt.Errorf("no memory list: make a to-do list called AI Memory and expose it to Assist")
	}
	if len(tasks) == 0 {
		return lists[0], "", fmt.Errorf("no AI task entity (ai_task.*) to consolidate with")
	}
	return lists[0], tasks[0], nil
}

func (g *Gateway) memoryItems(ctx context.Context, list string) ([]memItem, error) {
	res, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "call_service", "domain": "todo", "service": "get_items",
		"target": map[string]any{"entity_id": list}, "service_data": map[string]any{"status": "needs_action"}, "return_response": true}})
	if err != nil {
		return nil, err
	}
	if errs[0] != nil {
		return nil, errs[0]
	}
	var out struct {
		Response map[string]struct {
			Items []memItem `json:"items"`
		} `json:"response"`
	}
	data, _ := json.Marshal(res[0])
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.Response[list].Items, nil
}

func (g *Gateway) consolidate(ctx context.Context, task, instructions string) ([]string, error) {
	res, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "call_service", "domain": "ai_task", "service": "generate_data", "return_response": true,
		"service_data": map[string]any{
			"entity_id": task, "task_name": "Consolidate AI Assist's memory", "instructions": instructions,
			"structure": map[string]any{
				"facts":   map[string]any{"description": "The memory from now on, one short sentence each", "required": true, "selector": map[string]any{"text": map[string]any{"multiple": true}}},
				"dropped": map[string]any{"description": "How many existing facts were removed or merged", "selector": map[string]any{"number": map[string]any{}}},
			},
		}}})
	if err != nil {
		return nil, err
	}
	if errs[0] != nil {
		return nil, errs[0]
	}
	var out struct {
		Response struct {
			Data struct {
				Facts []string `json:"facts"`
			} `json:"data"`
		} `json:"response"`
	}
	data, _ := json.Marshal(res[0])
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.Response.Data.Facts, nil
}

func (g *Gateway) applyMemory(ctx context.Context, list string, add []string, remove []memItem) error {
	var reqs []map[string]any
	target := map[string]any{"entity_id": list}
	for _, it := range remove {
		reqs = append(reqs, map[string]any{"type": "call_service", "domain": "todo", "service": "remove_item", "target": target, "service_data": map[string]any{"item": it.UID}})
	}
	for _, f := range add {
		reqs = append(reqs, map[string]any{"type": "call_service", "domain": "todo", "service": "add_item", "target": target, "service_data": map[string]any{"item": f}})
	}
	if len(reqs) == 0 {
		return nil
	}
	_, errs, err := g.wsBatch(ctx, reqs)
	if err != nil {
		return err
	}
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func (r dreamResult) line() string {
	if !r.Applied {
		return fmt.Sprintf("dreaming: %d question(s), memory unchanged: %s", r.Questions, r.Note)
	}
	return fmt.Sprintf("dreaming: %d question(s), memory %d → %d (%d added, %d removed)", r.Questions, len(r.Before), len(r.After), r.Added, r.Removed)
}

func (d *dreamer) load() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if data, err := os.ReadFile(filepath.Join(d.dir, "last.json")); err == nil {
		var r dreamResult
		if json.Unmarshal(data, &r) == nil {
			d.last = &r
		}
	}
}

// keep writes the night's result as last.json and as its own dated file, keeping the newest 30 nights.
func (d *dreamer) keep(r dreamResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.last = &r
	if d.dir == "" || os.MkdirAll(d.dir, 0o700) != nil {
		return
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(filepath.Join(d.dir, "last.json"), data, 0o600)
	os.WriteFile(filepath.Join(d.dir, r.At.Format("2006-01-02T1504")+".json"), data, 0o600)
	files, _ := filepath.Glob(filepath.Join(d.dir, "20*.json"))
	sort.Strings(files)
	for len(files) > 30 {
		os.Remove(files[0])
		files = files[1:]
	}
}
