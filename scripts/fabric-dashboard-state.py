#!/usr/bin/env python3
"""Print one line from a dashboard /api/state JSON on stdin:

  routers=4/4 sessions=6/6 external=N

The counts come from the records, not the counters. Exit 0 only when every
configured router (--routers, default edge,spine,leaf1,leaf2) is listed and
reachable, the session list is non-empty, and the fabric-session counts
derived from it (peerAsn is a router's ASN; Established and not stale) equal
the snapshot's own established/sessionCount, and the snapshot's ts is no
older than --max-age (default four polls: a frozen snapshot proves nothing).
Exit 1 otherwise; invalid JSON exits 2. (Measured 2026-09-20: {"reachable":1,"routerCount":1,
"established":6,"sessionCount":6,"sessions":[]} passed the old comparison
of reachable == routerCount.)
"""
import argparse
import datetime
import json
import re
import sys

DEFAULT_ROUTERS = "edge,spine,leaf1,leaf2"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--routers", default=DEFAULT_ROUTERS,
                    help="comma-separated router names that must all be reachable")
    ap.add_argument("--max-age", type=float, default=None,
                    help="oldest acceptable snapshot ts in seconds (default: four polls)")
    args = ap.parse_args()
    want = [n for n in args.routers.split(",") if n]
    raw = sys.stdin.read()
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        print("fabric-dashboard-state: not JSON: %s" % e, file=sys.stderr)
        return 2
    if not isinstance(data, dict):
        print("fabric-dashboard-state: not an object", file=sys.stderr)
        return 2

    routers = data.get("routers")
    if not isinstance(routers, list):
        routers = []
    by_name = {r.get("name"): r for r in routers if isinstance(r, dict)}
    reachable = [n for n in want if by_name.get(n, {}).get("reachable") is True]
    router_asns = {r.get("asn") for r in by_name.values()
                   if isinstance(r.get("asn"), int) and r.get("asn") > 0}

    sessions = data.get("sessions")
    if not isinstance(sessions, list):
        sessions = []
    fabric = [s for s in sessions
              if isinstance(s, dict) and s.get("peerAsn") in router_asns]
    established = sum(1 for s in fabric
                      if s.get("state") == "Established" and not s.get("stale"))
    external = data.get("external") if isinstance(data.get("external"), int) else 0
    print("routers=%d/%d sessions=%d/%d external=%d" % (
        len(reachable), len(want), established, len(fabric), external))

    problems = []
    missing = [n for n in want if n not in by_name]
    if missing:
        problems.append("routers missing: %s" % ",".join(missing))
    down = [n for n in want if n in by_name and n not in reachable]
    if down:
        problems.append("routers unreachable: %s" % ",".join(down))
    if data.get("reachable") != len(want) or data.get("routerCount") != len(want):
        problems.append("counters reachable=%r routerCount=%r, want %d/%d" % (
            data.get("reachable"), data.get("routerCount"), len(want), len(want)))
    if not sessions:
        problems.append("no session records")
    if data.get("established") != established or data.get("sessionCount") != len(fabric):
        problems.append("counters established=%r sessionCount=%r disagree with the records %d/%d" % (
            data.get("established"), data.get("sessionCount"), established, len(fabric)))
    age_problem = snapshot_age_problem(data, args.max_age)
    if age_problem:
        problems.append(age_problem)
    if problems:
        print("fabric-dashboard-state: " + "; ".join(problems), file=sys.stderr)
        return 1
    return 0


def poll_seconds(raw):
    """Go's Duration string as the dashboard prints it ("2s", "1.5s", "500ms")."""
    m = re.fullmatch(r"(\d+(?:\.\d+)?)(ms|s|m)", str(raw or ""))
    if not m:
        return 2.0
    n, unit = float(m.group(1)), m.group(2)
    return n / 1000.0 if unit == "ms" else n * 60.0 if unit == "m" else n


def snapshot_age_problem(data, max_age):
    """A snapshot the poller stopped refreshing still serves; its ts says so."""
    ts = data.get("ts")
    try:
        when = datetime.datetime.strptime(str(ts), "%Y-%m-%dT%H:%M:%S.%fZ").replace(
            tzinfo=datetime.timezone.utc)
    except ValueError:
        return "no usable ts (%r)" % (ts,)
    limit = max_age if max_age is not None else 4 * poll_seconds(data.get("poll"))
    age = (datetime.datetime.now(datetime.timezone.utc) - when).total_seconds()
    if age > limit:
        return "snapshot ts is %.1f s old (limit %.1f s)" % (age, limit)
    return None


if __name__ == "__main__":
    sys.exit(main())
