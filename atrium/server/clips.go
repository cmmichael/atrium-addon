package main

// Recent clips from cameras whose integration publishes them through Home Assistant's media source (UniFi
// Protect). The apps ask /atrium/clips?all=1 (every camera's) or ?camera=<entity>; the add-on walks the media tree,
// keeps the answer (clipcache.go) and gives a plain list. Playing one: /atrium/clips?id=<media id> resolves it to
// a signed /api/… MP4 the apps open through the add-on.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type Clip struct {
	ID      string `json:"id"`              // media-source://… to resolve on tap
	Title   string `json:"title"`           // what happened: "Person", "Ring", "Motion"
	When    string `json:"when"`            // as Home Assistant wrote it, the house's time: "11/07/26 09:35:58"
	At      string `json:"at,omitempty"`    // the same moment for a viewer in any zone: "2026-11-07T17:35:58Z"
	Seconds int    `json:"seconds"`         // how long
	Thumb   string `json:"thumb,omitempty"` // /api/… picture, through the add-on
	Raw     string `json:"raw"`             // the node's own title
}

// "11/07/26 09:35:58 9s Object Detection - Person"
var clipTitle = regexp.MustCompile(`^(\d\d/\d\d/\d\d \d\d:\d\d:\d\d) (\d+)s (.+?)(?: - (.+))?$`)

// parseClip reads a UniFi Protect title; its time is the house's (loc; nil leaves At out).
func parseClip(id, title, thumb string, loc *time.Location) Clip {
	c := Clip{ID: id, Raw: title, Title: title, Thumb: thumb}
	if m := clipTitle.FindStringSubmatch(title); m != nil {
		c.When = m[1]
		if loc != nil {
			if t, err := time.ParseInLocation("01/02/06 15:04:05", c.When, loc); err == nil {
				c.At = t.UTC().Format(time.RFC3339)
			}
		}
		fmt.Sscan(m[2], &c.Seconds)
		c.Title = firstNonEmpty(m[4], strings.TrimSuffix(m[3], " Event"))
	}
	return c
}

// haWS opens one WebSocket to Home Assistant with the add-on's credential and hands back a call function.
func (g *Gateway) haWS(ctx context.Context, use func(call func(msg map[string]any) (any, error)) error) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, g.Addon.WS, nil)
	if err != nil {
		return err
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
		return err
	}
	auth, _ := json.Marshal(map[string]any{"type": "auth", "access_token": g.Addon.Token})
	if err := c.Write(ctx, websocket.MessageText, auth); err != nil {
		return err
	}
	if m, err := read(); err != nil || m["type"] != "auth_ok" {
		return fmt.Errorf("Home Assistant refused the add-on's token")
	}
	next := 0
	return use(func(msg map[string]any) (any, error) {
		next++
		msg["id"] = next
		data, _ := json.Marshal(msg)
		if err := c.Write(ctx, websocket.MessageText, data); err != nil {
			return nil, err
		}
		for {
			m, err := read()
			if err != nil {
				return nil, err
			}
			if id, _ := m["id"].(float64); int(id) != next || m["type"] != "result" {
				continue
			}
			if ok, _ := m["success"].(bool); !ok {
				e, _ := m["error"].(map[string]any)
				return nil, fmt.Errorf("%v", e["message"])
			}
			return m["result"], nil
		}
	})
}

type mediaNode struct {
	Title    string      `json:"title"`
	ID       string      `json:"media_content_id"`
	CanPlay  bool        `json:"can_play"`
	Thumb    string      `json:"thumbnail"`
	Children []mediaNode `json:"children"`
}

func asNode(v any) mediaNode {
	var n mediaNode
	data, _ := json.Marshal(v)
	json.Unmarshal(data, &n)
	return n
}

// protectCamera is the camera entity a UniFi Protect node stands for: its thumbnail is that camera's own picture.
var protectCamera = regexp.MustCompile(`/camera_proxy/(camera\.[a-z0-9_]+)`)

