#!/bin/sh
set -eu
mkdir -p /run/xrdp /run/dbus /data/home/kzure /data/.kzure-desktop
chown 1000:1000 /data/home/kzure
chmod 700 /data/.kzure-desktop
test -s /run/kzure/password
printf 'kzure:%s\n' "$(cat /run/kzure/password)" | chpasswd
if [ ! -s /data/.kzure-desktop/key.pem ]; then
  openssl req -x509 -newkey rsa:3072 -nodes -days 365 -subj "/CN=${KZURE_RESOURCE_ID:-kzure-desktop}" -keyout /data/.kzure-desktop/key.pem -out /data/.kzure-desktop/cert.pem 2>/dev/null
fi
chmod 600 /data/.kzure-desktop/key.pem
sed -i 's|^certificate=.*|certificate=/data/.kzure-desktop/cert.pem|;s|^key_file=.*|key_file=/data/.kzure-desktop/key.pem|' /etc/xrdp/xrdp.ini
/usr/sbin/xrdp-sesman --nodaemon &
exec /usr/sbin/xrdp --nodaemon
