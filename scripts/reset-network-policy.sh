#!/bin/sh
set -eu
# Only called by the explicit destructive reinstall workflow after workers and
# workloads stop. Never flush Docker's or the operator's filter table.
install="${KZURE_INSTALL_ID:-}"
case "$install" in *[!0-9a-f]*|'') exit 2;; esac
test "${#install}" -eq 32
prefix="KZ$(printf '%s' "$install" | cut -c1-8)"
iptables -w 5 -S > /tmp/kzure-reset-rules
awk -v p="$prefix" '$1=="-N" && index($2,p)==1 {print $2}' /tmp/kzure-reset-rules > /tmp/kzure-reset-chains
if [ ! -s /tmp/kzure-reset-chains ]; then echo 'No previous Kzure policy chains remain'; exit 0; fi
for suffix in F I O; do
  iptables -w 5 -S "${prefix}$suffix" > /tmp/kzure-reset-owner
  grep -F -- "kzure:$install" /tmp/kzure-reset-owner >/dev/null || { echo 'Policy chain ownership mismatch' >&2; exit 3; }
done
# Reject unknown chain names and foreign references before the first mutation.
while IFS= read -r chain; do
  printf '%s\n' "$chain" | grep -Eq "^${prefix}(F|I|O|G[0-9a-f]{8}(F|I|O|I[0-9a-f]{6}|O[0-9a-f]{6}))$" || exit 3
done < /tmp/kzure-reset-chains
awk -v p="$prefix" '
 $1=="-A" {
  for(i=3;i<NF;i++)if($i=="-j" && index($(i+1),p)==1){
   if(index($2,p)==1)next
   if(NF==4 && (($2=="DOCKER-USER" && $4==p"F") || ($2=="INPUT" && $4==p"I") || ($2=="OUTPUT" && $4==p"O")))next
   exit 1
  }
 }' /tmp/kzure-reset-rules || { echo 'Foreign references prevent policy cleanup' >&2; exit 3; }
for pair in 'DOCKER-USER F' 'INPUT I' 'OUTPUT O'; do
  set -- $pair
  while iptables -w 5 -C "$1" -j "${prefix}$2" 2>/dev/null; do iptables -w 5 -D "$1" -j "${prefix}$2"; done
done
while IFS= read -r chain; do iptables -w 5 -F "$chain"; done < /tmp/kzure-reset-chains
while IFS= read -r chain; do iptables -w 5 -X "$chain"; done < /tmp/kzure-reset-chains
echo 'Removed the previous installation policy chains and hooks'
