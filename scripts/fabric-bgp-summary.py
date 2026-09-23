#!/usr/bin/env python3
"""Parse FRR `show bgp summary json`. State is an exact field match
(`state` / `peerState` / `bgpState` == "Established"), never a substring
of the blob. usage:
  python3 scripts/fabric-bgp-summary.py                 # print ip<TAB>state<TAB>remoteAs
  python3 scripts/fabric-bgp-summary.py --require IP …  # exit 0 iff each IP is Established
Reads JSON from stdin. Invalid JSON → exit 2.
"""
import json, sys


def peers_from(data):
    if not isinstance(data, dict):
        return {}
    if "peers" in data and isinstance(data["peers"], dict):
        return data["peers"]
    out = {}
    for v in data.values():
        if isinstance(v, dict):
            out.update(peers_from(v))
    return out


def state_of(peer):
    if not isinstance(peer, dict):
        return ""
    for k in ("state", "peerState", "bgpState"):
        val = peer.get(k)
        if isinstance(val, str) and val:
            return val
    return ""


def remote_as(peer):
    if not isinstance(peer, dict):
        return ""
    for k in ("remoteAs", "remoteAS"):
        if k in peer:
            return str(peer[k])
    return ""


def main(argv):
    raw = sys.stdin.read()
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        print("fabric-bgp-summary: not JSON: %s" % e, file=sys.stderr)
        return 2
    peers = peers_from(data)
    require = []
    args = argv[1:]
    if args and args[0] == "--require":
        require = args[1:]
        args = []
    if args:
        print("usage: fabric-bgp-summary.py [--require IP …]", file=sys.stderr)
        return 2
    for ip, peer in sorted(peers.items()):
        print("%s\t%s\t%s" % (ip, state_of(peer), remote_as(peer)))
    if not require:
        return 0
    failed = 0
    for ip in require:
        peer = peers.get(ip)
        st = state_of(peer) if peer is not None else "ABSENT"
        if st != "Established":
            print("require %s: %s" % (ip, st), file=sys.stderr)
            failed = 1
    return failed


if __name__ == "__main__":
    sys.exit(main(sys.argv))
