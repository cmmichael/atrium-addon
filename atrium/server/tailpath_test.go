package main

import (
	"net/netip"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

func TestAPhonesTailnetPathIsNamed(t *testing.T) {
	direct, relayed := key.NewNode().Public(), key.NewNode().Public()
	st := &ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		direct:  {TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.2")}, CurAddr: "203.0.113.5:41641", Relay: "nyc"},
		relayed: {TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.96.139.56")}, Relay: "sfo"},
	}}
	for remote, want := range map[string]string{"100.64.0.2:5000": " (direct)", "100.96.139.56:60150": " (relayed via sfo)", "100.1.1.1:1": ""} {
		if got := peerPath(st, remote); got != want {
			t.Errorf("%s: %q, want %q", remote, got, want)
		}
	}
	var h handshakes
	h.Write([]byte("http: TLS handshake error from 100.96.139.56:60153: EOF\n"))
	h.Write([]byte("something else\n"))
	if h.died.Load() != 1 {
		t.Errorf("counted %d", h.died.Load())
	}
}
