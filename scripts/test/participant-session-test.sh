#!/usr/bin/env bash
# fixtures/participants/session scan and stop against stubs (#367): the planted controls never stay
# behind, on a pass, a hit, a read error or an interrupt; stop keeps A's configuration backup until
# A runs on it again, and touches no other checkout's session. No Docker, no fixture: docker, inject
# and dev-idp are stubs in a copy of the scripts.
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

# docker: A (fixtures/versions.env names the fixture) and Zitadel exist, A runs unless STUB_STOPPED
# is set, and each logs a line. The fixture claim is held by STUB_OWNER, this copy's checkout by
# default, on the daemon STUB_DAEMON (daemon-1, bin/up's record, by default). An inspect of the
# forwarder fails, as for a removed container. rm and network calls are logged, and log reads are
# logged apart, in STUB_LOGS_LOG. Set, STUB_PS_FAIL
# fails the container list, STUB_FWD still lists the forwarder,
# STUB_LOGS_FAIL fails a log read, and STUB_LOG_LEAK puts a session password in A's log.
cat >"$t/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "$1" in
context) echo stub ;;
info) echo "${STUB_DAEMON:-daemon-1}" ;;
rm | network) echo "docker $1" >>"$STUB_LOG" ;;
ps)
  [ -z "${STUB_PS_FAIL:-}" ] || exit 1
  printf 'bw-fixture-bronzeward-a\nbw-dev-idp\n'
  [ -z "${STUB_FWD:-}" ] || echo bw-dev-forward
  ;;
logs)
  echo "logs $2" >>"$STUB_LOGS_LOG"
  [ -z "${STUB_LOGS_FAIL:-}" ] || exit 1
  echo "log of $2"
  if [ -n "${STUB_LOG_LEAK:-}" ] && [ "$2" = bw-fixture-bronzeward-a ]; then echo "BWSYNTH-pw-abcd1234-5678"; fi
  ;;
inspect) case "$*" in *bw-fixture-claim*) echo "bw-fixture $STUB_OWNER" ;; *bw-dev-forward*) exit 1 ;; *State.Running*) if [ -n "${STUB_STOPPED:-}" ]; then echo false; else echo true; fi ;; *Networks*) echo "bw-fixture_default " ;; esac ;;
esac
EOF
# inject and dev-idp log their calls; inject kill fails while STUB_KILL_FAIL is set, and inject
# start while STUB_START_FAIL is.
cat >"$repo/fixtures/bin/inject" <<'EOF'
#!/usr/bin/env bash
echo "inject $*" >>"$STUB_LOG"
[ "$1 ${STUB_KILL_FAIL:-}" != "kill 1" ] && [ "$1 ${STUB_START_FAIL:-}" != "start 1" ]
EOF
# dev-idp up fails while STUB_IDP_UP_FAIL is set, as it does on another checkout's containers.
cat >"$repo/fixtures/dev-idp" <<'EOF'
#!/usr/bin/env bash
echo "dev-idp $*" >>"$STUB_LOG"
[ "$1 ${STUB_IDP_UP_FAIL:-}" != "up 1" ]
EOF
cat >"$repo/fixtures/bin/up" <<'EOF'
#!/usr/bin/env bash
echo "bin/up" >>"$STUB_LOG"
EOF
# start's other prerequisites, which the cases below never reach.
for c in go npm; do printf '#!/bin/sh\nexit 0\n' >"$t/bin/$c"; done
chmod +x "$t/bin/docker" "$t/bin/go" "$t/bin/npm" "$repo/fixtures/bin/inject" "$repo/fixtures/dev-idp" "$repo/fixtures/bin/up"
# bin/up's record of its daemon (lib.sh daemon_own), and session start's.
echo daemon-1 >"$repo/fixtures/.state/up-daemon"
echo daemon-1 >"$repo/.dev/participants/daemon"
export STUB_LOG=$t/calls STUB_LOGS_LOG=$t/log-reads
STUB_OWNER=$(CDPATH='' cd -P -- "$repo/fixtures" && pwd -P)
export STUB_OWNER

