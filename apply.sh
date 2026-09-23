#!/usr/bin/env bash
# apply.sh — bring the fabric up and record the sessions, the routes and the
# TCP-MD5 evidence into output/transcript.txt. Idempotent.
# Every docker call is --context "$CTX".
#   ./apply.sh
set -euo pipefail
cd "$(dirname "$0")"
# shellcheck disable=SC1091
. scripts/fabric-lib.sh
if ! fabric_refuse_wrong_ctx; then
  exit 1
fi
export RECORD_STRICT=1
HERE=$FABRIC_ROOT
FABRIC=$FABRIC_DIR
PROJECT=$FABRIC_PROJECT
TRANSCRIPT=$HERE/output/transcript.txt
DASH="http://127.0.0.1:${FABRIC_DASHBOARD_PORT}"
mkdir -p "$(dirname "$TRANSCRIPT")"
printf '\n### %s — bgp-fabric apply\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$TRANSCRIPT"
rec() { scripts/record.sh "$TRANSCRIPT" "$@"; }

echo "== 1. fabric-up.sh"
scripts/fabric-up.sh

echo "== 2. docker --context $CTX compose ps"
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" ps

echo "== 3. four show bgp summary"
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T edge vtysh -c 'show bgp summary'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T spine vtysh -c 'show bgp summary'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf1 vtysh -c 'show bgp summary'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf2 vtysh -c 'show bgp summary'

echo "== 4. show ip bgp on spine and edge"
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T spine vtysh -c 'show ip bgp'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T edge vtysh -c 'show ip bgp'

echo "== 5. show ip route on edge"
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T edge vtysh -c 'show ip route'

echo "== 6. client0 traceroute and ping to leaf1 loopback"
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T client0 traceroute -n 10.200.255.11
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T client0 ping -c 3 -W 2 10.200.255.11

echo "== 7. client0 ip route"
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T client0 ip route

echo "== 8. SERVERS group, listen range, prefix-lists, route-maps"
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf1 vtysh -c 'show bgp peer-group SERVERS'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf1 vtysh -c 'show bgp listen'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf1 vtysh -c 'show ip prefix-list'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf1 vtysh -c 'show route-map'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf2 vtysh -c 'show bgp peer-group SERVERS'
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  exec -T leaf2 vtysh -c 'show ip prefix-list'

# shellcheck disable=SC1091
. scripts/versions.env
NETSHOOT_IMAGE="${NETSHOOT_IMAGE:-nicolaka/netshoot:v0.16}"

echo "== 9. TCP MD5 evidence (kernel, FRR logs, wire, counters)"
# uname/grep run on the kernel that signs, not in this shell (SC2016 is the
# point). record.sh is a separate process, so anything it runs must be an
# exported function or an executable — a plain shell function would be
# "command not found" inside it.
# shellcheck disable=SC2016,SC2329
md5_kernel() {
  fabric_engine_sh 'echo "kernel=$(uname -r)"; grep -E "^CONFIG_TCP_MD5SIG=" /boot/config-$(uname -r) || true; zgrep -E "^CONFIG_TCP_MD5SIG=" /proc/config.gz 2>/dev/null || true'
}
export -f md5_kernel fabric_engine_sh fabric_manages_vm
export FABRIC_COLIMA_PROFILE FABRIC_ANY_CONTEXT
rec bash -c md5_kernel
unset -f md5_kernel
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  logs --no-log-prefix leaf1
rec docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
  logs --no-log-prefix leaf2
