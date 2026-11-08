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
	"regexp"
	"strconv"
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

const version = "0.9.4"

// The contract: the shape of house.json and of /atrium/*. Bumped only when an older app could no longer read what
// this add-on serves (adding an optional field never bumps it). The app knows which contracts it reads.
const contract = 1

// The oldest app this add-on serves well. Raised only when the app must have something newer; hello says
// update: true to an older one (its User-Agent says which it is), which then asks to be updated.
const minApp = "0.5"

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
	Admins   []string `json:"admins"`  // may change the layout for everyone, besides the login that signed the machine in
	Writers  []string `json:"writers"` // add-on slugs that may write the layout from inside Home Assistant (the Claude Code add-on, by default)
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
	// Another add-on on the Supervisor's network that the owner listed under writers: it may write the layout.
	Addon bool
	// On the add-on's port (home network, VPN), with a Home Assistant token Home Assistant accepted: the layout
	// and clips only (Local refuses every other path).
	Local bool
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
	MappingPath string   // the declared layout, atrium.yaml in the add-on's own configuration folder
	Slug        string   // this add-on's slug, for telling people where that folder is on the host
	SkillDir    string   // the atrium-mapping skill, carried in the image ("" = none)
	WebDir      string   // the built web app to serve ("" = none)
	Supervisor  []string // the addresses the Supervisor's ingress proxy connects from: the only peers the ingress listener believes
	// The Supervisor's API ("http://supervisor" in the add-on; a test server in tests) and how long it is given.
	SupervisorURL     string
	SupervisorTimeout time.Duration
	// Resolves an add-on slug to its addresses on the Supervisor's network (nil: by DNS, slug with dashes), and
	// asks the Supervisor whether that add-on is running at this address (nil: GET /addons/<slug>/info, which
	// needs hassio_api). A writer must pass both, on every request: container addresses are reused when containers
	// restart. Each says why not, in words the owner can act on.
	ResolveAddon func(slug string) ([]string, error)
	CheckAddon   func(slug, ip string) error

	mu           sync.Mutex
	home         string
	config       house.Config
	mapping      house.Mapping
	mappingErr   string              // why the file was refused, if it was (the last good mapping stays in use)
	mappingAt    time.Time           // the file's modification time last read
	problems     []string            // what the last render could not use
	stale        []house.StaleEntity // entities Home Assistant keeps for devices that are gone (listed, never acted on)
	rooms        int                 // rooms in the last render
	rendered     bool                // there has been a render (the first one is logged whatever it found)
	generation   int                 // bumped on every configuration or mapping change
	house        built
	houseDefault built // the same without atrium.yaml, for ?layout=default
	building     singleflight.Group
	admins       map[string]bool // Home Assistant user id → is an administrator
	adminsAt     time.Time
	setup        Setup
}

// Setup is where the add-on is in joining the tailnet, for the panel to walk the owner through it: it is
// reachable through Home Assistant's sidebar before Tailscale is signed in, so the sign-in link can be a button
// there instead of a line in the log.
type Setup struct {
	State   string   `json:"state"`             // "starting", "needs-login", "up"
	AuthURL string   `json:"authURL,omitempty"` // the Tailscale sign-in link, while needs-login
	Address string   `json:"address,omitempty"` // https://atrium.<tailnet>.ts.net (or http://…), once up
	Owner   string   `json:"owner,omitempty"`   // the login that signed the machine in, once known
	Layout  Layout   `json:"layout"`
	Writers []string `json:"writers"` // add-ons allowed to write the layout, so the page can say whether the Claude Code app is
}

// Layout is what the set-up page says about the mapping: is there one, and what in it could not be used.
type Layout struct {
	Path     string   `json:"path"`               // where atrium.yaml is read from, as Home Assistant sees it
	Exists   bool     `json:"exists"`             // there is a file
	Custom   bool     `json:"custom"`             // it fills at least one of the screens only it can (Home readouts, Security, Energy, Music)
	Rooms    int      `json:"rooms"`              // rooms the house has, as drawn
	Error    string   `json:"error,omitempty"`    // the file was refused (bad YAML, a room without an id…)
	Problems []string `json:"problems,omitempty"` // what the last render dropped
	// Entities Home Assistant still registers though their integration, loaded, has not provided them since it
	// started: left behind by devices that were removed or renamed. Only the owner may delete them, in Home
	// Assistant; the page says where.
	Stale []house.StaleEntity `json:"stale,omitempty"`
}

