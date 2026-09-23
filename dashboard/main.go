package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

//go:embed static
var staticFS embed.FS

// Set at link time: `-X main.revision=<sha> -X main.built=<rfc3339>`. Not a
// constant, because the source cannot know which commit it is being built
// from — and "unknown" is a real answer that says this binary was not built
// by the pipeline, which is worth knowing when a page looks wrong.
var (
	revision = "unknown"
	built    = "unknown"
)

// A 40-character hex sha, and whatever `git describe --dirty` put after it.
var fullSHA = regexp.MustCompile(`^([0-9a-f]{40})(.*)$`)

// shortRevision is the revision as the header shows it: seven characters of
// the sha, with any suffix KEPT. `-dirty` is the difference between "this
// commit" and "this commit plus edits nobody committed", and a header that
// drops it names a commit that does not contain the code being served.
// Anything that is not a sha — a tag, `git describe` of a shallow tree,
// "unknown" — is left exactly as it is: seven characters of it identify
// nothing.
func shortRevision() string {
	m := fullSHA.FindStringSubmatch(revision)
	if m == nil {
		return revision
	}
	return m[1][:7] + m[2]
}

const (
	defaultRouters = "edge=http://10.200.200.1:8080,spine=http://10.200.200.2:8080,leaf1=http://10.200.200.11:8080,leaf2=http://10.200.200.12:8080"
	defaultASNames = "65000=edge,65100=spine,65101=leaf1,65102=leaf2,65021=eg-poc1 (kube-vip),65022=eg-poc2 (MetalLB),65001=poc1 (Cilium),65002=poc2 (Cilium)"
	defaultPoll    = 2 * time.Second
	defaultListen  = ":8080"
	defaultEvents  = 500
)

type hub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
	poller  *poller
	events  []Event
	nextID  int
	ring    int
}

type wsClient struct {
	c    *websocket.Conn
	send chan []byte
}

func newHub(p *poller, ring int) *hub {
	return &hub{
		clients: map[*wsClient]struct{}{},
		poller:  p,
		ring:    ring,
	}
}

func (h *hub) addEvent(ev Event) Event {
	h.nextID++
	ev.ID = h.nextID
	h.events = append(h.events, ev)
	if len(h.events) > h.ring {
		h.events = h.events[len(h.events)-h.ring:]
	}
	return ev
}

func (h *hub) eventsSince(id int) []Event {
	out := []Event{}
	for _, e := range h.events {
		if e.ID > id {
			out = append(out, e)
		}
	}
	return out
}

func (h *hub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for cl := range h.clients {
		select {
		case cl.send <- msg:
		default:
			close(cl.send)
			delete(h.clients, cl)
		}
	}
}

func (h *hub) register(cl *wsClient) {
	h.mu.Lock()
	h.clients[cl] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) unregister(cl *wsClient) {
	h.mu.Lock()
	if _, ok := h.clients[cl]; ok {
		delete(h.clients, cl)
		// send may already be closed by broadcast
	}
	h.mu.Unlock()
}

func parseRouters(s string) []RouterCfg {
	var out []RouterCfg
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out = append(out, RouterCfg{Name: strings.TrimSpace(k), URL: strings.TrimSpace(v)})
	}
	return out
}

func parseASNames(s string) map[int]string {
	out := map[int]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(k))
		if err != nil {
			continue
		}
		out[n] = strings.TrimSpace(v)
	}
	return out
}

