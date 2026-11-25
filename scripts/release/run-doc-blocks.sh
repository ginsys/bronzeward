#!/usr/bin/env bash
# Run the operator pages' blocks marked {.bw-run} (ginsys/bronzeward#405, the clean-install part of
# ginsys/bronzeward#262 acceptance 3). A marked block is a fence whose opener is "```bash {.bw-run}".
# A block that also carries .bw-host runs on the host from the repository root, where mise and the
# toolchain are: the build and its checksum check. Every other marked block runs, in page order, in a
# clean container (fixtures/versions.env CURL_IMAGE: curl, tar and CA certificates only) with the
# build's dist/ copied in, against the disposable server of doc-blocks-fixture.sh. Fences are read
# by text, as scripts/verify-docs.py does, with no Markdown parser. The run fails when a block fails,
# when a marked block did not run, or when anything else ran.
#
#   run-doc-blocks.sh run [page...]            the whole run (CI: mise run doc-blocks)
#   run-doc-blocks.sh extract <dir> <page>...  write each marked block to <dir>, and <dir>/blocks.list
#   run-doc-blocks.sh account <blocks.list> <ran.list>
#
# BW_DOC_EXEC, set only by scripts/test/run-doc-blocks-test.sh, is a command that runs each block
# file in place of the host shell, the container and the fixture. BW_DOC_STATE is the state
# directory (default .dev/doc-blocks); its record/ holds doc-blocks.log and each block's output.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
default_pages=(docs/operator/clients/install.md docs/operator/clients/contexts.md
	docs/operator/clients/inventory.md docs/operator/clients/sign-in-and-renewal.md)

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
			open { print > file; lines++; next }
			/bw-run/ { die("bw-run outside a fence opener at the start of a line") }
			END { if (failed) exit 1; if (open) die("unterminated fence"); if (!n) die("no marked block on this page") }
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

run() {
	# state and ctr are not local: the EXIT trap reads them after this function returned.
	state=${BW_DOC_STATE:-$root/.dev/doc-blocks}
	ctr=bw-doc-blocks
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
	} >"$rec/doc-blocks.log"
	: >"$rec/ran.list"
	if [ -z "${BW_DOC_EXEC:-}" ]; then
		trap 'docker rm -f "$ctr" >/dev/null 2>&1 || true; "$root/scripts/release/doc-blocks-fixture.sh" stop "$state" || true' EXIT
	fi

	# exec_block <id> <phase> <file>: run it, keep its output, note that it ran, stop on a failure.
	exec_block() {
		local started=$SECONDS status=0
		# A container block runs as a file, with stdin from /dev/null: a block that read stdin would
		# otherwise eat the rest of its own script and still exit 0.
		if [ "$2" = container ] && { [ -z "${BW_DOC_EXEC:-}" ] || [ -n "${BW_DOC_DOCKER:-}" ]; }; then
			"${BW_DOC_DOCKER:-docker}" exec "$ctr" sh -e "/blocks/$1.sh" >"$rec/$1.out" 2>&1 </dev/null || status=$?
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
		if [ "$phase" = container ] && [ -z "${container_up:-}" ] && [ -z "${BW_DOC_EXEC:-}" ]; then
			"$root/scripts/release/doc-blocks-fixture.sh" start "$state" "$root/dist"
			cp -a "$root/dist" "$state/dist-copy"
			# dist/ is a copy: install.md unpacks into it. HOME is a tmpfs, as the install block writes
		# ~/.local/bin; "exec" because Docker mounts a tmpfs noexec and the client must run from it.
			docker run -d --rm --name "$ctr" --network host --user "$(id -u):$(id -g)" \
				--tmpfs "/home/bw:rw,exec,mode=0700,uid=$(id -u),gid=$(id -g)" -e HOME=/home/bw \
				-e PATH=/home/bw/.local/bin:/usr/local/bin:/usr/bin:/bin \
				-v "$state/dist-copy:/work/dist" -v "$rec/blocks:/blocks:ro" -v "$state/token.json:/run/bw-token.json:ro" \
				-w /work --entrypoint sleep "$image" 3600 >/dev/null
			container_up=1
		fi
		# The pages after install.md assume a context that works: the sample outputs name one called dev.
		if [ "$id" = contexts-1 ] && [ -z "${BW_DOC_EXEC:-}" ]; then
			docker exec "$ctr" bronzewardctl context add dev --server http://127.0.0.1:8080 \
				--issuer http://127.0.0.1:5556 --client-id bw-docs --audience bronzeward \
				--oidc-token-file /run/bw-token.json >"$rec/setup.out" 2>&1 || { cat "$rec/setup.out" >&2; return 1; }
			{ echo "-- bronzewardctl version, after the context add"; docker exec "$ctr" bronzewardctl version 2>&1 || echo "(exit $?)"; } >>"$rec/doc-blocks.log"
			echo "-- bronzeward version: $("$state/server/bronzeward" version 2>&1)" >>"$rec/doc-blocks.log"
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
