package main

// Updating the add-on from the apps, without asking the Supervisor for anything more than it already gives.
//
// Home Assistant keeps an update entity for every add-on (platform hassio, unique id "<slug>_version_latest"):
// its state is "on" while a newer version is published, its attributes name both versions, and the update.install
// service asks the Supervisor to install it. This add-on reaches Home Assistant with its own credential anyway, so
// the owner and the administrators may update it from the app on any route, Tailscale included. The alternative,
// hassio_role: manager, would let this add-on install or remove every add-on; this needs nothing of the kind.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// UpdateInfo is what GET /atrium/update answers.
type UpdateInfo struct {
	Installed  string `json:"installed"`            // this build, not the entity's installed_version: that one lags the Supervisor by minutes
	Latest     string `json:"latest,omitempty"`     // the newest version Home Assistant knows of
	Available  bool   `json:"available"`            // Latest is newer than Installed
	InProgress bool   `json:"inProgress,omitempty"` // the Supervisor is installing it now
	Entity     string `json:"entity,omitempty"`     // the update entity, for anyone who wants to look
	ReleaseURL string `json:"releaseURL,omitempty"`
	// Why nothing can be said: no Supervisor (a development run), or Home Assistant keeps no update entity for this slug.
	Unavailable string `json:"unavailable,omitempty"`
}

// updateEntity finds the add-on's update entity in Home Assistant's entity registry, once.
func (g *Gateway) updateEntity(ctx context.Context) (string, error) {
	g.mu.Lock()
	id, asked := g.updateID, g.updateAsked
	g.mu.Unlock()
	if id != "" || time.Since(asked) < time.Minute {
		return id, nil
	}
	if g.Slug == "" {
		return "", fmt.Errorf("this add-on does not know its slug (no Supervisor)")
	}
	want := g.Slug + "_version_latest"
	var found string
	err := g.haWS(ctx, func(call func(map[string]any) (any, error)) error {
		result, err := call(map[string]any{"type": "config/entity_registry/list"})
		if err != nil {
			return err
		}
		entries, _ := result.([]any)
		for _, e := range entries {
			m, _ := e.(map[string]any)
			if m["platform"] == "hassio" && m["unique_id"] == want {
				found, _ = m["entity_id"].(string)
				return nil
			}
		}
		return nil
	})
	g.mu.Lock()
	g.updateAsked = time.Now()
	if found != "" {
		g.updateID = found
	}
	g.mu.Unlock()
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("Home Assistant has no update entity for %s", g.Slug)
	}
	return found, nil
}

// updateStatus reads the update entity's state.
func (g *Gateway) updateStatus(ctx context.Context) UpdateInfo {
	info := UpdateInfo{Installed: version}
	id, err := g.updateEntity(ctx)
	if err != nil {
		info.Unavailable = err.Error()
		return info
	}
	info.Entity = id
	var st struct {
		State      string `json:"state"`
		Attributes struct {
			Latest     string `json:"latest_version"`
			InProgress any    `json:"in_progress"` // a bool, or a percentage while installing
			ReleaseURL string `json:"release_url"`
		} `json:"attributes"`
	}
	if err := g.haREST(ctx, "GET", "/api/states/"+id, nil, &st); err != nil {
		info.Unavailable = "could not read " + id + ": " + err.Error()
		return info
	}
	info.Latest = st.Attributes.Latest
	info.ReleaseURL = st.Attributes.ReleaseURL
	info.Available = info.Latest != "" && olderThan(version, info.Latest)
	switch p := st.Attributes.InProgress.(type) {
	case bool:
		info.InProgress = p
	case float64:
		info.InProgress = true
	}
	return info
}

// serveUpdate: GET says whether a newer add-on is published; POST (owner and administrators) asks Home Assistant to
// install it. The Supervisor stops this add-on partway through, so the answer comes first and the apps watch the
// version in /atrium/hello come back newer.
func (g *Gateway) serveUpdate(w http.ResponseWriter, r *http.Request, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case "GET":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g.updateStatus(r.Context()))
	case "POST":
		if !g.mayConfigure(id) {
			http.Error(w, "only the owner and administrators may update Atrium", http.StatusForbidden)
			return
		}
		info := g.updateStatus(r.Context())
		switch {
		case info.Unavailable != "":
			http.Error(w, info.Unavailable, http.StatusBadGateway)
			return
		case info.InProgress:
			// Already under way: say so, as a success.
		case !info.Available:
			http.Error(w, "Atrium "+version+" is the newest version", http.StatusConflict)
			return
		default:
			log.Printf("%s (%s) asked Home Assistant to update Atrium %s to %s", id.Login, id.Device, version, info.Latest)
			go func() {
				// Not the request's context: the caller goes away long before the Supervisor is done.
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
				defer cancel()
				body := map[string]any{"entity_id": info.Entity, "backup": false}
				if err := g.haREST(ctx, "POST", "/api/services/update/install", body, nil); err != nil {
					log.Printf("the update to %s did not go through: %v", info.Latest, err)
				}
			}()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"updating": true, "from": version, "to": info.Latest})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

// haREST calls Home Assistant's REST API with the add-on's credential. out may be nil.
func (g *Gateway) haREST(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(g.Addon.Base.String(), "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Addon.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
		}
		json.NewDecoder(res.Body).Decode(&e)
		if e.Message != "" {
			return fmt.Errorf("Home Assistant answered %d: %s", res.StatusCode, e.Message)
		}
		return fmt.Errorf("Home Assistant answered %d", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}
