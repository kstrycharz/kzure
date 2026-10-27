#!/bin/sh
set -eu
mkdir -p /run/sshd /data/home/kzure /data/.kzure-host
chmod 700 /data/.kzure-host
chown 1000:1000 /data/home/kzure
if [ ! -s /data/.kzure-host/ssh_host_ed25519_key ]; then
  if [ -s /run/kzure/host_key ]; then cp /run/kzure/host_key /data/.kzure-host/ssh_host_ed25519_key; else ssh-keygen -q -t ed25519 -N '' -f /data/.kzure-host/ssh_host_ed25519_key; fi
fi
ssh-keygen -y -f /data/.kzure-host/ssh_host_ed25519_key > /data/.kzure-host/ssh_host_ed25519_key.pub
chmod 600 /data/.kzure-host/ssh_host_ed25519_key
test -f /run/kzure/authorized_keys || touch /run/kzure/authorized_keys
chmod 644 /run/kzure/authorized_keys
exec /usr/sbin/sshd -D -e
