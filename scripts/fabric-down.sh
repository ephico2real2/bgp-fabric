#!/usr/bin/env bash
# fabric-down.sh — stop project bgp-fabric in the Colima VM.
# Only this project on this context; anything else on the machine is untouched.
# Images stay unless --rmi (removes bgp-fabric-agent:local and bgp-fabric-dashboard:local).
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
. scripts/fabric-lib.sh

rmi=0
if [ "${1:-}" = --rmi ]; then
  rmi=1
elif [ $# -gt 0 ]; then
  echo "usage: $0 [--rmi]" >&2
  exit 2
fi

if ! fabric_refuse_wrong_ctx; then
  exit 1
fi
if ! fabric_require_ctx; then
  exit 1
fi

fabric_compose down --remove-orphans
if [ "$rmi" -eq 1 ]; then
  dk image rm -f "$FABRIC_ROUTER_IMAGE" \
    "$FABRIC_DASHBOARD_IMAGE" >/dev/null 2>&1 || true
  echo "fabric-colima-down: project $FABRIC_PROJECT stopped; :colima images removed (Colima VM kept)"
else
  echo "fabric-colima-down: project $FABRIC_PROJECT stopped (Colima VM kept; images stay unless --rmi)"
fi