// parseRoles reads DASHBOARD_ROLES: `name=description` pairs separated by
// SEMICOLONS, because a description is a sentence and contains commas.
func parseRoles(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name := strings.TrimSpace(k)
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(v)
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	listen := envOr("DASHBOARD_LISTEN", defaultListen)
	poll, err := time.ParseDuration(envOr("DASHBOARD_POLL", "2s"))
	if err != nil {
		poll = defaultPoll
	}
	ring := defaultEvents
	if v := os.Getenv("DASHBOARD_EVENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ring = n
		}
	}
	routers := parseRouters(envOr("DASHBOARD_ROUTERS", defaultRouters))
	if len(routers) == 0 {
		log.Fatal("bgp-dashboard: DASHBOARD_ROUTERS is empty")
	}
	asNames := parseASNames(envOr("DASHBOARD_AS_NAMES", defaultASNames))
	p := newPoller(routers, asNames, poll)
	p.setRoles(parseRoles(os.Getenv("DASHBOARD_ROLES")))
	h := newHub(p, ring)

	go func() {
		// first poll immediately so /healthz can pass
		h.runTick()
		t := time.NewTicker(poll)
		defer t.Stop()
		for range t.C {
			h.runTick()
		}
	}()

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux := routes(h, static)
	log.Printf("bgp-dashboard: listen %s poll %s routers %d", listen, poll, len(routers))
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// routes is the server's URL map, out of main() so a test can ask the real
// mux. A handler that is correct but never registered serves 404, and a test
// that calls the method directly cannot see that — measured: deleting the
// /api/version line left every gate green while the endpoint 404'd.
func routes(h *hub, static fs.FS) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /api/state", h.apiState)
	mux.HandleFunc("GET /api/signal", h.apiSignal)
	mux.HandleFunc("GET /api/events", h.apiEvents)
	mux.HandleFunc("GET /api/version", h.apiVersion)
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /ws", h.ws)
	return mux
}

func (h *hub) runTick() {
	snap, events, stateChanged := h.poller.tick(time.Now())
	h.mu.Lock()
	var evMsgs [][]byte
	for _, ev := range events {
		// one shape for one object: the ring (/api/events) and the socket
		// frame carry the same Type
		ev.Type = "event"
		ev = h.addEvent(ev)
		b, err := json.Marshal(ev)
		if err == nil {
			evMsgs = append(evMsgs, b)
		}
	}
	h.mu.Unlock()
	for _, b := range evMsgs {
		h.broadcast(b)
	}
	if stateChanged {
		snap.Type = "state"
		if b, err := json.Marshal(snap); err == nil {
			h.broadcast(b)
		}
	}
	// The signal frame goes out on EVERY tick, state change or not: a steady
	// fabric changes no signature, and a heartbeat that only beats when the
	// topology changes is not a heartbeat.
	if b, err := json.Marshal(signalFrame(snap)); err == nil {
		h.broadcast(b)
	}
}

func (h *hub) apiState(w http.ResponseWriter, _ *http.Request) {
	snap, _ := h.poller.snapshot()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

// apiSignal is the HTTP form of the frame above, for a page whose WebSocket is
// down: it can keep the heartbeat honest at 2.4 KB a tick instead of pulling
// the whole 11.5 KB snapshot.
func (h *hub) apiSignal(w http.ResponseWriter, _ *http.Request) {
	snap, _ := h.poller.snapshot()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(signalFrame(snap))
}

func (h *hub) apiEvents(w http.ResponseWriter, r *http.Request) {
	since := 0
	if v := r.URL.Query().Get("since"); v != "" {
		since, _ = strconv.Atoi(v)
	}
	h.mu.Lock()
	ev := h.eventsSince(since)
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(ev)
}

// apiVersion is which build is serving this page. The page asks once — an
// image cannot change under a running container — and shows it in the header,
// so the question does not have to be answered by comparing served files
// against a checkout.
func (h *hub) apiVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"revision": revision,
		"short":    shortRevision(),
		"built":    built,
	})
}

func (h *hub) healthz(w http.ResponseWriter, _ *http.Request) {
	snap, ready := h.poller.snapshot()
	w.Header().Set("Cache-Control", "no-store")
	if !ready || snap.Reachable < snap.RouterCount || snap.RouterCount == 0 {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *hub) ws(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	cl := &wsClient{c: c, send: make(chan []byte, 8)}
	h.register(cl)
	defer func() {
		h.unregister(cl)
		_ = c.CloseNow()
	}()

	// The browser only listens. CloseRead drains the socket so ping/pong and
	// the client's close frame are answered, and cancels ctx when the peer is
	// gone. Without it the request context outlives the hijacked connection
	// and a closed tab keeps its goroutine until the next broadcast fails.
	ctx := c.CloseRead(r.Context())

	snap, _ := h.poller.snapshot()
	snap.Type = "state"
	if b, err := json.Marshal(snap); err == nil {
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = c.Write(wctx, websocket.MessageText, b)
		cancel()
		if err != nil {
			return
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-cl.send:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
