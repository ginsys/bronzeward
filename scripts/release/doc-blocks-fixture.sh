#!/usr/bin/env bash
# The disposable server that scripts/release/run-doc-blocks.sh runs the operator pages' blocks against
# (ginsys/bronzeward#405): the packaged server from the build's dist/, on the development database
# (mise run dev-db) and the fixture issuer (fixtures/oidc), both as `mise run dev` starts them. It
# starts no secret provider, so the inventory is empty. `start` leaves the server on 127.0.0.1:8080,
# the issuer on 127.0.0.1:5556 and, in <state>/token.json, a synthetic viewer's sign-in in
# bronzewardctl's own token file format. `stop` ends both processes; the database container stays
# (mise run dev-down removes it, and is shared by every worktree).
#
#   doc-blocks-fixture.sh start <state directory> <dist directory>
#   doc-blocks-fixture.sh stop <state directory>
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
issuer=http://127.0.0.1:5556
state=${2:?state directory}

# free <port>: a port is free only when the connection is refused (curl exit 7), as in `mise run dev`.
free() {
	local rc=0
	curl -s --noproxy '*' -o /dev/null --max-time 2 "http://127.0.0.1:$1/" || rc=$?
	[ "$rc" -eq 7 ] || { echo "doc-blocks-fixture: 127.0.0.1:$1 is in use; stop what listens there first" >&2; return 1; }
}

# up <name> <url>: wait for the process just started and its address.
up() {
	if curl -sf --noproxy '*' -o /dev/null --max-time 2 --retry 15 --retry-connrefused --retry-delay 1 "$2" &&
		kill -0 "$(cat "$state/$1.pid")" 2>/dev/null; then
		return 0
	fi
	echo "doc-blocks-fixture: the $1 did not come up at $2 (see $state/record/$1.log)" >&2
	return 1
}

case ${1:-} in
start)
	dist=$(cd "${3:?dist directory}" && pwd)
	free 8080
	free 5556
	# The logs go under record/, which CI uploads, so a fixture that fails to start leaves them behind.
	mkdir -p "$state/server" "$state/record"
	mise run dev-db >/dev/null
	tar -xzf "$dist/bronzeward_${BW_VERSION#v}_linux_amd64.tar.gz" -C "$state/server" bronzeward
	go build -o "$state/oidc-fixture" ./fixtures/oidc
	"$state/oidc-fixture" keygen -out "$state/oidc.key"
	"$state/oidc-fixture" serve -key "$state/oidc.key" -issuer "$issuer" >"$state/record/issuer.log" 2>&1 &
	echo $! >"$state/issuer.pid"
	up issuer "$issuer/.well-known/openid-configuration"
	if ! "$state/server/bronzeward" migrate -config examples/bronzeward.yaml >"$state/record/migrate.log" 2>&1; then
		echo "doc-blocks-fixture: migrate failed (see $state/record/migrate.log)" >&2
		exit 1
	fi
	"$state/server/bronzeward" serve -config examples/bronzeward.yaml >"$state/record/server.log" 2>&1 &
	echo $! >"$state/server.pid"
	up server http://127.0.0.1:8080/livez
	# The client's own file: one JSON object holding the access token, owner-only.
	( umask 077
	  jwt=$("$state/oidc-fixture" mint -key "$state/oidc.key" -issuer "$issuer" -human h-viewer)
	  printf '{"accessToken":"%s"}\n' "$jwt" >"$state/token.json" )
	;;
stop)
	# Each process is waited for (BW_DOC_STOP_TRIES half-seconds, default 30, so 15 s; the test sets
	# fewer) before its pid file goes; one still alive fails the stop and keeps its pid file.
	rc=0
	for name in server issuer; do
		[ -f "$state/$name.pid" ] || continue
		pid=$(cat "$state/$name.pid")
		kill "$pid" 2>/dev/null || true
		for _ in $(seq 1 "${BW_DOC_STOP_TRIES:-30}"); do
			kill -0 "$pid" 2>/dev/null || break
			sleep 0.5
		done
		if kill -0 "$pid" 2>/dev/null; then
			echo "doc-blocks-fixture: the $name (pid $pid) is still running after SIGTERM" >&2
			rc=1
		else
			rm -f "$state/$name.pid"
		fi
	done
	exit "$rc"
	;;
*)
	echo "usage: doc-blocks-fixture.sh start <state directory> <dist directory> | stop <state directory>" >&2
	exit 64
	;;
esac
