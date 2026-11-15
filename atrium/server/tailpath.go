package main

// How a phone reaches the add-on over the tailnet right now: directly, or relayed through one of Tailscale's DERP
// servers. A relayed path from a phone on a mobile network can take seconds to complete a TLS handshake, and the
// apps' connection times say "slow" without saying why; the add-on's own log line for each app connection does.
// Handshakes the far side gave up on are counted for /atrium/health.

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"time"

	"tailscale.com/ipn/ipnstate"
)

type statusSource interface {
	Status(context.Context) (*ipnstate.Status, error)
}

// peerPath is " (direct)", " (relayed via nyc)" or "" when the address is not a tailnet peer's.
func peerPath(st *ipnstate.Status, remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || st == nil {
		return ""
	}
	for _, p := range st.Peer {
		for _, a := range p.TailscaleIPs {
			if a != ip {
				continue
			}
			switch {
			case p.CurAddr != "":
				return " (direct)"
			case p.Relay != "":
				return " (relayed via " + p.Relay + ")"
			default:
				return " (path not yet known)"
			}
		}
	}
	return ""
}

func (g *Gateway) pathOf(remote string) string {
	if g.paths == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	st, err := g.paths.Status(ctx)
	if err != nil {
		return ""
	}
	return peerPath(st, remote)
}

// handshakes counts the TLS handshakes a caller gave up on (Go's server logs "TLS handshake error … EOF"), and
// passes every line on to the add-on's log.
type handshakes struct {
	died atomic.Int64
	last atomic.Int64 // unix seconds
}

func (h *handshakes) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("TLS handshake error")) {
		h.died.Add(1)
		h.last.Store(time.Now().Unix())
	}
	return os.Stderr.Write(p)
}
