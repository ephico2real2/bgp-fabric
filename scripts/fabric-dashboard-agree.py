#!/usr/bin/env python3
"""Does the dashboard's /api/state agree with the fabric's six eBGP sessions?

Reads a snapshot on stdin; prints "6/6" and exits 0 when every expected session
is Established, not stale, with the right peer ASN, and the counters say 6/6.
Otherwise prints why and exits non-zero. Used by the demo checks, which re-sample
it: the dashboard polls every 2 s, so one sample taken right after a session
changed reports a disagreement that is only a race.
"""
import json, sys
expected = {
    ("edge", "10.200.1.18"): 65100,
    ("spine", "10.200.1.2"): 65101,
    ("spine", "10.200.1.10"): 65102,
    ("spine", "10.200.1.19"): 65000,
    ("leaf1", "10.200.1.3"): 65100,
    ("leaf2", "10.200.1.11"): 65100,
}
try:
    data = json.loads(sys.stdin.read())
except ValueError:
    raise SystemExit("not JSON")
sessions = data.get("sessions") if isinstance(data, dict) else None
if not isinstance(sessions, list):
    raise SystemExit("no session records")
seen = {}
for sess in sessions:
    if isinstance(sess, dict):
        key = (sess.get("router"), sess.get("peer"))
        if key in expected:
            seen[key] = sess
missing = [k for k in expected if k not in seen]
if missing:
    raise SystemExit("dashboard lacks " + ",".join("%s/%s" % k for k in missing))
bad = [k for k, sess in seen.items()
       if sess.get("state") != "Established" or sess.get("stale")
       or sess.get("peerAsn") != expected[k]]
if bad:
    raise SystemExit("dashboard not Established: " + ",".join("%s/%s" % k for k in bad))
if data.get("established") != 6 or data.get("sessionCount") != 6:
    raise SystemExit("dashboard counters %s/%s" % (data.get("established"), data.get("sessionCount")))
print("6/6")