fail=0
ok() { echo "ok   $1"; }
bad() {
  echo "FAIL $1"
  fail=1
}
controls() { find "$repo" -name .bw-scan-control 2>/dev/null | wc -l; }
# running <pid>: the process exists and is not a zombie.
running() {
  local s
  s=$(cat "/proc/$1/stat" 2>/dev/null) || return 1
  s=${s##*) }
  [ "${s%% *}" != Z ]
}
run() { env PATH="$t/bin:$PATH" bash "$session" "$@" 2>&1; }

: >"$STUB_LOGS_LOG"
out=$(run scan)
rc=$?
# The log reads are recorded: the daemon cases below rely on that record.
if [ "$rc" = 0 ] && [ "$(controls)" = 0 ] && [ -s "$STUB_LOGS_LOG" ]; then
  ok "a clean scan passes, reads the logs and leaves no control"
else bad "clean scan: rc=$rc controls=$(controls) log reads=$(wc -l <"$STUB_LOGS_LOG"): $out"; fi

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

out=$(STUB_LOG_LEAK=1 run scan)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"scan-logs/bw-fixture-bronzeward-a.log"* ]] && [ "$(controls)" = 0 ]; then
  ok "a session password in A's container log is found"
else bad "log leak: rc=$rc: $out"; fi

out=$(STUB_LOGS_FAIL=1 run scan)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"cannot read the log of"* ]] && [ "$(controls)" = 0 ]; then
  ok "a container log that cannot be read fails the scan"
else bad "log read fails: rc=$rc: $out"; fi

# Another Docker daemon than session start's: its containers of the same names are not the session's.
: >"$STUB_LOGS_LOG"
out=$(STUB_DAEMON=daemon-2 run scan)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"this shell reaches daemon-2"* ]] && [ "$(controls)" = 0 ] && [ ! -s "$STUB_LOGS_LOG" ]; then
  ok "a scan on another Docker daemon than session start's fails, and reads no log"
else bad "scan, other daemon: rc=$rc controls=$(controls) log reads=$(wc -l <"$STUB_LOGS_LOG"): $out"; fi

# No record of session start's daemon: scan cannot tell whose the containers are.
rm -f "$repo/.dev/participants/daemon"
: >"$STUB_LOGS_LOG"
out=$(run scan)
rc=$?
echo daemon-1 >"$repo/.dev/participants/daemon"
if [ "$rc" != 0 ] && [[ $out == *"has no record of the Docker daemon"* ]] && [ "$(controls)" = 0 ] && [ ! -s "$STUB_LOGS_LOG" ]; then
  ok "a scan without the session's daemon record fails, and reads no log"
else bad "scan, no daemon record: rc=$rc controls=$(controls) log reads=$(wc -l <"$STUB_LOGS_LOG"): $out"; fi

# A file of the controls' name in a named path is not this scan's control: its hit counts.
mkdir -p "$t/profile"
printf 'BWSYNTH-pw-abcd1234-5678\n' >"$t/profile/.bw-scan-control"
out=$(run scan "$t/profile")
rc=$?
rm -rf "$t/profile"
if [ "$rc" != 0 ] && [[ $out == *"in $t/profile/.bw-scan-control"* ]] && [ "$(controls)" = 0 ]; then
  ok "a session password in another file of the controls' name is found"
else bad "same-named file: rc=$rc controls=$(controls): $out"; fi

# A file already at a control's path, holding a session password: the scan refuses and keeps it.
printf 'BWSYNTH-pw-abcd1234-5678\n' >"$repo/.dev/idp/.bw-scan-control"
out=$(run scan)
rc=$?
kept=$(cat "$repo/.dev/idp/.bw-scan-control" 2>/dev/null)
rm -f "$repo/.dev/idp/.bw-scan-control"
if [ "$rc" != 0 ] && [[ $out == *"did not plant it"* ]] && [ "$kept" = BWSYNTH-pw-abcd1234-5678 ] && [ "$(controls)" = 0 ]; then
  ok "a file already at a control's path fails the scan, and is kept as it was"
else bad "existing control path: rc=$rc kept=$kept controls=$(controls): $out"; fi

# A scan-logs that this scan did not make, with a control-named file that holds a session password:
# the scan refuses before it removes anything, and the file is kept.
mkdir -p "$repo/.dev/participants/scan-logs"
printf 'BWSYNTH-pw-abcd1234-5678\n' >"$repo/.dev/participants/scan-logs/.bw-scan-control"
out=$(run scan)
rc=$?
kept=$(cat "$repo/.dev/participants/scan-logs/.bw-scan-control" 2>/dev/null)
rm -rf "$repo/.dev/participants/scan-logs"
if [ "$rc" != 0 ] && [[ $out == *"scan-logs exists, and this scan did not make it"* ]] && [ "$kept" = BWSYNTH-pw-abcd1234-5678 ] &&
  [ "$(controls)" = 0 ]; then
  ok "an earlier scan-logs fails the scan, and is kept as it was"
