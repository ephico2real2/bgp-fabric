package main

import (
	"testing"
	"time"
)

func TestDiffSessionAndRouterAndRoute(t *testing.T) {
	ts := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	prev := Snapshot{
		Routers: []Router{{Name: "edge", Reachable: true}, {Name: "spine", Reachable: true}},
		Sessions: []Session{
			{Router: "spine", Peer: "10.200.1.19", State: "Established"},
			{Router: "leaf1", Peer: "172.20.0.3", State: "Established"},
		},
		Routes: []Route{
			{Router: "leaf1", Prefix: "10.198.0.10/32", Bestpath: true, Nexthop: "172.20.0.5"},
			{Router: "leaf1", Prefix: "10.200.255.2/32", Bestpath: true, Nexthop: "10.200.1.3"},
		},
	}
	next := Snapshot{
		Routers: []Router{{Name: "edge", Reachable: false}, {Name: "spine", Reachable: true}},
		Sessions: []Session{
			{Router: "spine", Peer: "10.200.1.19", State: "Idle"},
			{Router: "spine", Peer: "10.200.1.2", State: "Established"},
		},
		Routes: []Route{
			{Router: "leaf1", Prefix: "10.198.0.10/32", Bestpath: true, Nexthop: "172.20.0.6"},
			{Router: "leaf1", Prefix: "10.199.0.1/32", Bestpath: true, Nexthop: "10.200.1.3"},
		},
	}
	ev := diff(prev, next, ts)
	kinds := map[string]int{}
	var texts []string
	for _, e := range ev {
		kinds[e.Kind]++
		texts = append(texts, e.Text)
		if e.TS != "2026-09-20T12:00:00.000Z" {
			t.Fatalf("ts %q", e.TS)
		}
	}
	if kinds["router"] != 1 || kinds["session"] != 3 || kinds["route"] != 3 {
		t.Fatalf("kinds = %v texts = %v", kinds, texts)
	}
	want := []string{
		"router edge unreachable",
		"spine 10.200.1.19 Established → Idle",
		"spine 10.200.1.2 appeared Established",
		"leaf1 172.20.0.3 vanished (was Established)",
		"leaf1: 10.199.0.1/32 added via 10.200.1.3",
		"leaf1: 10.200.255.2/32 withdrawn",
		"leaf1: 10.198.0.10/32 bestpath via 172.20.0.5 → 172.20.0.6",
	}
	got := map[string]bool{}
	for _, t := range texts {
		got[t] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing event %q in %v", w, texts)
		}
	}
}

func TestDiffReachableAgain(t *testing.T) {
	ts := time.Date(2026, 9, 20, 12, 0, 1, 0, time.UTC)
	prev := Snapshot{Routers: []Router{{Name: "edge", Reachable: false}}}
	next := Snapshot{Routers: []Router{{Name: "edge", Reachable: true}}}
	ev := diff(prev, next, ts)
	if len(ev) != 1 || ev[0].Text != "router edge reachable again" {
		t.Fatalf("%+v", ev)
	}
}
