#!/usr/bin/env bash
# fixtures/participants/session scan and stop against stubs (#367): the planted controls never stay
# behind, on a pass, a hit, a read error or an interrupt, and stop keeps A's configuration backup
# until A runs on it again. No Docker, no fixture: docker, inject and dev-idp are stubs in a copy of
# the scripts.
set -uo pipefail
repo_src=$(CDPATH='' cd -- "$(dirname "$0")/../.." && pwd)
t=$(mktemp -d)
trap 'chmod -R u+rwx "$t" 2>/dev/null; rm -rf -- "$t"' EXIT
repo=$t/repo
mkdir -p "$repo/fixtures/participants" "$repo/fixtures/bin" "$repo/scripts/dev" "$t/bin"
cp "$repo_src/fixtures/lib.sh" "$repo_src/fixtures/versions.env" "$repo/fixtures/"
cp "$repo_src/fixtures/participants/session" "$repo/fixtures/participants/"
cp "$repo_src/scripts/dev/instance.sh" "$repo/scripts/dev/"
mkdir -p "$repo/fixtures/.state/server/a" "$repo/.dev/idp" "$repo/.dev/participants"
session=$repo/fixtures/participants/session

# docker: A (fixtures/versions.env names the fixture) and Zitadel exist and run, and each logs a
# line; the forwarder is gone; a container list fails while STUB_PS_FAIL is set.
cat >"$t/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "$1" in
context) echo stub ;;
ps) [ -z "${STUB_PS_FAIL:-}" ] || exit 1; printf 'bw-fixture-bronzeward-a\nbw-dev-idp\n' ;;
logs) echo "log of $2" ;;
inspect) case "$*" in *bw-dev-forward*) exit 1 ;; *State.Running*) echo true ;; *Networks*) echo "bw-fixture_default " ;; esac ;;
esac
EOF
# inject and dev-idp log their calls; inject kill fails while STUB_KILL_FAIL is set.
cat >"$repo/fixtures/bin/inject" <<'EOF'
#!/usr/bin/env bash
echo "inject $*" >>"$STUB_LOG"
[ "$1 ${STUB_KILL_FAIL:-}" != "kill 1" ]
EOF
cat >"$repo/fixtures/dev-idp" <<'EOF'
#!/usr/bin/env bash
echo "dev-idp $*" >>"$STUB_LOG"
EOF
chmod +x "$t/bin/docker" "$repo/fixtures/bin/inject" "$repo/fixtures/dev-idp"
export STUB_LOG=$t/calls

fail=0
ok() { echo "ok   $1"; }
bad() {
  echo "FAIL $1"
  fail=1
}
controls() { find "$repo" -name .bw-scan-control 2>/dev/null | wc -l; }
run() { env PATH="$t/bin:$PATH" bash "$session" "$@" 2>&1; }

out=$(run scan)
rc=$?
if [ "$rc" = 0 ] && [ "$(controls)" = 0 ]; then ok "a clean scan passes and leaves no control"; else bad "clean scan: rc=$rc controls=$(controls): $out"; fi

printf 'BWSYNTH-pw-abcd1234-5678\n' >"$repo/fixtures/.state/leak"
out=$(run scan)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"fixtures/.state/leak"* ]] && [ "$(controls)" = 0 ]; then
  ok "a session password is found, and no control stays"
else bad "hit: rc=$rc controls=$(controls): $out"; fi
rm -f "$repo/fixtures/.state/leak"

mkdir -p "$repo/.dev/idp/locked"
touch "$repo/.dev/idp/locked/f"
chmod 000 "$repo/.dev/idp/locked"
out=$(run scan)
rc=$?
chmod 755 "$repo/.dev/idp/locked"
rm -rf "$repo/.dev/idp/locked"
if [ "$(id -u)" = 0 ]; then
  ok "a read error fails the scan (skipped: root reads everything)"
elif [ "$rc" != 0 ] && [[ $out == *"proves nothing"* ]] && [ "$(controls)" = 0 ]; then
  ok "a read error fails the scan, and no control stays"
else bad "read error: rc=$rc controls=$(controls): $out"; fi

# A grep that hangs on the recursive read, so the scan is interrupted with its controls planted. The
# scan runs in its own process group, which is signalled whole, as a terminal's Ctrl-C reaches grep.
realgrep=$(type -P grep)
cat >"$t/bin/grep" <<EOF
#!/usr/bin/env bash
for a in "\$@"; do [ "\$a" = -rlE ] && exec sleep 30; done
exec '$realgrep' "\$@"
EOF
chmod +x "$t/bin/grep"
setsid env PATH="$t/bin:$PATH" bash "$session" scan >/dev/null 2>&1 &
pid=$!
for _ in $(seq 50); do
  [ "$(controls)" -ge 4 ] && break
  sleep 0.1
done
sleep 0.5
planted=$(controls)
kill -TERM -- "-$pid"
wait "$pid"
rc=$?
rm -f "$t/bin/grep"
if [ "$planted" = 4 ] && [ "$(controls)" = 0 ]; then
  ok "an interrupted scan removes its 4 controls (status $rc)"
else bad "interrupt: planted=$planted left=$(controls) status=$rc"; fi

cfg=$repo/fixtures/.state/server/a/config.yaml
setup() {
  echo participant >"$cfg"
  echo fixture >"$cfg.fixture"
  mkdir -p "$repo/.dev/participants"
  : >"$STUB_LOG"
}

setup
out=$(STUB_PS_FAIL=1 run stop)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"cannot list containers"* ]] && [ -f "$cfg.fixture" ] && [ -d "$repo/.dev/participants" ]; then
  ok "a failed container list fails stop, with the backup and the participant state kept"
else bad "container list fails: rc=$rc: $out"; fi

setup
out=$(STUB_KILL_FAIL=1 run stop)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"run session stop again"* ]] && [ -f "$cfg.fixture" ] && [ "$(cat "$cfg")" = fixture ] &&
  [ -d "$repo/.dev/participants" ]; then
  ok "a failed restart of A fails stop and keeps the backup for the next stop"
else bad "restart fails: rc=$rc: $out"; fi

setup
out=$(run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" = 0 ] && [ ! -e "$cfg.fixture" ] && [ "$(cat "$cfg")" = fixture ] &&
  [ "$calls" = "inject kill A;inject start A;dev-idp down;" ] && [ ! -e "$repo/.dev/participants" ]; then
  ok "stop puts A back on its configuration and restarts it, then removes the backup and the state"
else bad "stop: rc=$rc calls=$calls: $out"; fi

exit "$fail"
