# The images, and what each one is for

Five images take part in a run. **Two are built here**, one is the router
itself, one exists only while compiling, and one is a toolbox. Nothing in the
list is a fork of anything.

```text
  BUILD TIME — these never run in the lab
  ┌──────────────────────┐
  │ golang:1.25-alpine   │  compiles both Go binaries, then is thrown away
  └──────────┬───────────┘
             │ COPY --from=build
             ▼
  RUNTIME — these are what `docker compose up` starts
  ┌─────────────────────────────────┐        ┌──────────────────────────────┐
  │ quay.io/frrouting/frr:10.7.1    │        │ alpine:3.22                  │
  │ 309 MB · the actual router      │        │ 13.6 MB · a libc and a shell │
  └──────────────┬──────────────────┘        └──────────────┬───────────────┘
                 │ + frr-agent (5.3 MB)                     │ + dashboard (7.1 MB)
                 │ + fabric-router-start (912 B)            │ + uid 65532
                 │ + iptables                               │
                 ▼                                          ▼
  ┌─────────────────────────────────┐        ┌──────────────────────────────┐
  │ bgp-fabric-agent       326 MB   │        │ bgp-fabric-dashboard 23.4 MB │
  │ 11 layers (base has 7)          │        │ 4 layers                     │
  │ ×4 — edge, spine, leaf1, leaf2  │        │ ×1                           │
  └─────────────────────────────────┘        └──────────────────────────────┘
                 ▲                                          │
                 └──────────── HTTP GET, every 2 s ─────────┘
                        on the management LAN only

  ┌──────────────────────────────┐
  │ nicolaka/netshoot:v0.16      │  918 MB · client0, and tcpdump on the wire
  └──────────────────────────────┘
```

## The two we build

### `bgp-fabric-agent` — one per router

**Role: let something read a router without giving it the router.**

A dashboard needs `show bgp summary` from four routers every two seconds. The
obvious ways to get it are all worse than they look: `docker exec` needs the
Docker socket, which is the whole machine; SSH needs an account and a key on
every router; FRR's own northbound needs configuration this lab does not
have. So each router carries a 163-line Go server that answers a fixed list
of `show` commands over HTTP, and nothing else.

It is the **upstream FRR image plus 5.3 MB**: four extra layers on the base's
seven, and `bgpd --version` still reports `10.7.1_git`. FRR is not rebuilt,
forked or patched — the image's own `org.opencontainers.image.base.name`
label says which image it was added to.

What it will do:

```text
GET /show/bgp-summary     →  vtysh -c 'show bgp summary json'
GET /show/bgp-ipv4        →  vtysh -c 'show bgp ipv4 unicast json'
GET /show/bgp-neighbors   →  vtysh -c 'show bgp neighbors json'
GET /show/ip-route        →  vtysh -c 'show ip route json'
GET /show/interface       →  vtysh -c 'show interface brief json'
GET /healthz
```

That is the complete list. The URL names a **key** in a Go map; the **value**
is what reaches `vtysh`. Nothing from the URL is ever interpolated into a
command, so there is no string to escape and no escaping to get wrong —
`/show/bgp-summary%3Breboot` is not a command with a semicolon in it, it is a
key that is not in the map, and it returns 404.

Three more things it does not do:

- **No write path.** No `configure terminal`, no `clear`, no shell. The
  process could not change the router if it were told to.
- **It drops privilege.** `fabric-router-start` launches the agent as uid 100
  (`frr`) with `su -s /bin/sh frr -c`, then execs FRR's own entrypoint. That
  method was not the first choice — this FRR image has no `su-exec`, its
  BusyBox `setpriv` has no `--reuid`, and its BusyBox `chroot` has no
  `--userspec`, all measured rather than assumed.