else bad "existing scan-logs: rc=$rc kept=$kept controls=$(controls): $out"; fi

# A symbolic link below a root, to a log outside it that holds a session password: the scan follows it.
mkdir -p "$t/outside"
printf 'BWSYNTH-pw-abcd1234-5678\n' >"$t/outside/log"
ln -s "$t/outside/log" "$repo/fixtures/.state/linked-log"
out=$(run scan)
rc=$?
rm -f "$repo/fixtures/.state/linked-log"
if [ "$rc" != 0 ] && [[ $out == *"in $repo/fixtures/.state/linked-log"* ]] && [ "$(controls)" = 0 ]; then
  ok "a session password behind a symbolic link is found"
else bad "linked leak: rc=$rc controls=$(controls): $out"; fi

# A symbolic link that cannot be followed: the scan cannot tell what it hides, so it fails.
ln -s "$t/outside/gone" "$repo/.dev/participants/dangling"
out=$(run scan)
rc=$?
rm -f "$repo/.dev/participants/dangling"
if [ "$rc" != 0 ] && [[ $out == *"proves nothing"* ]] && [ "$(controls)" = 0 ]; then
  ok "a symbolic link that cannot be followed fails the scan"
else bad "dangling link: rc=$rc controls=$(controls): $out"; fi

# The participant's shell: no desktop session bus and no inherited token file reach its client, so a
# sign-in never goes to the desktop's credential store or outside the participant home.
mkdir -p "$repo/.dev/participants/bin"
cat >"$repo/.dev/participants/bin/bronzewardctl" <<'EOF'
#!/bin/sh
echo "bus=[${DBUS_SESSION_BUS_ADDRESS-unset}] tokenfile=[${BRONZEWARD_TOKEN_FILE-unset}] config=[$XDG_CONFIG_HOME]"
EOF
chmod +x "$repo/.dev/participants/bin/bronzewardctl"
out=$(printf 'bronzewardctl\nexit\n' | DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent BRONZEWARD_TOKEN_FILE=$t/elsewhere run shell)
rm -rf "$repo/.dev/participants/bin"
if [[ $out == *"bus=[unset] tokenfile=[unset] config=[$repo/.dev/participants/home/config]"* ]]; then
  ok "the participant's shell gives its client no session bus and no inherited token file"
else bad "shell environment: $out"; fi

# A grep that hangs on the recursive read, so the scan is interrupted with its controls planted. The
# scan runs in its own process group, which is signalled whole, as a terminal's Ctrl-C reaches grep.
realgrep=$(type -P grep)
cat >"$t/bin/grep" <<EOF
#!/usr/bin/env bash
for a in "\$@"; do [ "\$a" = -RlE ] && exec sleep 30; done
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

# A grep that fails on the final filter, after the controls were found, with a hit to report.
cat >"$t/bin/grep" <<EOF
#!/usr/bin/env bash
for a in "\$@"; do [ "\$a" = -vxF ] && exit 2; done
exec '$realgrep' "\$@"
EOF
chmod +x "$t/bin/grep"
printf 'BWSYNTH-pw-abcd1234-5678\n' >"$repo/fixtures/.state/leak"
out=$(run scan)
rc=$?
rm -f "$t/bin/grep" "$repo/fixtures/.state/leak"
if [ "$rc" != 0 ] && [[ $out == *"cannot filter"* ]] && [ "$(controls)" = 0 ]; then
  ok "a failed filter of the hits fails the scan"
else bad "filter fails: rc=$rc: $out"; fi

cfg=$repo/fixtures/.state/server/a/config.yaml
setup() {
  echo participant >"$cfg"
  echo fixture >"$cfg.fixture"
  mkdir -p "$repo/.dev/participants" "$repo/.dev/idp"
  # This session's start made the provider, on daemon-1.
  : >"$repo/.dev/participants/idp-started"
  echo daemon-1 >"$repo/.dev/participants/daemon"
  : >"$STUB_LOG"
}

