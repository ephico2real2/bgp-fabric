package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	stateEstablished  = "established"
	stateTransitional = "transitional"
	stateDown         = "down"
	stateStale        = "stale"
)

var transitionalStates = map[string]bool{
	"Connect":     true,
	"Active":      true,
	"OpenSent":    true,
	"OpenConfirm": true,
}

// routerByASN maps a fabric router's ASN to its configured name — but only
// when that ASN identifies exactly ONE router (edge 65000, spine 65100,
// leaf1 65101, leaf2 65102). An ASN shared by two routers resolves to
// neither: peers in it are then keyed by address, so two sessions never
// collapse onto one node and lose one another's detail (the fault filed
// against the source as fork issue 2). Cluster nodes share an AS (65021,
// 65022, …) and are always keyed by peer address, never by ASN.
func routerByASN(routers []Router, asNames map[int]string) map[int]string {
	out := map[int]string{}
	names := map[string]bool{}
	count := map[int]int{}
	for _, r := range routers {
		if r.ASN > 0 {
			count[r.ASN]++
		}
	}
	for _, r := range routers {
		names[r.Name] = true
		if r.ASN > 0 && count[r.ASN] == 1 {
			out[r.ASN] = r.Name
		}
	}
	for asn, label := range asNames {
		tok := strings.Fields(label)
		if len(tok) == 0 {
			continue
		}
		if count[asn] > 1 {
			continue // ambiguous: two routers answer to this AS
		}
		if names[tok[0]] {
			if _, ok := out[asn]; !ok {
				out[asn] = tok[0]
			}
		}
	}
	return out
}

func sessionClass(s Session) string {
	if s.Stale {
		return stateStale
	}
	switch {
	case s.State == "Established":
		return stateEstablished
	case transitionalStates[s.State]:
		return stateTransitional
	default:
		return stateDown
	}
}

func worseState(a, b string) string {
	rank := map[string]int{
		stateEstablished:  1,
		stateTransitional: 2,
		stateStale:        3,
		stateDown:         4,
	}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}

func peerNodeID(peer string, asn int, byASN map[int]string) (id, kind string) {
	if name, ok := byASN[asn]; ok {
		return name, "router"
	}
	return peer, "external"
}

func asLabel(asn int, asNames map[int]string) string {
	if n, ok := asNames[asn]; ok && n != "" {
		return n
	}
	return fmt.Sprintf("AS %d", asn)
}

// buildGraph turns sessions into nodes + merged edges. Pure.
func buildGraph(routers []Router, sessions []Session, asNames map[int]string) ([]Node, []Edge) {
	byASN := routerByASN(routers, asNames)
	nodes := make([]Node, 0, len(routers)+4)
	seen := map[string]bool{}
	for _, r := range routers {
		label := fmt.Sprintf("%s\nAS %d", r.Name, r.ASN)
		if r.ASN == 0 {
			label = r.Name
		}
		nodes = append(nodes, Node{ID: r.Name, Kind: "router", Label: label, ASN: r.ASN})
		seen[r.Name] = true
	}

	type pair struct {
		a, b string
	}
	edges := map[pair]*Edge{}
	order := []pair{}

	for _, s := range sessions {
		id, kind := peerNodeID(s.Peer, s.PeerASN, byASN)
		if !seen[id] {
			label := fmt.Sprintf("%s\n%s", asLabel(s.PeerASN, asNames), s.Peer)
			nodes = append(nodes, Node{ID: id, Kind: kind, Label: label, ASN: s.PeerASN, Addr: s.Peer})
			seen[id] = true
		}
		left, right := s.Router, id
		if left > right {
			left, right = right, left
		}
		k := pair{left, right}
		e, ok := edges[k]
		if !ok {
			e = &Edge{ID: left + "|" + right, Source: left, Target: right}
			edges[k] = e
			order = append(order, k)
		}
		cls := sessionClass(s)
		if e.State == "" {
			e.State = cls
		} else {
			e.State = worseState(e.State, cls)
		}
		if e.ARouter == "" || e.ARouter == s.Router {
			e.ARouter = s.Router
			e.APeer = s.Peer
			e.AUptime = s.Uptime
			e.APfxRcd = s.PfxRcd
			e.APfxSnt = s.PfxSnt
			e.AState = s.State
		} else {
			e.BRouter = s.Router
			e.BPeer = s.Peer
			e.BUptime = s.Uptime
			e.BPfxRcd = s.PfxRcd
			e.BPfxSnt = s.PfxSnt
			e.BState = s.State
		}
	}

	out := make([]Edge, 0, len(order))
	for _, k := range order {
		out = append(out, *edges[k])
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind == "router"
		}
		return nodes[i].ID < nodes[j].ID
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return nodes, out
}
