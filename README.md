# bgp-fabric

A four-router BGP fabric in containers, with a dashboard that reports the
state of the network rather than the state of itself.

Real FRR routers on real Linux bridges, eBGP between them, every session
signed with TCP MD5, and a live page that polls each router over an
out-of-band management LAN and draws what it finds. It runs the same way on
macOS and on Linux, and the whole thing comes up with one command.

It exists to be peered into. A Kubernetes cluster — kube-vip, MetalLB, Cilium
— can dial a leaf and announce its service addresses, and this fabric will
accept them, filter them by prefix-list, and show them arriving.

## What you get

- Four FRR 10.7.1 routers — `edge`, `spine`, `leaf1`, `leaf2` — in four
  autonomous systems (65000, 65100, 65101, 65102), converging from cold in
  seconds.
- Six fabric sessions, each **signed with TCP MD5**, proven three ways: the
  option on the wire, the kernel's `TcpExtTCPMD5*` counters, and a deliberate
  wrong password that takes one session down and leaves the other five up.
- A dashboard on `127.0.0.1:8098` that reaches routers over a management LAN
  and holds **no Docker socket, no shell, and no write path** to anything.
- Dynamic peering: the leaves `listen` for servers on a range instead of
  naming neighbours, so a cluster's nodes can arrive and leave on their own.
- RFC 8212 in effect — nothing is advertised or accepted without an explicit
  route-map, so a mistake is silence rather than a leak.
- Seventeen rows from `check.sh`, each a claim with a measurement behind it;
  the exit status is the FAIL count, so CI needs no parsing.
- 103 unit tests (54 Go for the dashboard, 8 Go for the agent, 41 for the
  browser UI) that need no lab, no registry and no browser.

## Architecture

```text
                                     10.200.100.0/24  wan
                          client0 ●───────────────────┐
                        .10                           │ .2
                                               ┌──────┴──────┐
                                               │    edge     │  AS 65000
                                               │ lo .255.1   │
                                               └──────┬──────┘
                                                      │ .19
                                    link-spine-edge   │ 10.200.1.16/29
                                                      │ .18
                                               ┌──────┴──────┐
                                               │    spine    │  AS 65100
                                               │ lo .255.2   │
                                               └──┬───────┬──┘
                                               .3 │       │ .11
                    link-leaf1-spine 10.200.1.0/29│       │10.200.1.8/29  link-leaf2-spine
                                               .2 │       │ .10
                                      ┌───────────┴─┐   ┌─┴───────────┐
                            AS 65101  │    leaf1    │   │    leaf2    │  AS 65102
                                      │ lo .255.11  │   │ lo .255.12  │
                                      └──────┬──────┘   └──────┬──────┘
                                             ╎                 ╎
                                             ╎  bgp listen range 172.20.0.0/17
                                             ╎  peer-group SERVERS (limit 16)
                                             ╎                 ╎
                                       servers dial in — nothing is named here

  ─────────────────────────────────────────────────────────────────────────────
  10.200.200.0/24  mgmt — out of band. The routers do not route it.

     edge .1      spine .2      leaf1 .11      leaf2 .12        dashboard .100
        │            │             │              │                   │
        └────────────┴──────┬──────┴──────────────┘                   │
                            │  GET /bgp/summary, /bgp/neighbors, …    │
                            └──────────────────◄──────────────────────┘
                                                     every 2 s

                                          dashboard ──► 127.0.0.1:8098 (browser)
```

| Name | Address | What it is | Who answers |
|---|---|---|---|
| `edge` | `10.200.1.19`, wan `10.200.100.2`, lo `10.200.255.1` | AS 65000, the way out | FRR + agent on `10.200.200.1:8080` |
| `spine` | `10.200.1.3` / `.18`, lo `10.200.255.2` | AS 65100, transit | FRR + agent on `10.200.200.2:8080` |
| `leaf1` | `10.200.1.2`, lo `10.200.255.11` | AS 65101, where servers attach | FRR + agent on `10.200.200.11:8080` |
| `leaf2` | `10.200.1.10`, lo `10.200.255.12` | AS 65102, the second one | FRR + agent on `10.200.200.12:8080` |
| `client0` | `10.200.100.10` | a host on the wan, for pinging loopbacks | netshoot |
| `dashboard` | `10.200.200.100`, published `127.0.0.1:8098` | polls the four agents, serves the page | Go, no daemon socket |

