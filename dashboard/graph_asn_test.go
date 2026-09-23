package main

import "testing"

// Two routers in one AS (an iBGP fabric, or a second leaf built from the
// same template) must not collapse onto one node: peerNodeID can only
// resolve a peer by ASN while the ASN identifies exactly one router.
func TestBuildGraphDoesNotCollapseTwoRoutersInOneAS(t *testing.T) {
	routers := []Router{
		{Name: "spine", ASN: 65100, Reachable: true},
		{Name: "leaf1", ASN: 65101, Reachable: true},
		{Name: "leaf2", ASN: 65101, Reachable: true},
	}
	sessions := []Session{
		{Router: "spine", Peer: "10.200.1.2", PeerASN: 65101, State: "Established"},  // leaf1
		{Router: "spine", Peer: "10.200.1.10", PeerASN: 65101, State: "Established"}, // leaf2
	}
	_, edges := buildGraph(routers, sessions, map[int]string{})
	if len(edges) != 2 {
		t.Fatalf("two peers in AS 65101 produced %d edge(s) — they collapsed onto one node: %+v", len(edges), edges)
	}
}
