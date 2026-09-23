#!/usr/bin/env bash
# check.sh — one PASS/FAIL/WARN row per claim this lab makes.
# Exit status is the FAIL count, so CI needs no parsing.
# A dead docker/vtysh is a FAIL, never a PASS. Session state is an exact
# JSON field match (Established), never a substring of the blob.
# TCP MD5 rows FAIL when signing is not real (wire count, mismatch, kernel).
#   check.sh
set -uo pipefail
cd "$(dirname "$0")" || exit 1
# shellcheck disable=SC1091
. scripts/fabric-lib.sh
# shellcheck disable=SC1091
. scripts/versions.env
NETSHOOT_IMAGE="${NETSHOOT_IMAGE:-nicolaka/netshoot:v0.16}"

fails=0
row() { # ok|fail|warn  what  measured  rule
  local st
  case "$1" in
    ok)   st=PASS ;;
    fail) st=FAIL; fails=$((fails + 1)) ;;
    warn) st=WARN ;;
    *)    st=$1 ;;
  esac
  printf '  %-6s %-70s %-52s %s\n' "$st" "$2" "$3" "$4"
}

if ! fabric_refuse_wrong_ctx; then
  exit 1
fi
if ! fabric_require_ctx; then
  exit 1
fi

COMPOSE=(docker --context "$CTX" compose -p "$FABRIC_PROJECT" -f "$FABRIC_DIR/compose.yaml")
DASH="http://127.0.0.1:${FABRIC_DASHBOARD_PORT}"
if [ -f "$FABRIC_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$FABRIC_DIR/.env"
  set +a
fi

printf '\n== bgp-fabric — four FRR routers, TCP MD5 enforced\n'
printf '  %-6s %-70s %-52s %s\n' STATUS WHAT MEASURED RULE

# 1. four routers running
ps_out=$("${COMPOSE[@]}" ps --format '{{.Name}} {{.State}}' 2>&1)
ps_rc=$?
if [ "$ps_rc" -ne 0 ]; then
  row fail "four routers running" "docker failed: $(printf '%s' "$ps_out" | tr '\n' ' ' | head -c 60)" \
    "R1 — edge spine leaf1 leaf2 running"
else
  n=0
  for svc in edge spine leaf1 leaf2; do
    if printf '%s\n' "$ps_out" | grep -Eq "${FABRIC_PROJECT}-${svc}-[0-9]+ running"; then
      n=$((n + 1))
    fi
  done
  if [ "$n" -eq 4 ]; then
    row ok "four routers running" "running=$n/4" "R1 — edge spine leaf1 leaf2 running"
  else
    row fail "four routers running" "running=$n/4" "R1 — edge spine leaf1 leaf2 running"
  fi
fi

# 2. six fabric sessions Established
expect_sessions() {
  local raw rc=0
  raw=$("${COMPOSE[@]}" exec -T "$1" vtysh -c 'show bgp summary json' 2>&1) || rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "vtysh-fail:$1"
    return 1
  fi
  shift
  if ! printf '%s' "$raw" | python3 scripts/fabric-bgp-summary.py --require "$@"; then
    echo "not-established:$1"
    return 1
  fi
  return 0
}
peer_state() { # service neighbor-ip → Established|other|ABSENT|FAIL
  local svc=$1 ip=$2 raw rc=0 st
  raw=$("${COMPOSE[@]}" exec -T "$svc" vtysh -c 'show bgp summary json' 2>&1) || rc=$?
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
sess_err=""
if ! e=$(expect_sessions edge 10.200.1.18 2>&1); then
  sess_err="edge:${e:-fail}"
elif ! e=$(expect_sessions spine 10.200.1.2 10.200.1.10 10.200.1.19 2>&1); then
  sess_err="spine:${e:-fail}"
elif ! e=$(expect_sessions leaf1 10.200.1.3 2>&1); then
  sess_err="leaf1:${e:-fail}"
elif ! e=$(expect_sessions leaf2 10.200.1.11 2>&1); then
  sess_err="leaf2:${e:-fail}"
fi
if [ -z "$sess_err" ]; then
  row ok "six fabric sessions Established" "6/6 Established" \
    "R1 — leaf1–spine, leaf2–spine, spine–edge, both directions"
else
  row fail "six fabric sessions Established" "$sess_err" \
    "R1 — leaf1–spine, leaf2–spine, spine–edge, both directions"
fi

# 3–6. loopbacks from client0
ping_lo() {
  local ip=$1 out rc=0
  out=$("${COMPOSE[@]}" exec -T client0 ping -c 1 -W 2 "$ip" 2>&1) || rc=$?
  if [ "$rc" -ne 0 ] || ! printf '%s\n' "$out" | grep -Eq '1 received|1 packets received'; then
    row fail "client0 ping $ip" "docker/ping rc=$rc" "R1 — loopback reachable from client0"
  else
    row ok "client0 ping $ip" "rc=0" "R1 — loopback reachable from client0"
  fi
}
ping_lo 10.200.255.1
ping_lo 10.200.255.2
ping_lo 10.200.255.11
ping_lo 10.200.255.12

# 7. wan in leaf1 via spine
route_out=$("${COMPOSE[@]}" exec -T leaf1 vtysh -c 'show ip route 10.200.100.0/24' 2>&1)
route_rc=$?
if [ "$route_rc" -ne 0 ]; then
  row fail "10.200.100.0/24 in leaf1 via spine" "vtysh failed rc=$route_rc" \
    "R1 — wan learned via 10.200.1.3"
elif printf '%s\n' "$route_out" | grep -F -q '10.200.100.0/24' \
  && printf '%s\n' "$route_out" | grep -F -q '10.200.1.3'; then
  row ok "10.200.100.0/24 in leaf1 via spine" "via 10.200.1.3" \
    "R1 — wan learned via 10.200.1.3"
else
  row fail "10.200.100.0/24 in leaf1 via spine" "prefix or nexthop absent" \
    "R1 — wan learned via 10.200.1.3"
fi

# 8. ECMP
ecmp_ok=1
ecmp_msg=""
for svc in spine leaf1 leaf2; do
  ecmp_out=$("${COMPOSE[@]}" exec -T "$svc" vtysh -c 'show running-config' 2>&1)
  ecmp_rc=$?
  if [ "$ecmp_rc" -ne 0 ]; then
    ecmp_ok=0
    ecmp_msg="${ecmp_msg}${svc}:vtysh-fail "
    continue
  fi
  if ! printf '%s\n' "$ecmp_out" | grep -Eq '^[[:space:]]*maximum-paths[[:space:]]+8[[:space:]]*$'; then
    ecmp_ok=0
    ecmp_msg="${ecmp_msg}${svc}:no-maximum-paths "
  fi
done
if [ "$ecmp_ok" -eq 1 ]; then
  row ok "ECMP maximum-paths on spine and leaves" "maximum-paths 8" "R5 — maximum-paths 8"
else
  row fail "ECMP maximum-paths on spine and leaves" "$ecmp_msg" "R5 — maximum-paths 8"
fi

# 9. SERVERS listen the Colima node LAN only + policy on both leaves
listen_ok=1
listen_msg=""
for leaf in leaf1 leaf2; do
  lo=$("${COMPOSE[@]}" exec -T "$leaf" vtysh -c 'show running-config' 2>&1)
  lr=$?
  if [ "$lr" -ne 0 ]; then
    listen_ok=0
    listen_msg="${listen_msg}${leaf}:vtysh-fail "
    continue
  fi
  if ! printf '%s\n' "$lo" | grep -F -q 'bgp listen range 172.20.0.0/17 peer-group SERVERS' \
     || printf '%s\n' "$lo" | grep -F -q 'bgp listen range 172.18.0.0/17' \
     || printf '%s\n' "$lo" | grep -F -q 'bgp listen range 172.19.0.0/17' \
     || ! printf '%s\n' "$lo" | grep -F -q 'maximum-prefix 64' \
     || ! printf '%s\n' "$lo" | grep -E -q 'neighbor SERVERS timers 3 9'; then
    listen_ok=0
    listen_msg="${listen_msg}${leaf}:policy "
  fi
done
if [ "$listen_ok" -eq 1 ]; then
  row ok "SERVERS listen 172.20.0.0/17 on both leaves" "leaf1+leaf2" \
    "P4 — Colima node LAN only; the Cilium lab /17 is not here"
else
  row fail "SERVERS listen 172.20.0.0/17 on both leaves" "$listen_msg" \
    "P4 — Colima node LAN only; the Cilium lab /17 is not here"
fi

# 10. per-cluster VIP prefix-lists
pl_out=$("${COMPOSE[@]}" exec -T leaf1 vtysh -c 'show running-config' 2>&1)
pl_rc=$?
pl_ok=1
if [ "$pl_rc" -ne 0 ]; then
  pl_ok=0
else
  for want in \
    'ip prefix-list EG-POC1-VIPS seq 10 permit 10.198.0.0/26 ge 32 le 32' \
    'ip prefix-list EG-POC2-VIPS seq 10 permit 10.198.0.64/26 ge 32 le 32' \
    'ip prefix-list EG-ANYCAST-VIPS seq 10 permit 10.198.0.192/26 ge 32 le 32' \
    'ip prefix-list CILIUM-POC1-VIPS seq 10 permit 10.199.0.0/26 ge 32 le 32' \
    'ip prefix-list CILIUM-POC2-VIPS seq 10 permit 10.199.0.64/26 ge 32 le 32' \
    'ip prefix-list CILIUM-ANYCAST-VIPS seq 10 permit 10.199.0.192/26 ge 32 le 32' \
    'ip prefix-list EG-VIPS seq 10 permit 10.198.0.0/24 ge 32 le 32'
  do
    if ! printf '%s\n' "$pl_out" | grep -Fxq "$want"; then
      pl_ok=0
      break
    fi
  done
fi
if [ "$pl_rc" -ne 0 ]; then
  row fail "per-cluster VIP prefix-lists" "vtysh failed rc=$pl_rc" \
    "R8 — prefix-list + as-path per cluster"
elif [ "$pl_ok" -eq 1 ]; then
  row ok "per-cluster VIP prefix-lists" "EG/CILIUM POC1/POC2/ANYCAST ge 32 le 32" \
    "R8 — prefix-list + as-path per cluster"
else
  row fail "per-cluster VIP prefix-lists" "exact permit lines absent or not permit" \
    "R8 — prefix-list + as-path per cluster"
fi

# 11. RFC 8212
rfc_ok=1
rfc_msg=""
for svc in edge spine leaf1 leaf2; do
  cfg=$("${COMPOSE[@]}" exec -T "$svc" vtysh -c 'show running-config' 2>&1)
  cr=$?
  if [ "$cr" -ne 0 ]; then
    rfc_ok=0
    rfc_msg="${rfc_msg}${svc}:vtysh-fail "
    continue
  fi
  if ! printf '%s\n' "$cfg" | grep -Eq '^frr defaults traditional[[:space:]]*$' \
     || ! printf '%s\n' "$cfg" | grep -Eq '^router bgp [0-9]+[[:space:]]*$' \
     || printf '%s\n' "$cfg" | grep -F -q 'no bgp ebgp-requires-policy'; then
    rfc_ok=0
    rfc_msg="${rfc_msg}${svc}:policy-unverified "
  fi
done
if [ "$rfc_ok" -eq 1 ]; then
  row ok "RFC 8212 in effect" "traditional profile, ebgp-requires-policy on" \
    "§8 row 5 — traditional defaults, explicit route-maps"
else
  row fail "RFC 8212 in effect" "$rfc_msg" \
    "§8 row 5 — traditional defaults, explicit route-maps"
fi

# 12. sessions signed on the wire — the TCP-MD5 option on the leaf1–spine
# session. The filter names the fabric peer on purpose: when servers peer
# with a leaf, that leaf's namespace also carries the SERVERS sessions, and
# those are signed too — an unfiltered capture counts them and PASSes with the
# fabric session in clear (measured 2026-09-20: 14 of 20 captured segments
# were SERVERS traffic, not fabric). Every captured segment of
# this session must carry the option; zero packets is a FAIL (an unsigned
# session shows zero kernel TcpExtTCPMD5* failures too, so the wire count is
# the positive evidence). -l keeps tcpdump line-buffered: with one session in
# the filter the -c limit may be reached after the timeout kills it.
FABRIC_PEER=10.200.1.3
cid=$("${COMPOSE[@]}" ps -q leaf1 2>&1)
cid_rc=$?
if [ "$cid_rc" -ne 0 ] || [ -z "$cid" ]; then
  row fail "sessions signed on the wire" "docker ps -q leaf1 failed" \
    "§8 row 3 — TCP-MD5 option on every leaf1–spine segment"
else
  cap=""
  cap_rc=0
  cap=$(docker --context "$CTX" run --rm --net "container:${cid}" \
    --cap-add NET_ADMIN --cap-add NET_RAW \
    "$NETSHOOT_IMAGE" \
    timeout 20 tcpdump -nn -v -l -c 10 -i any \
    "tcp port 179 and host ${FABRIC_PEER}" 2>&1) || cap_rc=$?
  if [ "$cap_rc" -ne 0 ] && [ "$cap_rc" -ne 124 ]; then
    row fail "sessions signed on the wire" "tcpdump/docker rc=$cap_rc" \
      "§8 row 3 — TCP-MD5 option on every leaf1–spine segment"
  else
    seg_n=$(printf '%s\n' "$cap" | grep -cE '^[[:space:]]+[0-9.]+\.[0-9]+ > [0-9.]+\.[0-9]+:')
    md5_n=$(printf '%s\n' "$cap" | grep -ciE 'md5valid|tcp-md5|md5')
    if [ "$md5_n" -gt 0 ] && [ "$md5_n" -eq "$seg_n" ]; then
      row ok "sessions signed on the wire" \
        "md5-option packets=${md5_n}/${seg_n} on ${FABRIC_PEER}" \
        "§8 row 3 — TCP-MD5 option on every leaf1–spine segment"
    else
      row fail "sessions signed on the wire" \
        "md5-option packets=${md5_n}/${seg_n} on ${FABRIC_PEER}" \
        "§8 row 3 — TCP-MD5 option on every leaf1–spine segment"
    fi
  fi
fi

# 13. a wrong password on ONE side of ONE session must take it down AND KEEP
# it down. A password change resets the session whatever the kernel does —
# FRR 10.7.1 peer_password_set() calls peer_notify_config_change() /
# bgp_session_reset() before it ever touches the socket (bgpd/bgpd.c:7546) —
# so one non-Established sample proves nothing. Only a session that cannot
# come back while the keys differ proves the key is enforced: the lab's own
# standard (KERNEL-EVIDENCE.md: "still Connect after 30 s") and what demo
# 54c's row measures (0/2 up in 10/10 samples).
# The password restored is the one the session is RUNNING with, read from the
# running config — fabric/.env may have been edited since apply, and restoring
# a value the spine does not share leaves the fabric down for good.
BAD_PW="wrong-colima-md5"
FABRIC_PEER=10.200.1.3
HOLD_SAMPLES=15
running_pw() { # the password leaf1 is actually using for the fabric peer
  "${COMPOSE[@]}" exec -T leaf1 vtysh -c 'show running-config' 2>/dev/null \
    | awk -v ip="$FABRIC_PEER" \
        '$1 == "neighbor" && $2 == ip && $3 == "password" { print $4; exit }'
}
set_leaf1_spine_pw() {
  "${COMPOSE[@]}" exec -T leaf1 vtysh \
    -c 'configure terminal' \
    -c 'router bgp 65101' \
    -c "neighbor ${FABRIC_PEER} password $1" >/dev/null 2>&1
}
mismatch_ok=1
mismatch_msg=""
ran_control=0
REAL_PW=$(running_pw)
before_st=$(peer_state leaf1 "$FABRIC_PEER")
if [ -z "$REAL_PW" ]; then
  # Never mutate a session whose key we could not read back.
  mismatch_ok=0
  mismatch_msg="no 'neighbor ${FABRIC_PEER} password' in leaf1's running config — control not run"
elif [ "$before_st" != Established ]; then
  mismatch_ok=0
  mismatch_msg="pre:leaf1/${FABRIC_PEER}=$before_st"
else
  ran_control=1
  set_rc=0
  set_leaf1_spine_pw "$BAD_PW" || set_rc=$?
  if [ "$set_rc" -ne 0 ]; then
    mismatch_ok=0
    mismatch_msg="vtysh-set-fail rc=$set_rc"
  else
    dropped=""
    i=0
    while [ "$i" -lt 30 ]; do
      st=$(peer_state leaf1 "$FABRIC_PEER")
      if [ "$st" != Established ] && [ "$st" != FAIL ] && [ "$st" != ABSENT ]; then
        dropped=$st
        break
      fi
      if [ "$st" = FAIL ]; then
        mismatch_ok=0
        mismatch_msg="vtysh-poll-fail"
        break
      fi
      i=$((i + 1))
      sleep 1
    done
    if [ "$mismatch_ok" -eq 1 ] && [ -z "$dropped" ]; then
      mismatch_ok=0
      mismatch_msg="still Established after 30 s (unsigned look-alike)"
    elif [ "$mismatch_ok" -eq 1 ]; then
      # It went down. Now it must STAY down while the keys differ.
      came_back=""
      down_samples=0
      k=0
      while [ "$k" -lt "$HOLD_SAMPLES" ]; do
        sleep 1
        st=$(peer_state leaf1 "$FABRIC_PEER")
        if [ "$st" = Established ]; then
          came_back=$((k + 1))
          break
        fi
        down_samples=$((down_samples + 1))
        k=$((k + 1))
      done
      if [ -n "$came_back" ]; then
        mismatch_ok=0
        mismatch_msg="Established→${dropped}→Established after ${came_back}s with the WRONG key (not enforced)"
      else
        mismatch_msg="Established→${dropped}, down in ${down_samples}/${HOLD_SAMPLES} samples"
      fi
    fi
  fi
fi
rest_rc=0
if [ "$ran_control" -eq 1 ]; then
  set_leaf1_spine_pw "$REAL_PW" || rest_rc=$?
fi
if [ "$ran_control" -eq 0 ]; then
  row fail "a wrong password breaks the session" "$mismatch_msg" \
    "§8 row 3 — mismatch keeps the session down; restore required"
elif [ "$rest_rc" -ne 0 ]; then
  echo "check.sh: leaf1 still carries ${BAD_PW} for ${FABRIC_PEER} — the fabric is DOWN." >&2
  echo "  restore by hand: docker --context $CTX compose -p $FABRIC_PROJECT -f $FABRIC_DIR/compose.yaml exec -T leaf1 vtysh -c 'configure terminal' -c 'router bgp 65101' -c 'neighbor ${FABRIC_PEER} password <the fabric key>'" >&2
  row fail "a wrong password breaks the session" "restore-fail rc=$rest_rc ($mismatch_msg)" \
    "§8 row 3 — mismatch keeps the session down; restore required"
else
  back=""
  j=0
  while [ "$j" -lt 40 ]; do
    st=$(peer_state leaf1 "$FABRIC_PEER")
    if [ "$st" = Established ]; then
      back=1
      break
    fi
    if [ "$st" = FAIL ]; then
      mismatch_ok=0
      mismatch_msg="${mismatch_msg}; restore-poll-fail"
      break
    fi
    j=$((j + 1))
    sleep 1
  done
  if [ -z "$back" ]; then
    echo "check.sh: leaf1/${FABRIC_PEER} did not return to Established after the key was put back." >&2
    echo "  the running key read before the control was '${REAL_PW}'; the fabric is DOWN until it matches the spine's." >&2
    row fail "a wrong password breaks the session" "could not restore Established ($mismatch_msg)" \
      "§8 row 3 — mismatch keeps the session down; restore required"
  elif [ "$mismatch_ok" -eq 1 ]; then
    row ok "a wrong password breaks the session" "$mismatch_msg; restored Established" \
      "§8 row 3 — mismatch keeps the session down; restore required"
  else
    row fail "a wrong password breaks the session" "$mismatch_msg; restored Established" \
      "§8 row 3 — mismatch keeps the session down; restore required"
  fi
fi

# 14. kernel has CONFIG_TCP_MD5SIG — read from the kernel that signs the
# sessions (the Colima VM on macOS, the host on Linux), never from a container:
# a container shares the host's kernel and would answer for the wrong one only
# by accident.
kcfg=""
krc=0
# uname runs over there, not in this shell (SC2016 is the point).
# shellcheck disable=SC2016
kcfg=$(fabric_engine_sh 'uname -r; grep -E "^CONFIG_TCP_MD5SIG=" /boot/config-$(uname -r) || zgrep -E "^CONFIG_TCP_MD5SIG=" /proc/config.gz 2>/dev/null' 2>&1) || krc=$?
if [ "$krc" -ne 0 ]; then
  row fail "kernel has CONFIG_TCP_MD5SIG" "could not read the kernel config rc=$krc" \
    "§8 row 3 — kernel CONFIG_TCP_MD5SIG=y"
elif printf '%s\n' "$kcfg" | grep -q '^CONFIG_TCP_MD5SIG=y'; then
  kver=$(printf '%s\n' "$kcfg" | awk '/^[0-9]+\./ {print; exit}')
  row ok "kernel has CONFIG_TCP_MD5SIG" "CONFIG_TCP_MD5SIG=y kernel=${kver:-?}" \
    "§8 row 3 — kernel CONFIG_TCP_MD5SIG=y"
else
  row fail "kernel has CONFIG_TCP_MD5SIG" "absent or not =y" \
    "§8 row 3 — kernel CONFIG_TCP_MD5SIG=y"
fi

# 15. dashboard reachable, 4/4 routers polled
dash_raw=""
dash_rc=0
dash_raw=$(curl -fsS --max-time 3 "${DASH}/api/state" 2>&1) || dash_rc=$?
if [ "$dash_rc" -ne 0 ]; then
  row fail "dashboard reachable, 4/4 routers polled" "curl rc=$dash_rc" \
    "D8 — /api/state from 127.0.0.1:${FABRIC_DASHBOARD_PORT}"
else
  dash_line=""
  dash_py=0
  dash_line=$(printf '%s' "$dash_raw" | python3 scripts/fabric-dashboard-state.py) || dash_py=$?
  if [ "$dash_py" -eq 0 ]; then
    row ok "dashboard reachable, 4/4 routers polled" "$dash_line" \
      "D8 — /api/state from 127.0.0.1:${FABRIC_DASHBOARD_PORT}"
  else
    row fail "dashboard reachable, 4/4 routers polled" "${dash_line:-parse-fail}" \
      "D8 — /api/state from 127.0.0.1:${FABRIC_DASHBOARD_PORT}"
  fi
fi

# 16. dashboard sessions agree with vtysh (6 fabric Established)
agree_ok=1
agree_msg=""
if [ "$dash_rc" -ne 0 ]; then
  agree_ok=0
  agree_msg="no /api/state"
elif ! agree_msg=$(printf '%s' "$dash_raw" | python3 scripts/fabric-dashboard-agree.py 2>&1 | tr '\n' ' ' | head -c 80); then
  agree_ok=0
  agree_msg=${agree_msg:-dashboard not 6/6}
fi
if [ "$agree_ok" -eq 1 ]; then
  if ! expect_sessions edge 10.200.1.18 >/dev/null 2>&1 \
     || ! expect_sessions spine 10.200.1.2 10.200.1.10 10.200.1.19 >/dev/null 2>&1 \
     || ! expect_sessions leaf1 10.200.1.3 >/dev/null 2>&1 \
     || ! expect_sessions leaf2 10.200.1.11 >/dev/null 2>&1; then
    agree_ok=0
    agree_msg="vtysh not 6/6"
  fi
fi

# The dashboard polls every 2 s, so ONE sample taken just after a session changed
# reads the snapshot from before that poll and reports a disagreement that is
# really a race — measured 2026-09-20: a run straight after the MD5 negative
# control had vtysh 6/6 while /api/state still showed two sessions down, and the
# two agreed seconds later. Re-sample for up to 12 s, and record how long it took.
# The re-sample re-reads BOTH sides: this row is an agreement, and a retry that
# only re-polls /api/state would drop half the comparison and could pass while
# vtysh is short of 6/6.
agree_waited=0
if [ "$agree_ok" -eq 0 ] && [ "$agree_msg" != "no /api/state" ] && [ "$agree_msg" != "vtysh not 6/6" ]; then
  for _ in 1 2 3 4 5 6; do
    sleep 2
    agree_waited=$((agree_waited + 2))
    dash_raw=$(curl -fsS --max-time 3 "${DASH}/api/state" 2>&1) || continue
    printf '%s' "$dash_raw" | python3 scripts/fabric-dashboard-agree.py >/dev/null 2>&1 || continue
    if expect_sessions edge 10.200.1.18 >/dev/null 2>&1 \
       && expect_sessions spine 10.200.1.2 10.200.1.10 10.200.1.19 >/dev/null 2>&1 \
       && expect_sessions leaf1 10.200.1.3 >/dev/null 2>&1 \
       && expect_sessions leaf2 10.200.1.11 >/dev/null 2>&1; then
      agree_ok=1
      agree_msg="6/6 = 6/6 after ${agree_waited}s"
      break
    fi
    agree_msg="dashboard 6/6 but vtysh not 6/6 after ${agree_waited}s"
  done
fi
if [ "$agree_ok" -eq 1 ]; then
  row ok "dashboard sessions agree with vtysh" "${agree_msg:-6/6 = 6/6}" \
    "D17 — state Established matches fabric-bgp-summary"
else
  row fail "dashboard sessions agree with vtysh" "$agree_msg" \
    "D17 — state Established matches fabric-bgp-summary"
fi

# 17. agent on mgmt only, show-only
http_code() {
  printf '%s\n' "$1" | awk '
    !seen && /^[[:space:]]*HTTP\/[0-9.]+ [0-9][0-9][0-9]([[:space:]]|$)/ {
      match($0, /HTTP\/[0-9.]+ [0-9][0-9][0-9]/)
      c = substr($0, RSTART + RLENGTH - 3, 3)
      seen = 1
    }
    END { print c + 0 }'
}
ports_raw=""
ports_rc=0
ports_raw=$("${COMPOSE[@]}" ps --format json 2>&1) || ports_rc=$?
ports_ok=0
if [ "$ports_rc" -eq 0 ]; then
  if printf '%s\n' "$ports_raw" | python3 -c '
import json, sys
raw = sys.stdin.read()
objs = []
try:
    parsed = json.loads(raw)
    if isinstance(parsed, list):
        objs = parsed
    elif isinstance(parsed, dict):
        objs = [parsed]
except json.JSONDecodeError:
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            objs.append(json.loads(line))
        except json.JSONDecodeError:
            continue
expected = {"edge", "spine", "leaf1", "leaf2"}
seen = set()
published = []
for o in objs:
    if not isinstance(o, dict):
        continue
    name = str(o.get("Service") or o.get("Name") or "")
    svc = name.replace("bgp-fabric-", "").rsplit("-", 1)[0] if name else ""
    if svc not in expected:
        continue
    seen.add(svc)
    if "Publishers" not in o and "Ports" not in o:
        published.append("%s:no-port-field" % svc)
        continue
    pubs = o.get("Publishers")
    if pubs is None:
        pubs = o.get("Ports") or []
    if isinstance(pubs, str) and pubs.strip() and pubs.strip() != "[]":
        published.append("%s:%s" % (svc, pubs))
    elif isinstance(pubs, list):
        for p in pubs:
            if isinstance(p, dict) and (p.get("PublishedPort") or p.get("URL")):
                published.append("%s:%s" % (svc, p.get("PublishedPort") or p.get("URL")))
            elif isinstance(p, str) and p.strip():
                published.append("%s:%s" % (svc, p))
missing = sorted(expected - seen)
if missing:
    print("inventory lacks " + ",".join(missing))
    raise SystemExit(1)
if published:
    print("published " + ",".join(published[:4]))
    raise SystemExit(1)
print("no published ports")
' >/dev/null 2>&1; then
    ports_ok=1
  fi
fi
reboot_out=""
sum_out=""
reboot_rc=0
sum_rc=0
reboot_out=$("${COMPOSE[@]}" exec -T dashboard wget -q -S -T 5 -O /dev/null \
  'http://10.200.200.11:8080/show/bgp-summary%3Breboot' 2>&1) || reboot_rc=$?
sum_out=$("${COMPOSE[@]}" exec -T dashboard wget -q -S -T 5 -O /dev/null \
  'http://10.200.200.11:8080/show/bgp-summary' 2>&1) || sum_rc=$?
reboot_code=$(http_code "$reboot_out")
sum_code=$(http_code "$sum_out")
client_blocked=0
client_rcs=""
for magent in 10.200.200.1 10.200.200.2 10.200.200.11 10.200.200.12; do
  client_rc=0
  "${COMPOSE[@]}" exec -T client0 curl --max-time 3 -s -o /dev/null \
    "http://$magent:8080/healthz" >/dev/null 2>&1 || client_rc=$?
  client_rcs="${client_rcs:+$client_rcs,}$client_rc"
  if [ "$client_rc" -eq 7 ] || [ "$client_rc" -eq 28 ]; then
    client_blocked=$((client_blocked + 1))
  fi
done
if [ "$ports_ok" -eq 1 ] \
   && [ "$reboot_code" = 404 ] && [ "$reboot_rc" -ne 0 ] \
   && [ "$sum_code" = 200 ] && [ "$sum_rc" -eq 0 ] \
   && [ "$client_blocked" -eq 4 ]; then
  row ok "agent on mgmt only, show-only" \
    "no ports; ;reboot=$reboot_code summary=$sum_code; client0_rc=$client_rcs" \
    "D8 — agent on 10.200.200.0/24, show-only"
else
  row fail "agent on mgmt only, show-only" \
    "ports_ok=$ports_ok ;reboot=${reboot_code:-?}/$reboot_rc summary=${sum_code:-?}/$sum_rc client0_rc=$client_rcs" \
    "D8 — agent on 10.200.200.0/24, show-only"
fi
echo
echo "bgp-fabric check: $fails FAIL"
exit "$fails"
