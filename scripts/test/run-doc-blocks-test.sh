#!/usr/bin/env bash
# Negative controls for scripts/release/run-doc-blocks.sh (ginsys/bronzeward#405): the real pages give
# their thirteen marked blocks in order, and each planted defect fails with its own message. No Docker:
# BW_DOC_EXEC stands in for the host shell, the container and the fixture.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
runner=$here/../release/run-doc-blocks.sh
pages=$here/../../docs/operator/clients
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

# expect <0|1> <name> <message expected on a failure> <command...>
expect() {
	local want=$1 name=$2 msg=$3 out rc=0
	shift 3
	out=$("$@" 2>&1) || rc=$?
	if [ "$want" -eq 0 ] && [ "$rc" -eq 0 ]; then
		pass=$((pass + 1))
	elif [ "$want" -ne 0 ] && [ "$rc" -ne 0 ] && grep -qF -- "$msg" <<<"$out"; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
		printf 'FAIL: %s: rc=%s\n%s\n' "$name" "$rc" "$out" >&2
	fi
}

# page <name> <lines...>: a page in $work.
page() {
	local f=$work/$1.md
	shift
	printf '%s\n' "$@" >"$f"
}

# The real pages: thirteen blocks, the first two on the host.
real=("$pages/install.md" "$pages/contexts.md" "$pages/inventory.md" "$pages/sign-in-and-renewal.md"
	"$pages/quick-start-cli.md" "$pages/client-troubleshooting.md")
"$runner" extract "$work/real" "${real[@]}"
got=$(tr '\n' ',' <"$work/real/blocks.list")
want="install-1 host,install-2 host,install-3 container,install-4 container,contexts-1 container,contexts-2 container,contexts-3 container,inventory-1 container,sign-in-and-renewal-1 container,quick-start-cli-1 container,quick-start-cli-2 container,quick-start-cli-3 container,client-troubleshooting-1 container,"
if [ "$got" = "$want" ]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL: real pages\n got %s\nwant %s\n' "$got" "$want" >&2; fi

