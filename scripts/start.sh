#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
node scripts/configure.mjs
node scripts/images.mjs
docker compose build
docker compose up -d --wait
printf '%s\n' 'Kzure is running at http://localhost:8088' 'Your administrator credentials are in .local/admin-credentials.txt'
