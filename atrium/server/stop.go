package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The Supervisor stops an add-on with SIGTERM and kills it ten seconds later. Atrium uses the time to tell the apps
// connected now that it is going away (WebSocket close 1001, so they reconnect at once instead of waiting out a dead
// socket) and to close its Tailscale node, then exits 0.

// socketGrace is how long stop waits for the apps' WebSockets to close.
const socketGrace = 3 * time.Second

// stopping is closed when the add-on is told to stop.
func (g *Gateway) stopping() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.quit == nil {
		g.quit = make(chan struct{})
	}
	return g.quit
}

// onStop adds work for the end of stop.
func (g *Gateway) onStop(f func()) {
	g.mu.Lock()
	g.stopHooks = append(g.stopHooks, f)
	g.mu.Unlock()
}

func (g *Gateway) stopOnSignal() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		g.stop(s.String())
		os.Exit(0)
	}()
}

// stop closes the apps' WebSockets, waits for them up to socketGrace, then runs the onStop work.
func (g *Gateway) stop(why string) {
	g.stopping() // makes the channel if nobody has asked for it yet
	log.Printf("Stopping (%s): closing %d app connection(s)", why, g.open.Load())
	g.quitOnce.Do(func() { close(g.quit) })
	closed := make(chan struct{})
	go func() { g.sockets.Wait(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(socketGrace):
		log.Printf("Stopping: %d app connection(s) still open after %s; leaving them", g.open.Load(), socketGrace)
	}
	g.mu.Lock()
	hooks := g.stopHooks
	g.mu.Unlock()
	for _, f := range hooks {
		f()
	}
	log.Printf("Stopped")
}

// serveUntilStopped serves ln. Once the add-on is stopping, the listener closed under it (Tailscale's close) is
// not an error: the caller must not exit before stop has finished.
func (g *Gateway) serveUntilStopped(ln net.Listener, h http.Handler) error {
	err := http.Serve(ln, h)
	select {
	case <-g.stopping():
		return nil
	default:
		return err
	}
}
