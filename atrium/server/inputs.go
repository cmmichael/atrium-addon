package main

// Inputs: Home Assistant's Assist can play, pause and set the volume of a media player, but has no tool to change
// its input (media_player.select_source), so "set the receiver to video" goes nowhere. media_select_input, on the
// same MCP server as the history tools, does it: only for a media player exposed to Assist, and only to a source
// in that player's own source_list, matched loosely ("video" finds "VIDEO" or "Video 1", "tv" finds "TV Audio" when
// it is the only one). A player that is off is turned on first. Where the layout gives a remote its own inputs
// (remotes[].inputs options, each an action: a script, a matrix select), those come first: integrations often list
// only some of a receiver's inputs, and the house's own way to reach the rest is what the file names.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
)

var inputTools = []map[string]any{
	{"name": "media_select_input", "description": "Change a media player's input or source: a receiver or AVR to \"Video\", \"TV\", \"Phono\", a TV to \"HDMI 2\". Turns the player on first if it is off. Without input, lists the inputs it has.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"player"}, "properties": map[string]any{
			"player": map[string]any{"type": "string", "description": "the receiver, TV or speaker as people name it (\"receiver\", \"living room AVR\"), or its entity id"},
			"input":  map[string]any{"type": "string", "description": "the input as people say it (\"video\", \"tv\", \"hdmi 2\"); leave out to list them"},
		}}},
}

// pickSource finds the source meant by what was asked: the same words first, then the only source that holds the
// asked words (or is held by them). Ambiguous or nothing: "" and why.
func pickSource(list []string, asked string) (string, string) {
	a := norm(asked)
	if a == "" {
		return "", "no input named"
	}
	squash := func(s string) string { return strings.ReplaceAll(norm(s), " ", "") }
	for _, s := range list {
		if norm(s) == a || squash(s) == squash(asked) {
			return s, ""
		}
	}
	var hits []string
	for _, s := range list {
		n := norm(s)
		if strings.Contains(" "+n+" ", " "+a+" ") || strings.HasPrefix(n, a) || strings.Contains(" "+a+" ", " "+n+" ") {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		return "", fmt.Sprintf("it has no input like %q", asked)
	}
	return "", fmt.Sprintf("%q could be %s", asked, strings.Join(hits, " or "))
}

func (g *Gateway) selectInput(ctx context.Context, ix *histIndex, player, input string) (string, error) {
	var players []*histEntity
	for _, e := range ix.find(player, 8) {
		if e.domain() == "media_player" {
			players = append(players, e)
		}
	}
	if len(players) == 0 {
		return "", fmt.Errorf("no media player exposed to Assist is called %q", player)
	}
	states, err := g.wsRequest(ctx, "get_states")
	if err != nil {
		return "", fmt.Errorf("Home Assistant did not answer: %v", err)
	}
	if text, done, err := g.layoutInput(ctx, players, objects(states), input); done {
		return text, err
	}
	type live struct {
		e       *histEntity
		state   string
		sources []string
		now     string
	}
	byID := map[string]map[string]any{}
	for _, s := range objects(states) {
		if id, _ := s["entity_id"].(string); id != "" {
			byID[id] = s
		}
	}
	var able []live
	for _, e := range distinctLabels(players) {
		s := byID[e.ID]
		attrs, _ := s["attributes"].(map[string]any)
		feat, _ := attrs["supported_features"].(float64)
		if int(feat)&2048 == 0 { // SELECT_SOURCE
			continue
		}
		st, _ := s["state"].(string)
		now, _ := attrs["source"].(string)
		able = append(able, live{e, st, strs(attrs["source_list"]), now})
	}
	if len(able) == 0 {
		return "", fmt.Errorf("%s cannot change its input", players[0].label())
	}
	if len(able) > 1 && input != "" { // two players: the one that has the input asked for
		var with []live
		for _, p := range able {
			if s, _ := pickSource(p.sources, input); s != "" {
				with = append(with, p)
			}
		}
		if len(with) > 0 {
			able = with
		}
	}
	if len(able) > 1 {
		var names []string
		for _, p := range able {
			names = append(names, p.e.label())
		}
		return "", fmt.Errorf("which one: %s?", strings.Join(names, " or "))
	}
	p := able[0]
	if input == "" {
		if len(p.sources) == 0 {
			return p.e.label() + " lists no inputs right now" + map[bool]string{true: " (it is off)", false: ""}[p.state == "off"] + ".", nil
		}
		return fmt.Sprintf("%s inputs: %s. Now: %s.", p.e.label(), strings.Join(p.sources, ", "), or(p.now, "it does not report which")), nil
	}
	src, why := pickSource(p.sources, input)
	if src == "" {
		if len(p.sources) == 0 {
			return "", fmt.Errorf("%s lists no inputs (state %s)", p.e.label(), p.state)
		}
		return "", fmt.Errorf("%s: %s; its inputs are %s", p.e.label(), why, strings.Join(p.sources, ", "))
	}
	target := map[string]any{"entity_id": p.e.ID}
	if p.state == "off" || p.state == "standby" {
		if _, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "call_service", "domain": "media_player", "service": "turn_on", "target": target}}); err != nil || errs[0] != nil {
			return "", fmt.Errorf("could not turn %s on: %v", p.e.label(), firstErr(err, errs))
		}
		time.Sleep(inputWarmup) // receivers ignore a source change while they wake
	}
	if _, errs, err := g.wsBatch(ctx, []map[string]any{{"type": "call_service", "domain": "media_player", "service": "select_source", "target": target, "service_data": map[string]any{"source": src}}}); err != nil || errs[0] != nil {
		return "", fmt.Errorf("%s did not take the input %s: %v", p.e.label(), src, firstErr(err, errs))
	}
	log.Printf("AI Assist: %s set to input %q (asked %q)", p.e.ID, src, input)
	was := ""
	if p.now != "" && p.now != src {
		was = " (was " + p.now + ")"
	}
	return fmt.Sprintf("%s: input set to %s%s.", p.e.label(), src, was), nil
}

