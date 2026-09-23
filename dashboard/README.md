# bgp-dashboard

Live topology of the company fabric. Idea from Gergő Vadász,
[Make BGP visible: a live topology dashboard with Containerlab](https://gergovadasz.hu/make-bgp-visible-a-live-topology-dashboard-with-containerlab/)
— clean-room Go implementation, not a fork (the source has no licence).

A fifth compose service on the out-of-band management LAN `mgmt` at
`10.200.200.100`, published on the Mac as `http://127.0.0.1:8098/`. It
polls each router's show-only HTTP agent on that LAN
(`10.200.200.{1,2,11,12}:8080`). No Docker socket. No auth — the
fabric networks are the boundary (lab).

Cluster nodes (kube-vip AS 65021, MetalLB 65022, later Cilium
65001/65002) appear as external leaves, keyed by **peer address**, never
by ASN.

The page fetches `/api/state` and `/api/events?since=0` over HTTP and
renders them before it opens `/ws`. Measured 2026-09-20 on
`quay.io/frrouting/frr:10.7.1`: Alpine 3.22.5, user `frr` uid 100 gid
101, group `frrvty` 102; no `su-exec`; BusyBox `setpriv` has no
`--reuid`; `su -s /bin/sh frr -c` works and `vtysh` as uid 100 returns
JSON (rc=0). FRR JSON field names (`ipv4Unicast.as`,
`peers.<addr>.{remoteAs,state,peerUptime,pfxRcd,pfxSnt,hostname,idType}`,
`routes.<pfx>[].{valid,bestpath,nexthops[].ip,path,origin,locPrf,metric,weight,peerId}`)
are from a throwaway router, not guessed. Cytoscape 3.34.3 is vendored
(MIT; sha256 in `static/vendor/VERSIONS`). `github.com/coder/websocket`
v1.8.15 is ISC. The `bgp-fabric-dashboard:local` image (built this run) is
Alpine 3.22 with BusyBox `wget` at `/usr/bin/wget`, `USER` 65532, no
`su-exec`.

## Endpoints

| Path | What |
|---|---|
| `/` | embedded UI (Cytoscape, no CDN) |
| `/api/state` | latest snapshot |
| `/api/events?since=<id>` | event ring (500) |
| `/healthz` | 200 once every router has answered once; 503 before |
| `/ws` | `event` / `state` on graph change (HTTP already painted the first frame) |

## Env

`DASHBOARD_ROUTERS`, `DASHBOARD_AS_NAMES`, `DASHBOARD_POLL` (2s),
`DASHBOARD_LISTEN` (`:8080`), `DASHBOARD_EVENTS` (500).
