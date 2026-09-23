#!/bin/sh
# fabric-router-start — background the show-only agent, then exec the
# mounted fabric-entrypoint (password render + docker-start).
#
# Privilege drop (measured 2026-09-20 on quay.io/frrouting/frr:10.7.1):
# - `which su-exec setpriv` → no su-exec; /bin/setpriv is BusyBox and has
#   no --reuid/--regid (only capability flags). BusyBox chroot has no
#   --userspec. Do not apk add at runtime.
# - `su -s /bin/sh frr -c` works: uid=100(frr) gid=101(frr) groups=102(frrvty).
# - `su frr` without -s fails: "This account is not available" (shell is
#   /sbin/nologin).
# - vtysh as uid 100: rc=0 and JSON on stdout (sockets /var/run/frr are
#   frr:frr; frr is in frrvty). No fallback to root.
set -eu
if [ -z "${FRR_AGENT_ADDR:-}" ]; then
  echo "fabric-router-start: FRR_AGENT_ADDR is required" >&2
  exit 1
fi
su -s /bin/sh frr -c '/usr/local/bin/frr-agent' &
exec /usr/local/bin/fabric-entrypoint
