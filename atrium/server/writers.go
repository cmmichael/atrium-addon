package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Granting writers from the set-up page. A Home Assistant administrator, through ingress only, picks an installed
// add-on and Atrium adds its slug to its own options (the same `writers` list the Configuration tab edits), then
// applies it at once, since options are otherwise read only at start. The Supervisor is the source of truth for
// what is installed: GET /supervisor/info lists the add-ons (older Supervisors; newer ones leave the list out), and
// GET /addons/<slug>/info checks a slug typed in. Both are allowed to the default role with hassio_api.

// AddonInfo is what the set-up page shows about an installed add-on before the administrator confirms it.
type AddonInfo struct {
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	Repository string `json:"repository"` // the Supervisor's id of its repository: "core", "local", or the hash of the URL
	State      string `json:"state"`
	Version    string `json:"version"`
	IP         string `json:"ip_address,omitempty"` // its address on the add-ons' network, for the writer check
}

// A slug as the Supervisor spells it: <repository-hash>_<name>, or a core/local add-on's name alone.
var slugPattern = regexp.MustCompile(`^[-_.A-Za-z0-9]{1,100}$`)

// serveAddons answers GET /atrium/addons: the installed add-ons (name, slug, repository), or with ?slug= one add-on
// checked with the Supervisor. For administrators through ingress only; serve checks that.
func (g *Gateway) serveAddons(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if slug := r.URL.Query().Get("slug"); slug != "" {
		info, err := g.addonInfo(ctx, slug)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(info)
		return
	}
	list, err := g.installedAddons(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"addons": list})
}

// serveWriters answers POST /atrium/writers {"slug": …}: checks the slug with the Supervisor, adds it to this add-on's
// options (the whole object is read back and posted again: /addons/self/options replaces all options), and applies
// the new list in memory. For administrators through ingress only; serve checks that.
func (g *Gateway) serveWriters(w http.ResponseWriter, r *http.Request, id Identity) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST {\"slug\": …}", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Slug string `json:"slug"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil || json.Unmarshal(data, &body) != nil || !slugPattern.MatchString(strings.TrimSpace(body.Slug)) {
		http.Error(w, "send {\"slug\": \"<repository-hash>_<name>\"}", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(body.Slug)
	if slug == "self" || (g.Slug != "" && slug == g.Slug) {
		http.Error(w, "Atrium does not list itself", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := g.addonInfo(ctx, slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// The Supervisor's own word on which add-on this is.
	if info.Slug != slug {
		http.Error(w, fmt.Sprintf("the Supervisor answered for %s, not %s", info.Slug, slug), http.StatusBadGateway)
		return
	}
	if g.Slug != "" && info.Slug == g.Slug {
		http.Error(w, "Atrium does not list itself", http.StatusBadRequest)
		return
	}
	writers, err := g.addWriter(ctx, slug)
	if err != nil {
		log.Printf("writers: %s could not add %s: %v", id.Login, slug, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("writers: %s (%s) allowed %s (%s) to write the layout; writers now %v", id.Login, id.Device, slug, info.Name, writers)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"writers": writers, "added": info})
}

// installedAddons lists the add-ons the Supervisor has installed, by name. An empty list means the Supervisor did
// not say (newer Supervisors leave `addons` out of /supervisor/info): the page then asks for a slug instead.
func (g *Gateway) installedAddons(ctx context.Context) ([]AddonInfo, error) {
	var out struct {
		Data struct {
			Addons []AddonInfo `json:"addons"`
		} `json:"data"`
	}
	if err := g.supervisorJSON(ctx, "GET", "/supervisor/info", nil, &out); err != nil {
		return nil, err
	}
	list := out.Data.Addons
	if list == nil {
		list = []AddonInfo{}
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	return list, nil
}

// addonInfo asks the Supervisor about one add-on.
func (g *Gateway) addonInfo(ctx context.Context, slug string) (AddonInfo, error) {
	if !slugPattern.MatchString(slug) {
		return AddonInfo{}, fmt.Errorf("%q is not an add-on slug", slug)
	}
	var out struct {
		Data AddonInfo `json:"data"`
	}
	if err := g.supervisorJSON(ctx, "GET", "/addons/"+url.PathEscape(slug)+"/info", nil, &out); err != nil {
		return AddonInfo{}, err
	}
	if out.Data.Slug == "" {
		out.Data.Slug = slug
	}
	return out.Data, nil
}

// addWriter puts a slug on this add-on's writers list, in the Supervisor's copy of the options and in memory.
func (g *Gateway) addWriter(ctx context.Context, slug string) ([]string, error) {
	var self struct {
		Data struct {
			Options map[string]any `json:"options"`
		} `json:"data"`
	}
	if err := g.supervisorJSON(ctx, "GET", "/addons/self/info", nil, &self); err != nil {
		return nil, err
	}
	if self.Data.Options == nil {
		return nil, fmt.Errorf("the Supervisor did not return this add-on's options")
	}
	writers := []string{}
	for _, s := range strs(self.Data.Options["writers"]) {
		if strings.TrimSpace(s) != "" {
			writers = append(writers, strings.TrimSpace(s))
		}
	}
	if !contains(writers, slug) {
		writers = append(writers, slug)
	}
	self.Data.Options["writers"] = writers
	if err := g.supervisorJSON(ctx, "POST", "/addons/self/options", map[string]any{"options": self.Data.Options}, nil); err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.Opts.Writers = writers
	g.mu.Unlock()
	return writers, nil
}

// supervisorJSON is one call to the Supervisor's API, as this add-on, with the answer decoded into out (nil: only
// the status matters). A non-200 is an error that carries the status and the Supervisor's message.
func (g *Gateway) supervisorJSON(ctx context.Context, method, path string, body any, out any) error {
	base := g.SupervisorURL
	if base == "" {
		base = "http://supervisor"
	}
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base, "/")+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Addon.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the Supervisor did not answer %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		json.Unmarshal(data, &e)
		return &supervisorError{Status: res.StatusCode, Method: method, Path: path, Message: firstNonEmpty(e.Message, strings.TrimSpace(string(data)))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("the Supervisor's answer to %s %s was not understood: %v", method, path, err)
		}
	}
	return nil
}

// supervisorError is a non-200 answer from the Supervisor: the status, so callers can say what it means, and the
// Supervisor's own message.
type supervisorError struct {
	Status       int
	Method, Path string
	Message      string
}

func (e *supervisorError) Error() string {
	return fmt.Sprintf("Supervisor status %d for %s %s: %s", e.Status, e.Method, e.Path, e.Message)
}
