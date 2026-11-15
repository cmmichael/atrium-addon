package main

// How a phone reaches the add-on over the tailnet right now: directly, or relayed through one of Tailscale's DERP
// servers. A relayed path from a phone on a mobile network can take seconds to complete a TLS handshake, and the
// apps' connection times say "slow" without saying why; the add-on's own log line for each app connection does.
// Handshakes the far side gave up on are counted for /atrium/health.

import (
	"bytes"
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/netip"
	"os"
	"regexp"
	"sync"
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
	g.mu.Lock()
	paths := g.paths
	g.mu.Unlock()
	if paths == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	st, err := paths.Status(ctx)
	if err != nil {
		return ""
	}
	return peerPath(st, remote)
}

// handshakes counts the TLS handshakes a caller gave up on (Go's server logs "TLS handshake error … EOF"), and
// passes every line on to the add-on's log with the name the caller asked for (SNI). A phone often opens two
// connections at once and drops the one that loses: a handshake given up on within raceWindow of a completed one
// from the same address was raced, not failed, and is not counted.
type handshakes struct {
	died atomic.Int64
	last atomic.Int64 // unix seconds

	mu     sync.Mutex
	hellos map[string]hello     // by remote address, until the handshake ends
	done   map[string]time.Time // by remote IP: the last completed handshake
	window time.Duration        // raceWindow; tests shorten it
}

const raceWindow = 3 * time.Second

type hello struct {
	sni string
	at  time.Time
}

// config is the listener's TLS configuration with each handshake's start and completion recorded.
func (h *handshakes) config(cert func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{GetCertificate: cert, GetConfigForClient: func(ch *tls.ClientHelloInfo) (*tls.Config, error) {
		if ch.Conn == nil || ch.Conn.RemoteAddr() == nil {
			return nil, nil
		}
		remote := ch.Conn.RemoteAddr().String()
		h.hello(remote, ch.ServerName)
		return &tls.Config{GetCertificate: cert, VerifyConnection: func(tls.ConnectionState) error {
			h.completed(remote)
			return nil
		}}, nil
	}}
}

func (h *handshakes) hello(remote, sni string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hellos == nil {
		h.hellos, h.done = map[string]hello{}, map[string]time.Time{}
	}
	now := time.Now()
	for a, x := range h.hellos { // handshakes that never ended either way
		if now.Sub(x.at) > time.Minute {
			delete(h.hellos, a)
		}
	}
	h.hellos[remote] = hello{sni, now}
}

func (h *handshakes) completed(remote string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done == nil {
		h.hellos, h.done = map[string]hello{}, map[string]time.Time{}
	}
	delete(h.hellos, remote)
	now := time.Now()
	for ip, t := range h.done {
		if now.Sub(t) > time.Minute {
			delete(h.done, ip)
		}
	}
	h.done[ipOf(remote)] = now
}

func (h *handshakes) racewin() time.Duration {
	if h.window > 0 {
		return h.window
	}
	return raceWindow
}

// raced reports whether ip completed a handshake within the race window of at.
func (h *handshakes) raced(ip string, at time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.done[ip]
	if !ok {
		return false
	}
	d := t.Sub(at)
	if d < 0 {
		d = -d
	}
	return d <= h.racewin()
}

func (h *handshakes) Write(p []byte) (int, error) {
	m := handshakeErr.FindSubmatch(p)
	if m == nil {
		return os.Stderr.Write(p)
	}
	remote := string(m[1])
	h.mu.Lock()
	x, ok := h.hellos[remote]
	delete(h.hellos, remote)
	h.mu.Unlock()
	line := bytes.TrimRight(p, "\n")
	if ok && x.sni != "" {
		line = append(append([]byte{}, line...), " (asked for "+x.sni+")"...)
	} else if !ok {
		line = append(append([]byte{}, line...), " (no hello)"...)
	}
	at, ip := time.Now(), ipOf(remote)
	if h.raced(ip, at) {
		os.Stderr.Write(append(line, ", raced with a completed handshake, not counted\n"...))
		return len(p), nil
	}
	os.Stderr.Write(append(line, '\n'))
	time.AfterFunc(h.racewin(), func() { // a connection racing this one may still complete
		if h.raced(ip, at) {
			log.Printf("TLS handshake from %s was raced with a completed one, not counted", remote)
			return
		}
		h.died.Add(1)
		h.last.Store(at.Unix())
	})
	return len(p), nil
}

var handshakeErr = regexp.MustCompile(`TLS handshake error from (\S+?):? `)

func ipOf(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}
