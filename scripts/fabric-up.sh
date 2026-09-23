#!/usr/bin/env bash
# fabric-up.sh — bring the four-router fabric up and prove it converged.
#
# On macOS this creates the Colima VM first (a Linux kernel is not optional:
# TCP-MD5 signing is a kernel feature, and the Docker Desktop kernel does not
# carry CONFIG_TCP_MD5SIG). On Linux the host's own Docker engine is the
# engine, there is no VM, and the same script runs unchanged.
#
# Hard constraints from Colima, recorded so nobody "fixes" them later:
#   - vmType and mountType cannot be changed after the VM is created
#   - disk can only grow, never shrink
#   - inherited host DNS breaks on VPN/split-DNS, so 8.8.8.8 / 8.8.4.4 is pinned
#   - only $HOME is virtiofs-mounted; a mktemp -d bind fails with "not a
#     directory" (measured 2026-09-20), so every bind here comes from the repo
#
# Every docker call is `docker --context "$CTX"`. `colima start` activates the
# context it creates, so this script restores the caller's previous context in
# an EXIT trap — a leftover switch orphans whatever else the machine runs.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
. scripts/fabric-lib.sh
# shellcheck disable=SC1091
. scripts/versions.env
export FRR_IMAGE="${FRR_IMAGE:-quay.io/frrouting/frr:10.7.1}"
export NETSHOOT_IMAGE="${NETSHOOT_IMAGE:-nicolaka/netshoot:v0.16}"
export FABRIC_ROUTER_IMAGE="$FABRIC_ROUTER_IMAGE"
export FABRIC_DASHBOARD_IMAGE="$FABRIC_DASHBOARD_IMAGE"
export FABRIC_DASHBOARD_PORT

TRANSCRIPT="${FABRIC_TRANSCRIPT:-$FABRIC_ROOT/output/transcript.txt}"
DEADLINE="${FABRIC_CONVERGE_SECS:-60}"
export RECORD_STRICT=1
mkdir -p "$(dirname "$TRANSCRIPT")"

if ! fabric_refuse_wrong_ctx; then
  exit 1
fi

fabric_save_ctx
trap fabric_restore_ctx EXIT

# Create the profile only when Lima has no directory for it. Re-passing
# --vm-type / --mount-type on an existing VM is refused (they are frozen).
if fabric_manages_vm; then
  profile_dir="${HOME}/.colima/${FABRIC_COLIMA_PROFILE}"
  if [ ! -d "$profile_dir" ]; then
    echo "== 0. colima start --profile $FABRIC_COLIMA_PROFILE (create)"
    # --activate=false: do not steal the caller's docker context. The trap
    # still restores in case a colima version ignores the flag.
    colima start --profile "$FABRIC_COLIMA_PROFILE" \
      --vm-type vz \
      --mount-type virtiofs \
      --mount-inotify \
      --cpu 4 \
      --memory 6 \
      --disk 40 \
      --dns 8.8.8.8 \
      --dns 8.8.4.4 \
      --network-address \
      --activate=false
  else
    echo "== 0. colima start --profile $FABRIC_COLIMA_PROFILE (existing; vmType/mountType frozen, disk can only grow)"
    if ! colima status --profile "$FABRIC_COLIMA_PROFILE" >/dev/null 2>&1; then
      colima start --profile "$FABRIC_COLIMA_PROFILE" --activate=false
    fi
  fi
else
  echo "== 0. no VM to manage — using the Docker engine behind context $CTX"
fi

if ! fabric_require_ctx; then
  exit 1
fi

# Without network.address the VM has no vzNAT interface and the host cannot
# route to any address inside it. Enabling it is a stop/start of the profile;
# the containers stay and the fabric is re-applied.
if fabric_manages_vm && [ -z "$(colima_vm_address)" ]; then
  echo "fabric-up: NOTE profile $FABRIC_COLIMA_PROFILE has no reachable address (network.address: false)." >&2
  echo "  The host cannot route to addresses inside this VM. To enable it:" >&2
  echo "    colima stop --profile $FABRIC_COLIMA_PROFILE && colima start --profile $FABRIC_COLIMA_PROFILE --network-address --activate=false" >&2
  echo "  then re-run this script." >&2