setup
out=$(STUB_PS_FAIL=1 run stop)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"cannot list containers"* ]] && [ -f "$cfg.fixture" ] && [ -d "$repo/.dev/participants" ]; then
  ok "a failed container list fails stop, with the backup and the participant state kept"
else bad "container list fails: rc=$rc: $out"; fi

setup
out=$(STUB_FWD=1 run stop)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"forwarder bw-dev-forward is still there"* ]] && [ -f "$cfg.fixture" ] && [ -d "$repo/.dev/participants" ]; then
  ok "a forwarder still listed after its removal fails stop"
else bad "forwarder stays: rc=$rc: $out"; fi

setup
out=$(STUB_KILL_FAIL=1 run stop)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"run session stop again"* ]] && [ -f "$cfg.fixture" ] && [ "$(cat "$cfg")" = fixture ] &&
  [ -d "$repo/.dev/participants" ]; then
  ok "a failed stop of A fails stop and keeps the backup for the next stop"
else bad "kill fails: rc=$rc: $out"; fi

# A stopped, then not started again: the next stop, which finds A stopped, starts it.
setup
out=$(STUB_START_FAIL=1 run stop)
rc=$?
if [ "$rc" != 0 ] && [[ $out == *"cannot start"* ]] && [ -f "$cfg.fixture" ]; then
  : >"$STUB_LOG"
  out=$(STUB_STOPPED=1 run stop)
  rc=$?
  calls=$(tr '\n' ';' <"$STUB_LOG")
  if [ "$rc" = 0 ] && [ "$calls" = "docker rm;docker network;inject start A;dev-idp down;" ] && [ ! -e "$cfg.fixture" ] &&
    [ ! -e "$cfg.restart" ]; then
    ok "a failed start of A fails stop, and the next stop starts A"
  else bad "retry after a failed start: rc=$rc calls=$calls: $out"; fi
else bad "start fails: rc=$rc: $out"; fi

