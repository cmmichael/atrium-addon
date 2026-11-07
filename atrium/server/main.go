// The Atrium add-on: puts Home Assistant's API and the Atrium web app on your tailnet, lets Tailscale say
// who is calling, and builds the house the apps show.
//
// It joins the tailnet as its own machine (tsnet: no Tailscale needed on the host), asks Tailscale who
// each connection belongs to (WhoIs), and, for a login that is allowed, forwards the request to Home
// Assistant with a credential that never leaves this add-on: by default the add-on's own access, or a
// long-lived token of the person's own Home Assistant user when one is configured. The app holds no
// password and no token.
//
// Only Home Assistant's API (/api/…, including the WebSocket) and its /local/ files are forwarded.
//
// Tailscale identifies the device, not what is running on it, so a web page open in a browser on an
// allowed person's device must not be able to use the gateway: requests that a browser marks as coming
// from a page (an Origin header, or Sec-Fetch-Site other than "none") are refused, and so is any request
// for a name that is not this machine's (DNS rebinding). Native apps send neither.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cmmichael/atrium/addon/atrium/server/house"
	"github.com/coder/websocket"
	"golang.org/x/sync/singleflight"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

const version = "0.5.0"

// Person is a Tailscale login that may use the gateway, optionally as a Home Assistant user of its own.
type Person struct {
	Login string `json:"login"`
	Token string `json:"token"` // a long-lived access token of that person's Home Assistant user; empty: the add-on's own access
}

// Options are the add-on's settings (/data/options.json).
type Options struct {
	Hostname string   `json:"hostname"`
	AuthKey  string   `json:"auth_key"`
	Allow    []string `json:"allow"`
	People   []Person `json:"people"`
	Admins   []string `json:"admins"` // may change the layout for everyone, besides the login that signed the machine in
}

// Identity is what Tailscale says about the other end of a connection.
type Identity struct {
	Login  string // "someone@example.com"
	Name   string
	Device string
	Tagged bool // a tagged machine has no person behind it
	Owner  bool // the person who signed this machine in to the tailnet, by Tailscale user id
	// Through Home Assistant's ingress: a signed-in Home Assistant user rather than a Tailscale login.
	HomeAssistant bool
	UserID        string
}

// Upstream is one way into Home Assistant.
type Upstream struct {
	Base  *url.URL // REST: Base + the request path
	WS    string   // the WebSocket API
	Token string
	As    string // "home-assistant" (the add-on's access) or "user"
}

type Gateway struct {
	Who   func(ctx context.Context, remoteAddr string) (Identity, error)
	Owner string // the login that signed this machine in to the tailnet: always allowed
	Opts  Options
	Addon Upstream // through the Supervisor, as the add-on
	Core  *url.URL // Home Assistant itself: /local/ files, and people with a token of their own
	Names []string // this machine's names and addresses: the only Hosts answered

	ConfigPath  string   // where the owner's preferences are kept ("" = not saved)
	MappingPath string   // the declared layout, atrium.yaml in Home Assistant's configuration folder
	WebDir      string   // the built web app to serve ("" = none)
	Supervisor  []string // the addresses the Supervisor's ingress proxy connects from: the only peers the ingress listener believes

	mu         sync.Mutex
	home       string
	config     house.Config
	mapping    house.Mapping
	mappingErr string    // why the file was refused, if it was (the last good mapping stays in use)
	mappingAt  time.Time // the file's modification time last read
	problems   []string  // what the last render could not use
	rendered   bool      // there has been a render (the first one is logged whatever it found)
	generation int       // bumped on every configuration or mapping change
	house      built
	building   singleflight.Group
	admins     map[string]bool // Home Assistant user id → is an administrator
	adminsAt   time.Time
	setup      Setup
}

// Setup is where the add-on is in joining the tailnet, for the panel to walk the owner through it: it is
// reachable through Home Assistant's sidebar before Tailscale is signed in, so the sign-in link can be a button
// there instead of a line in the log.
type Setup struct {
	State   string `json:"state"`             // "starting", "needs-login", "up"
	AuthURL string `json:"authURL,omitempty"` // the Tailscale sign-in link, while needs-login
	Address string `json:"address,omitempty"` // https://atrium.<tailnet>.ts.net (or http://…), once up
	Owner   string `json:"owner,omitempty"`   // the login that signed the machine in, once known
	Layout  Layout `json:"layout"`
}