// layout is the Layout as things stand; g.mu held.
func (g *Gateway) layout() Layout {
	return Layout{Path: g.mappingHostPath(), Exists: !g.mappingAt.IsZero(), Custom: g.mapping.Custom(), Rooms: g.rooms, Error: g.mappingErr, Problems: g.problems, Stale: g.stale}
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
	if id.Addon {
		return false // a writer gets the layout routes only (see serve), never the API proxy
	}
	if id.Local {
		return true
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
		// Not the Supervisor: perhaps an add-on the owner lets write the layout (the Claude Code add-on, running
		// the atrium-mapping skill). It is known by its container's address on the Supervisor's network. When it
		// is not one, the answer says why, so the skill can tell the owner what to do.
		slug, why := in.writerAt(peer)
		if slug == "" {
			log.Printf("ingress request from %s refused: %s", r.RemoteAddr, why)
			http.Error(w, "refused: "+why, http.StatusForbidden)
			return
		}
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			http.Error(w, "web pages may not use Atrium this way", http.StatusForbidden)
			return
		}
		in.serve(w, r, Identity{Login: slug, Name: "Add-on " + slug, Device: "add-on", Addon: true}, "")
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

// writerPath is what a listed add-on may reach: enough to make the layout, nothing of Home Assistant's API.
func writerPath(p string) bool {
	switch {
	case p == "/atrium/hello", p == "/atrium/setup", p == "/atrium/house", p == "/atrium/mapping", p == "/mcp":
		return true
	case strings.HasPrefix(p, "/atrium/skill/"):
		return true
	}
	return false
}

// writerAt says which listed add-on, if any, is at this address on the Supervisor's network: its slug with
// dashes must resolve to the address (the Supervisor's DNS) and the Supervisor must say the add-on is running
// there. Both are asked every time, since addresses are reused when containers restart. When neither holds, the
// second answer says why: an IPv6 peer, a slug that does not resolve, an address that is not a listed slug's, the
// Supervisor's status, or no slug listed at all. The Supervisor reports one IPv4 address per add-on, so an IPv6
// peer can never be confirmed (the ingress listener is IPv4-only for that reason; this check is for the rest).
func (g *Gateway) writerAt(ip string) (slug, why string) {
	ip = strings.TrimPrefix(ip, "::ffff:")
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Sprintf("%q is not an address", ip)
	}
	if parsed.To4() == nil {
		return "", fmt.Sprintf("IPv6 peer %s: add-ons are known by their IPv4 address only (the Supervisor reports one); connect over IPv4", ip)
	}
	writers := g.writers()
	if len(writers) == 0 {
		return "", fmt.Sprintf("%s is not the Supervisor, and no add-on is listed under writers (Apps that may write the layout)", ip)
	}
	var reasons []string
	for _, slug := range writers {
		var addrs []string
		var err error
		if g.ResolveAddon != nil {
			addrs, err = g.ResolveAddon(slug)
		} else {
			addrs, err = net.LookupHost(strings.ReplaceAll(slug, "_", "-"))
		}
		if err != nil || len(addrs) == 0 {
			reasons = append(reasons, fmt.Sprintf("%s does not resolve on the add-on network (is it installed and started? a slug is <repository-hash>_<name>)", slug))
			continue
		}
		var v4 []string
		for _, a := range addrs {
			if a = strings.TrimPrefix(a, "::ffff:"); net.ParseIP(a) != nil && net.ParseIP(a).To4() != nil {
				v4 = append(v4, a)
			}
		}
		if !contains(v4, ip) {
			reasons = append(reasons, fmt.Sprintf("%s is at %s, not %s", slug, strings.Join(v4, ", "), ip))
			continue
		}
		check := g.CheckAddon
		if check == nil {
			check = g.supervisorSaysRunningAt
		}
		if err := check(slug, ip); err != nil {
			reasons = append(reasons, fmt.Sprintf("%s resolves to %s but the Supervisor does not confirm it: %v", slug, ip, err))
			continue
		}
		return slug, ""
	}
	return "", fmt.Sprintf("%s is not a listed add-on: %s", ip, strings.Join(reasons, "; "))
}