// protectClips lists every UniFi Protect camera's events of the last day, newest first, by camera entity, over
// one connection. Nil, nil: no UniFi Protect here.
func (g *Gateway) protectClips(ctx context.Context, limit int) (map[string][]Clip, error) {
	var out map[string][]Clip
	err := g.haWS(ctx, func(call func(map[string]any) (any, error)) error {
		browse := func(id string) (mediaNode, error) {
			r, err := call(map[string]any{"type": "media_source/browse_media", "media_content_id": id})
			return asNode(r), err
		}
		root, err := browse("media-source://unifiprotect")
		if err != nil {
			return nil // no UniFi Protect: no clips
		}
		out = map[string][]Clip{}
		// The titles carry the house's own time: Home Assistant's zone turns them into moments.
		loc := time.Local
		if cfg, err := call(map[string]any{"type": "get_config"}); err == nil {
			if m, _ := cfg.(map[string]any); m != nil {
				if zone, _ := m["time_zone"].(string); zone != "" {
					if l, err := time.LoadLocation(zone); err == nil {
						loc = l
					}
				}
			}
		}
		// The cameras' nodes, under the root (one NVR) or one level down (several).
		nodes := map[string]string{} // camera entity → node id
		var find func(n mediaNode, depth int)
		find = func(n mediaNode, depth int) {
			for _, c := range n.Children {
				if m := protectCamera.FindStringSubmatch(c.Thumb); m != nil {
					nodes[m[1]] = c.ID
				} else if depth > 0 && strings.Contains(c.ID, ":browse:") && !strings.HasSuffix(c.ID, ":browse:all") {
					if sub, err := browse(c.ID); err == nil {
						find(sub, depth-1)
					}
				}
			}
		}
		find(root, 1)
		for camera, node := range nodes {
			day, err := browse(node + ":all:recent:1")
			if err != nil {
				return err
			}
			list := []Clip{}
			for _, c := range day.Children {
				if c.CanPlay && len(list) < limit {
					list = append(list, parseClip(c.ID, c.Title, c.Thumb, loc))
				}
			}
			out[camera] = list
		}
		return nil
	})
	return out, err
}

// resolveClip turns a clip's media id into the /api/… address of its video.
func (g *Gateway) resolveClip(ctx context.Context, id string) (string, error) {
	var url string
	err := g.haWS(ctx, func(call func(map[string]any) (any, error)) error {
		r, err := call(map[string]any{"type": "media_source/resolve_media", "media_content_id": id})
		if err != nil {
			return err
		}
		m, _ := r.(map[string]any)
		url, _ = m["url"].(string)
		return nil
	})
	return url, err
}

func (g *Gateway) serveClips(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if id := r.URL.Query().Get("id"); id != "" {
		if !strings.HasPrefix(id, "media-source://") {
			http.Error(w, `{"error":"not a media id"}`, http.StatusBadRequest)
			return
		}
		url, err := g.resolveClip(r.Context(), id)
		if err != nil || !strings.HasPrefix(url, "/api/") {
			http.Error(w, `{"error":"Home Assistant could not find that clip"}`, http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"url": url})
		return
	}
	all, at, err := g.allClips(r.Context())
	if err != nil {
		http.Error(w, `{"error":"Home Assistant did not answer"}`, http.StatusBadGateway)
		return
	}
	if r.URL.Query().Get("all") != "" { // every camera's at once, for the day's story
		if all == nil {
			all = map[string][]Clip{}
		}
		json.NewEncoder(w).Encode(map[string]any{"cameras": all, "at": at.UTC().Format(time.RFC3339)})
		return
	}
	camera := r.URL.Query().Get("camera")
	if !strings.HasPrefix(camera, "camera.") {
		http.Error(w, `{"error":"camera=<camera entity id>, or all=1"}`, http.StatusBadRequest)
		return
	}
	list := all[camera]
	if list == nil {
		list = []Clip{}
	}
	json.NewEncoder(w).Encode(map[string]any{"camera": camera, "clips": list})
}
