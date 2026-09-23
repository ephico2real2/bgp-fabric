#!/bin/sh
# Render ${FABRIC_BGP_PASSWORD} from the environment into /etc/frr/frr.conf,
# then exec the image's docker-start. Loopbacks are NOT set here: zebra
# applies `interface lo` / `ip address` from frr.conf so the /32, the
# `network` statement and `bgp router-id` stay one file (no race with
# zebra, no second source of truth).
set -eu
tmpl=/etc/frr/frr.conf.tmpl
out=/etc/frr/frr.conf
pw=${FABRIC_BGP_PASSWORD:-lab-bgp}
if [ ! -f "$tmpl" ]; then
  echo "fabric-entrypoint: missing $tmpl" >&2
  exit 1
fi
python3 -c '
import os, pathlib, sys
src, dst, pw = sys.argv[1], sys.argv[2], sys.argv[3]
text = pathlib.Path(src).read_text()
pathlib.Path(dst).write_text(text.replace("${FABRIC_BGP_PASSWORD}", pw))
os.chown(dst, 100, 101)
os.chmod(dst, 0o640)
' "$tmpl" "$out" "$pw"
# Out-of-band mgmt 10.200.200.0/24: same-LAN dashboard↔agent is L2
# (INPUT/OUTPUT). Drop FORWARD so wan/client0 cannot transit via a
# dual-homed router onto the management LAN.
mgmt=$(ip -o addr show to 10.200.200.0/24 2>/dev/null | awk '{print $2; exit}' || true)
if [ -n "$mgmt" ] && command -v iptables >/dev/null 2>&1; then
  iptables -C FORWARD -o "$mgmt" -j DROP 2>/dev/null || iptables -A FORWARD -o "$mgmt" -j DROP
  iptables -C FORWARD -i "$mgmt" -j DROP 2>/dev/null || iptables -A FORWARD -i "$mgmt" -j DROP
  # FORWARD only covers transit. A packet addressed to this router's OWN
  # management IP arrives on a data-plane interface and is delivered locally
  # (Linux accepts an address on any interface), so it never reaches FORWARD:
  # measured 2026-09-20, client0 read http://10.200.200.1:8080/show/bgp-summary
  # (200, the edge's whole table) through its default route. The agent port is
  # therefore accepted on mgmt and on lo (a container-local health probe is
  # delivered over lo) and dropped everywhere else.
  agent_port=${FRR_AGENT_ADDR:-}
  agent_port=${agent_port##*:}
  case "$agent_port" in
    ''|*[!0-9]*) agent_port="" ;;
  esac
  if [ -n "$agent_port" ]; then
    iptables -C INPUT -p tcp --dport "$agent_port" -i lo -j ACCEPT 2>/dev/null \
      || iptables -A INPUT -p tcp --dport "$agent_port" -i lo -j ACCEPT
    iptables -C INPUT -p tcp --dport "$agent_port" -i "$mgmt" -j ACCEPT 2>/dev/null \
      || iptables -A INPUT -p tcp --dport "$agent_port" -i "$mgmt" -j ACCEPT
    iptables -C INPUT -p tcp --dport "$agent_port" -j DROP 2>/dev/null \
      || iptables -A INPUT -p tcp --dport "$agent_port" -j DROP
  fi
fi
exec /usr/lib/frr/docker-start