Three point-to-point `/29`s, one wan `/24`, one management `/24`. The
management LAN is not redistributed and not routed: the only thing on it is
the dashboard and the four agents.

## Why Colima

The fabric needs a **Linux kernel that was built with `CONFIG_TCP_MD5SIG`**.
TCP MD5 is not something FRR does in user space — it is a `setsockopt` the
kernel either implements or refuses, and a fabric whose sessions are not
actually signed is a fabric that only looks signed.

That rules out the Docker Desktop VM, whose LinuxKit kernel does not carry
the option. [Colima](https://github.com/abiosoft/colima) runs a normal Ubuntu
kernel — measured here as `6.8.0-117-generic`, `CONFIG_TCP_MD5SIG=y` — and it
is a plain `brew install colima`, no licence, no account.

It also makes one repository work on both machines an engineer is likely to
have:

| | macOS | Linux |
|---|---|---|
| where the kernel is | inside the Colima VM | the host's own |
| what `scripts/fabric-up.sh` does first | creates/starts the profile | nothing — there is no VM |
| how the scripts read the kernel | `colima ssh` | `sh -c` |
| what changes in the lab | nothing | nothing |

`scripts/fabric-lib.sh` decides that with one predicate, `fabric_manages_vm`,
and everything downstream is identical. On Linux — and on a CI runner — set
`FABRIC_ANY_CONTEXT=1` and the same `apply.sh` runs against the host engine.

Colima has two constraints worth knowing before the first run, because
neither can be undone afterwards: **`vmType` and `mountType` are frozen at
creation**, and **the disk can only grow**. `fabric-up.sh` therefore passes
those flags only when it is creating the profile.

## Run it

```bash
./apply.sh
```

On macOS without Docker Desktop, install the engine's CLI and its two plugins
first — `docker compose` and `docker buildx` are plugins, and they arrive with
Desktop rather than with Colima:

```bash
brew install colima docker docker-compose docker-buildx
```

Homebrew then asks you to tell the Docker CLI where the plugins are, by adding
this to `~/.docker/config.json`:

```json
{ "cliPluginsExtraDirs": ["/opt/homebrew/lib/docker/cli-plugins"] }
```

`apply.sh` creates the VM if it is missing, builds the two images, brings the
six containers up, waits for the six sessions, records everything it did into
`output/transcript.txt`, and photographs the dashboard.

```bash
./check.sh                # exit status is the FAIL count
open http://127.0.0.1:8098
./cleanup.sh              # stops the project; never deletes the VM
```

On Linux, or anywhere with a single Docker engine:

```bash
FABRIC_ANY_CONTEXT=1 CTX=default ./apply.sh
```

### The knobs

| Variable | Default | What it changes |
|---|---|---|
| `CTX` | `colima-bgp-fabric` | the Docker context every call names |
| `FABRIC_ANY_CONTEXT` | `0` | `1` accepts whatever `CTX` names, and skips the VM |
| `FABRIC_COLIMA_PROFILE` | `bgp-fabric` | the Colima profile behind that context |
| `FABRIC_PROJECT` | `bgp-fabric` | the compose project name |
| `FABRIC_DASHBOARD_PORT` | `8098` | published on `127.0.0.1` only |
| `FABRIC_BGP_PASSWORD` | `lab-bgp` | the MD5 password, from `fabric/.env` |
| `FABRIC_REBUILD` | `0` | `1` rebuilds both images even if they exist |

## The images

Five take part in a run; two are built here. **[docs/IMAGES.md](docs/IMAGES.md)
explains what each one is for** — including the three we do not build, and why
the agent exists at all.

| Image | Base | What we add | Size | Published |
|---|---|---|---|---|
| `bgp-fabric-agent` | `quay.io/frrouting/frr:10.7.1` | one static Go binary (5.3 MB) and one start script | 326 MB | `quay.io/ephico2real/bgp-fabric-agent` |
| `bgp-fabric-dashboard` | `alpine:3.22` | one static Go binary (7.1 MB), uid 65532 | 23.4 MB | `quay.io/ephico2real/bgp-fabric-dashboard` |
| `quay.io/frrouting/frr:10.7.1` | — | nothing; it is the router | 309 MB | upstream |
| `golang:1.25-alpine` | — | build stage only, never shipped | — | upstream |
| `nicolaka/netshoot:v0.16` | — | `client0` and `tcpdump` on the wire | 918 MB | upstream |

Both of ours are pushed by CI for `linux/amd64` and `linux/arm64`, tagged
`sha-<short>` and `main`.

**FRR is not rebuilt, forked or patched.** The agent image is the upstream FRR
image with a read-only HTTP service added, and `docker inspect` says so:

```bash
docker inspect -f '{{json .Config.Labels}}' bgp-fabric-agent:local
```

```json
{
  "org.opencontainers.image.base.name": "quay.io/frrouting/frr:10.7.1",
  "org.opencontainers.image.created": "2026-09-23T20:53:58Z",
  "org.opencontainers.image.revision": "3b56509c68a6c9b4e5406f4215abe6a413d971d6",
  "org.opencontainers.image.title": "bgp-fabric-agent"
}
```

The revision is **computed, never typed**: `scripts/build-revision.sh` asks
git, and appends `-dirty` when the tree has uncommitted changes. A sha passed
by hand is an assertion nobody checks — this lab once served a page labelled
with a commit whose tree contained neither the endpoint nor the function
rendering the label, which is why the stamp now comes from `git` and reaches
both the OCI label and `/api/version` from the same build argument. The two
cannot disagree.

### What the agent will do, and what it will not

`frr-agent` is a Go HTTP server that runs `vtysh -c '<command>'` and returns
the JSON. Its safety is structural, not textual:

- The URL names a **key** in a fixed map; the **value** is what runs. There is
  no user-supplied text anywhere near a shell.
- It binds `FRR_AGENT_ADDR` on the management LAN. Binding an address is not a
  boundary on Linux — the weak host model accepts a packet for any local
  address on any interface — so the container's `iptables` rules drop the
  data plane's path to it, and the agent enforces a source allow-list itself.
- No write commands, no `configure terminal`, no `clear`, no shell.

The dashboard holds the browser's end: it never talks to a Docker daemon, and
it has no socket to hand anyone.

## The tools, and what each is for

| Tool | Version here | Why it is in this repo |
|---|---|---|
| [FRR](https://frrouting.org/) | 10.7.1 | the routers. `show ip bgp detail json` is the only source of truth the dashboard has |
| [Colima](https://github.com/abiosoft/colima) | 0.10.3 | a Linux kernel with `CONFIG_TCP_MD5SIG` on macOS; not needed on Linux |
| Docker engine | 29.5.2 | containers and the five bridges. `compose` and `buildx` are CLI plugins — without Docker Desktop they must be installed separately |
| Go | 1.25 | the dashboard and the agent. Static binaries, no runtime in the image |
| [coder/websocket](https://github.com/coder/websocket) | v1.8.15 | the dashboard's only Go dependency |
| [Cytoscape.js](https://js.cytoscape.org/) | 3.34.3, vendored | the graph. Vendored with its sha256 in `dashboard/static/vendor/VERSIONS`, so the page loads no third-party script at runtime |
| Node | 20 or newer | `node --test` for the UI helpers. Nothing is bundled, transpiled or minified |
| [netshoot](https://github.com/nicolaka/netshoot) | v0.16 | `tcpdump` on the wire and `ping` from `client0` |
| shellcheck | any | every shell script, at `-S warning`, in CI |
| Chrome/Chromium | optional | `apply.sh` photographs the dashboard when `CHROME` points at one; without it the run records that it skipped and passes |

## What the dashboard reports

The header is the point of the whole thing.

![the dashboard, photographed by CI on a clean runner](https://raw.githubusercontent.com/ephico2real2/bgp-fabric/ci-captures/2026-09-23T2058Z_run35919205129-1/dashboard-steady.png)

```text
poll 2s   routers 4/4   fabric sessions 6/6 · server sessions 0/0   age 0s   ● live
```

That is not a mock-up: it is the page as a GitHub runner saw it in
[run 35919205129](https://github.com/ephico2real2/bgp-fabric/actions/runs/35919205129),
built from commit `ee30b73`, which is the number the header itself is
reporting on the right.

Every number there is a fact about the **network**. `routers 4/4` means four
agents answered this poll. `fabric sessions 6/6` means six eBGP sessions are
Established right now, counted from `show bgp summary json` on each router.
`age 0s` is how long ago the newest of those answers arrived, and it ticks on
its own clock — a page that repaints only when something changes will
otherwise show a stale reading with a confident face. `server sessions 0/0`
is the fabric on its own; the count rises as servers dial into the leaves'
listen range. Measured here on a host with two Kubernetes clusters attached,
it reads `server sessions 8/8`.

A status that says "connected" is a fact about the page — it tells you a
socket is open, which is the one thing you could have guessed from the page
being on your screen at all. This one starts from what an operator came to
find out, and it never reports its own health as if it were the network's.

The same discipline runs down the page: a session that goes down turns red
and stays red until it comes back; a session that disappears from a router's
table is held for 30 seconds and then removed on a timer of its own, because
the events that would have swept it only arrive when something else changes.

```bash
curl -s http://127.0.0.1:8098/api/state | python3 -m json.tool | head -20
curl -s http://127.0.0.1:8098/api/version
```

## Peering a cluster into it

The leaves do not name their neighbours. They listen:

```text
bgp listen range 172.20.0.0/17 peer-group SERVERS
bgp listen limit 16
```

Any node in that range that dials `10.200.1.2` (leaf1) or `10.200.1.10`
(leaf2) with the right ASN and password becomes a session, and what it may
announce is decided by prefix-list, per cluster:

| Prefix-list | Block | For |
|---|---|---|
| `EG-POC1-VIPS` | `10.198.0.0/26` | the first Envoy Gateway cluster |
| `EG-POC2-VIPS` | `10.198.0.64/26` | the second |
| `EG-ANYCAST-VIPS` | `10.198.0.192/26` | an address both may announce |
| `CILIUM-POC1-VIPS` | `10.199.0.0/26` | the first Cilium cluster |
| `CILIUM-POC2-VIPS` | `10.199.0.64/26` | the second |
| `CILIUM-ANYCAST-VIPS` | `10.199.0.192/26` | an address both may announce |
| `COMPANY` | `10.200.0.0/16` | the fabric's own |

A `/26` per cluster, and an anycast `/26` that more than one may announce:
that is how "both clusters advertise the same VIP" stays a deliberate act and
not an accident. RFC 8212 means an address outside every list is not rejected
loudly — it is simply never accepted.

## Layout

```text
apply.sh check.sh cleanup.sh   bring it up · judge it · take it down
fabric/                        compose.yaml, the five bridges, four frr.conf
dashboard/                     the Go server, the page, 54 tests
frr-agent/                     the show-only agent, 8 tests
scripts/                       fabric-up/down/status, build-revision, record
tests/                         the three suites CI runs before any lab exists
.github/workflows/             the lab, on a runner, on every push
```

`output/` is written by a run and is not committed.

## Tests and CI

```bash
tests/fabric-dashboard-unit.sh   # go test + go vet, both modules
tests/dashboard-ui-unit.sh       # node --check + node --test on the UI helpers
tests/dashboard-build-args.sh    # the up-script really passes REVISION/BUILT
```

`.github/workflows/fabric-ci.yml` runs those three first — they need no lab, so a
logic regression fails in seconds rather than after a ten-minute bring-up —
then builds both images, reads the OCI labels back off them, brings the whole
fabric up on the runner's own engine, runs `check.sh`, photographs the
dashboard and publishes the pictures to a `ci-captures` branch. A pull request
without registry credentials skips the publish and still goes green.

The first run of it, on a clean `ubuntu-24.04` runner with no Colima anywhere:

```text
fabric-up: sessions Established after 1s
fabric-up: dashboard ready after 0s (routers=4/4 sessions=6/6 external=0)
PASS   sessions signed on the wire          md5-option packets=10/10 on 10.200.1.3
PASS   a wrong password breaks the session  Established→Idle, down in 15/15 samples; restored
PASS   kernel has CONFIG_TCP_MD5SIG         CONFIG_TCP_MD5SIG=y kernel=6.17.0-1022-azure
bgp-fabric check: 0 FAIL
```

## Licence

MIT. See [LICENSE](LICENSE).
