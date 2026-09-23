package main

import (
	"testing"
	"time"
)

// These four pin the contract the signal fields are sold on: HasDelta and
// HasTimers mean "measured on THIS tick". Each one failed before the guard it
// tests existed.

// A peer that is not Established still reports bgpTimerLastRead, and there it
// is the peer's AGE, not a heartbeat. Measured on real FRR 10.7.1 with a
// neighbour that never came up: bgpState "Active", bgpTimerLastRead 23000 then
// 64000 forty-one seconds later, against a holdMsec of 9000. Worse, bgp_vty.c
// sums only tm_sec+tm_min+tm_hour, so the value wraps every 24 h and a peer
// down for exactly a day reads 0 — which a heartbeat renders as "just spoke".
func TestTimersAreNotTakenFromANonEstablishedPeer(t *testing.T) {
	f := newFakeRouter(t)
	f.set(`{"ipv4Unicast":{"routerId":"10.200.255.11","as":65101,"peers":{
	 "10.200.1.3":{"remoteAs":65100,"state":"Active","peerUptime":"never","msgRcvd":0,"msgSent":0,"pfxRcd":0,"pfxSnt":0}}}}`, "")
	f.setNeighbors(`{"10.200.1.3":{"bgpState":"Active","bgpTimerHoldTimeMsecs":9000,"bgpTimerKeepAliveIntervalMsecs":3000,"bgpTimerLastRead":64000}}`)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	snap, _, _ := p.tick(time.Now())
	s := snap.Sessions[0]
	if s.HasTimers {
		t.Fatalf("an %s peer has no heartbeat: quietMsec=%d is %.0fx holdMsec",
			s.State, s.QuietMsec, float64(s.QuietMsec)/9000.0)
	}
	if s.QuietMsec != 0 {
		t.Fatalf("no timers must mean no numbers, got quietMsec=%d", s.QuietMsec)
	}
}

// A session replayed out of the hold-down is a peer that is GONE. It kept the
// signal it had when it was alive, so for the whole 30 s hold-down it claimed
// messages this tick and a one-second heartbeat — and Stale is deliberately
// false on it, so the page had nothing to gate on.
func TestAHeldSessionCarriesNoLiveSignal(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWithCounters("10.200.1.3", "Established", 100, 100, 1, 1, false, 10), "")
	f.setNeighbors(`{"10.200.1.3":{"bgpState":"Established","bgpTimerHoldTimeMsecs":9000,"bgpTimerKeepAliveIntervalMsecs":3000,"bgpTimerLastRead":1000}}`)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	t0 := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	p.tick(t0)
	f.set(summaryWithCounters("10.200.1.3", "Established", 110, 110, 1, 1, false, 10), "")
	p.tick(t0.Add(2 * time.Second))

	// the peer vanishes from a router that still answers
	f.set(`{"ipv4Unicast":{"routerId":"10.200.255.11","as":65101,"peers":{}}}`, "")
	for i, dt := range []time.Duration{4 * time.Second, 20 * time.Second} {
		snap, _, _ := p.tick(t0.Add(dt))
		var held *Session
		for j := range snap.Sessions {
			if snap.Sessions[j].Peer == "10.200.1.3" {
				held = &snap.Sessions[j]
			}
		}
		if held == nil {
			t.Fatalf("tick %d: the vanished session must be held, not dropped", i)
		}
		if held.State != "Idle" {
			t.Fatalf("tick %d: held session state=%q want Idle", i, held.State)
		}
		if held.HasDelta {
			t.Fatalf("tick %d: a gone peer claims %d messages this tick", i, held.DRcvd)
		}
		if held.HasTimers {
			t.Fatalf("tick %d: a gone peer claims it spoke %dms ago", i, held.QuietMsec)
		}
	}
}

// A peer that returns inside the 30 s hold-down must not be measured against
// the sample frozen when it vanished: that would report up to fifteen ticks of
// traffic as one tick's worth.
func TestAReturningSessionIsNotMeasuredAgainstItsFrozenSample(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWithCounters("10.200.1.3", "Established", 100, 100, 1, 1, false, 10), "")
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	t0 := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	p.tick(t0)
	f.set(`{"ipv4Unicast":{"routerId":"10.200.255.11","as":65101,"peers":{}}}`, "")
	p.tick(t0.Add(2 * time.Second))  // vanishes, held
	p.tick(t0.Add(10 * time.Second)) // still held

	// back, with 300 messages' worth of counters from the time it was away
	f.set(summaryWithCounters("10.200.1.3", "Established", 400, 400, 1, 1, false, 10), "")
	snap, _, _ := p.tick(t0.Add(20 * time.Second))
	s := snap.Sessions[0]
	if s.HasDelta {
		t.Fatalf("20s of traffic reported as one 2s poll: dRcvd=%d", s.DRcvd)
	}
}