var inputWarmup = 3 * time.Second

// layoutInput: the inputs the layout's remotes give the players asked about (a remote whose player or volume entity
// is one of them). done is false when no remote has its own inputs for them: then the player's source list decides.
func (g *Gateway) layoutInput(ctx context.Context, players []*histEntity, states []map[string]any, input string) (string, bool, error) {
	data, ok := g.lastHouse()
	if !ok {
		return "", false, nil
	}
	var h house.House
	if json.Unmarshal(data, &h) != nil {
		return "", false, nil
	}
	ids := map[string]*histEntity{}
	for _, p := range players {
		ids[p.ID] = p
	}
	var rm *house.Remote
	var who *histEntity
	for i := range h.Remotes {
		r := &h.Remotes[i]
		if r.Inputs == nil || len(r.Inputs.Options) == 0 {
			continue
		}
		if p := ids[r.Player]; p != nil {
			rm, who = r, p
			break
		}
		if p := ids[r.Volume]; p != nil && rm == nil {
			rm, who = r, p
		}
	}
	if rm == nil {
		return "", false, nil
	}
	var names []string
	now := ""
	state := map[string]string{}
	for _, s := range states {
		id, _ := s["entity_id"].(string)
		state[id], _ = s["state"].(string)
	}
	for _, o := range rm.Inputs.Options {
		names = append(names, o.Name)
		if o.Entity != "" && state[o.Entity] == o.Value {
			now = o.Name
		}
	}
	label := firstNonEmpty(rm.Name, who.label())
	if input == "" {
		return fmt.Sprintf("%s inputs: %s. Now: %s.", label, strings.Join(names, ", "), or(now, "not known")), true, nil
	}
	name, why := pickSource(names, input)
	if name == "" {
		return "", true, fmt.Errorf("%s: %s; its inputs are %s", label, why, strings.Join(names, ", "))
	}
	for _, o := range rm.Inputs.Options {
		if o.Name != name {
			continue
		}
		req := map[string]any{"type": "call_service", "domain": o.Action.Domain, "service": o.Action.Service}
		if o.Action.Entity != "" {
			req["target"] = map[string]any{"entity_id": o.Action.Entity}
		}
		if len(o.Action.Data) > 0 {
			req["service_data"] = o.Action.Data
		}
		if _, errs, err := g.wsBatch(ctx, []map[string]any{req}); err != nil || errs[0] != nil {
			return "", true, fmt.Errorf("%s did not take the input %s: %v", label, name, firstErr(err, errs))
		}
		log.Printf("AI Assist: %s set to input %q from the layout (%s.%s %s; asked %q)", label, name, o.Action.Domain, o.Action.Service, o.Action.Entity, input)
		return fmt.Sprintf("%s: input set to %s.", label, name), true, nil
	}
	return "", true, fmt.Errorf("%s: no input %s", label, name)
}

func or(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func firstErr(err error, errs []error) error {
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

// haMCPTools is every tool the MCP server offers Home Assistant: the history tools and the input tool.
func haMCPTools() []map[string]any {
	return append(append([]map[string]any{}, historyTools...), inputTools...)
}

// toolsSignature names the tools and their schemas: when a release changes it, Home Assistant's MCP client, which
// lists the tools once when its entry loads, has to load the entry again to see them.
func toolsSignature() string {
	data, _ := json.Marshal(haMCPTools())
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// refreshToolsInHA reloads Home Assistant's MCP entry for these tools when they changed since the last run, so a
// release's new or changed tool reaches the agent without anyone reloading it by hand. Nothing when the entry is not
// set up; the signature is kept only once the reload went through (or there was nothing to reload).
func (g *Gateway) refreshToolsInHA(ctx context.Context) {
	if g.HistoryTokenPath == "" {
		return
	}
	path := filepath.Join(filepath.Dir(g.HistoryTokenPath), "history-tools")
	sig := toolsSignature()
	if old, _ := os.ReadFile(path); strings.TrimSpace(string(old)) == sig {
		return
	}
	entry, err := g.historyConnected(ctx)
	if err != nil {
		log.Printf("history: could not look for the MCP entry to reload: %v", err)
		return
	}
	if entry != "" {
		if err := g.haREST(ctx, "POST", "/api/config/config_entries/entry/"+entry+"/reload", nil, nil); err != nil {
			log.Printf("history: the tools changed, but reloading Home Assistant's MCP entry failed: %v", err)
			return
		}
		log.Printf("history: the tools changed (%d now); reloaded Home Assistant's MCP entry so the agent sees them", len(haMCPTools()))
	}
	os.WriteFile(path, []byte(sig+"\n"), 0o600)
}
