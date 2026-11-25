#!/usr/bin/env bash
# Run the operator pages' blocks marked {.bw-run} (ginsys/bronzeward#405, the clean-install part of
# ginsys/bronzeward#262 acceptance 3). A marked block is a fence whose opener is "```bash {.bw-run}".
# A block that also carries .bw-host runs on the host from the repository root, where mise and the
# toolchain are: the build and its checksum check. Every other marked block runs, in page order, in a
# clean container (fixtures/versions.env CURL_IMAGE: curl, tar and CA certificates only) with the
# build's dist/ copied in, against the disposable server of doc-blocks-fixture.sh. In a container
# block one value is replaced and the record says so: the pages' server address,
# https://bronzeward.example.com, becomes the fixture's https://localhost:8443, whose private CA
# certificate is the pages' ./office-ca.pem. A block that also carries .bw-db is not this run's: it
# needs a secret provider and a machine. Fences are read by text, as scripts/verify-docs.py does,
# with no Markdown parser. The run fails when a block fails, when a marked block did not run, or when
# anything else ran.
#
#   run-doc-blocks.sh run [page...]            the whole run (CI: mise run doc-blocks)
#   run-doc-blocks.sh extract <dir> <page>...  write each marked block to <dir>, and <dir>/blocks.list
#   run-doc-blocks.sh account <blocks.list> <ran.list>
#
# BW_DOC_EXEC, BW_DOC_DOCKER and BW_DOC_FIXTURE, set only by scripts/test/run-doc-blocks-test.sh,
# stand in for the host shell, docker and the fixture script. BW_DOC_STATE is the state directory
# (default .dev/doc-blocks); its record/ holds doc-blocks.log, each block's output and the fixture's logs.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
default_pages=(docs/operator/clients/install.md docs/operator/clients/contexts.md
	docs/operator/clients/inventory.md docs/operator/clients/sign-in-and-renewal.md
	docs/operator/clients/quick-start-cli.md docs/operator/clients/client-troubleshooting.md)
# The server address the pages write, and the fixture's.
page_server=https://bronzeward.example.com
fixture_web=https://localhost:8443

