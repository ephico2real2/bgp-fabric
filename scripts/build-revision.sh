#!/usr/bin/env bash
# build-revision.sh — the two build arguments the dashboard image is stamped
# with, printed as `<revision>TAB<built>`.
#
# One source for both fabrics, because a revision typed by hand is an
# assertion nobody checks. Measured 2026-09-23: the Colima lab served a page
# labelled `build 4cf1864` — a commit whose tree contains neither the endpoint
# nor the function that renders that label — because the sha was passed on the
# command line from a working tree that had uncommitted changes. git is asked
# here instead, so the number on the page cannot be a typo.
#
# A dirty tree keeps its `-dirty` suffix all the way to the header: the sha
# alone would name a commit that does not contain what is running, which is
# the exact failure this stamp exists to prevent.
#
#   usage: IFS=$'\t' read -r REVISION BUILT < <(scripts/build-revision.sh)
set -euo pipefail
cd "$(dirname "$0")/.."

revision=unknown
if git rev-parse --git-dir >/dev/null 2>&1; then
  revision=$(git rev-parse HEAD 2>/dev/null || echo unknown)
  # --porcelain honours .gitignore, so a built binary sitting beside the
  # source does not make every build "dirty".
  if [ "$revision" != unknown ] && [ -n "$(git status --porcelain)" ]; then
    revision="${revision}-dirty"
  fi
fi

printf '%s\t%s\n' "$revision" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
