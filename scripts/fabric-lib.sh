#!/usr/bin/env bash
# fabric-lib.sh — the shared context gate and the compose wrapper.
# Source from the repo root after `cd` there. Never rely on the ACTIVE Docker
# context: every daemon call goes through `dk`, which is
# `docker --context "$CTX"`. A machine that runs more than one lab has more
# than one daemon, and the active context is whatever the last command left
# behind; naming the context on every call is the only way a script can be
# sure which engine it tore down.
#
#   CTX                     the docker context to talk to (colima-bgp-fabric)
#   FABRIC_ANY_CONTEXT=1    accept whatever CTX names — for a machine with one
#                           daemon (a CI runner, a Linux host with no Colima)
#   FABRIC_COLIMA_PROFILE   the Colima profile behind that context, on macOS
#   FABRIC_PROJECT          the compose project (bgp-fabric)
#   FABRIC_DASHBOARD_PORT   published on 127.0.0.1 (default 8098)
#   FABRIC_ROOT             the repo root as the scripts see it (default .)
set -uo pipefail

CTX="${CTX:-colima-bgp-fabric}"
FABRIC_COLIMA_PROFILE="${FABRIC_COLIMA_PROFILE:-bgp-fabric}"
FABRIC_PROJECT="${FABRIC_PROJECT:-bgp-fabric}"
FABRIC_DASHBOARD_PORT="${FABRIC_DASHBOARD_PORT:-8098}"
FABRIC_ROOT="${FABRIC_ROOT:-.}"
FABRIC_DIR="${FABRIC_DIR:-$FABRIC_ROOT/fabric}"
FABRIC_ROUTER_IMAGE="${FABRIC_ROUTER_IMAGE:-bgp-fabric-agent:local}"
FABRIC_DASHBOARD_IMAGE="${FABRIC_DASHBOARD_IMAGE:-bgp-fabric-dashboard:local}"
export FABRIC_ROUTER_IMAGE FABRIC_DASHBOARD_IMAGE

# True when this run is expected to create and start a Colima VM: macOS with
# colima installed, and no explicit opt-out. On Linux the Docker engine is the
# host's own and there is no VM to manage — the same scripts then talk to the
# daemon directly. Read as a command: `if fabric_manages_vm; then`.
fabric_manages_vm() {
  [ "${FABRIC_ANY_CONTEXT:-0}" != 1 ] && command -v colima >/dev/null 2>&1
}

# Refuse before the first docker daemon call: the name of the context is the
# only check that happens BEFORE something is torn down.
fabric_refuse_wrong_ctx() {
  # The deliberate way past the gate, for a machine where there is exactly one
  # Docker daemon. The gate exists because a laptop may run several labs on
  # several contexts, and a wrong-context run tears down the wrong one; that
  # reason does not apply where there is only one engine, and refusing there
  # would mean the lab could never prove itself on a CI runner.
  if [ "${FABRIC_ANY_CONTEXT:-0}" = 1 ]; then
    return 0
  fi
  if [ "$CTX" != "colima-${FABRIC_COLIMA_PROFILE}" ]; then
    echo "fabric: refusing CTX=$CTX." >&2
    echo "  This lab talks to docker --context colima-${FABRIC_COLIMA_PROFILE} (Colima profile ${FABRIC_COLIMA_PROFILE})." >&2
    echo "  Set both or neither: CTX=colima-<profile> FABRIC_COLIMA_PROFILE=<profile>." >&2
    echo "  On a host with a single Docker engine, set FABRIC_ANY_CONTEXT=1 instead." >&2
    return 1
  fi
  return 0
}

# Context exists and its VM answers. Used by every script except the start
# path in fabric-up.sh (which creates the profile first).
fabric_require_ctx() {
  fabric_refuse_wrong_ctx || return 1
  if ! docker context inspect "$CTX" >/dev/null 2>&1; then
    echo "fabric: docker context $CTX does not exist." >&2
    echo "Create and start it: scripts/fabric-up.sh" >&2
    return 1
  fi
  if ! docker --context "$CTX" info >/dev/null 2>&1; then
    echo "fabric: docker context $CTX exists but its VM is not running." >&2
    echo "Start it: colima start --profile $FABRIC_COLIMA_PROFILE" >&2
    return 1
  fi
  return 0
}

# Run a shell command on the machine whose KERNEL carries the BGP sockets.
# TCP-MD5 is signed by that kernel, so "is MD5 compiled in" and "how many
# signature failures" are questions about it and not about the container or
# about this machine. On macOS that kernel is inside the Colima VM and the way
# in is `colima ssh`; on Linux the engine is the host and the way in is `sh`.
# Same command text either way, so the recorded evidence reads the same.
fabric_engine_sh() {
  if fabric_manages_vm; then
    colima ssh --profile "$FABRIC_COLIMA_PROFILE" -- sh -c "$1"
  else
    sh -c "$1"
  fi
}

# Every daemon call. Callers must have passed fabric_require_ctx
# (or just created the profile) so this never targets another engine.
dk() {
  docker --context "$CTX" "$@"
}

fabric_compose() {
  dk compose -p "$FABRIC_PROJECT" -f "$FABRIC_DIR/compose.yaml" "$@"
}

# The VM's reachable address — Lima's vzNAT interface (col0), present only
# when the profile runs with network.address (`colima start --network-address`).
# Empty when it does not: then no host route can reach this VM. The address
# is per PROFILE, not per machine — reading another profile's address and
# routing to it is a real mistake, made by hand on 2026-09-20.
colima_vm_address() {
  colima list --json 2>/dev/null | python3 -c '
import json, sys
want = sys.argv[1]
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    d = json.loads(line)
    if d.get("name") == want:
        print(d.get("address") or "")
        break
' "$FABRIC_COLIMA_PROFILE"
}

# colima start --activate (the default) switches the active docker context.
# Restore the caller's context on EXIT, always — a leftover colima-bgp-fabric
# context orphans whatever else the machine runs (measured 2026-09-20).
fabric_save_ctx() {
  FABRIC_PREV_CTX=$(docker context show 2>/dev/null || true)
}

fabric_restore_ctx() {
  local now
  [ -n "${FABRIC_PREV_CTX:-}" ] || return 0
  now=$(docker context show 2>/dev/null || true)
  if [ -n "$now" ] && [ "$now" != "$FABRIC_PREV_CTX" ]; then
    docker context use "$FABRIC_PREV_CTX" >/dev/null 2>&1 || \
      echo "fabric: WARNING could not restore docker context $FABRIC_PREV_CTX (now $now)" >&2
  fi
}
