package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
)

// The add-on is an MCP server too (Streamable HTTP, JSON-RPC over POST /mcp), so any Claude that can reach it —
// signed in by Tailscale, or an administrator through Home Assistant — can make the layout with the same tools
// the atrium-mapping skill describes: read the contract and the skill, look at the house, draft, save, check.
// Nothing here needs a token: the connection's identity is the caller's.

const mcpProtocol = "2025-03-26"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (g *Gateway) serveMCP(w http.ResponseWriter, r *http.Request, id Identity) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "MCP over Streamable HTTP: POST JSON-RPC here", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "not JSON-RPC: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID == nil || strings.HasPrefix(req.Method, "notifications/") { // a notification: nothing to answer
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rpcErr := g.mcpCall(r.Context(), id, req.Method, req.Params)
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rpcErr != nil {
		out["error"] = rpcErr
	} else {
		out["result"] = result
	}
	json.NewEncoder(w).Encode(out)
}

func (g *Gateway) mcpCall(ctx context.Context, id Identity, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpProtocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "atrium", "version": version},
			"instructions":    "Atrium renders one declared layout, atrium.yaml. To make or change it: read atrium_contract and atrium_skill, look at the house with atrium_home (and atrium_draft for a starting point), write the file, then atrium_mapping_put, which checks it and reports what Home Assistant does not have.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		json.Unmarshal(params, &p)
		text, isErr, err := g.mcpTool(ctx, id, p.Name, p.Arguments)
		if err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "no such method: " + method}
	}
}

var mcpTools = []map[string]any{
	{"name": "atrium_contract", "description": "The layout file's contract (mapping.md): every key of atrium.yaml and the rules the add-on applies. Read before writing a layout.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "atrium_skill", "description": "The atrium-mapping skill (SKILL.md): how to draft, review and deliver a layout, and the judgment calls to make.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "atrium_home", "description": "What Home Assistant has: floors, areas, and every entity with its area, device class, unit, platform and current state. Filter by domain (e.g. \"light\") or area id to keep it short.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"domain": map[string]any{"type": "string", "description": "only this domain, e.g. light, climate, sensor"},
			"area":   map[string]any{"type": "string", "description": "only this area id"},
		}}},
	{"name": "atrium_draft", "description": "A draft atrium.yaml made by the skill's draft script from the registries: floors, one room per area with something to control, scenes, readouts, security, energy, music. Guesses carry a trailing '# check'. Review it (see atrium_skill), then atrium_mapping_put.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "atrium_mapping_get", "description": "The current atrium.yaml, as saved.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "atrium_mapping_put", "description": "Save atrium.yaml (owner and administrators only). The file is checked first and refused with the reason if it is not right; otherwise it is saved and rendered, and the answer lists what Home Assistant does not have.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"yaml"}, "properties": map[string]any{
			"yaml": map[string]any{"type": "string", "description": "the whole file"},
		}}},
	{"name": "atrium_layout_status", "description": "Whether there is a layout, how many rooms it names, the file's error if it was refused, and what the last render could not use.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
}

// SkillDir is where the add-on carries the atrium-mapping skill (copied in by tools/build-web.sh).
func (g *Gateway) skillFile(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(g.SkillDir, name))
	if err != nil {
		return "", fmt.Errorf("the add-on has no %s", name)
	}
	return string(data), nil
}

func (g *Gateway) mcpTool(ctx context.Context, id Identity, name string, args map[string]any) (text string, isError bool, err error) {
	str := func(k string) string { s, _ := args[k].(string); return s }
	switch name {
	case "atrium_contract":
		text, err = g.skillFile("mapping.md")
		return
	case "atrium_skill":
		text, err = g.skillFile("SKILL.md")
		return
	case "atrium_home":
		d, err := g.dump(ctx)
		if err != nil {
			return "Home Assistant did not answer: " + err.Error(), true, nil
		}
		return homeText(d, str("domain"), str("area")), false, nil
	case "atrium_draft":
		return g.draft(ctx)
	case "atrium_mapping_get":
		data, err := os.ReadFile(g.MappingPath)
		if err != nil {
			return "There is no layout yet.", true, nil
		}
		return string(data), false, nil
	case "atrium_mapping_put":
		if !g.mayConfigure(id) {
			return "Only the owner and administrators may change the layout.", true, nil
		}
		return g.putMapping(ctx, id, []byte(str("yaml")))
	case "atrium_layout_status":
		g.mu.Lock()
		l := Layout{Path: g.mappingHostPath(), Exists: !g.mappingAt.IsZero(), Rooms: len(g.mapping.Rooms), Error: g.mappingErr, Problems: g.problems}
		g.mu.Unlock()
		out, _ := json.MarshalIndent(l, "", "  ")
		return string(out), false, nil
	default:
		return "", false, fmt.Errorf("no such tool: %s", name)
	}
}