fence='```'
page ok "text" "${fence}bash {.bw-run .bw-host}" "echo host" "$fence" "${fence}bash {.bw-run}" "echo box" "$fence" "${fence}bash" "echo not marked" "$fence"
expect 0 "a host block, a container block and an unmarked block" "" "$runner" extract "$work/o1" "$work/ok.md"
if [ "$(wc -l <"$work/o1/blocks.list")" -eq 2 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the unmarked block was taken" >&2; fi

# A .bw-db block belongs to the Go test that runs the journey pages: it is not this run's.
page db "${fence}bash {.bw-run}" "echo box" "$fence" "${fence}bash {.bw-run .bw-db}" "echo db" "$fence" "${fence}bash {.bw-run}" "echo box again" "$fence"
expect 0 "a .bw-db block between two container blocks" "" "$runner" extract "$work/o1b" "$work/db.md"
got=$(tr '\n' ',' <"$work/o1b/blocks.list")
if [ "$got" = "db-1 container,db-2 container," ] && ! grep -rq 'echo db' "$work/o1b"; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the .bw-db block was taken: $got" >&2; fi
page dbopen "${fence}bash {.bw-run}" "echo box" "$fence" "${fence}bash {.bw-run .bw-db}" "echo never closed"
expect 1 "an unterminated .bw-db fence" "unterminated fence" "$runner" extract "$work/o1c" "$work/dbopen.md"

page indented "  ${fence}bash {.bw-run}" "  echo hidden" "  $fence"
expect 1 "a marked fence the reader cannot see" "bw-run outside a fence opener at the start of a line" "$runner" extract "$work/o2" "$work/indented.md"

page open "${fence}bash {.bw-run}" "echo never closed"
expect 1 "an unterminated marked fence" "unterminated fence" "$runner" extract "$work/o3" "$work/open.md"

page late "${fence}bash {.bw-run}" "echo box" "$fence" "${fence}bash {.bw-run .bw-host}" "echo host" "$fence"
expect 1 "a host block after a container block" "a .bw-host block after a container block" "$runner" extract "$work/o4" "$work/late.md"

page first "${fence}bash {.bw-run}" "echo box" "$fence"
page second "${fence}bash {.bw-run .bw-host}" "echo host" "$fence"
expect 1 "a host block on a later page than a container block" "a .bw-host block after a container block" "$runner" extract "$work/o4b" "$work/first.md" "$work/second.md"

page none "No marked block on this page."
expect 1 "a page with no marked block" "no marked block on this page" "$runner" extract "$work/o4c" "$work/none.md"
page empty "${fence}bash {.bw-run}" "$fence"
expect 1 "an empty marked block" "an empty marked block" "$runner" extract "$work/o4d" "$work/empty.md"

page prose "The marker is bw-run and nothing else."
expect 1 "a mention in prose" "bw-run outside a fence opener" "$runner" extract "$work/o5" "$work/prose.md"

# account: the ids that ran are the marked ids, each once.
printf 'a-1 host\na-2 container\n' >"$work/list"
printf 'a-1\na-2\n' >"$work/ran"
expect 0 "every marked block ran once" "" "$runner" account "$work/list" "$work/ran"
printf 'a-1\n' >"$work/ran"
expect 1 "a marked block that did not run" "are not the marked blocks" "$runner" account "$work/list" "$work/ran"
printf 'a-1\na-2\na-3\n' >"$work/ran"
expect 1 "a block that ran and is not marked" "are not the marked blocks" "$runner" account "$work/list" "$work/ran"
printf 'a-1\na-2\na-2\n' >"$work/ran"
expect 1 "a block that ran twice" "are not the marked blocks" "$runner" account "$work/list" "$work/ran"
: >"$work/list"
: >"$work/ran"
expect 1 "no marked block at all" "no marked block found" "$runner" account "$work/list" "$work/ran"

# run, with the stub: it passes on the real pages, and a failing block fails it by its id.
printf '#!/bin/sh\nexit 0\n' >"$work/stub-ok"
cat >"$work/stub-planted" <<'EOF'
#!/bin/sh
if grep -q planted "$1"; then exit 3; fi
exit 0
EOF
cat >"$work/stub-sh" <<'EOF'
#!/bin/sh
exec sh -e "$1"
EOF
chmod +x "$work/stub-ok" "$work/stub-planted" "$work/stub-sh"
# Stand-ins for docker and the fixture script. The fake docker maps /blocks to the record directory, as the
# bind mount does, hands a container block to the host stub with its stdin untouched, and insists on the
# `exec <container> sh -e /blocks/<id>.sh` shape; each verb's exit status can be forced by an environment variable.
cat >"$work/fake-docker" <<'EOF'
#!/bin/sh
case $1 in
run | rm) exit 0 ;;
ps) [ "$2 $3 $4" = '-aq --filter name=^bw-doc-blocks$' ] || exit 99; printf '%s' "${BW_FAKE_PS_OUT:-}"; exit "${BW_FAKE_PS_RC:-0}" ;;
exec)
	case $3 in
	sh) [ "$4" = -e ] || exit 99; exec "$BW_DOC_EXEC" "$BW_FAKE_BLOCKS/${5#/blocks/}" ;;
	bronzewardctl) case $4 in context) exit 0 ;; version) exit "${BW_FAKE_VERSION_RC:-0}" ;; esac ;;
	esac ;;
