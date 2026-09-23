# frr-agent

Read-only HTTP front for FRR's `vtysh`. `GET /show/{name}` looks `name` up
in a fixed allow-list; nothing from the URL reaches the command line.

No auth — the fabric networks are the boundary (lab). No port is
published on the routers; the dashboard reaches
`10.200.200.{1,2,11,12}:8080` on the out-of-band management LAN.
`client0` on wan has no path to that subnet.

`FRR_AGENT_ADDR` is required (the management address compose attaches).
The process retries the bind every 500 ms for 120 s. Loopbacks stay
BGP's; zebra still programs them from `frr.conf`.

Measured 2026-09-20 on `quay.io/frrouting/frr:10.7.1`: start as uid 100
(`su -s /bin/sh frr`) — `vtysh` rc=0. No `su-exec` in the image.
