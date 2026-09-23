#!/usr/bin/env bash
# cleanup.sh — stop project bgp-fabric in the Colima VM.
# Only this project on this context; anything else on the machine is untouched.
#   cleanup.sh
set -euo pipefail
cd "$(dirname "$0")"
# shellcheck disable=SC1091
. scripts/fabric-lib.sh
if ! fabric_refuse_wrong_ctx; then
  exit 1
fi
scripts/fabric-down.sh
echo "bgp-fabric removed (KEPT: the Colima profile $FABRIC_COLIMA_PROFILE — cleanup.sh never deletes a VM)"