# extract <outdir> <page>...: <outdir>/<page name>-<n>.sh for each marked block, and
# <outdir>/blocks.list, one "<id> <host|container>" line per block in page order. A marked fence the
# reader cannot see (indented, unterminated) or a .bw-host block after a container block is refused:
# a block that is skipped silently is the failure this script exists to catch.
extract() {
	local out=$1 page seen
	shift
	mkdir -p "$out"
	: >"$out/blocks.list"
	for page in "$@"; do
		# seen: container blocks listed so far, over every earlier page, so the order rule spans pages.
		seen=$(grep -c ' container$' "$out/blocks.list" || true)
		awk -v out="$out" -v name="$(basename "$page" .md)" -v page="$page" -v seen="$seen" '
			function die(msg) { printf "run-doc-blocks: %s:%d: %s\n", page, NR, msg > "/dev/stderr"; failed = 1; exit 1 }
			skip { if ($0 ~ /^```$/) skip = 0; next }
			/^```[a-z]* [{][^}]*[.]bw-db([ \t][^}]*)?[}]$/ && !open { skip = 1; next }
			/^```[a-z]* [{][^}]*[.]bw-run[^}]*[}]$/ && !open {
				n++; open = 1; lines = 0
				phase = ($0 ~ /[.]bw-host/) ? "host" : "container"
				if (phase == "host" && seen) die("a .bw-host block after a container block")
				if (phase == "container") seen = 1
				file = sprintf("%s/%s-%d.sh", out, name, n)
				printf "" > file
				printf "%s-%d %s\n", name, n, phase >> (out "/blocks.list")
				next
			}
			open && /^```$/ { if (!lines) die("an empty marked block"); open = 0; close(file); next }
			open { print > file; if ($0 !~ /^[ \t]*(#.*)?$/) lines++; next }
			/bw-run/ { die("bw-run outside a fence opener at the start of a line") }
			END { if (failed) exit 1; if (open || skip) die("unterminated fence"); if (!n) die("no marked block on this page") }
		' "$page" || return 1
	done
}

# account <blocks.list> <ran.list>: the ids that ran are the marked ids, each once.
account() {
	local want got
	want=$(cut -d' ' -f1 "$1" | LC_ALL=C sort)
	got=$(cut -d' ' -f1 "$2" | LC_ALL=C sort)
	[ -n "$want" ] || { echo "run-doc-blocks: no marked block found" >&2; return 1; }
	[ "$want" = "$got" ] && return 0
	echo "run-doc-blocks: the blocks that ran are not the marked blocks (< marked, > ran):" >&2
	diff <(printf '%s\n' "$want") <(printf '%s\n' "$got") >&2 || true
	return 1
}

# cleanup, the EXIT trap of run: remove the container and stop the fixture. A failure of either turns a
# successful run into a failed one (a process left behind is not a pass) and never hides an earlier failure.
cleanup() {
	local rc=$? left
	"$dk" rm -f "$ctr" >/dev/null 2>&1 || true
	# The absence is read from a listing. A failed `inspect` would read as "gone" when Docker is unreachable.
	if left=$("$dk" ps -aq --filter "name=^$ctr\$"); then
		if [ -n "$left" ]; then
			echo "run-doc-blocks: the container $ctr is still there after its removal" >&2
			rc=1
		fi
	else
		echo "run-doc-blocks: could not list containers to confirm that $ctr is gone" >&2
		rc=1
	fi
	"$fx" stop "$state" || { echo "run-doc-blocks: stopping the fixture failed" >&2; rc=1; }
	exit "$rc"
}

run() {
	# These are not local: the EXIT trap (cleanup) reads them after this function returned. dk, fx and
	# BW_DOC_EXEC are test seams: scripts/test/run-doc-blocks-test.sh stands in for Docker, the fixture and the host shell.
	state=${BW_DOC_STATE:-$root/.dev/doc-blocks}
	ctr=bw-doc-blocks
	dk=${BW_DOC_DOCKER:-docker}
	fx=${BW_DOC_FIXTURE:-$root/scripts/release/doc-blocks-fixture.sh}
	local rec image id phase file rc=0
	local -a pages=("$@")
	[ "${#pages[@]}" -gt 0 ] || pages=("${default_pages[@]}")
	cd "$root"
	rec=$state/record
	rm -rf "$state"
	mkdir -p "$rec"
	# The pages name this version in their own commands (install.md), so the build must be this one.
	export BW_VERSION=v0.0.0-dev
	image=$(sed -n 's/^CURL_IMAGE=//p' fixtures/versions.env)
	case $image in *@sha256:*) ;; *) echo "run-doc-blocks: CURL_IMAGE in fixtures/versions.env has no digest" >&2; return 1 ;; esac
	extract "$rec/blocks" "${pages[@]}"
	# A run with no host block or no container block would skip a whole phase and still end 0.
	for id in host container; do
		grep -q " $id\$" "$rec/blocks/blocks.list" || { echo "run-doc-blocks: no marked $id block: that phase would be skipped" >&2; return 1; }
	done
	{
		echo "revision $(git rev-parse HEAD)"
		echo "version $BW_VERSION"
		echo "image $image"
		echo "host $(uname -sm)"
		echo "substitution $page_server -> $fixture_web (container blocks)"
	} >"$rec/doc-blocks.log"
	# The address as a literal, and only where it ends: not a longer host name, and not one with a port.
	local literal=${page_server//./\\.}
	while read -r id phase; do
		[ "$phase" = container ] || continue
		sed -E -i "s#$literal([^A-Za-z0-9.:-]|\$)#$fixture_web\\1#g" "$rec/blocks/$id.sh"
	done <"$rec/blocks/blocks.list"
	export BW_DOC_WEB_URL=$fixture_web
	: >"$rec/ran.list"
	trap cleanup EXIT

	# exec_block <id> <phase> <file>: run it, keep its output, note that it ran, stop on a failure.
	exec_block() {
		local started=$SECONDS status=0
		# A container block runs as a file, with stdin from /dev/null: a block that read stdin would
		# otherwise eat the rest of its own script and still exit 0.
		if [ "$2" = container ]; then
			"$dk" exec "$ctr" sh -e "/blocks/$1.sh" >"$rec/$1.out" 2>&1 </dev/null || status=$?
		elif [ -n "${BW_DOC_EXEC:-}" ]; then
			"$BW_DOC_EXEC" "$3" >"$rec/$1.out" 2>&1 </dev/null || status=$?
		else
			bash -e "$3" >"$rec/$1.out" 2>&1 </dev/null || status=$?
		fi
		echo "$1" >>"$rec/ran.list"
		printf '%s\t%s\texit %s\t%ss\n' "$1" "$2" "$status" "$((SECONDS - started))" >>"$rec/doc-blocks.log"
		[ "$status" -eq 0 ] && return 0
		echo "run-doc-blocks: $1 ($2) exited $status; its last output:" >&2
		tail -n 20 "$rec/$1.out" >&2
		return 1
	}

	# The container phase starts once the host phase left a build in dist/.
	while read -r id phase <&3; do
		file=$rec/blocks/$id.sh
		if [ "$phase" = container ] && [ -z "${container_up:-}" ]; then
			"$fx" start "$state" "$root/dist"
			# dist-copy, made by the fixture, is a copy because install.md unpacks into dist/. HOME is a
			# tmpfs, as the install block writes ~/.local/bin; "exec" because Docker mounts a tmpfs
			# noexec and the client must run from it.
			"$dk" run -d --rm --name "$ctr" --network host --user "$(id -u):$(id -g)" \
				--tmpfs "/home/bw:rw,exec,mode=0700,uid=$(id -u),gid=$(id -g)" -e HOME=/home/bw \
				-e PATH=/home/bw/.local/bin:/usr/local/bin:/usr/bin:/bin \
				-v "$state/dist-copy:/work/dist" -v "$rec/blocks:/blocks:ro" -v "$state/token.json:/run/bw-token.json:ro" \
				-v "$state/office-ca.pem:/work/office-ca.pem:ro" \
				-w /work --entrypoint sleep "$image" 3600 >/dev/null
			container_up=1
		fi
		# The pages after install.md assume a context that works: the sample outputs name one called dev.
		# bronzewardctl version reaches the server, so it also shows the context works; a failure stops the run.
		if [ "$id" = contexts-1 ]; then
			"$dk" exec "$ctr" bronzewardctl context add dev --server http://127.0.0.1:8080 \
				--issuer http://127.0.0.1:5556 --client-id bw-docs --audience bronzeward \
				--oidc-token-file /run/bw-token.json >"$rec/setup.out" 2>&1 || { cat "$rec/setup.out" >&2; return 1; }
			echo "-- bronzewardctl version, after the context add" >>"$rec/doc-blocks.log"
			"$dk" exec "$ctr" bronzewardctl version >>"$rec/doc-blocks.log" 2>&1 || {
				echo "run-doc-blocks: bronzewardctl version failed after the context add; see doc-blocks.log" >&2; return 1; }
		fi
		exec_block "$id" "$phase" "$file" || rc=1
		[ "$rc" -eq 0 ] || break
	done 3<"$rec/blocks/blocks.list"
	account "$rec/blocks/blocks.list" "$rec/ran.list" || rc=1
	[ "$rc" -eq 0 ] && echo "run-doc-blocks: every marked block ran and exited 0 ($(wc -l <"$rec/ran.list") blocks)"
	return "$rc"
}

case ${1:-} in
run) shift; run "$@" ;;
extract) shift; extract "$@" ;;
account) shift; account "$@" ;;
*) echo "usage: run-doc-blocks.sh run [page...] | extract <dir> <page>... | account <blocks.list> <ran.list>" >&2; exit 64 ;;
esac
