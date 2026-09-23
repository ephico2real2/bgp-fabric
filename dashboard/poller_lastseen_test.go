package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A router whose agent has NEVER answered reports LastSeen empty, as model.go
// promises ("Empty if the agent has never answered") — not a zero time, which
// the page would render as "56y ago".
func TestLastSeenEmptyWhenNeverAnswered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // dead from the first tick
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: url}}, map[int]string{}, 2*time.Second)
	snap, _, _ := p.tick(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC))
	if snap.Routers[0].Reachable {
		t.Fatal("dead agent must be unreachable")
	}
	if snap.Routers[0].LastSeen != "" {
		t.Fatalf("never-answered LastSeen=%q want empty", snap.Routers[0].LastSeen)
	}
}

// The middle branch: the agent ANSWERED but FRR printed `{}` (no BGP
// instance). That is a successful poll, so LastSeen is now — and the empty
// routerView stored there must still carry seenAt, or the next unreachable
// tick would freeze at the OLDER poll and replay sessions that are gone.
func TestLastSeenAfterEmptyBGPThenUnreachable(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWith(map[string]string{"10.200.1.3": "Established"}), "")
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	t0 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	snap, _, _ := p.tick(t0)
	if snap.Routers[0].LastSeen != nowRFC3339ms(t0) || snap.Routers[0].ASN != 65101 {
		t.Fatalf("t0: %+v", snap.Routers[0])
	}

	t1 := t0.Add(5 * time.Second)
	f.set(`{}`, "")
	snap, _, _ = p.tick(t1)
	if !snap.Routers[0].Reachable {
		t.Fatal("t1: `{}` is an answered poll, the router is reachable")
	}
	if snap.Routers[0].LastSeen != nowRFC3339ms(t1) {
		t.Fatalf("t1: LastSeen=%q want %s (the poll succeeded)", snap.Routers[0].LastSeen, nowRFC3339ms(t1))
	}
	if snap.Routers[0].ASN != 0 || snap.Routers[0].RouterID != "" {
		t.Fatalf("t1: `{}` carries no instance, want ASN/RouterID zeroed, got %+v", snap.Routers[0])
	}

	f.srv.Close()
	t2 := t1.Add(7 * time.Second)
	snap, _, _ = p.tick(t2)
	if snap.Routers[0].Reachable {
		t.Fatal("t2: closed server must be unreachable")
	}
	if snap.Routers[0].LastSeen != nowRFC3339ms(t1) {
		t.Fatalf("t2: LastSeen=%q want frozen at t1 %s", snap.Routers[0].LastSeen, nowRFC3339ms(t1))
	}
	for _, s := range snap.Sessions {
		if s.Stale {
			t.Fatalf("t2: a stale session was replayed from the empty `{}` view: %+v", s)
		}
	}
}

// LastSeen never moves while the agent stays down: two unreachable ticks in a
// row both report the last good poll, never `now`.
func TestLastSeenDoesNotAdvanceWhileDown(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWith(map[string]string{"10.200.1.3": "Established"}), "")
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	t0 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	p.tick(t0)
	f.srv.Close()
	for i, dt := range []time.Duration{3 * time.Second, 40 * time.Second} {
		snap, _, _ := p.tick(t0.Add(dt))
		if snap.Routers[0].LastSeen != nowRFC3339ms(t0) {
			t.Fatalf("down tick %d: LastSeen=%q want %s", i, snap.Routers[0].LastSeen, nowRFC3339ms(t0))
		}
	}
}
