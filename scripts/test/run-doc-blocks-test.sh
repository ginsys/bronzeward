#!/usr/bin/env bash
# Negative controls for scripts/release/run-doc-blocks.sh (ginsys/bronzeward#405): the real pages give
# their seven marked blocks in order, and each planted defect fails with its own message. No Docker:
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

# The real pages: seven blocks, the first two on the host.
real=("$pages/install.md" "$pages/contexts.md" "$pages/inventory.md" "$pages/sign-in-and-renewal.md")
"$runner" extract "$work/real" "${real[@]}"
got=$(tr '\n' ',' <"$work/real/blocks.list")
want="install-1 host,install-2 host,install-3 container,install-4 container,contexts-1 container,inventory-1 container,sign-in-and-renewal-1 container,"
if [ "$got" = "$want" ]; then pass=$((pass + 1)); else fail=$((fail + 1)); printf 'FAIL: real pages\n got %s\nwant %s\n' "$got" "$want" >&2; fi

fence='```'
page ok "text" "${fence}bash {.bw-run .bw-host}" "echo host" "$fence" "${fence}bash {.bw-run}" "echo box" "$fence" "${fence}bash" "echo not marked" "$fence"
expect 0 "a host block, a container block and an unmarked block" "" "$runner" extract "$work/o1" "$work/ok.md"
if [ "$(wc -l <"$work/o1/blocks.list")" -eq 2 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the unmarked block was taken" >&2; fi

page indented "  ${fence}bash {.bw-run}" "  echo hidden" "  $fence"
expect 1 "a marked fence the reader cannot see" "bw-run outside a fence opener at the start of a line" "$runner" extract "$work/o2" "$work/indented.md"

page open "${fence}bash {.bw-run}" "echo never closed"
expect 1 "an unterminated marked fence" "unterminated fence" "$runner" extract "$work/o3" "$work/open.md"

page late "${fence}bash {.bw-run}" "echo box" "$fence" "${fence}bash {.bw-run .bw-host}" "echo host" "$fence"
expect 1 "a host block after a container block" "a .bw-host block after a container block" "$runner" extract "$work/o4" "$work/late.md"

page first "${fence}bash {.bw-run}" "echo box" "$fence"
page second "${fence}bash {.bw-run .bw-host}" "echo host" "$fence"
expect 1 "a host block on a later page than a container block" "a .bw-host block after a container block" "$runner" extract "$work/o4b" "$work/first.md" "$work/second.md"

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
chmod +x "$work/stub-ok" "$work/stub-planted"
export BW_DOC_STATE=$work/state
export BW_DOC_EXEC=$work/stub-ok
expect 0 "the real pages, every block exits 0" "" "$runner" run
if grep -q 'sign-in-and-renewal-1.container.exit 0' "$work/state/record/doc-blocks.log"; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the record does not list the last block" >&2; fi
page bad "${fence}bash {.bw-run}" "echo ok" "$fence" "${fence}bash {.bw-run}" "echo planted" "$fence" "${fence}bash {.bw-run}" "echo after" "$fence"
export BW_DOC_EXEC=$work/stub-planted
expect 1 "a planted failing block" "bad-2 (container) exited 3" "$runner" run "$work/bad.md"
if [ "$(wc -l <"$work/state/record/ran.list")" -eq 2 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: the run went on after the failing block" >&2; fi

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

echo "run-doc-blocks-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