// Layout is what the set-up page says about the mapping: is there one, and what in it could not be used.
type Layout struct {
	Path     string   `json:"path"`               // where atrium.yaml is read from, as Home Assistant sees it
	Exists   bool     `json:"exists"`             // there is a file
	Rooms    int      `json:"rooms"`              // rooms it names
	Error    string   `json:"error,omitempty"`    // the file was refused (bad YAML, a room without an id…)
	Problems []string `json:"problems,omitempty"` // what the last render dropped
}

// isMine says whether a request's Host names this machine (with or without a port).
func (g *Gateway) isMine(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	g.mu.Lock()
	names := g.Names
	g.mu.Unlock()
	for _, n := range names {
		if host == strings.ToLower(n) {
			return true
		}
	}
	return false
}

// foreignPage says whether a browser sent this on behalf of a page that is not the add-on's own web app.
// Native apps send neither header; the add-on's own pages send its own name as Origin and same-origin.
func (g *Gateway) foreignPage(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !g.isMine(u.Host) {
			return true
		}
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "none", "same-origin":
		return false
	}
	return true // cross-site, or same-site: another machine on the tailnet's domain
}

func (g *Gateway) allowed(id Identity) bool {
	if id.Tagged || id.Login == "" {
		return false
	}
	if id.HomeAssistant {
		// Through ingress everyone gets the add-on's own (administrator) access, so only Home Assistant's
		// administrators are allowed, whatever the panel's own setting says.
		return g.isHAAdmin(id.UserID)
	}
	if id.Owner {
		return true
	}
	login := strings.ToLower(id.Login)
	if g.Owner != "" && login == strings.ToLower(g.Owner) {
		return true
	}
	for _, a := range append(append([]string{}, g.Opts.Allow...), g.Opts.Admins...) {
		if login == strings.ToLower(strings.TrimSpace(a)) {
			return true
		}
	}
	for _, p := range g.Opts.People {
		if login == strings.ToLower(strings.TrimSpace(p.Login)) {
			return true
		}
	}
	return false
}

func (g *Gateway) upstreamFor(id Identity) Upstream {
	for _, p := range g.Opts.People {
		if p.Token != "" && strings.EqualFold(strings.TrimSpace(p.Login), id.Login) {
			ws := *g.Core
			ws.Scheme = map[string]string{"http": "ws", "https": "wss"}[g.Core.Scheme]
			ws.Path = "/api/websocket"
			return Upstream{Base: g.Core, WS: ws.String(), Token: p.Token, As: "user"}
		}
	}
	return g.Addon
}

// ServeHTTP is the tailnet side: Tailscale says who is calling.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.isMine(r.Host) {
		http.Error(w, "not this machine's name", http.StatusMisdirectedRequest)
		return
	}
	if g.foreignPage(r) {
		http.Error(w, "web pages may not use Atrium", http.StatusForbidden)
		return
	}
	id, err := g.Who(r.Context(), r.RemoteAddr)
	if err != nil {
		http.Error(w, "Tailscale does not know this connection", http.StatusForbidden)
		return
	}
	g.serve(w, r, id, "")
}

// Ingress is the Home Assistant side: the Supervisor proxies the sidebar panel here and says which Home
// Assistant user is signed in. The listener is reachable by every container on the add-on network, so only
// the Supervisor's own address is believed; anyone else could claim to be anyone.
type Ingress struct{ *Gateway }