- **It answers one LAN.** `FRR_AGENT_ADDR` is required with no default, and
  the container's `iptables` rules drop the data plane's path to it. Binding
  a management address is *not* a boundary on Linux — under the weak host
  model a packet addressed to any local address is accepted on any interface
  — so the agent also enforces a source allow-list in-process. Two mechanisms
  because the obvious one is not a mechanism at all.

### `bgp-fabric-dashboard` — one per fabric

**Role: turn four routers' answers into one page, and hold nothing else.**

A 7.1 MB static Go binary on `alpine:3.22`, four layers, running as uid
65532, listening on 8080 and published to `127.0.0.1:8098` only. It polls the
four agents over the management LAN and serves:

```text
GET /               the page (embedded, no CDN — Cytoscape is vendored)
GET /api/state      the whole current picture
GET /api/signal     what changed, for the header
GET /api/events     session and route events since a mark
GET /api/version    which build is serving this page
GET /healthz
GET /ws             the same frames, pushed
```

**It has no Docker socket, no shell and no write path.** The only thing it can
reach is four HTTP endpoints that themselves only read. If someone got the
dashboard, they would have what any reader of the page already has.

## The three we do not build

| Image | Size | Role | Runs in the lab? |
|---|---|---|---|
| `quay.io/frrouting/frr:10.7.1` | 309 MB | **the routers.** bgpd, zebra, vtysh — the thing the whole lab is about | yes, as the base of every agent image |
| `golang:1.25-alpine` | — | compiles both binaries in a build stage | **no** — `COPY --from=build` takes the binary and the stage is discarded |
| `alpine:3.22` | 13.6 MB | a libc and a shell under the dashboard binary | yes, as the dashboard's base |
| `nicolaka/netshoot:v0.16` | 918 MB | `client0`, the host that pings loopbacks; and `tcpdump` in a router's network namespace for the TCP-MD5 wire capture | yes, as `client0` and as a throwaway |

netshoot is 40× the dashboard because it is a toolbox, not a service. It is
in the lab to *look at* the network, never to be part of it — nothing routes
through it and nothing depends on its contents but `ping`, `ip` and
`tcpdump`.

## What ships, and where

Both of ours are published by CI, multi-arch (`linux/amd64`, `linux/arm64`),
tagged with the commit that built them:

```text
quay.io/ephico2real/bgp-fabric-agent:sha-<short>       and :main
quay.io/ephico2real/bgp-fabric-dashboard:sha-<short>   and :main
```

Locally the same builds are tagged `bgp-fabric-agent:local` and
`bgp-fabric-dashboard:local`, which is what `fabric-up.sh` asks compose for.

Every build is stamped, and the stamp is **computed, never typed**:

```bash
docker inspect -f '{{json .Config.Labels}}' bgp-fabric-agent:local
```

```json
{
  "org.opencontainers.image.base.name": "quay.io/frrouting/frr:10.7.1",
  "org.opencontainers.image.created": "2026-09-23T20:53:58Z",
  "org.opencontainers.image.revision": "cda9c7c11e204e0abeccc68441ae9e5a417c4a71",
  "org.opencontainers.image.title": "bgp-fabric-agent"
}
```

The same two build arguments become the OCI labels **and** the values the
dashboard serves on `/api/version`, so `docker inspect` and the page cannot
disagree about which build is running. `ARG` is per stage in a Containerfile,
so both final stages re-declare `REVISION` — without that the binary is right
and the label is silently blank, which is the failure this whole mechanism
exists to prevent.

## Why the agent exists at all

It replaced a Docker socket.

The straightforward way to build a live BGP page is to mount
`/var/run/docker.sock` into it and run `docker exec <router> vtysh -c …`. It
works immediately, and it hands whatever is serving that page the ability to
start a privileged container on the host — which is the host. A page with a
bug in it is then a machine with a bug in it.

Four small read-only servers on a LAN that carries nothing else cost 5.3 MB
per router and remove that entirely. The dashboard cannot do anything to a
router that a `show` command cannot do, because a `show` command is all it can
send.