// writers is the slugs allowed to write the layout, trimmed; g.mu is taken, since the set-up page can add one.
func (g *Gateway) writers() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, slug := range g.Opts.Writers {
		if slug = strings.TrimSpace(slug); slug != "" {
			out = append(out, slug)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// supervisorSaysRunningAt asks the Supervisor (GET /addons/<slug>/info, which needs hassio_api; the default role
// allows it) whether an add-on is started and has this address. Any other answer refuses, and says what it was:
// a 403 means hassio_api is missing, a 404 that the slug is not installed.
func (g *Gateway) supervisorSaysRunningAt(slug, ip string) error {
	timeout := g.SupervisorTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	info, err := g.addonInfo(ctx, slug)
	if err != nil {
		var se *supervisorError
		switch {
		case errors.As(err, &se) && se.Status == http.StatusForbidden:
			return fmt.Errorf("%v (this add-on lacks hassio_api)", err)
		case errors.As(err, &se) && se.Status == http.StatusNotFound:
			return fmt.Errorf("%v (no such add-on)", err)
		}
		return err
	}
	if info.State != "started" {
		return fmt.Errorf("the Supervisor says %s is %q, not started", slug, info.State)
	}
	if info.IP != ip {
		return fmt.Errorf("the Supervisor says %s is at %q, not %s", slug, info.IP, ip)
	}
	return nil
}

func (g *Gateway) fromSupervisor(ip string) bool {
	for _, s := range g.Supervisor {
		if ip == s {
			return true
		}
	}
	return false
}

// serveIngress serves the Home Assistant side on an IPv4-only listener. ":8099" on "tcp" would be dual-stack, and
// an add-on whose hostname also resolves to IPv6 would then arrive as an IPv6 peer, which the Supervisor can never
// confirm (it reports one IPv4 address per add-on). With IPv6 refused at the socket, clients fall back to IPv4; the
// Supervisor's own ingress proxy connects over IPv4 anyway.
func serveIngress(addr string, g *Gateway) error {
	ln, err := ingressListener(addr)
	if err != nil {
		return err
	}
	return http.Serve(ln, Ingress{g})
}

func ingressListener(addr string) (net.Listener, error) { return net.Listen("tcp4", addr) }

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
	if id.Addon { // a listed add-on: the layout, the house it renders, the skill, MCP — not Home Assistant's API
		ok = writerPath(r.URL.Path)
	}

	if r.URL.Path == "/atrium/hello" {
		// Who the app is to this house. Answered to anyone on the tailnet, so the app can say why not.
		w.Header().Set("Content-Type", "application/json")
		app := appVersion(r.UserAgent())
		json.NewEncoder(w).Encode(map[string]any{
			"atrium": contract, "version": version, "minApp": minApp, "update": app != "" && olderThan(app, minApp), "home": g.homeName(r.Context()),
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
	case r.URL.Path == "/atrium/clips":
		g.serveClips(w, r)
	case r.URL.Path == "/atrium/setup":
		g.mu.Lock()
		setup := g.setup
		setup.Owner = g.Owner
		setup.Writers = append([]string{}, g.Opts.Writers...)
		setup.Layout = g.layout()
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(setup)
	case r.URL.Path == "/atrium/house":
		g.serveHouse(w, r)
	case r.URL.Path == "/atrium/config":
		g.serveConfig(w, r, id)
	case r.URL.Path == "/atrium/mapping":
		g.serveMapping(w, r, id)
	case r.URL.Path == "/atrium/addons", r.URL.Path == "/atrium/writers":
		// Granting writers: Home Assistant administrators through ingress only (allowed has checked they are one);
		// never a writer (writerPath leaves these out), never the home-network port, not even the tailnet.
		if !id.HomeAssistant {
			http.Error(w, "writers are granted from the set-up page in Home Assistant only", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/atrium/addons" {
			g.serveAddons(w, r)
		} else {
			g.serveWriters(w, r, id)
		}
	case r.URL.Path == "/mcp":
		g.serveMCP(w, r, id)
	case strings.HasPrefix(r.URL.Path, "/atrium/skill/") && g.SkillDir != "":
		name := path.Base(r.URL.Path) // SKILL.md, draft.py, mapping.md: so "point your Claude at this URL" works too
		if f, err := os.Stat(filepath.Join(g.SkillDir, name)); err != nil || f.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(g.SkillDir, name))
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
		page, webBase := "index.html", base
		if id.HomeAssistant {
			if r.URL.Path == "/app" || strings.HasPrefix(r.URL.Path, "/app/") {
				// The app itself, through Home Assistant (for a computer that cannot be on the tailnet), under its
				// own prefix so its links stay apart from the set-up page's. The API stays at the ingress root.
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/app")
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
				webBase = base + "/app"
			} else if fileExists(filepath.Join(g.WebDir, "setup.html")) {
				page = "setup.html" // in Home Assistant's sidebar: the set-up page first
			}
		}
		g.serveWeb(w, r, webBase, page)
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
		g.house, g.houseDefault = built{}, built{} // rebuild on the next request
		log.Printf("mapping: %s is gone; the rooms stay, the custom screens are empty until there is one", g.MappingPath)
		return true
	case err != nil:
		return false
	case info.ModTime().Equal(g.mappingAt):
		return false
	}
	g.mappingAt = info.ModTime()
	g.generation++
	g.house, g.houseDefault = built{}, built{} // rebuild on the next request
	m, err := house.LoadMapping(g.MappingPath)
	if err != nil {
		g.mappingErr = err.Error()
		log.Printf("mapping: %s refused: %v (the previous layout stays)", g.MappingPath, err)
		return true
	}
	g.mapping, g.mappingErr = m, ""
	log.Printf("mapping: read %s: %d room overrides, custom screens: %v", g.MappingPath, len(m.Rooms), m.Custom())
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

// mappingHostPath is where the mapping file is, as the Terminal add-on and a backup see it.
func (g *Gateway) mappingHostPath() string {
	if g.Slug == "" {
		return g.MappingPath
	}
	return "/addon_configs/" + g.Slug + "/" + filepath.Base(g.MappingPath)
}

// addonSlug asks the Supervisor what this add-on is called ("df84f1fd_atrium" when installed from the repository).
func (g *Gateway) addonSlug() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var info struct {
		Data struct {
			Slug string `json:"slug"`
		} `json:"data"`
	}
	if g.supervisorJSON(ctx, "GET", "/addons/self/info", nil, &info) != nil {
		return ""
	}
	return info.Data.Slug
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
		g.SkillDir = env("ATRIUM_SKILL", "")
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
		if local := os.Getenv("ATRIUM_LOCAL"); local != "" { // e.g. 127.0.0.1:8126: the add-on's port, with Home Assistant tokens
			go func() { log.Fatal(http.ListenAndServe(local, NewLocal(g))) }()
		}
		if ingress := os.Getenv("ATRIUM_INGRESS"); ingress != "" { // e.g. 127.0.0.1:8099: pretends to be the Supervisor's ingress
			g.Supervisor = []string{"127.0.0.1"}
			go func() { log.Fatal(serveIngress(ingress, g)) }()
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
	if skill := env("ATRIUM_SKILL", "/app/skill"); fileExists(filepath.Join(skill, "SKILL.md")) {
		g.SkillDir = skill
	}
	// The declared layout lives in the add-on's own configuration folder, mapped read-only at /config here and
	// /addon_configs/<slug>/ on the host. Only that folder: Home Assistant's own configuration (secrets, auth
	// storage) is not mounted.
	g.Slug = g.addonSlug()
	g.MappingPath = env("ATRIUM_MAPPING", "/config/atrium.yaml") // the add-on's own folder (addon_config), not Home Assistant's
	if !g.readMapping() {
		log.Printf("mapping: no %s yet; the rooms are drawn from Home Assistant's areas, and the custom screens wait for the atrium-mapping skill (or you)", g.MappingPath)
	}
	go g.watchMapping()
	go g.houseJSON(context.Background()) // the first render before anyone asks

	// The Home Assistant side: the sidebar panel, through the Supervisor's ingress. Only the Supervisor's own
	// address is believed on that port (resolved once; 172.30.32.2 is its usual address).
	if addrs, err := net.LookupHost("supervisor"); err == nil && len(addrs) > 0 {
		g.Supervisor = addrs
	} else {
		g.Supervisor = []string{"172.30.32.2"}
		log.Printf("could not resolve \"supervisor\" (%v); believing only %v on the ingress port", err, g.Supervisor)
	}
	go func() {
		log.Printf("Panel listening for ingress on :%s (IPv4 only), from the Supervisor at %v and the listed add-ons %v", env("ATRIUM_INGRESS_PORT", "8099"), g.Supervisor, g.writers())
		log.Fatal(serveIngress(":"+env("ATRIUM_INGRESS_PORT", "8099"), g))
	}()

	// The home network's side: the app at home or over a VPN, without Tailscale, with its Home Assistant token.
	go func() {
		port := env("ATRIUM_LOCAL_PORT", "8126")
		log.Printf("Layout for the app on the home network on :%s (a Home Assistant sign-in is asked for)", port)
		if err := http.ListenAndServe(":"+port, NewLocal(g)); err != nil {
			log.Printf("home network port: %v (the app reaches Atrium over Tailscale or through Home Assistant only)", err)
		}
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

var appUA = regexp.MustCompile(`\bAtrium/(\d+(?:\.\d+)*)`)

// appVersion is the app's version from its User-Agent ("Atrium/0.7 (iOS 27.0)"); "" when it is not the app.
func appVersion(ua string) string {
	if m := appUA.FindStringSubmatch(ua); m != nil {
		return m[1]
	}
	return ""
}

// olderThan compares dotted versions numerically: 0.10 is newer than 0.9; a missing part is 0.
func olderThan(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}