func (in Ingress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !in.fromSupervisor(peer) {
		log.Printf("ingress request from %s, which is not the Supervisor: refused", r.RemoteAddr)
		http.Error(w, "only the Supervisor may use this port", http.StatusForbidden)
		return
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "none", "same-origin": // the panel's own requests; the Supervisor checks the session
	default:
		http.Error(w, "web pages may not use the Atrium panel", http.StatusForbidden)
		return
	}
	user := r.Header.Get("X-Remote-User-Name")
	if user == "" {
		http.Error(w, "no Home Assistant user", http.StatusForbidden)
		return
	}
	id := Identity{Login: strings.ToLower(user), Name: firstNonEmpty(r.Header.Get("X-Remote-User-Display-Name"), user), Device: "Home Assistant", HomeAssistant: true, UserID: r.Header.Get("X-Remote-User-Id")}
	in.serve(w, r, id, strings.TrimSuffix(r.Header.Get("X-Ingress-Path"), "/"))
}

func (g *Gateway) fromSupervisor(ip string) bool {
	for _, s := range g.Supervisor {
		if ip == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// serve answers one request from an identified caller. base is the path the web app is mounted at ("" on the
// tailnet, the ingress path through Home Assistant).
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request, id Identity, base string) {
	ok := g.allowed(id)

	if r.URL.Path == "/atrium/hello" {
		// Who the app is to this house. Answered to anyone on the tailnet, so the app can say why not.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"atrium": 1, "version": version, "home": g.homeName(r.Context()),
			"login": id.Login, "name": id.Name, "device": id.Device, "allowed": ok, "as": g.upstreamFor(id).As, "configures": ok && g.mayConfigure(id),
		})
		return
	}
	if !ok {
		log.Printf("refused %s (%s) %s %s", id.Login, id.Device, r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		message := "This Tailscale login is not allowed by Atrium"
		if id.HomeAssistant {
			message = "Only Home Assistant administrators may use the Atrium panel"
		}
		json.NewEncoder(w).Encode(map[string]any{"message": message, "login": id.Login})
		return
	}
	if clean := path.Clean(r.URL.Path); clean != r.URL.Path && clean+"/" != r.URL.Path {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	up := g.upstreamFor(id)
	switch {
	case r.URL.Path == "/atrium/setup":
		g.mu.Lock()
		setup := g.setup
		setup.Owner = g.Owner
		setup.Layout = Layout{Path: "/config/atrium.yaml", Exists: !g.mappingAt.IsZero(), Rooms: len(g.mapping.Rooms), Error: g.mappingErr, Problems: g.problems}
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(setup)
	case r.URL.Path == "/atrium/house":
		g.serveHouse(w, r)
	case r.URL.Path == "/atrium/config":
		g.serveConfig(w, r, id)
	case r.URL.Path == "/api/websocket":
		log.Printf("%s (%s) connected", id.Login, id.Device)
		g.serveWebSocket(w, r, id, up)
	case strings.HasPrefix(r.URL.Path, "/api/hassio"): // /api/hassio/…, /api/hassio_ingress/…, /api/hassio_app/…
		http.Error(w, "the Supervisor is not reachable through Atrium", http.StatusForbidden) // the app has no use for it
	case strings.HasPrefix(r.URL.Path, "/api/"):
		forward(w, r, up.Base, up.Token)
	case strings.HasPrefix(r.URL.Path, "/local/"):
		forward(w, r, g.Core, "")
	case g.WebDir != "" && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/atrium/"):
		page := "index.html"
		if id.HomeAssistant && fileExists(filepath.Join(g.WebDir, "setup.html")) {
			page = "setup.html" // in Home Assistant's sidebar: the set-up page, not the app (that lives on the tailnet)
		}
		g.serveWeb(w, r, base, page)
	default:
		http.NotFound(w, r)
	}
}

// serveWeb serves the built web app: files as they are, and index.html for everything else (its routes are
// client-side), with <base href> set to where the app is mounted, so the same build works on the tailnet and
// through ingress.
func (g *Gateway) serveWeb(w http.ResponseWriter, r *http.Request, base, page string) {
	name := path.Clean("/" + r.URL.Path)
	if name != "/" {
		if f, err := os.Stat(filepath.Join(g.WebDir, filepath.FromSlash(name))); err == nil && !f.IsDir() {
			http.ServeFile(w, r, filepath.Join(g.WebDir, filepath.FromSlash(name)))
			return
		}
	}
	index, err := os.ReadFile(filepath.Join(g.WebDir, page))
	if err != nil {
		http.Error(w, "the web app is not installed", http.StatusNotFound)
		return
	}
	html := strings.Replace(string(index), "<head>", `<head><base href="`+base+`/">`, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(html))
}

// forward sends one HTTP request on to Home Assistant, with the credential set here and nothing of the caller's.
func forward(w http.ResponseWriter, r *http.Request, base *url.URL, token string) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = base.Scheme, base.Host
			pr.Out.URL.Path = strings.TrimSuffix(base.Path, "/") + pr.In.URL.Path
			pr.Out.URL.RawPath = ""
			pr.Out.Host = base.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			if token != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+token)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return // the caller gave up (an app closing a snapshot, say); Home Assistant is fine
			}
			log.Printf("Home Assistant did not answer: %v", err)
			http.Error(w, "Home Assistant did not answer", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// serveWebSocket joins the caller's WebSocket to Home Assistant's. Home Assistant opens by asking for a
// token; the caller answers with a placeholder, which is replaced here by the real one.
func (g *Gateway) serveWebSocket(w http.ResponseWriter, r *http.Request, id Identity, up Upstream) {
	// On the tailnet an Origin must match the Host (foreignPage has already refused other sites' pages). Through
	// ingress the Host is the Supervisor's view of the add-on and the Origin is Home Assistant's, so that check
	// cannot hold; the ingress listener has already made sure the peer is the Supervisor and the page its own.
	var opts *websocket.AcceptOptions
	if id.HomeAssistant {
		opts = &websocket.AcceptOptions{InsecureSkipVerify: true}
	}
	client, err := websocket.Accept(w, r, opts)
	if err != nil {
		log.Printf("%s (%s): WebSocket refused: %v", id.Login, id.Device, err)
		return
	}
	defer client.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	server, _, err := websocket.Dial(ctx, up.WS, nil)
	if err != nil {
		log.Printf("Home Assistant's WebSocket did not answer: %v", err)
		client.Close(websocket.StatusBadGateway, "Home Assistant did not answer")
		return
	}
	defer server.CloseNow()
	client.SetReadLimit(-1)
	server.SetReadLimit(-1) // the registries are megabytes

	done := make(chan struct{}, 2)
	go func() { // caller → Home Assistant
		defer func() { done <- struct{}{} }()
		signedIn := false
		for {
			kind, data, err := client.Read(ctx)
			if err != nil {
				return
			}
			if !signedIn && kind == websocket.MessageText {
				if with, ok := withToken(data, up.Token); ok {
					data, signedIn = with, true
				}
			}
			if server.Write(ctx, kind, data) != nil {
				return
			}
		}
	}()
	go func() { // Home Assistant → caller
		defer func() { done <- struct{}{} }()
		for {
			kind, data, err := server.Read(ctx)
			if err != nil {
				return
			}
			if client.Write(ctx, kind, data) != nil {
				return
			}
		}
	}()
	<-done
	client.Close(websocket.StatusNormalClosure, "")
	server.Close(websocket.StatusNormalClosure, "")
}

// withToken puts the real token into a Home Assistant "auth" message.
func withToken(message []byte, token string) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(message, &m) != nil || m["type"] != "auth" {
		return nil, false
	}
	m["access_token"] = token
	out, err := json.Marshal(m)
	return out, err == nil
}

