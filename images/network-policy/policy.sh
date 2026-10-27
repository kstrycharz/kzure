#!/bin/sh
set -eu
if [ "${1:-inspect}" = inspect ]; then
  iptables --version
  iptables -w 5 -S DOCKER-USER
  if [ -r /proc/sys/net/bridge/bridge-nf-call-iptables ]; then cat /proc/sys/net/bridge/bridge-nf-call-iptables; fi
  exit 0
fi
if [ "${1:-}" != apply ]; then exit 2; fi
install="${KZURE_INSTALL_ID:-}"
case "$install" in *[!0-9a-f]*|'') exit 2;; esac
test "${#install}" -eq 32
prefix="KZ$(printf '%s' "$install" | cut -c1-8)"
test -s /run/kzure/policy.rules
for suffix in F I O; do
  stable="${prefix}${suffix}"
  if iptables -w 5 -S "$stable" >/tmp/chain 2>/dev/null; then
    grep -F "kzure:$install" /tmp/chain >/dev/null || { echo 'Existing chain ownership mismatch' >&2; exit 3; }
  fi
done
# The restore is an atomic filter-table transaction. It never flushes Docker or
# operator chains; the input contains only this installation's generated chains.
iptables-restore -w 5 --noflush < /run/kzure/policy.rules
for pair in 'DOCKER-USER F' 'INPUT I' 'OUTPUT O'; do
  set -- $pair
  iptables -w 5 -C "$1" -j "${prefix}$2" 2>/dev/null || iptables -w 5 -I "$1" 1 -j "${prefix}$2"
done
# Revoke old state only for addresses explicitly protected by this generation.
if [ -f /run/kzure/addresses ]; then
  while IFS= read -r address; do
    case "$address" in *[!0-9.]*|'') exit 4;; esac
    conntrack -D -s "$address" >/dev/null 2>&1 || true
    conntrack -D -d "$address" >/dev/null 2>&1 || true
  done < /run/kzure/addresses
fi
# Drop obsolete generated chains belonging to this installation only.
iptables -w 5 -S | awk -v p="${prefix}G" '$1=="-N" && index($2,p)==1 {print $2}' >/tmp/old-chains
while IFS= read -r chain; do
  grep -F ":$chain " /run/kzure/policy.rules >/dev/null || iptables -w 5 -F "$chain"
done < /tmp/old-chains
while IFS= read -r chain; do
  grep -F ":$chain " /run/kzure/policy.rules >/dev/null || iptables -w 5 -X "$chain"
done < /tmp/old-chains
echo 'Applied Kzure-owned filter generation'
