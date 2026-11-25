#!/usr/bin/env bash
# The disposable server that scripts/release/run-doc-blocks.sh runs the operator pages' blocks against
# (ginsys/bronzeward#405): the packaged server from the build's dist/, on the development database
# (mise run dev-db) and the fixture issuer (fixtures/oidc), both as `mise run dev` starts them, the
# issuer with a device sign-in that approves at once for a synthetic viewer. It starts no secret
# provider, so the inventory is empty. It also hosts the build's web archive over TLS, with the
# archive's own Caddyfile in the pinned Caddy image (packaging/versions.env) at BW_DOC_WEB_URL (a
# https://localhost:<port> URL), under Caddy's own throwaway CA, with a bronzeward-client.json that
# names the fixture issuer. `start` leaves the server on 127.0.0.1:8080, the issuer on
# 127.0.0.1:5556, the CA certificate in <state>/office-ca.pem and, in <state>/token.json, a synthetic
# viewer's sign-in in bronzewardctl's own token file format. `stop` ends both processes and removes
# the web container; the database container stays (mise run dev-down removes it, and is shared by
# every worktree). BW_DOC_DOCKER, set only by the test, stands in for docker in `stop`.
#
#   doc-blocks-fixture.sh start <state directory> <dist directory>
#   doc-blocks-fixture.sh stop <state directory>
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
issuer=http://127.0.0.1:5556
state=${2:?state directory}
dk=${BW_DOC_DOCKER:-docker}
web=bw-doc-blocks-web

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
	port=${BW_DOC_WEB_URL:?BW_DOC_WEB_URL, as https://localhost:8443}
	port=${port#https://localhost:}
	free 8080
	free 5556
	free "$port"
	# The logs go under record/, which CI uploads, so a fixture that fails to start leaves them behind.
	mkdir -p "$state/server" "$state/record"
	mise run dev-db >/dev/null
	tar -xzf "$dist/bronzeward_${BW_VERSION#v}_linux_amd64.tar.gz" -C "$state/server" bronzeward
	# The container gets a copy of dist/, because install.md unpacks into it; the server's version goes in the record.
	cp -a "$dist" "$state/dist-copy"
	"$state/server/bronzeward" version >>"$state/record/doc-blocks.log" 2>&1
	go build -o "$state/oidc-fixture" ./fixtures/oidc
	"$state/oidc-fixture" keygen -out "$state/oidc.key"
	"$state/oidc-fixture" serve -key "$state/oidc.key" -issuer "$issuer" -device-human h-viewer >"$state/record/issuer.log" 2>&1 &
	echo $! >"$state/issuer.pid"
	up issuer "$issuer/.well-known/openid-configuration"
	if ! "$state/server/bronzeward" migrate -config examples/bronzeward.yaml >"$state/record/migrate.log" 2>&1; then
		echo "doc-blocks-fixture: migrate failed (see $state/record/migrate.log)" >&2
		exit 1
	fi
	"$state/server/bronzeward" serve -config examples/bronzeward.yaml >"$state/record/server.log" 2>&1 &
	echo $! >"$state/server.pid"
	up server http://127.0.0.1:8080/livez
	# The web host: the build's own web archive and Caddyfile, with a client configuration file next to the
	# web files. The global options added in front keep Caddy off the host's port 80 and admin port,
	# which the host network would otherwise take; the site block is the archive's, unchanged.
	name=bronzeward-web_${BW_VERSION#v}
	mkdir -p "$state/web"
	tar -xzf "$dist/$name.tar.gz" -C "$state/web"
	printf '{"issuer":"%s","audience":"bronzeward","cliClientId":"bw-docs","webClientId":"bw-docs-web"}\n' "$issuer" \
		>"$state/web/$name/dist/bronzeward-client.json"
	# default_bind keeps the site on loopback, which the host network would otherwise publish.
	{ printf '{\n\tadmin off\n\tauto_https disable_redirects\n\tdefault_bind 127.0.0.1\n}\n'; cat "$state/web/$name/Caddyfile"; } >"$state/web/$name/Caddyfile.doc-blocks"
	image=$(sed -n 's/^CADDY_IMAGE=//p' packaging/versions.env)
	case $image in *@sha256:*) ;; *) echo "doc-blocks-fixture: CADDY_IMAGE in packaging/versions.env has no digest" >&2; exit 1 ;; esac
	# The marker comes first, so that `stop` removes a container whose start failed halfway.
	echo "$web" >"$state/web.container"
	"$dk" run -d --name "$web" --network host -v "$state/web/$name:/srv:ro" \
		-e "BW_SITE_ADDRESS=$BW_DOC_WEB_URL" -e BW_SERVER=127.0.0.1:8080 -e BW_WEB_ROOT=/srv/dist \
		-e "BW_ISSUER_ORIGIN=$issuer" -e "BW_PROVIDER_ORIGINS=$issuer" \
		"$image" caddy run --config /srv/Caddyfile.doc-blocks --adapter caddyfile >/dev/null
	# The CA is read from the container, so no root-owned file is left in the state directory. It exists
	# once Caddy has made the site's certificate; the check below then verifies the site against it.
	for _ in $(seq 1 30); do
		if "$dk" exec "$web" cat /data/caddy/pki/authorities/local/root.crt >"$state/office-ca.pem" 2>/dev/null &&
			curl -sf --noproxy '*' --cacert "$state/office-ca.pem" -o /dev/null --max-time 2 "$BW_DOC_WEB_URL/livez"; then
			web_up=1
			break
		fi
		sleep 1
	done
	if [ -z "${web_up:-}" ]; then
		"$dk" logs "$web" >"$state/record/web.log" 2>&1 || true
		echo "doc-blocks-fixture: the web host did not answer on $BW_DOC_WEB_URL (see $state/record/web.log)" >&2
		exit 1
	fi
	chmod 0644 "$state/office-ca.pem"
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
	if [ -f "$state/web.container" ]; then
		"$dk" logs "$web" >"$state/record/web.log" 2>&1 || true
		"$dk" rm -f "$web" >/dev/null 2>&1 || true
		# The absence is read from a listing, as for the clean container (run-doc-blocks.sh).
		if left=$("$dk" ps -aq --filter "name=^$web\$") && [ -z "$left" ]; then
			rm -f "$state/web.container"
		else
			echo "doc-blocks-fixture: could not confirm that the container $web is gone" >&2
			rc=1
		fi
	fi
	exit "$rc"
	;;
*)
	echo "usage: doc-blocks-fixture.sh start <state directory> <dist directory> | stop <state directory>" >&2
	exit 64
	;;
esac