// homeName is what Home Assistant calls the house, asked once.
func (g *Gateway) homeName(ctx context.Context) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.home != "" {
		return g.home
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(g.Addon.Base.String(), "/")+"/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+g.Addon.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "Home"
	}
	defer res.Body.Close()
	var cfg struct {
		Name string `json:"location_name"`
	}
	if json.NewDecoder(res.Body).Decode(&cfg) == nil && cfg.Name != "" {
		g.home = cfg.Name
		return g.home
	}
	return "Home"
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fileExists(p string) bool {
	f, err := os.Stat(p)
	return err == nil && !f.IsDir()
}

// readMapping reads atrium.yaml when it changed (or first), keeps the last good one when the new one is refused,
// and bumps the generation so the next house.json is rendered from it. Returns whether anything changed.
func (g *Gateway) readMapping() bool {
	if g.MappingPath == "" {
		return false
	}
	info, err := os.Stat(g.MappingPath)
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case errors.Is(err, os.ErrNotExist):
		if g.mappingAt.IsZero() && g.mapping.Empty() && g.mappingErr == "" {
			return false
		}
		g.mapping, g.mappingErr, g.mappingAt = house.Mapping{}, "", time.Time{}
		g.generation++
		g.house = built{} // rebuild on the next request
		log.Printf("mapping: %s is gone; the house is empty until there is one", g.MappingPath)
		return true
	case err != nil:
		return false
	case info.ModTime().Equal(g.mappingAt):
		return false
	}
	g.mappingAt = info.ModTime()
	g.generation++
	g.house = built{} // rebuild on the next request
	m, err := house.LoadMapping(g.MappingPath)
	if err != nil {
		g.mappingErr = err.Error()
		log.Printf("mapping: %s refused: %v (the previous layout stays)", g.MappingPath, err)
		return true
	}
	g.mapping, g.mappingErr = m, ""
	log.Printf("mapping: read %s: %d rooms", g.MappingPath, len(m.Rooms))
	return true
}

