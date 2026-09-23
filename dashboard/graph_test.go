package main

import (
	"testing"
)

func testRouters() []Router {
	return []Router{
		{Name: "edge", ASN: 65000, Reachable: true},
		{Name: "spine", ASN: 65100, Reachable: true},
		{Name: "leaf1", ASN: 65101, Reachable: true},
		{Name: "leaf2", ASN: 65102, Reachable: true},
	}
}

func testASNames() map[int]string {
	return parseASNames(defaultASNames)
}

func TestBuildGraphMergesPairAndKeepsExternal(t *testing.T) {
	sessions := []Session{
		{Router: "edge", Peer: "10.200.1.18", PeerASN: 65100, State: "Established", Uptime: "00:00:04", PfxRcd: 3},
		{Router: "spine", Peer: "10.200.1.19", PeerASN: 65000, State: "Established", Uptime: "00:00:04", PfxRcd: 2},
		{Router: "spine", Peer: "10.200.1.2", PeerASN: 65101, State: "Established", PfxRcd: 1},
		{Router: "leaf1", Peer: "10.200.1.3", PeerASN: 65100, State: "Established", PfxRcd: 4},
		{Router: "leaf1", Peer: "172.20.0.3", PeerASN: 65021, State: "Established", PfxRcd: 2},
		{Router: "leaf2", Peer: "172.20.0.3", PeerASN: 65021, State: "Established", PfxRcd: 2},
	}
	nodes, edges := buildGraph(testRouters(), sessions, testASNames())
	kinds := map[string]string{}
	for _, n := range nodes {
		kinds[n.ID] = n.Kind
	}
	if kinds["edge"] != "router" || kinds["spine"] != "router" {
		t.Fatalf("router kinds = %v", kinds)
	}
	if kinds["172.20.0.3"] != "external" {
		t.Fatalf("expected external node 172.20.0.3, got %v", kinds)
	}
	var ext Node
	for _, n := range nodes {
		if n.ID == "172.20.0.3" {
			ext = n
		}
	}
	if ext.Label != "eg-poc1 (kube-vip)\n172.20.0.3" {
		t.Fatalf("external label %q", ext.Label)
	}
	// one edge for edge↔spine (merged), one spine↔leaf1, two leaf→external?
	// both leaves see the same address → two edges leaf1|172.20.0.3 and leaf2|172.20.0.3
	if len(edges) != 4 {
		t.Fatalf("edges = %d %+v", len(edges), edges)
	}
	var merged *Edge
	for i := range edges {
		if edges[i].ID == "edge|spine" {
			merged = &edges[i]
		}
	}
	if merged == nil {
		t.Fatalf("missing merged edge %+v", edges)
	}
	if merged.State != stateEstablished || merged.APfxRcd == 0 || merged.BPfxRcd == 0 {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestBuildGraphWorseState(t *testing.T) {
	sessions := []Session{
		{Router: "edge", Peer: "10.200.1.18", PeerASN: 65100, State: "Established"},
		{Router: "spine", Peer: "10.200.1.19", PeerASN: 65000, State: "Active"},
	}
	_, edges := buildGraph(testRouters(), sessions, testASNames())
	if len(edges) != 1 || edges[0].State != stateTransitional {
		t.Fatalf("edges = %+v", edges)
	}
}

func TestBuildGraphIdleIsDownStaleWins(t *testing.T) {
	sessions := []Session{
		{Router: "leaf1", Peer: "172.20.0.3", PeerASN: 65021, State: "Idle"},
		{Router: "leaf1", Peer: "10.200.1.3", PeerASN: 65100, State: "Established", Stale: true},
	}
	_, edges := buildGraph(testRouters(), sessions, testASNames())
	byID := map[string]string{}
	for _, e := range edges {
		byID[e.ID] = e.State
	}
	if byID["172.20.0.3|leaf1"] != stateDown {
		t.Fatalf("down edge %v", byID)
	}
	if byID["leaf1|spine"] != stateStale {
		t.Fatalf("stale edge %v", byID)
	}
}

func TestWorseState(t *testing.T) {
	if worseState(stateEstablished, stateTransitional) != stateTransitional {
		t.Fatal("want transitional")
	}
	if worseState(stateDown, stateStale) != stateDown {
		t.Fatal("want down")
	}
}
