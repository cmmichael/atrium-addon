package main

// Recent clips from cameras whose integration publishes them through Home Assistant's media source (UniFi
// Protect). The apps ask /atrium/clips?camera=<entity>; the add-on walks the media tree and answers a plain list.
// Playing one: /atrium/clip?id=<media id> resolves it to a signed /api/… MP4 the apps open through the add-on.

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
	When    string `json:"when"`            // as Home Assistant wrote it, local time: "11/07/26 09:35:58"
	Seconds int    `json:"seconds"`         // how long
	Thumb   string `json:"thumb,omitempty"` // /api/… picture, through the add-on
	Raw     string `json:"raw"`             // the node's own title
}

// "11/07/26 09:35:58 9s Object Detection - Person"
var clipTitle = regexp.MustCompile(`^(\d\d/\d\d/\d\d \d\d:\d\d:\d\d) (\d+)s (.+?)(?: - (.+))?$`)

func parseClip(id, title, thumb string) Clip {
	c := Clip{ID: id, Raw: title, Title: title, Thumb: thumb}
	if m := clipTitle.FindStringSubmatch(title); m != nil {
		c.When = m[1]
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

// clips lists a camera's events of the last day, newest first. Nil, nil: the camera publishes none.
func (g *Gateway) clips(ctx context.Context, camera string, limit int) ([]Clip, error) {
	var out []Clip
	err := g.haWS(ctx, func(call func(map[string]any) (any, error)) error {
		browse := func(id string) (mediaNode, error) {
			r, err := call(map[string]any{"type": "media_source/browse_media", "media_content_id": id})
			return asNode(r), err
		}
		root, err := browse("media-source://unifiprotect")
		if err != nil {
			return nil // no UniFi Protect: no clips
		}
		// The camera's node: its thumbnail is the camera's own picture (camera_proxy/<entity>). One NVR or several.
		var cam string
		var find func(n mediaNode, depth int) error
		find = func(n mediaNode, depth int) error {
			for _, c := range n.Children {
				if strings.Contains(c.Thumb, "/"+camera+"?") || strings.HasSuffix(c.Thumb, "/"+camera) {
					cam = c.ID
					return nil
				}
			}
			if depth > 0 {
				for _, c := range n.Children {
					if !strings.Contains(c.ID, ":browse:") || strings.HasSuffix(c.ID, ":browse:all") {
						continue
					}
					sub, err := browse(c.ID)
					if err == nil && find(sub, depth-1) == nil && cam != "" {
						return nil
					}
				}
			}
			return nil
		}
		find(root, 1)
		if cam == "" {
			return nil
		}
		day, err := browse(cam + ":all:recent:1")
		if err != nil {
			return err
		}
		for _, c := range day.Children {
			if !c.CanPlay {
				continue
			}
			out = append(out, parseClip(c.ID, c.Title, c.Thumb))
			if len(out) == limit {
				break
			}
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
	camera := r.URL.Query().Get("camera")
	if !strings.HasPrefix(camera, "camera.") {
		http.Error(w, `{"error":"camera=<camera entity id>"}`, http.StatusBadRequest)
		return
	}
	list, err := g.clips(r.Context(), camera, 30)
	if err != nil {
		http.Error(w, `{"error":"Home Assistant did not answer"}`, http.StatusBadGateway)
		return
	}
	if list == nil {
		list = []Clip{}
	}
	json.NewEncoder(w).Encode(map[string]any{"camera": camera, "clips": list})
}
