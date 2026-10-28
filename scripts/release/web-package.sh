#!/usr/bin/env bash
# Build the web package at BW_VERSION and write build/bronzeward-web_<version>.tar.gz, plus the
# notices file the archives share (ginsys/bronzeward#262 P3, P6): web/dist, web/headers.json, the
# Caddy configuration rendered from it, the example client configuration, the licence status and
# the notices. Reproducible: npm ci from the lockfile, sorted names, the commit time on every
# entry, numeric owner 0 and gzip -n.
set -euo pipefail
: "${BW_VERSION:?set BW_VERSION, e.g. v0.0.0-dev}"
cd "$(git rev-parse --show-toplevel)"
out=build
name=bronzeward-web_${BW_VERSION#v}
epoch=$(git log -1 --format=%ct)

(cd web && npm ci --no-audit --no-fund && npm run types && npx vite build)
rm -rf "$out"
mkdir -p "$out/$name"
bash scripts/release/notices.sh "$out/THIRD_PARTY_NOTICES.txt"
cp -R web/dist "$out/$name/dist"
cp web/headers.json packaging/bronzeward-client.example.json LICENSE-STATUS.md "$out/THIRD_PARTY_NOTICES.txt" "$out/$name/"
python3 scripts/release/render-caddy.py web/headers.json >"$out/$name/Caddyfile"
tar --sort=name --format=gnu --mtime="@$epoch" --owner=0 --group=0 --numeric-owner \
	--mode='u+rwX,go+rX,go-w' -C "$out" -cf - "$name" | gzip -n -9 >"$out/$name.tar.gz"
