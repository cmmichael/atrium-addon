package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

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
}

func TestOnlyHandshakesNotRacedByACompletedOneAreCounted(t *testing.T) {
	h := handshakes{window: 50 * time.Millisecond}
	h.Write([]byte("something else\n"))
	// A phone gives up on one connection; nothing from it completes.
	h.hello("100.96.139.56:60153", "atrium.example.ts.net")
	h.Write([]byte("http: TLS handshake error from 100.96.139.56:60153: EOF\n"))
	// Another phone races two connections: the loser is dropped just after the winner completed...
	h.hello("100.64.0.2:5000", "atrium.example.ts.net")
	h.hello("100.64.0.2:5001", "atrium.example.ts.net")
	h.completed("100.64.0.2:5000")
	h.Write([]byte("http: TLS handshake error from 100.64.0.2:5001: EOF\n"))
	// ...and a third drops the loser just before the winner completes.
	h.hello("100.64.0.3:7000", "")
	h.Write([]byte("http: TLS handshake error from 100.64.0.3:7000: EOF\n"))
	h.completed("100.64.0.3:7001")
	time.Sleep(150 * time.Millisecond)
	if h.died.Load() != 1 {
		t.Errorf("counted %d, want only the phone that never connected", h.died.Load())
	}
	if len(h.hellos) != 0 {
		t.Errorf("hellos kept: %v", h.hellos)
	}
}

func TestARealHandshakeIsSeenWithItsName(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.StartTLS() // only for its certificate
	cert := srv.TLS.Certificates[0]
	srv.Close()
	var h handshakes
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	tl := tls.NewListener(ln, h.config(func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }))
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, ServerName: "atrium.example.ts.net"})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	for i := 0; i < 50; i++ {
		h.mu.Lock()
		_, ok := h.done["127.0.0.1"]
		n := len(h.hellos)
		h.mu.Unlock()
		if ok && n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("completed handshake not seen: %v %v", h.done, h.hellos)
}
