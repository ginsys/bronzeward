#!/usr/bin/env bash
# Write the third-party notices of every release archive (ginsys/bronzeward#262 P3) to $1: the
# licence and notice files of each Go module the two binaries link on Linux and macOS, of the Go
# distribution itself, and of each npm package the web bundle includes (web/'s production
# dependencies; run npm ci in web/ first). A component with no licence file fails the build
# rather than ship without its notice. Generated at packaging time, never committed.
set -euo pipefail
out=$1
cd "$(git rev-parse --show-toplevel)"

notice() { # <component> <directory>
	local files
	files=$(find "$2" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' -o -iname 'PATENTS*' \) | LC_ALL=C sort)
	if [ -z "$files" ]; then
		echo "notices: $1 has no licence file in $2" >&2
		return 1
	fi
	while IFS= read -r f; do
		printf '================================================================================\n%s: %s\n\n' "$1" "${f##*/}"
		cat "$f"
		printf '\n'
	done <<<"$files"
}

modules=$(for os in linux darwin; do
	CGO_ENABLED=0 GOOS=$os go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/bronzewardctl ./cmd/bronzeward || exit 1
done | LC_ALL=C sort -u)
packages=$(cd web && npm ls --omit=dev --all --parseable | tail -n +2 | LC_ALL=C sort -u)

{
	printf 'Third-party notices for Bronzeward. Bronzeward itself: see LICENSE-STATUS.md.\n\n'
	notice "Go $(go env GOVERSION) (standard library and runtime)" "$(go env GOROOT)"
	while read -r mod dir; do
		notice "$mod" "$dir"
	done <<<"$modules"
	# vite build also inserts its module-preload helper into the bundle.
	while IFS= read -r dir; do
		notice "npm:$(cd "$dir" && node -p 'const p = require("./package.json"); p.name + "@" + p.version')" "$dir"
	done <<<"$packages"$'\n'"$PWD/web/node_modules/vite"
} >"$out"
