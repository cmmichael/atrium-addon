package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClipTitles(t *testing.T) {
	for title, want := range map[string]Clip{
		"11/07/26 09:35:58 9s Object Detection - Person": {Title: "Person", When: "11/07/26 09:35:58", Seconds: 9},
		"11/07/26 08:24:01 12s Ring Event":               {Title: "Ring", When: "11/07/26 08:24:01", Seconds: 12},
		"something else":                                 {Title: "something else"},
	} {
		c := parseClip("id", title, "", nil)
		if c.Title != want.Title || c.When != want.When || c.Seconds != want.Seconds || c.At != "" {
			t.Errorf("%q → %+v", title, c)
		}
	}
	// The title's time is the house's: a phone in another zone needs the moment itself.
	la, _ := time.LoadLocation("America/Los_Angeles")
	if c := parseClip("id", "11/07/26 09:35:58 9s Object Detection - Person", "", la); c.At != "2026-11-07T17:35:58Z" {
		t.Errorf("at = %q", c.At)
	}
}

func TestClipsAreKeptAndRefreshedBehind(t *testing.T) {
	g := &Gateway{}
	listed := 0
	release := make(chan struct{}, 8)
	g.clips.list = func(ctx context.Context) (map[string][]Clip, []string, error) {
		listed++
		n := listed
		<-release
		if n == 1 {
			return map[string][]Clip{"camera.door": {{ID: "clip1"}}, "camera.gate": {{ID: "gate1"}}}, nil, nil
		}
		return map[string][]Clip{"camera.door": {{ID: fmt.Sprint("clip", n)}}}, []string{"camera.gate"}, nil // the gate did not answer
	}
	g.clips.fetch = func(ctx context.Context, p string) ([]byte, string, error) { return []byte("jpg"), "image/jpeg", nil }
	release <- struct{}{}
	all, _, err := g.allClips(context.Background()) // the first caller waits for the listing
	if err != nil || all["camera.door"][0].ID != "clip1" {
		t.Fatalf("first listing: %v %v", all, err)
	}
	if all, _, _ = g.allClips(context.Background()); all["camera.door"][0].ID != "clip1" || listed != 1 {
		t.Fatalf("a young listing is answered as it is: %v, listed %d times", all, listed)
	}
	// Grown old: answered at once all the same, with a refresh behind it.
	g.clips.mu.Lock()
	g.clips.at = time.Now().Add(-time.Minute)
	g.clips.mu.Unlock()
	if all, _, _ = g.allClips(context.Background()); all["camera.door"][0].ID != "clip1" {
		t.Fatalf("an old listing is still answered at once: %v", all)
	}
	release <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if all, _, _ = g.allClips(context.Background()); all["camera.door"][0].ID == "clip2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresh behind never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A camera whose listing failed keeps the clips it had, and the health report names it.
	if len(all["camera.gate"]) != 1 || all["camera.gate"][0].ID != "gate1" {
		t.Errorf("the camera that did not answer lost its clips: %v", all)
	}
	if h := g.clipsHealth(); h.ListedAt == nil || h.Cameras != 2 || h.Clips != 2 || len(h.Failed) != 1 || h.Failed[0] != "camera.gate" {
		t.Errorf("health: %+v", h)
	}
}

func TestThumbnailsAreFetchedOnceAndOnlyClipsPictures(t *testing.T) {
	g := &Gateway{}
	fetched := 0
	g.clips.fetch = func(ctx context.Context, p string) ([]byte, string, error) {
		fetched++
		return []byte("jpg"), "image/jpeg", nil
	}
	ask := func(p string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		g.serveThumb(w, httptest.NewRequest("GET", "/atrium/thumb?p="+url.QueryEscape(p), nil))
		return w
	}
	for range 3 {
		if w := ask("/api/unifiprotect/thumbnail/nvr/e1?width=185"); w.Code != 200 || w.Body.String() != "jpg" || !strings.Contains(w.Header().Get("Cache-Control"), "max-age") {
			t.Fatalf("thumb: %d %q %v", w.Code, w.Body, w.Header())
		}
	}
	if fetched != 1 {
		t.Errorf("fetched %d times, want once", fetched)
	}
	for _, p := range []string{"/api/states", "/api/unifiprotect/thumbnail/../../states", "http://elsewhere/x", "",
		"/api/unifiprotect/thumbnail/%2e%2e/%2e%2e/states", "/api/unifiprotect/thumbnail/a/./b", "/api/unifiprotect/thumbnail//x", "/api/unifiprotect/thumbnail/n/e#x"} {
		if w := ask(p); w.Code != 400 {
			t.Errorf("%q answered %d, want 400", p, w.Code)
		}
	}
	if fetched != 1 {
		t.Errorf("a path that is not a clip's picture was fetched")
	}
}

func TestThumbnailsStayWithinTheirBudget(t *testing.T) {
	g := &Gateway{}
	g.clips.fetch = func(ctx context.Context, p string) ([]byte, string, error) {
		return make([]byte, 1<<20), "image/jpeg", nil
	}
	for i := range 40 {
		g.thumb(context.Background(), fmt.Sprint("/api/unifiprotect/thumbnail/n/", i))
	}
	if g.clips.bytes > thumbsBudget || len(g.clips.thumbs) != 32 {
		t.Errorf("kept %d bytes in %d thumbnails", g.clips.bytes, len(g.clips.thumbs))
	}
	if g.clips.thumbs["/api/unifiprotect/thumbnail/n/39"] == nil || g.clips.thumbs["/api/unifiprotect/thumbnail/n/0"] != nil {
		t.Error("the newest is kept and the oldest goes")
	}
}
