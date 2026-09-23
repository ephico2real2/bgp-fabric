#!/usr/bin/env bash
# test: syntax and node --test for the bgp-fabric dashboard UI helpers.
#
# Both copies are checked. They are kept identical by
# tests/dashboard-copies-in-sync.sh, but running the suite against each one
# means a copy that drifts fails here too rather than only in the sync gate.
# usage: bash tests/dashboard-ui-unit.sh
set -euo pipefail
R=$(cd "$(dirname "$0")/.." && pwd)
for d in dashboard/static dashboard/static; do
  cd "$R/$d"
  node --check ui.js
  node --check app.js
  node --test ui.test.js
  echo "  ok: $d"
done
echo "TEST PASS: dashboard ui.js + app.js syntax and unit tests,"