// putMapping is what PUT /atrium/mapping and the MCP tool share: check, save, render, report.
func (g *Gateway) putMapping(ctx context.Context, id Identity, data []byte) (string, bool, error) {
	if len(data) > 1<<20 {
		return "A layout is at most 1 MiB.", true, nil
	}
	if _, err := house.ParseMapping(data); err != nil {
		return "Refused: " + err.Error(), true, nil
	}
	if g.MappingPath == "" {
		return "This add-on has nowhere to keep a layout.", true, nil
	}
	tmp := g.MappingPath + ".tmp"
	err := os.WriteFile(tmp, data, 0o644)
	if err == nil {
		err = os.Rename(tmp, g.MappingPath)
	}
	if err != nil {
		os.Remove(tmp)
		return "Could not save: " + err.Error(), true, nil
	}
	g.mu.Lock()
	g.mappingAt = time.Time{} // re-read whatever the modification time says: two saves can share a second
	g.mu.Unlock()
	g.readMapping()
	log.Printf("layout written by %s (%s)", id.Login, id.Device)
	if _, err := g.houseJSON(ctx); err != nil {
		return "Saved, but Home Assistant did not answer, so it is not rendered yet: " + err.Error(), true, nil
	}
	g.mu.Lock()
	rooms, problems, path := len(g.mapping.Rooms), append([]string{}, g.problems...), g.mappingHostPath()
	g.mu.Unlock()
	out, _ := json.Marshal(map[string]any{"rooms": rooms, "problems": problems, "path": path})
	return string(out), false, nil
}

// draft runs the skill's draft script here, against Home Assistant through the Supervisor.
func (g *Gateway) draft(ctx context.Context) (string, bool, error) {
	script := filepath.Join(g.SkillDir, "draft.py")
	if _, err := os.Stat(script); err != nil {
		return "The add-on has no draft script.", true, nil
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return "The add-on has no Python, so it cannot draft here; run skills/atrium-mapping/draft.py elsewhere.", true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", script, "--url", g.Addon.Base.String())
	cmd.Env = append(os.Environ(), "HA_TOKEN="+g.Addon.Token)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return "The draft failed: " + err.Error() + "\n" + errs.String(), true, nil
	}
	return out.String(), false, nil
}

// homeText is the house as a Claude would want to read it: one line per entity, grouped by area.
func homeText(d house.Dump, domain, area string) string {
	areaName := map[string]string{}
	var areas []string
	for _, a := range d.Areas {
		if id, _ := a["area_id"].(string); id != "" {
			areaName[id], _ = a["name"].(string)
			areas = append(areas, id)
		}
	}
	sort.Strings(areas)
	deviceArea := map[string]string{}
	for _, dev := range d.Devices {
		if id, _ := dev["id"].(string); id != "" {
			deviceArea[id], _ = dev["area_id"].(string)
		}
	}
	type row struct{ id, line string }
	byArea := map[string][]row{}
	for _, e := range d.Entities {
		id, _ := e["entity_id"].(string)
		if id == "" || e["disabled_by"] != nil || e["hidden_by"] != nil || e["entity_category"] != nil {
			continue
		}
		if domain != "" && !strings.HasPrefix(id, domain+".") {
			continue
		}
		a, _ := e["area_id"].(string)
		if a == "" {
			dev, _ := e["device_id"].(string)
			a = deviceArea[dev]
		}
		if area != "" && a != area {
			continue
		}
		st := d.States[id]
		var parts []string
		if st != nil {
			if n := st.Attributes["friendly_name"]; n != nil {
				parts = append(parts, fmt.Sprint(n))
			}
			if c := st.Attributes["device_class"]; c != nil {
				parts = append(parts, "class="+fmt.Sprint(c))
			}
			parts = append(parts, "state="+st.State)
			if u := st.Attributes["unit_of_measurement"]; u != nil {
				parts = append(parts, "unit="+fmt.Sprint(u))
			}
		}
		if p, _ := e["platform"].(string); p != "" {
			parts = append(parts, "platform="+p)
		}
		byArea[a] = append(byArea[a], row{id, id + "  " + strings.Join(parts, " · ")})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Home Assistant: %s\n\nFloors:\n", d.Name)
	for _, f := range d.Floors {
		fmt.Fprintf(&b, "  %s  %v (level %v)\n", f["floor_id"], f["name"], f["level"])
	}
	fmt.Fprintf(&b, "\nAreas (id  name  floor):\n")
	for _, id := range areas {
		var floor string
		for _, a := range d.Areas {
			if a["area_id"] == id {
				floor, _ = a["floor_id"].(string)
			}
		}
		fmt.Fprintf(&b, "  %s  %s  %s\n", id, areaName[id], floor)
	}
	keys := append([]string{}, areas...)
	keys = append(keys, "")
	for _, a := range keys {
		rows := byArea[a]
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
		if a == "" {
			fmt.Fprintf(&b, "\nNo area:\n")
		} else {
			fmt.Fprintf(&b, "\n%s (%s):\n", areaName[a], a)
		}
		for _, r := range rows {
			fmt.Fprintf(&b, "  %s\n", r.line)
		}
	}
	return b.String()
}
