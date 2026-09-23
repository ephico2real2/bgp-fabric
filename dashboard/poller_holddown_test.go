package main

import (
	"fmt"
	"testing"
	"time"
)

func summaryPeers(asn int, uptime string, peers map[string]string) string {
	body := `{"ipv4Unicast":{"routerId":"10.200.255.11","as":65101,"peers":{`
	first := true
	for addr, state := range peers {
		if !first {
			body += ","
		}
		first = false
		body += fmt.Sprintf(`%q:{"remoteAs":%d,"state":%q,"peerUptime":%q,"pfxRcd":1,"pfxSnt":1}`, addr, asn, state, uptime)
	}
	return body + "}}}"
}

// A vanished session is shown Idle (never Established) for holdDown, then
// gone; under dynamic-neighbour churn `held` stays bounded by holdDown/poll
// and lastOK stays one entry per configured router.
func TestVanishedSessionIdleThenGoneAndHeldBounded(t *testing.T) {
	f := newFakeRouter(t)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65101: "leaf1"}, 2*time.Second)
	t0 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	now := t0

	f.set(summaryPeers(65021, "00:01:00", map[string]string{"172.20.0.2": "Established"}), "")
	snap, _, _ := p.tick(now)
	if len(snap.Sessions) != 1 || snap.Sessions[0].State != "Established" {
		t.Fatalf("tick0 sessions %+v", snap.Sessions)
	}

	f.set(summaryPeers(65021, "00:01:00", map[string]string{}), "") // peer gone from the table
	now = now.Add(2 * time.Second)
	snap, events, _ := p.tick(now)
	if len(snap.Sessions) != 1 || snap.Sessions[0].State != "Idle" || snap.Sessions[0].Stale {
		t.Fatalf("tick1: want one held Idle session, got %+v", snap.Sessions)
	}
	if snap.ServerEstablished != 0 {
		t.Fatalf("tick1: serverEstablished %d", snap.ServerEstablished)
	}
	if len(events) != 1 || events[0].To != "Idle" {
		t.Fatalf("tick1 events %+v", events)
	}
	for i := 0; i < 14; i++ {
		now = now.Add(2 * time.Second)
		snap, _, _ = p.tick(now)
		for _, s := range snap.Sessions {
			if s.State == "Established" {
				t.Fatalf("t+%s: shown Established after the router stopped reporting it: %+v", now.Sub(t0), s)
			}
		}
	}
	now = now.Add(2 * time.Second) // 32 s after goneAt
	snap, _, _ = p.tick(now)
	if len(snap.Sessions) != 0 || len(p.held) != 0 {
		t.Fatalf("after holdDown: sessions=%+v held=%d", snap.Sessions, len(p.held))
	}

	maxHeld := 0
	for i := 0; i < 200; i++ { // a new dynamic-neighbour address every tick
		f.set(summaryPeers(65021, "00:01:00", map[string]string{fmt.Sprintf("172.20.%d.%d", i/250, i%250+2): "Established"}), "")
		now = now.Add(2 * time.Second)
		p.tick(now)
		if len(p.held) > maxHeld {
			maxHeld = len(p.held)
		}
	}
	bound := int(holdDown/(2*time.Second)) + 1
	if maxHeld > bound {
		t.Fatalf("held grew to %d under churn, bound %d", maxHeld, bound)
	}
	if len(p.lastOK) != 1 {
		t.Fatalf("lastOK has %d entries for one router", len(p.lastOK))
	}
	t.Logf("held peaked at %d (bound %d); lastOK=%d", maxHeld, bound, len(p.lastOK))
}

// The router answers 200 with `{}` (bgpd up, no BGP instance — measured on
// frr 10.7.1; `{ }` with an instance and no neighbours prints the same). An
// answered empty summary is a reachable router with zero sessions: the old
// ones must not stay Established (stale) for ever.
func TestAnsweredEmptySummaryIsNotStaleForever(t *testing.T) {
	f := newFakeRouter(t)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65101: "leaf1"}, 2*time.Second)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	f.set(summaryPeers(65021, "00:01:00", map[string]string{"172.20.0.2": "Established"}), "")
	p.tick(now)

	f.set("{}\n", `{"warning":"Default BGP instance not found"}`)
	for i := 0; i < 30; i++ { // 60 s, twice the hold-down
		now = now.Add(2 * time.Second)
		snap, _, _ := p.tick(now)
		if !snap.Routers[0].Reachable {
			t.Fatalf("t+%ds: router marked unreachable although it answered", 2*(i+1))
		}
		for _, s := range snap.Sessions {
			if s.State == "Established" {
				t.Fatalf("t+%ds: session still Established (stale=%v) after the router reported no BGP instance: %+v", 2*(i+1), s.Stale, s)
			}
		}
		if i >= 16 && len(snap.Sessions) != 0 {
			t.Fatalf("t+%ds: %d sessions still shown past the hold-down", 2*(i+1), len(snap.Sessions))
		}
	}
}

// Behaviour preservation for the state signature: peerUptime ticks on its own
// and must not push a whole snapshot every poll.
func TestTickReportsNoChangeWhenOnlyUptimeMoves(t *testing.T) {
	f := newFakeRouter(t)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	f.set(summaryPeers(65100, "00:01:00", map[string]string{"10.200.1.3": "Established"}), "")
	p.tick(now)
	f.set(summaryPeers(65100, "00:01:02", map[string]string{"10.200.1.3": "Established"}), "")
	_, events, changed := p.tick(now.Add(2 * time.Second))
	if len(events) != 0 {
		t.Fatalf("steady fabric produced events: %+v", events)
	}
	if changed {
		t.Fatal("uptime alone made the tick report a state change (a full snapshot every poll)")
	}
}