# shellcheck disable=SC2329
md5_wire() {
  set -euo pipefail
  cid=$(docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" ps -q leaf1)
  echo "leaf1 container=$cid"
  docker --context "$CTX" run --rm --net "container:${cid}" \
    --cap-add NET_ADMIN --cap-add NET_RAW \
    "$NETSHOOT_IMAGE" \
    timeout 15 tcpdump -nn -v -c 20 -i any 'tcp port 179' || true
}
export -f md5_wire
export CTX PROJECT FABRIC NETSHOOT_IMAGE
rec bash -c md5_wire
unset -f md5_wire
# shellcheck disable=SC2016,SC2329
md5_counters() {
  fabric_engine_sh 'nstat -az 2>/dev/null | grep -E "TcpExtTCPMD5" || awk "/TcpExt/ {print}" /proc/net/netstat'
}
export -f md5_counters
rec bash -c md5_counters
unset -f md5_counters

echo "== 10. dashboard /api/state"
rec curl -sS --max-time 5 "${DASH}/api/state"
# shellcheck disable=SC2329
state_head() {
  curl -sS --max-time 5 "${DASH}/api/state" | python3 -m json.tool | head -60
}
export -f state_head
export DASH
rec bash -c state_head
unset -f state_head
rec bash -c "curl -fsS --max-time 5 '${DASH}/api/state' | python3 scripts/fabric-dashboard-state.py"

CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
# shellcheck disable=SC1091
. scripts/browser-shot.sh
SHOTDIR=$HERE/output/screenshots
mkdir -p "$SHOTDIR"

echo "== 11. screenshot, steady"
# shellcheck disable=SC2329
shot_steady() {
  BROWSER_SHOT_PATH="$PWD/$HERE/output/screenshots/dashboard-steady.png"
  BROWSER_SHOT_URL="${DASH}/?router=spine"
  BROWSER_SHOT_WIDTH=1200
  BROWSER_SHOT_HEIGHT=700
  BROWSER_SHOT_VIRTUAL_TIME_MS=4000
  BROWSER_SHOT_FILE_LABEL="$HERE/output/screenshots/dashboard-steady.png"
  browser_shot
}
export -f shot_steady browser_shot
export CHROME HERE BROWSER_SHOT_PATH BROWSER_SHOT_URL BROWSER_SHOT_WIDTH BROWSER_SHOT_HEIGHT BROWSER_SHOT_VIRTUAL_TIME_MS BROWSER_SHOT_FILE_LABEL DASH
rec bash -c shot_steady
unset -f shot_steady

echo "== 12. clear bgp * on spine, events, recovery shots"
# shellcheck disable=SC2329
clear_and_watch() {
  set -euo pipefail
  mark=$(curl -fsS --max-time 3 "${DASH}/api/events?since=0" \
    | python3 -c 'import json,sys; ev=json.load(sys.stdin); print(ev[-1]["id"] if ev else 0)')
  echo "event mark before clear: id=$mark"
  docker --context "$CTX" compose -p "$PROJECT" -f "$FABRIC/compose.yaml" \
    exec -T spine vtysh -c 'clear bgp *'
  echo "clear bgp * issued on spine"
  drop_start=$(python3 -c 'import time; print("%.6f" % time.time())')
  showed=""
  drop_i=0
  while [ "$drop_i" -lt 60 ]; do
    st=$(curl -fsS --max-time 2 "${DASH}/api/state" 2>/dev/null || true)
    if printf '%s' "$st" | python3 -c '
import json, sys
try:
    data = json.loads(sys.stdin.read() or "{}")
except json.JSONDecodeError:
    raise SystemExit(1)
for e in data.get("edges") or []:
    if not isinstance(e, dict):
        continue
    if str(e.get("state") or "").lower() != "established":
        raise SystemExit(0)
raise SystemExit(1)
'; then
      showed=1
      break
    fi
    drop_i=$((drop_i + 1))
    sleep 0.25
  done
  drop_elapsed=$(DROP_START="$drop_start" python3 -c 'import os,time; print("%.2f" % (time.time() - float(os.environ["DROP_START"])))')
  if [ -n "$showed" ]; then
    echo "dashboard showed the drop after ${drop_elapsed} s"
  else
    echo "dashboard never showed a non-established edge within 15 s"
  fi
  BROWSER_SHOT_PATH="$PWD/$HERE/output/screenshots/dashboard-clear-bgp.png"
  BROWSER_SHOT_URL="${DASH}/?router=spine"
  BROWSER_SHOT_WIDTH=1200
  BROWSER_SHOT_HEIGHT=700
  BROWSER_SHOT_VIRTUAL_TIME_MS=4000
  BROWSER_SHOT_FILE_LABEL="$HERE/output/screenshots/dashboard-clear-bgp.png"
  browser_shot
  rec_start=$(python3 -c 'import time; print("%.6f" % time.time())')
  recovered=""
  deadline=$(( $(date +%s) + 60 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    ev=$(curl -fsS --max-time 2 "${DASH}/api/events?since=$mark" || true)
    st=$(curl -fsS --max-time 2 "${DASH}/api/state" || true)
    if printf '%s' "$st" | python3 -c '
import json, sys
try:
    data = json.loads(sys.stdin.read() or "{}")
except json.JSONDecodeError:
    raise SystemExit(1)
edges = [e for e in (data.get("edges") or []) if isinstance(e, dict)]
if not edges:
    raise SystemExit(1)
for e in edges:
    if str(e.get("state") or "").lower() != "established":
        raise SystemExit(1)
raise SystemExit(0)
' && printf '%s' "$ev" | python3 -c '
import json, sys
try:
    data = json.loads(sys.stdin.read() or "[]")
except json.JSONDecodeError:
    raise SystemExit(1)
for e in data:
    if not isinstance(e, dict):
        continue
    if e.get("kind") == "session" and e.get("router") == "spine" and e.get("to") == "Established":
        raise SystemExit(0)
raise SystemExit(1)
'; then
      recovered=1
      break
    fi
    sleep 0.25
  done
  rec_elapsed=$(REC_START="$rec_start" python3 -c 'import os,time; print("%.2f" % (time.time() - float(os.environ["REC_START"])))')
  if [ -n "$recovered" ]; then
    echo "dashboard confirmed recovery after ${rec_elapsed} s (polled after the screenshots)"
  else
    echo "dashboard: no spine to=Established event within 60 s"
  fi
  BROWSER_SHOT_PATH="$PWD/$HERE/output/screenshots/dashboard-recovered.png"
  BROWSER_SHOT_FILE_LABEL="$HERE/output/screenshots/dashboard-recovered.png"
  browser_shot
  MARK="$mark" DASH="$DASH" python3 - <<'PY'
import json, os, urllib.request
raw = urllib.request.urlopen("%s/api/events?since=%s" % (os.environ["DASH"], os.environ["MARK"]), timeout=3).read()
ev = json.loads(raw)
print("events after the clear: %d" % len(ev))
print("ts\tkind\trouter\tpeer\tprefix\tfrom→to\ttext")
for e in ev[-30:]:
    print("%s\t%s\t%s\t%s\t%s\t%s→%s\t%s" % (
        e.get("ts",""), e.get("kind",""), e.get("router",""),
        e.get("peer",""), e.get("prefix",""),
        e.get("from",""), e.get("to",""), e.get("text","")))
spine = [e for e in ev if e.get("kind")=="session" and e.get("router")=="spine"]
idle = next((e for e in spine if e.get("to")=="Idle"), None)
last_up = None
for e in spine:
    if e.get("to") == "Established":
        last_up = e
first_idle_ts = idle.get("ts") if idle else None
last_up_ts = last_up.get("ts") if last_up else None
window = ""
if first_idle_ts and last_up_ts:
    import datetime
    fmt = "%Y-%m-%dT%H:%M:%S.%fZ"
    window = " window=%.3f s" % (
        datetime.datetime.strptime(last_up_ts, fmt) - datetime.datetime.strptime(first_idle_ts, fmt)
    ).total_seconds()
print("spine recovery: first Idle %s last Established %s recovered=%s%s" % (
    first_idle_ts, last_up_ts, "yes" if last_up else "no", window))
PY
}
export -f clear_and_watch browser_shot
export CHROME HERE CTX PROJECT FABRIC DASH
rec bash -c clear_and_watch
unset -f clear_and_watch browser_shot

echo "dashboard: ${DASH}/ (Mac browser)"
echo "bgp-fabric apply: recorded"
