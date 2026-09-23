package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Field names below are as FRR 10.7.1 emits them (measured 2026-09-20 on a
// throwaway `quay.io/frrouting/frr:10.7.1` with `show bgp summary json` /
// `show bgp ipv4 unicast json`; Active peer and a locally-originated /32).
// hostname is absent on Active (present on Established — fabric transcript).
// valid / bestpath / locPrf are omitted on a local-originated route that is
// not yet selected; FRR adds `"valid": true` and `"bestpath": true` (boolean)
// when BGP_PATH_VALID / BGP_PATH_SELECTED (bgpd/bgp_route.c @ frr-10.7.1).

type frrSummary struct {
	IPv4Unicast *frrFamily `json:"ipv4Unicast"`
}

type frrFamily struct {
	RouterID string             `json:"routerId"`
	AS       int                `json:"as"`
	Peers    map[string]frrPeer `json:"peers"`
	// Router-wide counters. DynamicPeers is 2 on each leaf (the kube-vip
	// nodes) and 0 on spine and edge, which is the difference demo 54c exists
	// to show: a leaf whose listen range has members is accepting cluster
	// peers, one whose range is empty is up but idle.
	TableVersion int `json:"tableVersion"`
	PeerCount    int `json:"totalPeers"`
	DynamicPeers int `json:"dynamicPeers"`
	FailedPeers  int `json:"failedPeers"`
}

type frrPeer struct {
	RemoteAS   int    `json:"remoteAs"`
	State      string `json:"state"`
	PeerUptime string `json:"peerUptime"`
	PfxRcd     int    `json:"pfxRcd"`
	PfxSnt     int    `json:"pfxSnt"`
	Hostname   string `json:"hostname"`
	IDType     string `json:"idType"`
	// Counters the page needs to tell a session that is CARRYING something
	// from one that is merely up. Measured on the live fabric 2026-09-21
	// (leaf1, `show bgp summary json`): msgRcvd 3440, msgSent 3445, inq 0,
	// outq 0, tableVersion 84, connectionsDropped 8, dynamicPeer true for
	// the two kube-vip nodes and absent for every configured fabric peer.
	MsgRcvd int `json:"msgRcvd"`
	MsgSent int `json:"msgSent"`
	InQ     int `json:"inq"`
	OutQ    int `json:"outq"`
	// TableVersion is the ROUTER's table version, repeated on every peer —
	// all three of leaf1's peers read 84 on the same poll. It is a per-router
	// "something changed" counter, not a per-session one.
	TableVersion int `json:"tableVersion"`
	// ConnectionsDropped is FRR's own flap count and survives between polls.
	// It catches a flap the 2s poll never saw: leaf1-spine read 8 after the
	// MD5 tests, each password change having reset the peer.
	ConnectionsDropped int `json:"connectionsDropped"`
	// DynamicPeer marks a peer that arrived through `bgp listen range` rather
	// than a `neighbor` line — on this fabric, exactly a cluster node.
	DynamicPeer bool `json:"dynamicPeer"`
}

// frrNeighbor is the per-peer view from `show bgp neighbors json`, whose
// top-level is a flat map of peer address to this object (measured on leaf1:
// keys 172.20.0.3, 172.20.0.4, 10.200.1.3).
//
// LastReadMsec is why this second call is worth making. It is FRR's own
// measurement of how long ago the peer last sent an UPDATE or a KEEPALIVE —
// those two message types only, so an OPEN or a NOTIFY does not reset it — and
// it therefore neither aliases with our poll interval nor can disagree with
// FRR's hold-timer decision.
//
// Two properties make it unsafe to read on its own, and the poller guards
// both. It is truncated to whole seconds, so a healthy session reads exactly
// keepaliveMsec on about a third of ticks and "quiet >= keepalive" would false
// alarm. And it is emitted for a peer in ANY state, where it is the peer's age
// rather than a heartbeat: measured on FRR 10.7.1 with a neighbour that never
// came up, bgpState "Active" read 23000 and then 64000 forty-one seconds later
// against a holdMsec of 9000. It also wraps every 24 h, because bgp_vty.c sums
// tm_sec+tm_min+tm_hour and drops the day. BGPState is what the poller gates
// on, which is why it is decoded here. Measured once a second on leaf1 2026-09-21, it is a clean sawtooth
// 0 -> 1000 -> 2000 -> 3000 -> 0, resetting on each keepalive, each peer on its
// own phase. Counting msgRcvd deltas across 2s polls could not see this: with
// keepalive 3s and poll 2s a healthy session reads 0 on about one tick in three.
type frrNeighbor struct {
	HoldMsec      int    `json:"bgpTimerHoldTimeMsecs"`
	KeepaliveMsec int    `json:"bgpTimerKeepAliveIntervalMsecs"`
	LastReadMsec  int    `json:"bgpTimerLastRead"`
	LastWriteMsec int    `json:"bgpTimerLastWrite"`
	PeerGroup     string `json:"peerGroup"`
	BGPState      string `json:"bgpState"`
}

type frrIPv4 struct {
	RouterID string                   `json:"routerId"`
	LocalAS  int                      `json:"localAS"`
	Routes   map[string][]frrPathJSON `json:"routes"`
}

type frrPathJSON struct {
	Valid    bool         `json:"valid"`
	Bestpath bestpathFlag `json:"bestpath"`
	Nexthops []frrNexthop `json:"nexthops"`
	Path     string       `json:"path"`
	Origin   string       `json:"origin"`
	LocPrf   int          `json:"locPrf"`
	Metric   int          `json:"metric"`
	Weight   int          `json:"weight"`
	PeerID   string       `json:"peerId"`
	Network  string       `json:"network"`
}

type frrNexthop struct {
	IP string `json:"ip"`
}

// bestpathFlag accepts FRR's boolean (`true` on the table view) and the
// object form used by the detail view.
type bestpathFlag struct {
	Set bool
}

func (b *bestpathFlag) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte("false")) {
		b.Set = false
		return nil
	}
	if bytes.Equal(data, []byte("true")) {
		b.Set = true
		return nil
	}
	if data[0] == '{' {
		b.Set = true
		return nil
	}
	return fmt.Errorf("bestpath: %s", data)
}

func decodeSummary(raw []byte) (frrSummary, error) {
	var s frrSummary
	if err := json.Unmarshal(firstJSON(raw), &s); err != nil {
		return s, err
	}
	return s, nil
}

func decodeNeighbors(raw []byte) (map[string]frrNeighbor, error) {
	var n map[string]frrNeighbor
	if err := json.Unmarshal(firstJSON(raw), &n); err != nil {
		return nil, err
	}
	return n, nil
}

func decodeIPv4(raw []byte) (frrIPv4, error) {
	var t frrIPv4
	if err := json.Unmarshal(firstJSON(raw), &t); err != nil {
		return t, err
	}
	return t, nil
}

func firstJSON(raw []byte) []byte {
	i := bytes.IndexByte(raw, '{')
	if i < 0 {
		return raw
	}
	return raw[i:]
}
