package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const holdDown = 30 * time.Second

type heldSession struct {
	Session
	goneAt time.Time
}

type poller struct {
	routers []RouterCfg
	asNames map[int]string
	poll    time.Duration
	client  *http.Client

	mu     sync.Mutex
	prev   Snapshot
	prevAt time.Time // when prev was taken, for the serve-time age
	ready  bool
	held   map[string]heldSession // router|peer
	lastOK map[string]routerView  // last successful poll per router
	// measuredTV is the tableVersion of the last tick that actually read each
	// router's table. See the note where it is consumed.
	measuredTV map[string]int
	// roles is static description text from DASHBOARD_ROLES, stamped onto each
	// router so the page can say what the node is for.
	roles map[string]string
}

// setRoles is separate from newPoller so the poller's signature, and every
// test that calls it, stays as it was: the roles are presentation text and
// change nothing the poller measures.
func (p *poller) setRoles(roles map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roles = roles
}

type routerView struct {
	asn      int
	routerID string
	sessions []Session
	routes   []Route
	seenAt   time.Time
}

func newPoller(routers []RouterCfg, asNames map[int]string, poll time.Duration) *poller {
	return &poller{
		routers:    routers,
		asNames:    asNames,
		poll:       poll,
		client:     &http.Client{Timeout: 3 * time.Second},
		held:       map[string]heldSession{},
		lastOK:     map[string]routerView{},
		measuredTV: map[string]int{},
	}
}

