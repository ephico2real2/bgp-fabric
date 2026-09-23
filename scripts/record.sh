#!/usr/bin/env bash
# record.sh — run one command, show it on screen, and append the command AND its real output to a
# transcript file. Every demo in this repo captures its evidence this way, so the outputs quoted in
# the READMEs are never retyped from memory or reconstructed from documentation.
#
# Usage:
#   scripts/record.sh <transcript-file> <command> [args...]
#
# Example:
#   scripts/record.sh output/transcript.txt \
#     docker --context colima-bgp-fabric compose -p bgp-fabric ps
#
# Notes for readers:
#   - stderr is folded into stdout on purpose: a demo's failure output is evidence too, and the
#     whole point of these transcripts is that the failures are kept, not tidied away.
#   - the exit code is recorded when it is non-zero, because several demos here PROVE something by
#     failing (a curl that times out is how an L3 policy denial looks).
#   - a UTC timestamp is written per command so a transcript can be lined up against `hubble observe`
#     output, which is timestamped in the same way.
#   - RECORD_STRICT=1 (opt-in): after tee, exit with the wrapped command's rc so a `set -e`
#     caller (apply.sh) fails on a failed wait. The default stays 0 — demos prove things by
#     failing, so record.sh must not abort a set -e caller unless asked.
set -uo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <transcript-file> <command> [args...]" >&2
  exit 2
fi

OUT="$1"; shift
mkdir -p "$(dirname "$OUT")"

{
  printf '### %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf '$ %s\n' "$*"
  "$@" 2>&1
  rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '[exit code: %s]\n' "$rc"
  fi
  printf '\n'
  if [ "${RECORD_STRICT:-}" = 1 ]; then
    exit "$rc"
  fi
} | tee -a "$OUT"
