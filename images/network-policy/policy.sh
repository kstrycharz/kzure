#!/bin/sh
set -eu
if [ "${1:-inspect}" = inspect ]; then
  iptables --version
  iptables -w 5 -S DOCKER-USER
  if [ -r /proc/sys/net/bridge/bridge-nf-call-iptables ]; then cat /proc/sys/net/bridge/bridge-nf-call-iptables; fi
  exit 0
fi
case "${1:-}" in apply|reconcile) ;; *) exit 2;; esac
install="${KZURE_INSTALL_ID:-}"
case "$install" in *[!0-9a-f]*|'') exit 2;; esac
test "${#install}" -eq 32
prefix="KZ$(printf '%s' "$install" | cut -c1-8)"
test -s /run/kzure/policy.rules
# Capture canonical kernel rules, including their order, instead of comparing
# restore input with iptables' differently formatted output. Ignore packet/byte
# counters and unrelated operator rules. Hook positions remain part of the hash.
snapshot() {
  iptables-save -t filter > /tmp/filter.rules
  awk -v p="$prefix" '
    /^:/ { if (index(substr($1,2),p)==1) { sub(/\[[0-9]+:[0-9]+\]/,"[0:0]"); print } }
    $1=="-A" {
      if (index($2,p)==1) print
      if ($2=="DOCKER-USER" || $2=="INPUT" || $2=="OUTPUT") {
        position[$2]++
        if (index($0,"-j " p)>0) print "hook",position[$2],$0
      }
    }
  ' /tmp/filter.rules > /tmp/owned.rules
  sha256sum /tmp/owned.rules | cut -d' ' -f1
}
hooks_valid() {
  for pair in 'DOCKER-USER F' 'INPUT I' 'OUTPUT O'; do
    set -- $pair
    iptables -w 5 -S "$1" > /tmp/hook.rules || return 1
    first=$(awk '$1=="-A" {print; exit}' /tmp/hook.rules)
    [ "$first" = "-A $1 -j ${prefix}$2" ] || return 1
    [ "$(grep -Fxc -- "-A $1 -j ${prefix}$2" /tmp/hook.rules)" -eq 1 ] || return 1
  done
}
expected="${KZURE_POLICY_FINGERPRINT:-}"
case "$expected" in *[!0-9a-f]*) exit 2;; esac
if [ -n "$expected" ]; then
  test "${#expected}" -eq 64
  observed=$(snapshot)
  if [ "$observed" = "$expected" ] && hooks_valid; then
    echo 'Verified Kzure-owned filter generation; no changes'
    echo "KZURE_POLICY_FINGERPRINT=$observed"
    exit 0
  fi
fi
for suffix in F I O; do
  stable="${prefix}${suffix}"
  if iptables -w 5 -S "$stable" >/tmp/chain 2>/dev/null; then
    if ! grep -F "kzure:$install" /tmp/chain >/dev/null; then
      # A previously observed stable chain may have been flushed. Never replace
      # nonempty foreign content merely because its short chain prefix matches.
      if [ -z "$expected" ] || grep -q '^-A ' /tmp/chain; then
        echo 'Existing chain ownership mismatch' >&2; exit 3
      fi
    fi
  fi
done
# The restore is an atomic filter-table transaction. It never flushes Docker or
# operator chains; the input contains only this installation's generated chains.
iptables-restore -w 5 --noflush < /run/kzure/policy.rules
for pair in 'DOCKER-USER F' 'INPUT I' 'OUTPUT O'; do
  set -- $pair
  # Re-establish exactly one first-position owned hook, preserving every other
  # host rule. This also repairs a moved or duplicated hook.
  while iptables -w 5 -C "$1" -j "${prefix}$2" 2>/dev/null; do
    iptables -w 5 -D "$1" -j "${prefix}$2"
  done
  iptables -w 5 -I "$1" 1 -j "${prefix}$2"
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
hooks_valid || { echo 'Owned policy hook verification failed' >&2; exit 5; }
echo "KZURE_POLICY_FINGERPRINT=$(snapshot)"