// A router that answers `{}` is reachable with tableVersion 0. Using that zero
// as the next tick's baseline reported the router's entire table version as one
// tick's change.
func TestAnEmptyBGPTickDoesNotPoisonTheRouterDelta(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWithCounters("10.200.1.3", "Established", 100, 100, 1, 1, false, 86), "")
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	t0 := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	p.tick(t0)

	f.set(`{}`, "") // reachable, no BGP instance, tableVersion 0
	snap, _, _ := p.tick(t0.Add(2 * time.Second))
	if !snap.Routers[0].Reachable {
		t.Fatal("an answered `{}` is a reachable router")
	}

	f.set(summaryWithCounters("10.200.1.3", "Established", 110, 110, 1, 1, false, 86), "")
	snap, _, _ = p.tick(t0.Add(4 * time.Second))
	r := snap.Routers[0]
	if r.HasDelta && r.DTableVersion == 86 {
		t.Fatalf("the whole table version reported as one tick's change: dTableVersion=%d", r.DTableVersion)
	}
	if r.DTableVersion != 0 {
		t.Fatalf("the table did not move across those ticks, got dTableVersion=%d", r.DTableVersion)
	}
}

// The neighbors call is the third vtysh call of the tick and must not sit on
// the critical path: a bgpd that has stopped answering makes every call run to
// its deadline, so in series that is one more full timeout per router per tick
// against a 2s poll. Fetched beside the ipv4 call, a slow neighbors answer
// costs the tick max(ipv4, neighbors) instead of their sum.
func TestNeighborsFetchIsNotOnTheCriticalPath(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWithCounters("10.200.1.3", "Established", 100, 100, 1, 1, false, 10), "")
	f.setNeighbors(`{"10.200.1.3":{"bgpState":"Established","bgpTimerHoldTimeMsecs":9000,"bgpTimerKeepAliveIntervalMsecs":3000,"bgpTimerLastRead":1000}}`)
	f.setDelays(700*time.Millisecond, 700*time.Millisecond)
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)

	start := time.Now()
	snap, _, _ := p.tick(time.Now())
	el := time.Since(start)
	if !snap.Sessions[0].HasTimers {
		t.Fatal("the timers must still arrive when the call is merely slow")
	}
	// In series: 700 + 700 = 1.4s. Beside each other: about 700ms.
	if el > 1100*time.Millisecond {
		t.Fatalf("tick took %s: the neighbors call is in series with the ipv4 call", el.Round(time.Millisecond))
	}
	t.Logf("tick took %s", el.Round(time.Millisecond))
}

// Roles are description text from DASHBOARD_ROLES, and the separator is a
// SEMICOLON because a role description is a sentence with commas in it.
func TestParseRolesSplitsOnSemicolonsNotCommas(t *testing.T) {
	got := parseRoles("edge=the border: the WAN, and client0, live beyond it;spine=transit only;leaf1=where clusters attach")
	if len(got) != 3 {
		t.Fatalf("roles=%d want 3: %+v", len(got), got)
	}
	if got["edge"] != "the border: the WAN, and client0, live beyond it" {
		t.Fatalf("a comma inside a description split the entry: %q", got["edge"])
	}
	if got["spine"] != "transit only" || got["leaf1"] != "where clusters attach" {
		t.Fatalf("roles wrong: %+v", got)
	}
	// An unset variable is not an error; the legend simply has nothing to say.
	if len(parseRoles("")) != 0 {
		t.Fatal("empty DASHBOARD_ROLES must produce no roles")
	}
	// A malformed entry is skipped rather than poisoning the rest.
	if r := parseRoles("bare;spine=transit"); len(r) != 1 || r["spine"] != "transit" {
		t.Fatalf("a malformed entry was not skipped: %+v", r)
	}
}

// The role reaches the snapshot, and a router with no role configured simply
// has none — the field is omitempty so the page can tell.
func TestRoleIsStampedOntoTheRouter(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWithCounters("10.200.1.3", "Established", 100, 100, 1, 1, false, 10), "")
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	p.setRoles(map[string]string{"leaf1": "where clusters attach"})
	snap, _, _ := p.tick(time.Now())
	if snap.Routers[0].Role != "where clusters attach" {
		t.Fatalf("role=%q want the configured text", snap.Routers[0].Role)
	}

	p2 := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65101: "leaf1"}, 2*time.Second)
	snap, _, _ = p2.tick(time.Now())
	if snap.Routers[0].Role != "" {
		t.Fatalf("an unconfigured router must have no role, got %q", snap.Routers[0].Role)
	}
}
