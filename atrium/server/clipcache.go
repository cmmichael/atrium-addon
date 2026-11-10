package main

// The cameras' clips and their pictures, kept so the apps have them at once.
//
// Listing a day of clips means walking Home Assistant's media tree, a second or more per camera, and every
// thumbnail is fetched from the recorder when asked. So the add-on keeps the last listing and answers it at once,
// refreshing behind while someone is looking, and keeps the thumbnails in memory: /atrium/thumb?p=<the clip's
// thumb> answers from there, with a day of Cache-Control so the apps keep them too.

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	clipsFresh   = 20 * time.Second // a listing this young is answered as it is
	clipsKeep    = 30 * time.Minute // an older one is still answered at once, and refreshed behind
	clipsPerCam  = 30
	thumbsBudget = 32 << 20 // bytes of thumbnails kept
	thumbMax     = 2 << 20  // one thumbnail's limit
	thumbsAhead  = 8        // the newest clips of each camera whose thumbnails are fetched before anyone asks
)

type clipStore struct {
	mu      sync.Mutex
	lists   map[string][]Clip
	at      time.Time
	loading chan struct{} // closed when the refresh under way ends
	err     error         // the last refresh's
	took    time.Duration // how long the last listing took
	failed  []string      // cameras whose listing failed the last time (their kept clips stay)
	thumbs  map[string]*thumbnail
	bytes   int
	// What lists the clips and fetches a picture (tests replace them).
	list  func(ctx context.Context) (lists map[string][]Clip, failed []string, err error)
	fetch func(ctx context.Context, path string) ([]byte, string, error)
}

type thumbnail struct {
	data []byte
	kind string
	used time.Time
}

// allClips is every camera's clips of the last day, and when they were listed. A listing kept is answered at
// once, however the refresh behind it goes; with none kept, the caller waits for the first.
func (g *Gateway) allClips(ctx context.Context) (map[string][]Clip, time.Time, error) {
	s := &g.clips
	s.mu.Lock()
	age := time.Since(s.at)
	if s.lists != nil && age < clipsFresh {
		defer s.mu.Unlock()
		return s.lists, s.at, nil
	}
	wait := s.loading
	if wait == nil {
		wait = make(chan struct{})
		s.loading = wait
		go g.refreshClips(wait)
	}
	if s.lists != nil && age < clipsKeep {
		defer s.mu.Unlock()
		return s.lists, s.at, nil
	}
	s.mu.Unlock()
	select {
	case <-wait:
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, time.Time{}, s.err
	}
	return s.lists, s.at, nil
}

func (g *Gateway) refreshClips(done chan struct{}) {
	s := &g.clips
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	list := s.list
	if list == nil {
		list = g.listClips
	}
	began := time.Now()
	lists, failed, err := list(ctx)
	s.mu.Lock()
	s.err, s.took, s.failed = err, time.Since(began), failed
	if err == nil {
		for _, camera := range failed { // a camera that did not answer keeps what it had
			if kept, ok := s.lists[camera]; ok {
				lists[camera] = kept
			}
		}
		s.lists, s.at = lists, time.Now()
	} else {
		log.Printf("clips: %v", err)
	}
	s.loading = nil
	s.mu.Unlock()
	close(done)
	if err == nil {
		g.warmThumbs(lists)
	}
}

// listClips gathers the clips of every source Home Assistant has.
func (g *Gateway) listClips(ctx context.Context) (map[string][]Clip, []string, error) {
	out, failed, err := g.protectClips(ctx, clipsPerCam)
	if err != nil {
		return nil, nil, err
	}
	if out == nil {
		out = map[string][]Clip{}
	}
	return out, failed, nil
}

// warmThumbs fetches the newest clips' thumbnails that are not kept yet, a few at a time.
func (g *Gateway) warmThumbs(lists map[string][]Clip) {
	var want []string
	s := &g.clips
	s.mu.Lock()
	for _, list := range lists {
		for i, c := range list {
			if i < thumbsAhead && thumbPath(c.Thumb) && s.thumbs[c.Thumb] == nil {
				want = append(want, c.Thumb)
			}
		}
	}
	s.mu.Unlock()
	sort.Strings(want)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, p := range want {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			g.thumb(ctx, p)
		}()
	}
	wg.Wait()
}