esac
exit 99
EOF
cat >"$work/fake-fixture" <<'EOF'
#!/bin/sh
case $1 in
start) exit "${BW_FAKE_START_RC:-0}" ;;
stop) exit "${BW_FAKE_STOP_RC:-0}" ;;
esac
exit 99
EOF
chmod +x "$work/fake-docker" "$work/fake-fixture"
export BW_DOC_STATE=$work/state BW_DOC_DOCKER=$work/fake-docker BW_DOC_FIXTURE=$work/fake-fixture BW_FAKE_BLOCKS=$work/state/record/blocks
export BW_DOC_EXEC=$work/stub-ok
expect 0 "the real pages, every block exits 0" "" "$runner" run
if grep -q 'sign-in-and-renewal-1.container.exit 0' "$work/state/record/doc-blocks.log"; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the record does not list the last block" >&2; fi
page bad "${fence}bash {.bw-run .bw-host}" "echo built" "$fence" "${fence}bash {.bw-run}" "echo ok" "$fence" "${fence}bash {.bw-run}" "echo planted" "$fence" "${fence}bash {.bw-run}" "echo after" "$fence"
export BW_DOC_EXEC=$work/stub-planted
expect 1 "a planted failing block" "bad-3 (container) exited 3" "$runner" run "$work/bad.md"
if [ "$(wc -l <"$work/state/record/ran.list")" -eq 3 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the run went on after the failing block" >&2; fi

# A run with only container blocks, or only host blocks, would skip a whole phase.
export BW_DOC_EXEC=$work/stub-ok
page onlybox "${fence}bash {.bw-run}" "echo box" "$fence"
expect 1 "no host block" "no marked host block" "$runner" run "$work/onlybox.md"
page onlyhost "${fence}bash {.bw-run .bw-host}" "echo host" "$fence"
expect 1 "no container block" "no marked container block" "$runner" run "$work/onlyhost.md"

# A container block runs as a file with stdin from /dev/null: this block reads stdin and fails when it
# gets anything, and the run is fed "poison" on stdin.
# shellcheck disable=SC2016 # the block's text is literal: its own shell expands $line
page stdin "${fence}bash {.bw-run .bw-host}" "echo built" "$fence" "${fence}bash {.bw-run}" 'read -r line || line=' '[ -z "$line" ]' "$fence"
stdin_run() { printf 'poison\n' | BW_DOC_EXEC=$work/stub-sh "$runner" run "$work/stdin.md"; }
expect 0 "a container block that reads stdin gets none" "" stdin_run

# A container block's one declared value, the pages' server address, is the fixture's; a host block is left as written.
page address "${fence}bash {.bw-run .bw-host}" "echo host https://bronzeward.example.com" "$fence" "${fence}bash {.bw-run}" "echo box https://bronzeward.example.com" "$fence"
BW_DOC_EXEC=$work/stub-sh expect 0 "a page that names the server" "" "$runner" run "$work/address.md"
if grep -qx 'box https://localhost:8443' "$work/state/record/address-2.out" && grep -qx 'host https://bronzeward.example.com' "$work/state/record/address-1.out" &&
	grep -qx 'substitution https://bronzeward.example.com -> https://localhost:8443 (container blocks)' "$work/state/record/doc-blocks.log"; then
	pass=$((pass + 1))
else
	fail=$((fail + 1))
	echo "FAIL: the server address was not substituted in the container block alone, or the record does not say so" >&2
fi

# Only that address: another host that differs from it in a dot, or goes on after it, is left as written.
page lookalike "${fence}bash {.bw-run .bw-host}" "echo built" "$fence" "${fence}bash {.bw-run}" \
	"echo https://bronzeward-example.com https://bronzeward.example.community https://bronzeward.example.com:9 https://bronzeward.example.com/x 'https://bronzeward.example.com'" "$fence"
BW_DOC_EXEC=$work/stub-sh expect 0 "a page with look-alike addresses" "" "$runner" run "$work/lookalike.md"
if grep -qx 'https://bronzeward-example.com https://bronzeward.example.community https://bronzeward.example.com:9 https://localhost:8443/x https://localhost:8443' "$work/state/record/lookalike-2.out"; then
	pass=$((pass + 1))
else
	fail=$((fail + 1))
	echo "FAIL: a look-alike address was substituted, or the address itself was not: $(cat "$work/state/record/lookalike-2.out")" >&2
fi

# Only the class .bw-db itself is skipped: a longer name keeps its .bw-run block in this run.
page dbname "${fence}bash {.bw-run}" "echo box" "$fence" "${fence}bash {.bw-run .bw-db-extra}" "echo extra" "$fence"
expect 0 "a class that only starts with .bw-db" "" "$runner" extract "$work/o1d" "$work/dbname.md"
if [ "$(wc -l <"$work/o1d/blocks.list")" -eq 2 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the .bw-db-extra block was skipped" >&2; fi

# A marked block with no command (blank lines, comments) is an empty block.
page blank "${fence}bash {.bw-run}" "" "# only a note" "  " "$fence"
expect 1 "a block of blank lines and a comment" "an empty marked block" "$runner" extract "$work/o6" "$work/blank.md"

# The version command after the context add reaches the server: its failure stops the run.
BW_FAKE_VERSION_RC=1 expect 1 "bronzewardctl version fails" "bronzewardctl version failed" "$runner" run
# A fixture that does not start fails the run.
BW_FAKE_START_RC=1 expect 1 "the fixture does not start" "" "$runner" run
# Cleanup failures turn a pass into a failure, and never hide an earlier failure.
BW_FAKE_STOP_RC=1 expect 1 "the fixture does not stop" "stopping the fixture failed" "$runner" run
BW_FAKE_PS_OUT=3f9a1c expect 1 "the container is still there" "is still there" "$runner" run
# A listing that fails (Docker unreachable after the last block) is not an absence: the old check, a failed `docker inspect`, read it as one.
BW_FAKE_PS_RC=1 expect 1 "docker cannot list the containers after the last block" "could not list containers" "$runner" run
BW_DOC_EXEC=$work/stub-planted BW_FAKE_STOP_RC=1 expect 1 "a failing block and a failing stop" "bad-3 (container) exited 3" "$runner" run "$work/bad.md"

# The fixture's stop waits for each process: one that ends is gone and loses its pid file, one that
# ignores SIGTERM fails the stop and keeps its pid file.
fixture=$here/../release/doc-blocks-fixture.sh
mkdir -p "$work/fx"
sleep 300 &
echo $! >"$work/fx/server.pid"
(trap '' TERM; exec sleep 300) &
stubborn=$!
echo "$stubborn" >"$work/fx/issuer.pid"
BW_DOC_STOP_TRIES=4 expect 1 "a process that ignores SIGTERM" "the issuer (pid $stubborn) is still running" "$fixture" stop "$work/fx"
if [ ! -f "$work/fx/server.pid" ] && [ -f "$work/fx/issuer.pid" ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: stop did not drop the ended server's pid file and keep the stubborn one's" >&2; fi
kill -KILL "$stubborn" 2>/dev/null || true
wait "$stubborn" 2>/dev/null || true
expect 0 "a stop with the process gone" "" "$fixture" stop "$work/fx"

# The web container: stop removes it, and fails when the listing still shows it or cannot be read.
cat >"$work/fake-web-docker" <<'EOF'
#!/bin/sh
case $1 in
logs | rm) exit 0 ;;
ps) [ "$2 $3 $4" = '-aq --filter name=^bw-doc-blocks-web$' ] || exit 99; printf '%s' "${BW_FAKE_PS_OUT:-}"; exit "${BW_FAKE_PS_RC:-0}" ;;
esac
exit 99
EOF
chmod +x "$work/fake-web-docker"
mkdir -p "$work/web/record"
export BW_DOC_DOCKER=$work/fake-web-docker
echo bw-doc-blocks-web >"$work/web/web.container"
BW_FAKE_PS_OUT=3f9a1c expect 1 "the web container is still there" "could not confirm that the container bw-doc-blocks-web is gone" "$fixture" stop "$work/web"
BW_FAKE_PS_RC=1 expect 1 "docker cannot list the web container" "could not confirm that the container bw-doc-blocks-web is gone" "$fixture" stop "$work/web"
expect 0 "the web container is gone" "" "$fixture" stop "$work/web"
if [ ! -f "$work/web/web.container" ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: stop kept the web container's marker" >&2; fi

echo "run-doc-blocks-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