// watchMapping re-reads the file whenever it changes. Home Assistant's configuration folder is a plain mount, so
// the modification time is looked at every few seconds rather than watched.
func (g *Gateway) watchMapping() {
	for {
		time.Sleep(3 * time.Second)
		g.readMapping()
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		log.Fatalf("bad address %q", s)
	}
	return u
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	var opts Options
	if data, err := os.ReadFile(env("ATRIUM_OPTIONS", "/data/options.json")); err == nil {
		if err := json.Unmarshal(data, &opts); err != nil {
			log.Fatalf("options: %v", err)
		}
	}
	if opts.Hostname == "" {
		opts.Hostname = "atrium"
	}
	g := &Gateway{Opts: opts, ConfigPath: env("ATRIUM_CONFIG", "/data/config.json")}
	if cfg, err := house.Load(g.ConfigPath); err != nil {
		log.Fatalf("configuration %s: %v", g.ConfigPath, err)
	} else {
		g.config = cfg
	}

	// For developing the app: no Tailscale, a made-up identity, and any Home Assistant as upstream. Never set in the add-on.
	if login := os.Getenv("ATRIUM_DEV_LOGIN"); login != "" {
		up := mustURL(env("ATRIUM_UPSTREAM", "http://127.0.0.1:8124"))
		ws := *up
		ws.Scheme, ws.Path = "ws", "/api/websocket"
		g.Addon = Upstream{Base: up, WS: ws.String(), Token: env("ATRIUM_UPSTREAM_TOKEN", "dev"), As: "home-assistant"}
		g.Core = up
		g.Owner = env("ATRIUM_DEV_OWNER", login)
		g.Who = func(context.Context, string) (Identity, error) {
			return Identity{Login: login, Name: "Developer", Device: "dev"}, nil
		}
		listen := env("ATRIUM_LISTEN", "127.0.0.1:8125")
		if os.Getenv("ATRIUM_CONFIG") == "" {
			g.ConfigPath = os.TempDir() + "/atrium-config.json"
			g.config, _ = house.Load(g.ConfigPath)
		}
		g.Names = []string{"localhost", "127.0.0.1", "::1"}
		if host, _, err := net.SplitHostPort(listen); err == nil && host != "" {
			g.Names = append(g.Names, host)
		}
		g.WebDir = env("ATRIUM_WEB", "")
		g.MappingPath = env("ATRIUM_MAPPING", "")
		g.readMapping()
		go g.watchMapping()
		switch os.Getenv("ATRIUM_DEV_SETUP") { // lets the panel's set-up card be seen without a tailnet
		case "needs-login":
			g.setup = Setup{State: "needs-login", AuthURL: "https://login.tailscale.com/a/0123456789abcd"}
		case "starting":
			g.setup = Setup{State: "starting"}
		default:
			g.setup = Setup{State: "up", Address: "http://" + listen}
		}
		if ingress := os.Getenv("ATRIUM_INGRESS"); ingress != "" { // e.g. 127.0.0.1:8099: pretends to be the Supervisor's ingress
			g.Supervisor = []string{"127.0.0.1", "::1"}
			go func() { log.Fatal(http.ListenAndServe(ingress, Ingress{g})) }()
		}
		log.Printf("DEVELOPMENT MODE: no Tailscale; every caller is %s. Listening on http://%s, upstream %s, web %q", login, listen, up, g.WebDir)
		log.Fatal(http.ListenAndServe(listen, g))
	}

	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		log.Fatal("no SUPERVISOR_TOKEN: this runs as a Home Assistant add-on (homeassistant_api: true)")
	}
	g.Addon = Upstream{Base: mustURL("http://supervisor/core"), WS: "ws://supervisor/core/websocket", Token: token, As: "home-assistant"}
	g.Core = mustURL(env("ATRIUM_CORE", "http://homeassistant:8123"))
	if web := env("ATRIUM_WEB", "/app/web"); fileExists(filepath.Join(web, "index.html")) {
		g.WebDir = web
	}
	// The declared layout lives in Home Assistant's configuration folder (mapped read-only at /homeassistant).
	g.MappingPath = env("ATRIUM_MAPPING", "/homeassistant/atrium.yaml")
	if !g.readMapping() {
		log.Printf("mapping: no %s yet; the house is empty until the atrium-mapping skill (or you) writes one", g.MappingPath)
	}
	go g.watchMapping()

	// The Home Assistant side: the sidebar panel, through the Supervisor's ingress. Only the Supervisor's own
	// address is believed on that port (resolved once; 172.30.32.2 is its usual address).
	if addrs, err := net.LookupHost("supervisor"); err == nil && len(addrs) > 0 {
		g.Supervisor = addrs
	} else {
		g.Supervisor = []string{"172.30.32.2"}
		log.Printf("could not resolve \"supervisor\" (%v); believing only %v on the ingress port", err, g.Supervisor)
	}
	go func() {
		log.Printf("Panel listening for ingress on :%s, from the Supervisor at %v only", env("ATRIUM_INGRESS_PORT", "8099"), g.Supervisor)
		log.Fatal(http.ListenAndServe(":"+env("ATRIUM_INGRESS_PORT", "8099"), Ingress{g}))
	}()

	srv := &tsnet.Server{Hostname: opts.Hostname, Dir: env("ATRIUM_STATE", "/data/tsnet"), AuthKey: opts.AuthKey, UserLogf: log.Printf, Logf: func(string, ...any) {}}
	defer srv.Close()
	log.Printf("Atrium %s: joining the tailnet as %q. The first time, sign in from the Atrium panel in Home Assistant (or open the link below once).", version, opts.Hostname)
	g.setup = Setup{State: "starting"}
	if err := srv.Start(); err != nil {
		log.Fatalf("Tailscale: %v", err)
	}
	lc, err := srv.LocalClient()
	if err != nil {
		log.Fatalf("Tailscale: %v", err)
	}
	// While Up waits for the sign-in, the panel shows the link: the state is read every second until it is Running.
	joined := make(chan struct{})
	go func() {
		for {
			select {
			case <-joined:
				return
			case <-time.After(time.Second):
			}
			st, err := lc.Status(context.Background())
			if err != nil || st == nil {
				continue
			}
			g.mu.Lock()
			if st.BackendState == "NeedsLogin" && st.AuthURL != "" {
				g.setup = Setup{State: "needs-login", AuthURL: st.AuthURL}
			} else if st.BackendState != "Running" && g.setup.State != "needs-login" {
				g.setup = Setup{State: "starting"}
			}
			g.mu.Unlock()
		}
	}()
	status, err := srv.Up(context.Background())
	close(joined)
	if err != nil {
		log.Fatalf("Tailscale: %v", err)
	}
	// Who owns this machine, and what it is called, come from Tailscale's status. Right after Up the status can
	// still be missing its user map or DNS name, so both are taken again until they are known.
	var selfUser tailcfg.UserID
	var scheme, dnsName string // for the set-up card's address: the scheme once the listeners are up, the name as last learnt
	learn := func(st *ipnstate.Status) bool {
		if st == nil || st.Self == nil {
			return false
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if !st.Self.IsTagged() && st.Self.UserID != 0 {
			selfUser = st.Self.UserID
			if owner, ok := st.User[st.Self.UserID]; ok && owner.LoginName != "" {
				g.Owner = owner.LoginName
			}
		}
		names := []string{opts.Hostname}
		if dns := strings.TrimSuffix(st.Self.DNSName, "."); dns != "" {
			names = append(names, dns)
			dnsName = dns
			if scheme != "" { // the card's address follows the name, which can arrive after the listeners are up
				g.setup.Address = scheme + "://" + dns
			}
		}
		for _, ip := range st.TailscaleIPs {
			names = append(names, ip.String())
		}
		g.Names = names
		return st.Self.IsTagged() || (g.Owner != "" && st.Self.DNSName != "")
	}
	known := learn(status)
	g.Who = func(ctx context.Context, remoteAddr string) (Identity, error) {
		who, err := lc.WhoIs(ctx, remoteAddr)
		if err != nil {
			return Identity{}, err
		}
		if who.Node == nil || who.UserProfile == nil {
			return Identity{}, errors.New("no identity")
		}
		g.mu.Lock()
		owner := selfUser != 0 && who.UserProfile.ID == selfUser
		g.mu.Unlock()
		return Identity{Login: who.UserProfile.LoginName, Name: who.UserProfile.DisplayName, Device: who.Node.ComputedName, Tagged: who.Node.IsTagged(), Owner: owner}, nil
	}

	name := opts.Hostname
	if status.Self != nil && status.Self.DNSName != "" {
		name = strings.TrimSuffix(status.Self.DNSName, ".")
	}
	allowed := append(append([]string{}, opts.Allow...), opts.Admins...)
	for _, p := range opts.People {
		allowed = append(allowed, p.Login)
	}
	if g.Owner != "" {
		log.Printf("Signed in to the tailnet by %s, who is always allowed. Also allowed: %v", g.Owner, allowed)
	} else if status.Self != nil && status.Self.IsTagged() {
		log.Printf("This machine is tagged, so it has no owner. Allowed: %v", allowed)
	} else {
		log.Printf("Tailscale has not said yet who signed this machine in; asking again. Allowed meanwhile: %v", allowed)
	}
	if !known {
		go func() {
			for i := 0; i < 150; i++ {
				time.Sleep(2 * time.Second)
				st, err := lc.Status(context.Background())
				if err == nil && learn(st) {
					g.mu.Lock()
					log.Printf("Tailscale now says: signed in by %s, names %v", g.Owner, g.Names)
					g.mu.Unlock()
					return
				}
			}
			log.Printf("Tailscale never said who signed this machine in; only the listed logins are allowed")
		}()
	}

	serve := func(ln net.Listener, scheme string) {
		log.Printf("Listening on %s://%s", scheme, name)
		if err := http.Serve(ln, g); err != nil {
			log.Fatalf("%s: %v", scheme, err)
		}
	}
	plain, err := srv.Listen("tcp", ":80")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	g.mu.Lock()
	scheme = "http"
	g.mu.Unlock()
	if secure, err := srv.ListenTLS("tcp", ":443"); err == nil {
		go serve(secure, "https")
		g.mu.Lock()
		scheme = "https"
		g.mu.Unlock()
	} else {
		log.Printf("No HTTPS (%v). Plain HTTP inside the tailnet's encryption is used instead; enable HTTPS certificates in the Tailscale admin console to add it.", err)
	}
	g.mu.Lock()
	g.setup = Setup{State: "up", Address: scheme + "://" + firstNonEmpty(dnsName, name)}
	g.mu.Unlock()
	fmt.Fprintln(os.Stderr)
	serve(plain, "http")
}
