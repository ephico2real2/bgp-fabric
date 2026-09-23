package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeRouter answers the two agent URLs the poller fetches with whatever
// bodies the test sets; a closed server stands in for an unreachable router.
type fakeRouter struct {
	mu      sync.Mutex
	summary string
	ipv4    string
	// neighbors is empty by default, so /show/bgp-neighbors 404s and the
	// poller takes its non-fatal path: the router stays reachable and its
	// sessions carry HasTimers=false.
	neighbors string
	// failing makes the agent answer 500 without closing the listener, so a
	// test can bring the same router back on the same URL.
	failing bool
	// delays stand in for a bgpd that has stopped answering. The agent's own
	// vtysh deadline is 3s, so a hung call is a SLOW call, not a refused one,
	// and a 500 cannot model it.
	neighborsDelay time.Duration
	ipv4Delay      time.Duration
	srv            *httptest.Server
}

func newFakeRouter(t *testing.T) *fakeRouter {
	f := &fakeRouter{ipv4: `{"routerId":"10.200.255.11","localAS":65101,"routes":{}}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		sum, v4, nbr, failing := f.summary, f.ipv4, f.neighbors, f.failing
		nbrDelay, v4Delay := f.neighborsDelay, f.ipv4Delay
		f.mu.Unlock()
		if failing {
			http.Error(w, "agent down", http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/show/bgp-summary":
			_, _ = w.Write([]byte(sum))
		case "/show/bgp-ipv4":
			time.Sleep(v4Delay)
			_, _ = w.Write([]byte(v4))
		case "/show/bgp-neighbors":
			time.Sleep(nbrDelay)
			if nbr == "" {
				http.Error(w, "unknown", http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(nbr))
		default:
			http.Error(w, "unknown", http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRouter) setDelays(ipv4, neighbors time.Duration) {
	f.mu.Lock()
	f.ipv4Delay = ipv4
	f.neighborsDelay = neighbors
	f.mu.Unlock()
}

func (f *fakeRouter) setFailing(failing bool) {
	f.mu.Lock()
	f.failing = failing
	f.mu.Unlock()
}

func (f *fakeRouter) setNeighbors(neighbors string) {
	f.mu.Lock()
	f.neighbors = neighbors
	f.mu.Unlock()
}

func (f *fakeRouter) set(summary, ipv4 string) {
	f.mu.Lock()
	f.summary = summary
	if ipv4 != "" {
		f.ipv4 = ipv4
	}
	f.mu.Unlock()
}

func summaryWith(peers map[string]string) string {
	body := `{"ipv4Unicast":{"routerId":"10.200.255.11","as":65101,"peers":{`
	first := true
	for addr, state := range peers {
		if !first {
			body += ","
		}
		first = false
		body += fmt.Sprintf(`%q:{"remoteAs":65100,"state":%q,"peerUptime":"00:01:00","pfxRcd":1,"pfxSnt":1}`, addr, state)
	}
	return body + "}}}"
}

// FRR 10.7.1 answers `{}` (rc 0) to `show bgp summary json` with no `router
// bgp`, and `{ }` with an instance and no neighbours (measured 2026-09-20
// in a throwaway container from frr-agent:local). An answered empty summary
// is a reachable router with zero sessions: its old sessions go Idle under
// the hold-down and are gone after it, never Established (stale) for ever.
func TestAnsweredEmptySummaryHoldsDownThenClears(t *testing.T) {
	f := newFakeRouter(t)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	f.set(summaryWith(map[string]string{"10.200.1.3": "Established"}), "")
	if snap, _, _ := p.tick(now); len(snap.Sessions) != 1 || snap.Sessions[0].State != "Established" {
		t.Fatalf("tick0: %+v", snap.Sessions)
	}

	f.set("{}\n", `{"warning":"Default BGP instance not found"}`)
	now = now.Add(2 * time.Second)
	snap, events, _ := p.tick(now)
	if !snap.Routers[0].Reachable {
		t.Fatal("an answered poll must leave the router reachable")
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].State != "Idle" || snap.Sessions[0].Stale {
		t.Fatalf("after `{}`: want the old session held Idle, got %+v", snap.Sessions)
	}
	if len(events) != 1 || events[0].From != "Established" || events[0].To != "Idle" {
		t.Fatalf("after `{}`: want one Established→Idle event, got %+v", events)
	}
	for now.Sub(time.Date(2026, 9, 20, 12, 0, 2, 0, time.UTC)) < holdDown {
		now = now.Add(2 * time.Second)
		snap, _, _ = p.tick(now)
		for _, s := range snap.Sessions {
			if s.State == "Established" {
				t.Fatalf("t+%s: shown Established (stale=%v) after the router reported no BGP instance", now.Sub(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)), s.Stale)
			}
		}
	}
	if len(snap.Sessions) != 0 || len(p.held) != 0 {
		t.Fatalf("past the hold-down: sessions=%+v held=%d", snap.Sessions, len(p.held))
	}
}

// A route-only change (no session, node or reachability change) must be
// broadcast as type=state, or the RIB pane keeps a prefix the Events pane has
// just reported withdrawn. Identical ticks must NOT be broadcast: the routes
// slice is built from FRR's JSON maps, whose iteration order is random per
// tick, so the signature has to be order-independent.
func TestRouteOnlyChangeIsBroadcast(t *testing.T) {
	const twoPaths = `{"routerId":"10.200.255.11","localAS":65101,"routes":{
	 "10.198.0.10/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"172.20.0.2"}],"path":"65021","origin":"IGP","weight":0,"peerId":"172.20.0.2"},
	                  {"valid":true,"nexthops":[{"ip":"172.20.0.3"}],"path":"65021","origin":"IGP","weight":0,"peerId":"172.20.0.3"}],
	 "10.198.0.11/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"172.20.0.2"}],"path":"65021","origin":"IGP","weight":0,"peerId":"172.20.0.2"}],
	 "10.200.255.11/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"0.0.0.0"}],"path":"","origin":"IGP","weight":32768,"peerId":"(unspec)"}]}}`
	const onePath = `{"routerId":"10.200.255.11","localAS":65101,"routes":{
	 "10.198.0.10/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"172.20.0.2"}],"path":"65021","origin":"IGP","weight":0,"peerId":"172.20.0.2"}],
	 "10.198.0.11/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"172.20.0.2"}],"path":"65021","origin":"IGP","weight":0,"peerId":"172.20.0.2"}],
	 "10.200.255.11/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"0.0.0.0"}],"path":"","origin":"IGP","weight":32768,"peerId":"(unspec)"}]}}`
	const movedNexthop = `{"routerId":"10.200.255.11","localAS":65101,"routes":{
	 "10.198.0.10/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"172.20.0.3"}],"path":"65021","origin":"IGP","weight":0,"peerId":"172.20.0.3"}],
	 "10.198.0.11/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"172.20.0.2"}],"path":"65021","origin":"IGP","weight":0,"peerId":"172.20.0.2"}],
	 "10.200.255.11/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"0.0.0.0"}],"path":"","origin":"IGP","weight":32768,"peerId":"(unspec)"}]}}`
	const withdrawn = `{"routerId":"10.200.255.11","localAS":65101,"routes":{
	 "10.200.255.11/32":[{"valid":true,"bestpath":true,"nexthops":[{"ip":"0.0.0.0"}],"path":"","origin":"IGP","weight":32768,"peerId":"(unspec)"}]}}`

	f := newFakeRouter(t)
	summary := summaryWith(map[string]string{"10.200.1.3": "Established"})
	f.set(summary, twoPaths)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	tick := func() (int, []Event, bool) {
		now = now.Add(2 * time.Second)
		snap, ev, changed := p.tick(now)
		return len(snap.Routes), ev, changed
	}
	if n, _, changed := tick(); !changed || n != 4 {
		t.Fatalf("first tick: routes=%d changed=%v", n, changed)
	}
	for i := 0; i < 25; i++ {
		if _, ev, changed := tick(); changed || len(ev) != 0 {
			t.Fatalf("identical tick %d: changed=%v events=%d (an order-dependent signature flaps)", i, changed, len(ev))
		}
	}
	f.set(summary, onePath) // an ECMP path gone: no event, the RIB rows differ
	if n, ev, changed := tick(); !changed || n != 3 {
		t.Fatalf("ECMP path removed: routes=%d changed=%v events=%v", n, changed, ev)
	}
	if _, _, changed := tick(); changed {
		t.Fatal("steady tick after the ECMP change was broadcast")
	}
	f.set(summary, movedNexthop) // bestpath via another neighbour: one route event
	if _, ev, changed := tick(); !changed || len(ev) != 1 {
		t.Fatalf("bestpath moved: changed=%v events=%v", changed, ev)
	}
	f.set(summary, withdrawn) // two prefixes withdrawn: the RIB must follow the Events pane
	if n, ev, changed := tick(); !changed || n != 1 || len(ev) != 2 {
		t.Fatalf("withdrawn: routes=%d changed=%v events=%v", n, changed, ev)
	}
}

// LastSeen is the last successful poll, frozen while the agent is down, so
// the page can say "cannot reach leaf1 — last seen 12s ago" instead of
// presenting last-known RIB rows as current.
func TestLastSeenFrozenWhenUnreachable(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWith(map[string]string{"10.200.1.3": "Established"}), "")
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	t0 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	snap, _, _ := p.tick(t0)
	if !snap.Routers[0].Reachable || snap.Routers[0].LastSeen != nowRFC3339ms(t0) {
		t.Fatalf("reachable: %+v", snap.Routers[0])
	}
	f.srv.Close()
	t1 := t0.Add(12 * time.Second)
	snap, _, _ = p.tick(t1)
	if snap.Routers[0].Reachable {
		t.Fatal("closed server must be unreachable")
	}
	if snap.Routers[0].LastSeen != nowRFC3339ms(t0) {
		t.Fatalf("unreachable lastSeen=%q want frozen at %s", snap.Routers[0].LastSeen, nowRFC3339ms(t0))
	}
}