// thumbPath says whether a path is a clip's picture, as a listing gives it: the only ones /atrium/thumb fetches.
// Nothing encoded (a second decoding further on must not turn it into another path), and no way up the tree.
func thumbPath(p string) bool {
	if !strings.HasPrefix(p, "/api/unifiprotect/thumbnail/") && !strings.HasPrefix(p, "/api/nest/event_media/") {
		return false
	}
	if strings.ContainsAny(p, "%\\#") {
		return false
	}
	at, _, _ := strings.Cut(p, "?")
	return path.Clean(at) == at
}

// thumb is one clip's picture, from memory or from Home Assistant.
func (g *Gateway) thumb(ctx context.Context, p string) ([]byte, string, error) {
	s := &g.clips
	s.mu.Lock()
	if t := s.thumbs[p]; t != nil {
		t.used = time.Now()
		s.mu.Unlock()
		return t.data, t.kind, nil
	}
	fetch := s.fetch
	s.mu.Unlock()
	if fetch == nil {
		fetch = g.fetchPicture
	}
	data, kind, err := fetch(ctx, p)
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.thumbs == nil {
		s.thumbs = map[string]*thumbnail{}
	}
	if s.thumbs[p] == nil {
		s.thumbs[p] = &thumbnail{data: data, kind: kind, used: time.Now()}
		s.bytes += len(data)
		for s.bytes > thumbsBudget { // the one unused longest goes
			oldest := ""
			for k, t := range s.thumbs {
				if k != p && (oldest == "" || t.used.Before(s.thumbs[oldest].used)) {
					oldest = k
				}
			}
			if oldest == "" {
				break
			}
			s.bytes -= len(s.thumbs[oldest].data)
			delete(s.thumbs, oldest)
		}
	}
	return data, kind, nil
}

// fetchPicture gets a picture under /api from Home Assistant with the add-on's credential.
func (g *Gateway) fetchPicture(ctx context.Context, p string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(g.Addon.Base.String(), "/")+p, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.Addon.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	kind := res.Header.Get("Content-Type")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(kind, "image/") {
		return nil, "", fmt.Errorf("Home Assistant answered %d (%s) for a clip's picture", res.StatusCode, kind)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, thumbMax+1))
	if err != nil || len(data) == 0 || len(data) > thumbMax {
		return nil, "", fmt.Errorf("a clip's picture could not be read (%d bytes): %v", len(data), err)
	}
	return data, kind, nil
}

// serveThumb answers /atrium/thumb?p=<a clip's thumb, as the listing gave it>.
func (g *Gateway) serveThumb(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	if !thumbPath(p) {
		http.Error(w, "p=<a clip's thumb>", http.StatusBadRequest)
		return
	}
	data, kind, err := g.thumb(r.Context(), p)
	if err != nil {
		if r.Context().Err() == nil {
			log.Printf("thumb: %v", err)
		}
		http.Error(w, "Home Assistant has no picture for that clip", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable") // an event's picture does not change
	w.Write(data)
}

// clipsHealth is what /atrium/health says of the clips: enough to see that listings happen and how long they
// take, without a picture or a title.
type clipsHealth struct {
	ListedAt    *time.Time `json:"listedAt,omitempty"` // nil: nobody has asked since the add-on started
	Seconds     float64    `json:"seconds"`            // how long the last listing took
	Cameras     int        `json:"cameras"`
	Clips       int        `json:"clips"`
	Failed      []string   `json:"failed,omitempty"` // cameras whose listing failed the last time
	Error       string     `json:"error,omitempty"`  // why the last listing failed as a whole
	Thumbnails  int        `json:"thumbnails"`       // kept in memory
	ThumbnailKB int        `json:"thumbnailKB"`
}

func (g *Gateway) clipsHealth() clipsHealth {
	s := &g.clips
	s.mu.Lock()
	defer s.mu.Unlock()
	h := clipsHealth{Seconds: float64(s.took.Milliseconds()) / 1000, Cameras: len(s.lists), Failed: s.failed, Thumbnails: len(s.thumbs), ThumbnailKB: s.bytes >> 10}
	if !s.at.IsZero() {
		at := s.at
		h.ListedAt = &at
	}
	for _, list := range s.lists {
		h.Clips += len(list)
	}
	if s.err != nil {
		h.Error = s.err.Error()
	}
	return h
}