setup
out=$(run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" = 0 ] && [ ! -e "$cfg.fixture" ] && [ "$(cat "$cfg")" = fixture ] &&
  [ "$calls" = "docker rm;docker network;inject kill A;inject start A;dev-idp down;" ] && [ ! -e "$repo/.dev/participants" ]; then
  ok "stop puts A back on its configuration and restarts it, then removes the backup and the state"
else bad "stop: rc=$rc calls=$calls: $out"; fi

# Another checkout holds the fixture: its forwarder, A and provider are left alone.
setup
out=$(STUB_OWNER=/elsewhere/fixtures run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" != 0 ] && [[ $out == *"belongs to the checkout at /elsewhere/fixtures"* ]] && [ -z "$calls" ] &&
  [ -f "$cfg.fixture" ] && [ -d "$repo/.dev/participants" ]; then
  ok "stop refuses a fixture another checkout holds, and touches nothing"
else bad "foreign fixture: rc=$rc calls=$calls: $out"; fi

# No session in this checkout: nothing is touched.
setup
rm -rf "$repo/.dev/participants" "$cfg.fixture"
out=$(run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" = 0 ] && [[ $out == *"nothing to stop"* ]] && [ -z "$calls" ]; then
  ok "stop without a session in this checkout touches nothing"
else bad "no session: rc=$rc calls=$calls: $out"; fi

# The shell reaches another daemon than bin/up's: A reads as gone there, and nothing is touched.
setup
out=$(STUB_DAEMON=daemon-2 run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" != 0 ] && [[ $out == *"daemon-2"* ]] && [ -z "$calls" ] && [ -f "$cfg.fixture" ] && [ -d "$repo/.dev/participants" ]; then
  ok "stop on another Docker daemon than bin/up's touches nothing"
else bad "other daemon: rc=$rc calls=$calls: $out"; fi

# fixtures/bin/down removed .state and bin/up's daemon record, and the shell reaches another
# daemon: the session's own record still tells, and nothing is touched.
setup
mv "$repo/fixtures/.state" "$repo/fixtures/.state.away"
out=$(STUB_DAEMON=daemon-2 run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
mv "$repo/fixtures/.state.away" "$repo/fixtures/.state"
if [ "$rc" != 0 ] && [[ $out == *"started on the Docker daemon daemon-1, and this shell reaches daemon-2"* ]] && [ -z "$calls" ] &&
  [ -f "$repo/.dev/participants/idp-started" ]; then
  ok "stop after fixtures/bin/down, on another Docker daemon, touches nothing"
else bad "other daemon, no .state: rc=$rc calls=$calls: $out"; fi

# Participant state without the daemon record: stop cannot tell whose the containers are.
setup
rm -f "$repo/.dev/participants/daemon"
out=$(run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" != 0 ] && [[ $out == *"has no record of the Docker daemon"* ]] && [ -z "$calls" ] && [ -f "$cfg.fixture" ]; then
  ok "stop without the session's daemon record refuses, and touches nothing"
else bad "no daemon record: rc=$rc calls=$calls: $out"; fi

# start without this checkout's dev-idp state: no dev-idp down, which would remove another
# checkout's provider. dev-idp up refuses that provider, and the rollback leaves it too.
setup
rm -f "$cfg.fixture"
rm -rf "$repo/.dev/idp" "$repo/.dev/participants"
out=$(STUB_IDP_UP_FAIL=1 run start)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
mkdir -p "$repo/.dev/idp"
# The rollback stop ran its teardown, so start recorded its daemon before it.
if [ "$rc" != 0 ] && [[ $calls == "bin/up;dev-idp up --participants;docker rm;"* ]] && [[ $calls != *"dev-idp down"* ]]; then
  ok "start without this checkout's provider state never takes dev-idp down"
else bad "start, other provider: rc=$rc calls=$calls: $out"; fi

# A provider state, which can be stale, from another Docker daemon: start refuses before it
# changes anything, and takes no provider down.
setup
rm -f "$cfg.fixture"
rm -rf "$repo/.dev/participants"
out=$(run start)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" != 0 ] && [[ $out == *"run fixtures/dev-idp down"* ]] && [ -z "$calls" ]; then
  ok "start with a provider state already there refuses, and touches nothing"
else bad "start, provider state: rc=$rc calls=$calls: $out"; fi

# An earlier session's state, with its web host still running: start refuses and keeps the state,
# so stop can still find that web host.
setup
rm -f "$cfg.fixture"
rm -rf "$repo/.dev/idp"
(exec -a "$repo/.dev/participants/webhost" sleep 60) &
host=$!
echo "$host" >"$repo/.dev/participants/webhost.pid"
out=$(run start)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
mkdir -p "$repo/.dev/idp"
if [ "$rc" != 0 ] && [[ $out == *"run session stop first"* ]] && [ -z "$calls" ] && running "$host" &&
  [ "$(cat "$repo/.dev/participants/webhost.pid")" = "$host" ]; then
  ok "start with an earlier session's state refuses, and keeps its web host's record"
else bad "start, earlier session: rc=$rc calls=$calls: $out"; fi
kill "$host" 2>/dev/null
wait "$host" 2>/dev/null

# A provider state that this session's start did not make: stop leaves the provider alone.
setup
rm -f "$repo/.dev/participants/idp-started"
out=$(run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" = 0 ] && [[ $calls != *"dev-idp down"* ]] && [ ! -e "$repo/.dev/participants" ]; then
  ok "stop takes down only the provider that this session's start made"
else bad "stop, provider not this session's: rc=$rc calls=$calls: $out"; fi

# The web host's pid now names another process: stop leaves it alone. The control runs under the
# web host's command, and stop ends it.
setup
sleep 60 &
other=$!
echo "$other" >"$repo/.dev/participants/webhost.pid"
out=$(run stop)
rc=$?
if [ "$rc" = 0 ] && kill -0 "$other" 2>/dev/null; then
  setup
  (exec -a "$repo/.dev/participants/webhost" sleep 60) &
  own=$!
  echo "$own" >"$repo/.dev/participants/webhost.pid"
  out=$(run stop)
  rc=$?
  # The control ends by itself after 60 s, so it must end within 5 s of stop. A signalled child
  # stays a zombie until wait reaps it, and kill -0 still finds a zombie.
  for _ in $(seq 50); do running "$own" || break; sleep 0.1; done
  ended=yes
  if running "$own"; then ended=no; fi
  wait "$own" 2>/dev/null
  if [ "$rc" = 0 ] && [ "$ended" = yes ]; then
    ok "stop signals the web host, and not another process with its pid"
  else
    kill "$own" 2>/dev/null
    bad "stop, own web host: rc=$rc: $out"
  fi
else bad "stop, reused pid: rc=$rc: $out"; fi
kill "$other" 2>/dev/null

exit "$fail"