fi

if [ ! -f "$FABRIC_DIR/.env" ] && [ -f "$FABRIC_DIR/.env.example" ]; then
  cp "$FABRIC_DIR/.env.example" "$FABRIC_DIR/.env"
fi
if [ -f "$FABRIC_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$FABRIC_DIR/.env"
  set +a
fi
export FABRIC_BGP_PASSWORD="${FABRIC_BGP_PASSWORD:-lab-bgp}"

# One fabric per engine — the /29s overlap with any second copy *in this
# context*. A fabric on another daemon is not visible here and cannot collide.
while read -r net_id; do
  [ -n "$net_id" ] || continue
  subnet=$(dk network inspect -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' "$net_id" 2>/dev/null || true)
  printf '%s' "$subnet" | grep -qF '10.200.1.0/29' || continue
  proj=$(dk network inspect -f '{{index .Labels "com.docker.compose.project"}}' "$net_id" 2>/dev/null || true)
  if [ -n "$proj" ] && [ "$proj" != "$FABRIC_PROJECT" ]; then
    echo "fabric-up: one fabric per engine — project $proj already owns 10.200.1.0/29 on context $CTX" >&2
    exit 1
  fi
done < <(dk network ls -q 2>/dev/null || true)

port_holder=$(dk ps --filter "publish=${FABRIC_DASHBOARD_PORT}" \
  --format '{{.Names}}' 2>/dev/null | grep -v "^${FABRIC_PROJECT}-" | head -1 || true)
if [ -n "$port_holder" ]; then
  echo "fabric-up: 127.0.0.1:${FABRIC_DASHBOARD_PORT} is published by container $port_holder (not this project)" >&2
  exit 1
fi

# The image is stamped with the commit it was built from, and the stamp is
# COMPUTED, never typed: a hand-passed sha is an assertion nobody checks, and
# the page then names a commit that does not contain the code it is serving
# (measured 2026-09-23 — the lab served `build 4cf1864`, a commit with neither
# the endpoint nor the label function in its tree). A dirty tree keeps its
# `-dirty` marker.
IFS=$'\t' read -r REVISION BUILT < <(scripts/build-revision.sh)
export REVISION BUILT

rec() { scripts/record.sh "$TRANSCRIPT" "$@"; }
printf '\n### %s — fabric-colima-up project=%s ctx=%s\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$FABRIC_PROJECT" "$CTX" >>"$TRANSCRIPT"

say() { echo "== $*"; }

need_image() {
  if [ "${FABRIC_REBUILD:-0}" = 1 ]; then
    return 0
  fi
  ! dk image inspect "$1" >/dev/null 2>&1
}

say "1. local images ($FABRIC_ROUTER_IMAGE, $FABRIC_DASHBOARD_IMAGE) via docker --context $CTX"
if need_image "$FABRIC_ROUTER_IMAGE"; then
  rec docker --context "$CTX" build -t "$FABRIC_ROUTER_IMAGE" --build-arg FRR_IMAGE="$FRR_IMAGE" \
    -f "$FABRIC_ROOT/frr-agent/Containerfile" "$FABRIC_ROOT/frr-agent"
else
  rec echo "image $FABRIC_ROUTER_IMAGE present"
fi
if need_image "$FABRIC_DASHBOARD_IMAGE"; then
  rec docker --context "$CTX" build -t "$FABRIC_DASHBOARD_IMAGE" \
    --build-arg REVISION="$REVISION" --build-arg BUILT="$BUILT" \
    -f "$FABRIC_ROOT/dashboard/Containerfile" "$FABRIC_ROOT/dashboard"
else
  rec echo "image $FABRIC_DASHBOARD_IMAGE present"
fi

say "2. docker --context $CTX compose up -d --wait (project $FABRIC_PROJECT)"
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" up -d --wait

require_sessions() {
  local svc=$1
  shift
  local raw rc=0
  raw=$(fabric_compose exec -T "$svc" vtysh -c 'show bgp summary json') || rc=$?
  if [ "$rc" -ne 0 ] || [ -z "$raw" ]; then
    echo "fabric-up: vtysh json failed on $svc rc=$rc" >&2
    return 1
  fi
  printf '%s' "$raw" | python3 scripts/fabric-bgp-summary.py --require "$@"
}

say "3. wait for the six fabric sessions (deadline ${DEADLINE}s)"
start=$(date +%s)
ok=0
polls=0
while :; do
  polls=$((polls + 1))
  if require_sessions edge 10.200.1.18 \
     && require_sessions spine 10.200.1.2 10.200.1.10 10.200.1.19 \
     && require_sessions leaf1 10.200.1.3 \
     && require_sessions leaf2 10.200.1.11; then
    ok=1
    break
  fi
  now=$(date +%s)
  if [ $((now - start)) -ge "$DEADLINE" ]; then
    break
  fi
  sleep 2
done
elapsed=$(( $(date +%s) - start ))
if [ "$ok" -ne 1 ]; then
  echo "fabric-up: fabric sessions not Established after ${elapsed}s" >&2
  rec echo "not converged after ${elapsed} s (${polls} polls)"
  rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
    -f "$FABRIC_DIR/compose.yaml" exec -T edge vtysh -c 'show bgp summary'
  rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
    -f "$FABRIC_DIR/compose.yaml" exec -T spine vtysh -c 'show bgp summary'
  rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
    -f "$FABRIC_DIR/compose.yaml" exec -T leaf1 vtysh -c 'show bgp summary'
  rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
    -f "$FABRIC_DIR/compose.yaml" exec -T leaf2 vtysh -c 'show bgp summary'
  exit 1
fi
rec echo "converged after ${elapsed} s (${polls} polls)"
echo "fabric-up: sessions Established after ${elapsed}s"

say "4. show bgp summary json on all four"
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" exec -T edge vtysh -c 'show bgp summary json'
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" exec -T spine vtysh -c 'show bgp summary json'
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" exec -T leaf1 vtysh -c 'show bgp summary json'
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" exec -T leaf2 vtysh -c 'show bgp summary json'

say "5. loopbacks from client0"
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" exec -T client0 ping -c 1 -W 2 10.200.255.11
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" exec -T client0 ping -c 1 -W 2 10.200.255.12

say "6. client0 path"
rec docker --context "$CTX" compose -p "$FABRIC_PROJECT" \
  -f "$FABRIC_DIR/compose.yaml" exec -T client0 ip route

say "7. dashboard healthz and 4/4 on 127.0.0.1:${FABRIC_DASHBOARD_PORT} (deadline 60s)"
dash_start=$(date +%s)
dash_ok=0
dash_line=""
while :; do
  if curl -fsS --max-time 2 "http://127.0.0.1:${FABRIC_DASHBOARD_PORT}/healthz" >/dev/null 2>&1; then
    if dash_line=$(curl -fsS --max-time 2 "http://127.0.0.1:${FABRIC_DASHBOARD_PORT}/api/state" \
         | python3 scripts/fabric-dashboard-state.py); then
      dash_ok=1
      break
    fi
  fi
  now=$(date +%s)
  if [ $((now - dash_start)) -ge 60 ]; then
    break
  fi
  sleep 1
done
dash_elapsed=$(( $(date +%s) - dash_start ))
if [ "$dash_ok" -ne 1 ]; then
  echo "fabric-up: dashboard not ready after ${dash_elapsed}s" >&2
  rec echo "dashboard not ready after ${dash_elapsed} s (${dash_line:-no /api/state})"
  rec curl -sS --max-time 2 "http://127.0.0.1:${FABRIC_DASHBOARD_PORT}/healthz" || true
  rec curl -sS --max-time 2 "http://127.0.0.1:${FABRIC_DASHBOARD_PORT}/api/state" || true
  exit 1
fi
rec echo "dashboard ready after ${dash_elapsed} s ($dash_line)"
echo "fabric-up: dashboard ready after ${dash_elapsed}s ($dash_line)"

echo "fabric-up: ready (project $FABRIC_PROJECT, ctx $CTX, ${elapsed}s to Established)"