func (p *poller) tick(now time.Time) (Snapshot, []Event, bool) {
	type result struct {
		name    string
		url     string
		ok      bool
		summary frrSummary
		ipv4    frrIPv4
		// neighbors carries FRR's own timers. It is fetched separately and
		// NON-FATALLY: a router that answers the summary is reachable even if
		// this third call fails, and the page then shows the session without a
		// heartbeat rather than showing the router as down.
		neighbors map[string]frrNeighbor
	}
	ch := make(chan result, len(p.routers))
	var wg sync.WaitGroup
	for _, r := range p.routers {
		wg.Add(1)
		go func(r RouterCfg) {
			defer wg.Done()
			res := result{name: r.Name, url: r.URL}
			sumRaw, err := p.get(r.URL + "/show/bgp-summary")
			if err != nil {
				ch <- res
				return
			}
			sum, err := decodeSummary(sumRaw)
			if err != nil {
				ch <- res
				return
			}
			// The neighbors call runs BESIDE the ipv4 one, not after it. Warm
			// it costs 7ms (measured on leaf1: 30 calls, 0.21s, the same as
			// the 6x smaller summary — vtysh fork+exec dominates, not the
			// payload). But a bgpd that stops answering makes every call sit
			// on its 3s deadline, and in series that is one more full timeout
			// per router per tick against a 2s poll. Concurrent, the third
			// call costs nothing in either case. It stays behind a successful
			// summary, so a router that is already failing is not asked again.
			nbrCh := make(chan map[string]frrNeighbor, 1)
			go func() {
				raw, err := p.get(r.URL + "/show/bgp-neighbors")
				if err != nil {
					nbrCh <- nil
					return
				}
				nbr, err := decodeNeighbors(raw)
				if err != nil {
					nbrCh <- nil
					return
				}
				nbrCh <- nbr
			}()
			v4raw, err := p.get(r.URL + "/show/bgp-ipv4")
			if err != nil {
				<-nbrCh
				ch <- res
				return
			}
			v4, err := decodeIPv4(v4raw)
			if err != nil {
				<-nbrCh
				ch <- res
				return
			}
			res.ok = true
			res.summary = sum
			res.ipv4 = v4
			res.neighbors = <-nbrCh
			ch <- res
		}(r)
	}
	wg.Wait()
	close(ch)

	got := map[string]result{}
	for r := range ch {
		got[r.name] = r
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Deltas are measured against the PREVIOUS snapshot's own counters. A
	// session that was replayed stale last tick is skipped: its counters came
	// from an older poll, so subtracting them would report several ticks of
	// traffic as one tick's worth.
	prevSession := map[string]Session{}
	for _, s := range p.prev.Sessions {
		// GoneAt marks a session replayed out of the hold-down: its counters
		// are frozen at the tick it vanished, so a peer that comes back inside
		// the 30s window would have up to fifteen ticks of traffic reported as
		// one tick's worth.
		if !s.Stale && s.GoneAt == "" {
			prevSession[sessionKey(s)] = s
		}
	}
	// The router delta is measured against the last tick that actually READ
	// this router's table, which is not the same as the last tick it was
	// reachable: a router answering `{}` is Reachable with TableVersion 0, and
	// subtracting that zero reported the router's whole table version as one
	// tick's change.
	prevMeasuredTV := p.measuredTV
	p.measuredTV = map[string]int{}

	snap := Snapshot{
		TS:   nowRFC3339ms(now),
		Poll: p.poll.String(),
	}
	var sessions []Session
	var routes []Route

	for _, cfg := range p.routers {
		res := got[cfg.Name]
		rt := Router{Name: cfg.Name, URL: cfg.URL, Reachable: res.ok, Role: p.roles[cfg.Name]}
		if res.ok && res.summary.IPv4Unicast != nil {
			fam := res.summary.IPv4Unicast
			rt.ASN = fam.AS
			rt.RouterID = fam.RouterID
			rt.TableVersion = fam.TableVersion
			rt.DynamicPeers = fam.DynamicPeers
			rt.FailedPeers = fam.FailedPeers
			rt.PeerCount = fam.PeerCount
			p.measuredTV[cfg.Name] = fam.TableVersion
			if prevTV, ok := prevMeasuredTV[cfg.Name]; ok {
				rt.HasDelta = true
				rt.DTableVersion = fam.TableVersion - prevTV
			}
			var ss []Session
			for addr, peer := range fam.Peers {
				s := Session{
					Router:   cfg.Name,
					Peer:     addr,
					PeerASN:  peer.RemoteAS,
					State:    peer.State,
					Uptime:   peer.PeerUptime,
					PfxRcd:   peer.PfxRcd,
					PfxSnt:   peer.PfxSnt,
					Hostname: peer.Hostname,
					MsgRcvd:  peer.MsgRcvd,
					MsgSent:  peer.MsgSent,
					InQ:      peer.InQ,
					OutQ:     peer.OutQ,
					Flaps:    peer.ConnectionsDropped,
					Dynamic:  peer.DynamicPeer,
				}
				// FRR emits bgpTimerLastRead for EVERY peer, up or not, and for
				// a peer that is not Established it is not a heartbeat at all:
				// it is the peer's age. Measured on real FRR 10.7.1 with a
				// neighbour that never came up — bgpState "Active",
				// bgpTimerLastRead 23000 and then 64000 forty-one seconds
				// later, against a holdMsec of 9000. bgp_vty.c sums only
				// tm_sec+tm_min+tm_hour, dropping the day, so the value also
				// WRAPS every 24 h: a peer down for exactly a day reads 0,
				// which a heartbeat renders as "it just spoke". FRR's own
				// bgpState is what says whether the reading means anything.
				if nbr, ok := res.neighbors[addr]; ok && nbr.BGPState == "Established" && s.State == "Established" {
					s.HasTimers = true
					s.QuietMsec = nbr.LastReadMsec
					s.HoldMsec = nbr.HoldMsec
					s.KeepaliveMsec = nbr.KeepaliveMsec
					s.PeerGroup = nbr.PeerGroup
				}
				setSessionDelta(&s, prevSession[sessionKey(s)], prevSession)
				ss = append(ss, s)
			}
			var rs []Route
			for pfx, paths := range res.ipv4.Routes {
				for _, path := range paths {
					nh := ""
					if len(path.Nexthops) > 0 {
						nh = path.Nexthops[0].IP
					}
					rs = append(rs, Route{
						Router:   cfg.Name,
						Prefix:   pfx,
						Valid:    path.Valid,
						Bestpath: path.Bestpath.Set,
						Nexthop:  nh,
						Path:     path.Path,
						Origin:   path.Origin,
						LocPrf:   path.LocPrf,
						Metric:   path.Metric,
						Weight:   path.Weight,
						PeerID:   path.PeerID,
					})
				}
			}
			rt.LastSeen = nowRFC3339ms(now)
			p.lastOK[cfg.Name] = routerView{asn: rt.ASN, routerID: rt.RouterID, sessions: ss, routes: rs, seenAt: now}
			for _, s := range ss {
				delete(p.held, sessionKey(s))
			}
			sessions = append(sessions, ss...)
			routes = append(routes, rs...)
		} else if res.ok {
			// Answered, but no ipv4Unicast: FRR 10.7.1 prints `{}` with no
			// `router bgp` and `{ }` with an instance and no neighbours
			// (measured 2026-09-20). That is a reachable router with zero
			// sessions, not a lost one: its old sessions take the hold-down
			// below like any vanished peer. Only an unanswered poll replays
			// the last good view as stale.
			rt.LastSeen = nowRFC3339ms(now)
			p.lastOK[cfg.Name] = routerView{seenAt: now}
		} else if last, ok := p.lastOK[cfg.Name]; ok {
			rt.ASN = last.asn
			rt.RouterID = last.routerID
			if !last.seenAt.IsZero() {
				rt.LastSeen = nowRFC3339ms(last.seenAt)
			}
			for _, s := range last.sessions {
				s.Stale = true
				s.clearSignal()
				sessions = append(sessions, s)
			}
			routes = append(routes, last.routes...)
		}
		snap.Routers = append(snap.Routers, rt)
	}

	// Detect peers that vanished from a reachable router's table.
	prevByRouter := map[string][]Session{}
	for _, s := range p.prev.Sessions {
		prevByRouter[s.Router] = append(prevByRouter[s.Router], s)
	}
	liveNow := map[string]bool{}
	for _, s := range sessions {
		liveNow[sessionKey(s)] = true
	}
	for _, rt := range snap.Routers {
		if !rt.Reachable {
			continue
		}
		for _, s := range prevByRouter[rt.Name] {
			k := sessionKey(s)
			if liveNow[k] {
				continue
			}
			if _, ok := p.held[k]; !ok {
				s.State = "Idle"
				s.Stale = false
				s.GoneAt = nowRFC3339ms(now)
				s.clearSignal()
				p.held[k] = heldSession{Session: s, goneAt: now}
			}
		}
	}
	for k, h := range p.held {
		if now.Sub(h.goneAt) >= holdDown {
			delete(p.held, k)
			continue
		}
		if liveNow[k] {
			delete(p.held, k)
			continue
		}
		h.State = "Idle"
		sessions = append(sessions, h.Session)
	}

	snap.Sessions = sessions
	snap.Routes = routes
	snap.Nodes, snap.Edges = buildGraph(snap.Routers, sessions, p.asNames)
	for _, r := range snap.Routers {
		if r.Reachable {
			snap.Reachable++
		}
	}
	snap.RouterCount = len(snap.Routers)
	byASN := routerByASN(snap.Routers, p.asNames)
	for _, s := range sessions {
		if _, ok := byASN[s.PeerASN]; ok {
			snap.SessionCount++
			if s.State == "Established" && !s.Stale {
				snap.Established++
			}
		} else {
			snap.ServerSessions++
			if s.State == "Established" && !s.Stale {
				snap.ServerEstablished++
			}
		}
	}
	for _, n := range snap.Nodes {
		if n.Kind == "external" {
			snap.External++
		}
	}

	events := diff(p.prev, snap, now)
	stateChanged := stateSignature(p.prev) != stateSignature(snap)
	p.prev = snap
	p.prevAt = now
	p.ready = true
	return snap, events, stateChanged
}

// stateSignature decides whether a tick is broadcast as type=state. It covers
// the graph (nodes, edges and their states, reachability) AND the routes: a
// route-only change — a withdrawal, a bestpath move, an ECMP path — leaves the
// graph as it was, and the RIB pane would keep showing a prefix the Events
// pane had just reported withdrawn (measured 2026-09-20). Routes are sorted
// first: the slice is built from FRR's JSON maps, whose iteration order is
// random per tick.
func stateSignature(s Snapshot) string {
	return graphSignature(s) + "/" + routeSig(s.Routes)
}

func routeSig(routes []Route) string {
	keys := make([]string, 0, len(routes))
	for _, r := range routes {
		keys = append(keys, fmt.Sprintf("%s|%s|%s|%v|%v|%s|%s|%d|%d|%d|%s",
			r.Router, r.Prefix, r.Nexthop, r.Bestpath, r.Valid, r.Path, r.Origin, r.LocPrf, r.Metric, r.Weight, r.PeerID))
	}
	sort.Strings(keys)
	return strings.Join(keys, ";")
}

func graphSignature(s Snapshot) string {
	return fmt.Sprintf("%d/%d/%d/%s/%s", len(s.Nodes), len(s.Edges), s.Reachable, edgeSig(s.Edges), nodeSig(s.Nodes))
}

func edgeSig(edges []Edge) string {
	out := ""
	for _, e := range edges {
		out += e.ID + ":" + e.State + ";"
	}
	return out
}

func nodeSig(nodes []Node) string {
	out := ""
	for _, n := range nodes {
		out += n.ID + ":" + n.Kind + ";"
	}
	return out
}

func (p *poller) get(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return body, nil
}

func (p *poller) snapshot() (Snapshot, bool) {
	return p.snapshotAt(time.Now())
}

// snapshotAt stamps how old the snapshot is AT THE MOMENT IT IS SERVED, which
// is the number the page needs: if the poll loop stalls, every field below is
// still the last good reading and only this one says so. A browser cannot work
// it out from TS without trusting its own clock against the server's.
func (p *poller) snapshotAt(now time.Time) (Snapshot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snap := p.prev
	if !p.prevAt.IsZero() {
		snap.AgeMsec = now.Sub(p.prevAt).Milliseconds()
	}
	return snap, p.ready
}

// clearSignal drops every field that is a measurement OF THIS TICK. It is
// called on a session the poll did not read this tick — one replayed stale
// because its router did not answer, and one replayed out of the hold-down
// because it vanished from the table.
//
// The last-known counters (MsgRcvd, PfxRcd, Flaps) stay: they are labelled by
// Stale or GoneAt and the page shows them as last-known. The per-tick deltas
// and FRR's timers cannot survive that, because there is no tick behind them.
// Without this a held session was replayed with HasDelta true and a one-second
// heartbeat for the whole 30s it was gone — and Stale is deliberately false on
// a held session, so the page had nothing to gate on.
func (s *Session) clearSignal() {
	s.HasDelta = false
	s.DRcvd, s.DSent, s.DPfxRcd, s.DPfxSnt = 0, 0, 0, 0
	s.HasTimers = false
	s.QuietMsec, s.HoldMsec, s.KeepaliveMsec = 0, 0, 0
	s.InQ, s.OutQ = 0, 0
}

// setSessionDelta fills the per-tick deltas from the previous sample of the
// same session.
//
// A counter that went DOWN means the session was reset between polls (bgpd
// restarts these at zero), so there is no interval to measure and HasDelta
// stays false — reporting a negative pulse, or clamping it to zero, would both
// be inventions. The flap itself is still visible: FRR's connectionsDropped
// survives the reset and is carried as Flaps.
func setSessionDelta(s *Session, prev Session, have map[string]Session) {
	if _, ok := have[sessionKey(*s)]; !ok {
		return // first sight of this session: nothing to subtract
	}
	if s.MsgRcvd < prev.MsgRcvd || s.MsgSent < prev.MsgSent {
		return
	}
	s.HasDelta = true
	s.DRcvd = s.MsgRcvd - prev.MsgRcvd
	s.DSent = s.MsgSent - prev.MsgSent
	s.DPfxRcd = s.PfxRcd - prev.PfxRcd
	s.DPfxSnt = s.PfxSnt - prev.PfxSnt
}
