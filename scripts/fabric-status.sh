#!/usr/bin/env bash
# fabric-status.sh — four `show bgp summary` tables, `show ip bgp` on
# spine, a text topology, and the dashboard one-liner (port 8098).
# Reads running containers in CTX=colima-bgp-fabric; does not start them.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
. scripts/fabric-lib.sh

if ! fabric_refuse_wrong_ctx; then
  exit 1
fi
if ! fabric_require_ctx; then
  exit 1
fi

compose_exec() {
  local svc=$1
  shift
  fabric_compose exec -T "$svc" "$@"
}

peer_state() {
  local svc=$1 ip=$2 raw rc=0 st
  raw=$(compose_exec "$svc" vtysh -c 'show bgp summary json') || rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$raw" ]; then
    printf 'FAIL'
    return
  fi
  st=$(printf '%s' "$raw" | python3 -c '
import json, sys
ip = sys.argv[1]
try:
    data = json.loads(sys.stdin.read())
except json.JSONDecodeError:
    print("FAIL")
    raise SystemExit
peers = {}
def walk(o):
    if isinstance(o, dict):
        if "peers" in o and isinstance(o["peers"], dict):
            peers.update(o["peers"])
        for v in o.values():
            walk(v)
walk(data)
p = peers.get(ip)
if p is None:
    print("ABSENT")
    raise SystemExit
for k in ("state", "peerState", "bgpState"):
    v = p.get(k)
    if isinstance(v, str) and v:
        print(v)
        raise SystemExit
print("ABSENT")
' "$ip") || st=FAIL
  printf '%s' "${st:-FAIL}"
}

echo "== project $FABRIC_PROJECT ctx=$CTX — show bgp summary"
echo
echo "---- edge ----"
compose_exec edge vtysh -c 'show bgp summary' || echo "vtysh failed on edge"
echo
echo "---- spine ----"
compose_exec spine vtysh -c 'show bgp summary' || echo "vtysh failed on spine"
echo
echo "---- leaf1 ----"
compose_exec leaf1 vtysh -c 'show bgp summary' || echo "vtysh failed on leaf1"
echo
echo "---- leaf2 ----"
compose_exec leaf2 vtysh -c 'show bgp summary' || echo "vtysh failed on leaf2"
echo
echo "== spine show ip bgp"
compose_exec spine vtysh -c 'show ip bgp' || echo "vtysh failed on spine"
echo

e_s=$(peer_state edge 10.200.1.18)
s_e=$(peer_state spine 10.200.1.19)
s_l1=$(peer_state spine 10.200.1.2)
s_l2=$(peer_state spine 10.200.1.10)
l1_s=$(peer_state leaf1 10.200.1.3)
l2_s=$(peer_state leaf2 10.200.1.11)

cat <<EOF
== topology (session state from show bgp summary json)

  client0 10.200.100.10
      |
      | wan 10.200.100.0/24
      v
   edge  AS65000  lo 10.200.255.1
      |  edge→spine $e_s / spine→edge $s_e
      |  10.200.1.16/29
      v
   spine AS65100  lo 10.200.255.2
     / \\
    /   \\
   |     |
   | spine→leaf1 $s_l1 / leaf1→spine $l1_s
   | 10.200.1.0/29
   v
 leaf1 AS65101  lo 10.200.255.11     leaf2 AS65102  lo 10.200.255.12
                                    spine→leaf2 $s_l2 / leaf2→spine $l2_s
                                    10.200.1.8/29

EOF

echo
if dash=$(curl -fsS --max-time 2 "http://127.0.0.1:${FABRIC_DASHBOARD_PORT}/api/state" 2>/dev/null \
     | python3 scripts/fabric-dashboard-state.py); then
  echo "== dashboard $dash"
else
  echo "== dashboard ${dash:-unreachable}"
fi

for state in "$e_s" "$s_e" "$s_l1" "$s_l2" "$l1_s" "$l2_s"; do
  if [ "$state" != Established ]; then
    exit 1
  fi
done
exit 0
