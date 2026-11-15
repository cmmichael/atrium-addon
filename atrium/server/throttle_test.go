package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Bad tokens sent all at once from one address: no more than three reach Home Assistant, the rest are throttled.
// A good token afterwards from another address is not held up, and Home Assistant not answering counts nothing.
func TestParallelBadTokensCannotPassTheThrottle(t *testing.T) {
	var asked atomic.Int32
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		time.Sleep(50 * time.Millisecond)
		if r.Header.Get("Authorization") == "Bearer good.to.ken" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(401)
	}))
	defer core.Close()
	base, _ := url.Parse(core.URL)
	tc := &tokenCheck{ok: map[string]time.Time{}, failed: map[string][]time.Time{}}
	var wg sync.WaitGroup
	var throttled atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tc.check(context.Background(), base, "10.0.0.9", "bad.to.ken") == tokenThrottled {
				throttled.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := asked.Load(); n > 3 {
		t.Errorf("%d guesses reached Home Assistant, want at most 3", n)
	}
	if throttled.Load() < 17 {
		t.Errorf("only %d of 20 were throttled", throttled.Load())
	}
	if v := tc.check(context.Background(), base, "10.0.0.10", "good.to.ken"); v != tokenGood {
		t.Errorf("a good token from another address: %v", v)
	}
	if len(tc.failed["10.0.0.10"]) != 0 {
		t.Errorf("a good token left a failure behind: %v", tc.failed["10.0.0.10"])
	}
	core.Close() // Home Assistant gone: not the caller's fault
	if v := tc.check(context.Background(), base, "10.0.0.11", "some.to.ken"); v != tokenBad || len(tc.failed["10.0.0.11"]) != 0 {
		t.Errorf("Home Assistant not answering: %v, failures %v", v, tc.failed["10.0.0.11"])
	}
}
