package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Local is the add-on's port on the Home Assistant machine, for the app at home or over a VPN that reaches the
// home network (WireGuard), without Tailscale. The caller shows its own Home Assistant access token, which Home
// Assistant itself checks. It gets what only the add-on has, the layout and the camera clips, and nothing else:
// its Home Assistant connection is its own, straight to Home Assistant, with that person's rights.
type Local struct {
	*Gateway
	tokens *tokenCheck
}

func NewLocal(g *Gateway) Local {
	return Local{g, &tokenCheck{ok: map[string]time.Time{}, failed: map[string][]time.Time{}}}
}

// localPath is all the port answers: what the app needs from the add-on when Home Assistant is reached directly.
func localPath(p string) bool {
	return p == "/atrium/hello" || p == "/atrium/house" || p == "/atrium/clips"
}

func (l Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "web pages may not use Atrium", http.StatusForbidden)
		return
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "none":
	default:
		http.Error(w, "web pages may not use Atrium", http.StatusForbidden)
		return
	}
	if !localPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	id := Identity{Name: "Home Assistant user", Device: "home network"}
	switch l.tokens.check(r.Context(), l.Core, peer, token) {
	case tokenGood:
		id.Login, id.Local = "home-assistant-user", true
	case tokenThrottled:
		http.Error(w, "too many refused tokens from this address; try again later", http.StatusTooManyRequests)
		return
	}
	l.serve(w, r, id, "")
}

type tokenVerdict int

const (
	tokenBad tokenVerdict = iota
	tokenGood
	tokenThrottled
)

// tokenCheck asks Home Assistant whether a token is good, and remembers the answer for a while. Home Assistant
// counts refused tokens against the address they come from (the add-on's, here) when its IP banning is on, so
// an address that keeps showing bad ones is not passed on for a while.
type tokenCheck struct {
	mu     sync.Mutex
	ok     map[string]time.Time   // token hash → good until
	failed map[string][]time.Time // caller address → recent refusals
}

func (t *tokenCheck) check(ctx context.Context, core interface{ String() string }, peer, token string) tokenVerdict {
	if token == "" || len(token) > 4096 || strings.Count(token, ".") != 2 { // Home Assistant's access tokens are JWTs
		return tokenBad
	}
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	now := time.Now()
	t.mu.Lock()
	if until, ok := t.ok[key]; ok && now.Before(until) {
		t.mu.Unlock()
		return tokenGood
	}
	recent := t.failed[peer][:0]
	for _, at := range t.failed[peer] {
		if now.Sub(at) < 10*time.Minute {
			recent = append(recent, at)
		}
	}
	t.failed[peer] = recent
	if len(recent) >= 3 {
		t.mu.Unlock()
		return tokenThrottled
	}
	t.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(core.String(), "/")+"/api/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tokenBad // Home Assistant did not answer: not the caller's fault, so not counted
	}
	resp.Body.Close()
	t.mu.Lock()
	defer t.mu.Unlock()
	if resp.StatusCode == http.StatusOK {
		for k, until := range t.ok {
			if now.After(until) {
				delete(t.ok, k)
			}
		}
		t.ok[key] = now.Add(5 * time.Minute)
		return tokenGood
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.failed[peer] = append(t.failed[peer], now)
	}
	return tokenBad
}
