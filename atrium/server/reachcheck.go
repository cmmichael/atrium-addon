package main

// Whether the add-on can be reached on the tailnet, tested rather than taken from Tailscale's state: "up" said
// nothing the night nothing from the phone arrived for 17 minutes. Every two minutes the add-on pings up to three
// online peers with Tailscale's own path test (disco); a peer that answers proves the way between them works both
// ways. Health reports the last check, and the log says when none answers and when one does again.

import (
	"context"
	"log"
	"net/netip"
	"sort"
	"sync"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

type pinger interface {
	Ping(ctx context.Context, ip netip.Addr, kind tailcfg.PingType) (*ipnstate.PingResult, error)
}

type reachResult struct {
	At          time.Time `json:"at"`
	PeersOnline int       `json:"peersOnline"` // online peers Tailscale knows of
	Tried       int       `json:"tried"`
	Answered    int       `json:"answered"`
	Error       string    `json:"error,omitempty"`
}

type reachState struct {
	mu   sync.Mutex
	last *reachResult
}

func checkReach(ctx context.Context, src statusSource, p pinger) reachResult {
	r := reachResult{At: time.Now()}
	st, err := src.Status(ctx)
	if err != nil || st == nil {
		r.Error = "Tailscale did not answer"
		return r
	}
	var peers []*ipnstate.PeerStatus
	for _, ps := range st.Peer {
		if ps != nil && ps.Online && len(ps.TailscaleIPs) > 0 {
			peers = append(peers, ps)
		}
	}
	r.PeersOnline = len(peers)
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].LastSeen.After(peers[j].LastSeen) || peers[i].HostName < peers[j].HostName
	})
	if len(peers) > 3 {
		peers = peers[:3]
	}
	r.Tried = len(peers)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ps := range peers {
		wg.Add(1)
		go func(ip netip.Addr) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if res, err := p.Ping(pctx, ip, tailcfg.PingDisco); err == nil && res != nil && res.Err == "" {
				mu.Lock()
				r.Answered++
				mu.Unlock()
			}
		}(ps.TailscaleIPs[0])
	}
	wg.Wait()
	return r
}

// unreachable: online peers were tried and none answered.
func (r reachResult) unreachable() bool { return r.Error == "" && r.Tried > 0 && r.Answered == 0 }

func (g *Gateway) watchReach(src statusSource, p pinger, first, every time.Duration) {
	time.Sleep(first)
	wasDown := false
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		r := checkReach(ctx, src, p)
		cancel()
		g.reach.mu.Lock()
		g.reach.last = &r
		g.reach.mu.Unlock()
		switch {
		case r.unreachable() && !wasDown:
			log.Printf("tailnet: none of %d online devices answered a ping; the apps may not reach Domiel (Tailscale says it is up)", r.Tried)
		case !r.unreachable() && wasDown && r.Answered > 0:
			log.Printf("tailnet: devices answer again (%d of %d)", r.Answered, r.Tried)
		}
		wasDown = r.unreachable()
		select {
		case <-g.stopping():
			return
		case <-time.After(every):
		}
	}
}
