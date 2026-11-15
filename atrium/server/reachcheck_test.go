package main

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type fakeStatus struct{ st *ipnstate.Status }

func (f fakeStatus) Status(context.Context) (*ipnstate.Status, error) { return f.st, nil }

type fakePinger map[string]bool // address → answers

func (f fakePinger) Ping(_ context.Context, ip netip.Addr, _ tailcfg.PingType) (*ipnstate.PingResult, error) {
	if f[ip.String()] {
		return &ipnstate.PingResult{IP: ip.String()}, nil
	}
	return &ipnstate.PingResult{IP: ip.String(), Err: "timeout"}, errors.New("timeout")
}

func peer(ip string, online bool) *ipnstate.PeerStatus {
	return &ipnstate.PeerStatus{Online: online, TailscaleIPs: []netip.Addr{netip.MustParseAddr(ip)}, HostName: ip, LastSeen: time.Now()}
}

// The check pings online devices only, at most three, and says unreachable only when some were tried and none
// answered: no device online is not an outage of the add-on.
func TestTheReachCheckPingsOnlineDevices(t *testing.T) {
	st := &ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): peer("100.64.0.1", true),
		key.NewNode().Public(): peer("100.64.0.2", true),
		key.NewNode().Public(): peer("100.64.0.3", false),
	}}
	r := checkReach(context.Background(), fakeStatus{st}, fakePinger{"100.64.0.2": true})
	if r.PeersOnline != 2 || r.Tried != 2 || r.Answered != 1 || r.unreachable() {
		t.Errorf("one of two answers: %+v", r)
	}
	r = checkReach(context.Background(), fakeStatus{st}, fakePinger{})
	if !r.unreachable() {
		t.Errorf("none answers: %+v", r)
	}
	for i := 0; i < 5; i++ {
		st.Peer[key.NewNode().Public()] = peer(netip.AddrFrom4([4]byte{100, 64, 1, byte(i)}).String(), true)
	}
	if r = checkReach(context.Background(), fakeStatus{st}, fakePinger{}); r.Tried != 3 || r.PeersOnline != 7 {
		t.Errorf("tried %d of %d online, want 3 of 7", r.Tried, r.PeersOnline)
	}
	r = checkReach(context.Background(), fakeStatus{&ipnstate.Status{}}, fakePinger{})
	if r.unreachable() || r.Tried != 0 {
		t.Errorf("no device online: %+v", r)
	}
}
