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
# dev-idp up fails while STUB_IDP_UP_FAIL is set, as it does on another checkout's containers. With
# STUB_IDP_ENV set, dev-idp up writes the provider's idp.env.
cat >"$repo/fixtures/dev-idp" <<'EOF'
#!/usr/bin/env bash
echo "dev-idp $*" >>"$STUB_LOG"
if [ "$1" = up ] && [ -n "${STUB_IDP_ENV:-}" ]; then
  mkdir -p "$(dirname "$0")/../.dev/idp"
  printf '%s\n' BW_TEST_IDP_ISSUER=http://localhost:11080 BW_TEST_IDP_PROJECT=1001 \
    BW_TEST_IDP_CLI_CLIENT=1002 BW_TEST_IDP_WEB_CLIENT=1003 >"$(dirname "$0")/../.dev/idp/idp.env"
fi
[ "$1 ${STUB_IDP_UP_FAIL:-}" != "up 1" ]
EOF
# go build -o <path>: at a path that ends in /webhost, a web host stand-in. It listens on its -listen
# address, with bw-stub-webhost-<port> as its last argument. With STUB_PID_FAIL set, the web host's pid
# record cannot be written, as on a full disk.
cat >"$t/bin/go" <<'EOF'
#!/usr/bin/env bash
[ "$1 $2" = "build -o" ] && [ "${3##*/}" = webhost ] || exit 0
cat >"$3" <<'W'
#!/usr/bin/env bash
exec python3 -c 'import socket, sys, time
s = socket.socket(); h, p = sys.argv[1].rsplit(":", 1); s.bind((h, int(p))); s.listen(); time.sleep(60)' \
  "$2" "bw-stub-webhost-${2##*:}"
W
chmod +x "$3"
[ -z "${STUB_PID_FAIL:-}" ] || mkdir -p "${3%/*}/webhost.pid"
EOF
cat >"$repo/fixtures/bin/up" <<'EOF'
#!/usr/bin/env bash
echo "bin/up" >>"$STUB_LOG"
EOF
# cp copies, except with STUB_CP_FAIL set: a copy to A's backup path then writes part of the file
# and fails, as on a full disk.
cat >"$t/bin/cp" <<'EOF'
#!/usr/bin/env bash
dst=${*: -1}
if [ -n "${STUB_CP_FAIL:-}" ] && [[ $dst == *config.yaml.fixture* ]]; then
  head -c 10 -- "${*: -2:1}" >"$dst"
  exit 1
fi
exec /bin/cp "$@"
EOF
# mv moves, except with STUB_MV_FAIL set: the rename to A's backup then fails.
cat >"$t/bin/mv" <<'EOF'
#!/usr/bin/env bash
if [ -n "${STUB_MV_FAIL:-}" ] && [[ ${*: -1} == *config.yaml.fixture ]]; then exit 1; fi
exec /bin/mv "$@"
EOF
# chmod changes modes. With STUB_DAEMON_FAIL set, it also puts a directory where session start
# writes its daemon record, so that write fails.
cat >"$t/bin/chmod" <<'EOF'
#!/usr/bin/env bash
/bin/chmod "$@" || exit
if [ -n "${STUB_DAEMON_FAIL:-}" ]; then /bin/mkdir -p -- "${*: -1}/daemon"; fi
EOF
chmod +x "$t/bin/cp" "$t/bin/mv" "$t/bin/chmod"
# start's other prerequisites: npm, and mise for the web build.
for c in npm mise; do printf '#!/bin/sh\nexit 0\n' >"$t/bin/$c"; done
chmod +x "$t/bin/docker" "$t/bin/go" "$t/bin/npm" "$t/bin/mise" "$repo/fixtures/bin/inject" "$repo/fixtures/dev-idp" "$repo/fixtures/bin/up"
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
echo "bus=[${DBUS_SESSION_BUS_ADDRESS-unset}] tokenfile=[${BRONZEWARD_TOKEN_FILE-unset}] context=[${BRONZEWARD_CONTEXT-unset}] config=[$XDG_CONFIG_HOME]"
EOF
chmod +x "$repo/.dev/participants/bin/bronzewardctl"
out=$(printf 'bronzewardctl\nexit\n' | DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent BRONZEWARD_TOKEN_FILE=$t/elsewhere \
  BRONZEWARD_CONTEXT=desk run shell)
rm -rf "$repo/.dev/participants/bin"
if [[ $out == *"bus=[unset] tokenfile=[unset] context=[unset] config=[$repo/.dev/participants/home/config]"* ]]; then
  ok "the participant's shell gives its client no session bus, inherited token file or context"
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
# Temporary files that a failed start left beside A's configuration.
echo partial >"$cfg.partial"
echo partial >"$cfg.fixture.partial"
out=$(run stop)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
if [ "$rc" = 0 ] && [ ! -e "$cfg.fixture" ] && [ ! -e "$cfg.partial" ] && [ ! -e "$cfg.fixture.partial" ] &&
  [ "$(cat "$cfg")" = fixture ] &&
  [ "$calls" = "docker rm;docker network;inject kill A;inject start A;dev-idp down;" ] && [ ! -e "$repo/.dev/participants" ]; then
  ok "stop puts A back on its configuration and restarts it, then removes the backup and the state"
else bad "stop: rc=$rc calls=$calls: $out"; fi

# A temporary file beside A's configuration, without a backup (its rename failed): stop removes it.
setup
rm -f "$cfg.fixture"
echo participant >"$cfg"
echo partial >"$cfg.fixture.partial"
out=$(run stop)
rc=$?
if [ "$rc" = 0 ] && [ ! -e "$cfg.fixture.partial" ] && [ "$(cat "$cfg")" = participant ]; then
  ok "stop removes a temporary file beside A's configuration, also without a backup"
else bad "stop, partial without a backup: rc=$rc: $out"; fi

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

# A copy of A's configuration to its backup fails part-way, or its rename fails: the rollback
# leaves A's configuration as it was, and no backup, partial or not.
for stub in STUB_CP_FAIL STUB_MV_FAIL; do
  setup
  rm -f "$cfg.fixture"
  rm -rf "$repo/.dev/idp" "$repo/.dev/participants"
  printf '    issuer: http://issuer:5556\n    plainHTTPHosts: [issuer]\n    audience: bronzeward\n' >"$cfg"
  before=$(cat "$cfg")
  out=$(env "$stub=1" STUB_IDP_ENV=1 PATH="$t/bin:$PATH" bash "$session" start 2>&1)
  rc=$?
  mkdir -p "$repo/.dev/idp"
  if [ "$rc" != 0 ] && [[ $out == *"cannot back up A's configuration"*"session start failed"* ]] &&
    [ "$(cat "$cfg")" = "$before" ] && [ ! -e "$cfg.fixture" ] && [ ! -e "$cfg.fixture.partial" ]; then
    ok "a failed backup of A's configuration ($stub) leaves the configuration as it was, and no backup"
  else bad "start, failed backup ($stub): rc=$rc cfg=$(head -c 40 "$cfg"): $out"; fi
done

# The daemon record cannot be written: start leaves no participant state, which neither start nor
# stop would accept afterwards, and starts nothing.
setup
rm -f "$cfg.fixture"
rm -rf "$repo/.dev/idp" "$repo/.dev/participants"
out=$(STUB_DAEMON_FAIL=1 run start)
rc=$?
calls=$(tr '\n' ';' <"$STUB_LOG")
mkdir -p "$repo/.dev/idp"
if [ "$rc" != 0 ] && [[ $out == *"daemon: Is a directory"* ]] && [ ! -e "$repo/.dev/participants" ] && [ -z "$calls" ]; then
  ok "a failed write of the daemon record leaves no participant state, and starts nothing"
else bad "start, daemon record fails: rc=$rc calls=$calls: $out"; fi
mkdir -p "$repo/.dev/participants"

# A start that fails after it spawns the web host, because the web host's pid record cannot be
# written: the rollback ends the web host by the pid that start captured, and its port is free. The
# copies use a free port, not 58443, which a real session on this host can hold. The negative
# control is the same start without kill_spawned in its trap, and it must leave the web host. The
# start's list of spawned pids also holds the pid of a process that is not its child, as after the
# web host exits and another process gets its pid. That process must survive.
# Both copies wait until the web host listens before the pid record fails. Without the wait, the
# rollback's stop can remove the participant directory, and the web host script in it, before the
# web host starts: the negative control then finds no web host, and the case proves nothing. A web
# host that does not listen within 5 s fails the start before the pid record, so the case fails.
port=$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
with_port=$repo/fixtures/participants/session-port
no_kill=$repo/fixtures/participants/session-nokill
printf '%s\n' "  for _ in \$(seq 50); do (exec 3<>/dev/tcp/127.0.0.1/$port) 2>/dev/null && break; sleep 0.1; done" \
  "  (exec 3<>/dev/tcp/127.0.0.1/$port) 2>/dev/null || die \"test: the stub web host did not listen within 5 s\"" \
  >"$t/wait-listen"
# shellcheck disable=SC2016 # the session copy's own shell text, not this script's
sed -e "s/58443/$port/g" -e '/^  spawned+=("\$!")$/r '"$t/wait-listen" "$session" >"$with_port"
sed 's/kill_spawned; stop/stop/' "$with_port" >"$no_kill"
# shellcheck disable=SC2016 # the session copy's own shell text, not this script's
sed -i 's/spawned+=("\$!")/spawned+=("$STUB_FOREIGN" "$!")/' "$with_port"
sleep 60 &
foreign=$!
export STUB_FOREIGN=$foreign
listens() { (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; }
stub_host() { pgrep -f " bw-stub-webhost-$port\$"; }
# spawn_start <script>: a fresh start with the provider stub's idp.env and a failing pid record.
spawn_start() {
  setup
  rm -f "$cfg.fixture"
  rm -rf "$repo/.dev/idp" "$repo/.dev/participants"
  printf '    issuer: http://issuer:5556\n    plainHTTPHosts: [issuer]\n    audience: bronzeward\n' >"$cfg"
  out=$(STUB_PID_FAIL=1 STUB_IDP_ENV=1 env PATH="$t/bin:$PATH" bash "$1" start 2>&1)
  rc=$?
  mkdir -p "$repo/.dev/idp"
}
# shellcheck disable=SC2016 # the session copy's own shell text, not this script's
if grep -q "127.0.0.1:$port" "$with_port" && grep -qF 'spawned+=("$STUB_FOREIGN"' "$with_port" &&
  grep -qF "/dev/tcp/127.0.0.1/$port)" "$with_port" && grep -qF "/dev/tcp/127.0.0.1/$port)" "$no_kill" &&
  grep -q 'kill_spawned; stop' "$with_port" && ! grep -q 'kill_spawned; stop' "$no_kill" && ! listens; then
  spawn_start "$no_kill"
  for _ in $(seq 50); do listens && break; sleep 0.1; done
  left=$(stub_host)
  if [ "$rc" != 0 ] && [ -n "$left" ] && listens; then
    for p in $left; do kill "$p" 2>/dev/null; done
    for _ in $(seq 50); do listens || break; sleep 0.1; done
    spawn_start "$with_port"
    sleep 1
    # The start reached the spawn: it failed on the pid record, and the rollback ran.
    if [ "$rc" != 0 ] && [[ $out == *"webhost.pid: Is a directory"*"session start failed"* ]] &&
      [ -z "$(stub_host)" ] && ! listens && running "$foreign"; then
      ok "a start that fails after it spawns the web host ends it, without its pid record, and no other process"
    else bad "start, pid record fails: rc=$rc host=$(stub_host) foreign running=$(running "$foreign" && echo yes || echo no): $out"; fi
  else bad "start, pid record fails, negative control left no web host: rc=$rc: $out"; fi
  for p in $left $(stub_host); do kill "$p" 2>/dev/null; done
else bad "start, pid record fails: the copies with port $port are wrong, or the port is taken"; fi
kill "$foreign" 2>/dev/null
wait "$foreign" 2>/dev/null

# T9's two HTTP call sites, t9_open (the reader before the stop) and t9_read (the resumption and
# the full replay), taken from the session with their helpers and run against a stub A. Each must
# pass on 200 with an event stream, and fail on 404 with a JSON body, on 201, on 200 text/plain and
# on a token A refuses. curl exits 0 on each of these refusals; only t9_head_ok fails the step. The
# pass cases also prove that each call site sends the token and the resumption its Last-Event-ID.
# The operation ID in the path picks the stub's answer.
cat >"$t/stub-a.py" <<'EOF'
import http.server, sys, time
class H(http.server.BaseHTTPRequestHandler):
    def answer(self, code, ctype, events, gap=0):
        self.send_response(code); self.send_header("Content-Type", ctype); self.end_headers()
        for i in range(events):
            self.wfile.write(f"id: 1:{i + 1}\ndata: e\n\n".encode()); self.wfile.flush(); time.sleep(gap)
    def do_GET(self):
        if self.headers.get("Authorization") != "Bearer BWSYNTH-token-t9":
            return self.answer(401, "application/json", 0)
        op = self.path.split("/")[-2]
        if op == "op-404": self.answer(404, "application/json", 0); self.wfile.write(b'{"error":"refused"}')
        elif op == "op-201": self.answer(201, "text/event-stream", 3)
        elif op == "op-plain": self.answer(200, "text/plain", 3)
        elif op == "op-live": self.answer(200, "text/event-stream", 40, 0.25)
        elif op == "op-resume" and self.headers.get("Last-Event-ID") != "1:1": self.answer(400, "application/json", 0)
        else: self.answer(200, "text/event-stream", 3)
    def log_message(self, *a): pass
class S(http.server.ThreadingHTTPServer):
    # A reader that the test kills closes its stream: not an error.
    def handle_error(self, request, client_address): pass
s = S(("127.0.0.1", 0), H)
open(sys.argv[1], "w").write(str(s.server_address[1]))
s.serve_forever()
EOF
python3 -I "$t/stub-a.py" "$t/stub-a.port" &
stub_a=$!
for _ in $(seq 50); do [ -s "$t/stub-a.port" ] && break; sleep 0.1; done
mkdir -p "$t/t9-state"
printf 'BWSYNTH-token-t9' >"$t/t9-state/automation-token"
printf 'BWSYNTH-token-other' >"$t/t9-state/other-token"
sed -n -e '/^t9_auth() {.*}$/p' -e '/^t9_get() {$/,/^}$/p' -e '/^t9_head_ok() {$/,/^}$/p' \
  -e '/^t9_read() {$/,/^}$/p' -e '/^t9_open() {$/,/^}$/p' -e '/^t9_ids() {$/,/^}$/p' "$session" >"$t/t9.sh"
# t9_case <op> <shell text>: the text in a shell as strict as the session, with only the T9
# functions, a die that exits 1, and the stub A's port. STUB_TOKEN=other sends a token A refuses.
t9_case() {
  local tok=automation-token
  [ "${STUB_TOKEN:-}" != other ] || tok='other-token'
  mkdir -p "$t/t9-$tok"
  cp "$t/t9-state/$tok" "$t/t9-$tok/automation-token"
  # shellcheck disable=SC2016 # the inner shell's own text
  STATE=$t/t9-$tok SERVER_A_PORT=$(cat "$t/stub-a.port") t9_op=$1 bash -c \
    'set -euo pipefail; die() { echo "die: $*"; exit 1; }; . "$0"; eval "$1"' "$t/t9.sh" "$2" 2>&1
}
# The reader that t9_open leaves running must be curl itself, so that t9_clean's kill reaches it.
# shellcheck disable=SC2016 # the inner shell's own text
open_ok='t9_open "$T/open"; echo "reader=$(ps -o comm= -p "$t9_reader")"; kill "$t9_reader"; wait "$t9_reader" || :; kill -0 "$t9_reader" 2>/dev/null || echo ended'
export T=$t
# shellcheck disable=SC2016 # each t9_case text is the inner shell's own
if [ "$(grep -cE '^t9_(auth|get|head_ok|read|open|ids)\(\) \{' "$t/t9.sh")" = 6 ] && [ -s "$t/stub-a.port" ]; then
  out=$(t9_case op-live "$open_ok")
  rc=$?
  if [ "$rc" = 0 ] && [[ $out == *"reader=curl"*ended* ]] && grep -q '^id:' "$t/open"; then
    ok "t9_open accepts an open 200 event stream, and its reader is curl"
  else bad "t9_open, event stream: rc=$rc: $out"; fi
  out=$(t9_case op-resume 't9_read "the resumed stream" "$T/read" 10 1:1')
  rc=$?
  if [ "$rc" = 0 ] && [ "$(grep -c '^id:' "$t/read")" = 3 ]; then
    ok "t9_read accepts a 200 event stream, and sends the token and the Last-Event-ID"
  else bad "t9_read, event stream: rc=$rc: $out"; fi
  # The control: curl alone ends with status 0 on a refused read.
  out=$(t9_case op-404 't9_auth | t9_get "$T/control" 10')
  rc=$?
  if [ "$rc" = 0 ] && [ "$(grep -c '^id:' "$t/control")" = 0 ]; then
    ok "control: curl alone ends with status 0 on a 404 answer"
  else bad "control, curl on 404: rc=$rc: $out"; fi
  for c in "op-404:HTTP 404 application/json" "op-201:HTTP 201 text/event-stream" \
    "op-plain:HTTP 200 text/plain" "other-token:HTTP 401 application/json"; do
    op=${c%%:*} want=${c#*:} tok=''
    if [ "$op" = other-token ]; then op=op-live tok=other; fi
    out=$(STUB_TOKEN=$tok t9_case "$op" 't9_open "$T/open-bad"')
    rc=$?
    out2=$(STUB_TOKEN=$tok t9_case "$op" 't9_read "the full replay" "$T/read-bad" 10')
    rc2=$?
    if [ "$rc" != 0 ] && [[ $out == *"the stream on A was refused: $want,"* ]] && [ "$rc2" != 0 ] &&
      [[ $out2 == *"the full replay was refused: $want,"* ]]; then
      ok "t9_open and t9_read fail on ${c%%:*} ($want)"
    else bad "${c%%:*}: t9_open rc=$rc ($out), t9_read rc=$rc2 ($out2)"; fi
  done
else bad "T9: the functions were not found in the session, or the stub A did not start"; fi
kill "$stub_a" 2>/dev/null
wait "$stub_a" 2>/dev/null

# handback and t9_clean (ginsys/bronzeward#509), taken out of the session with stubs around them.
# PAUSED names the paused containers, INSPECT_FAIL one whose inspect fails, A_STOPPED stops A,
# NO_IDP makes A's namespace miss Zitadel, and ISSUER_FAIL, RESTART_FAIL and FWD_FAIL end
# set_issuer, restart_a and forward_idp as their die does. INT_ON sends the shell a Ctrl-C (SIGINT)
# during the inspect of that container, INT_PROBE during idp_reached, as a second Ctrl-C during the
# cleanup does. PREP_FAIL ends prepare, as a release of T9's hold that fails does. HOLD_FAIL ends
# prepare t9-hold, HOLD_INT sends the shell a Ctrl-C during it, SQL_FAIL ends every database read,
# and ERREXIT runs the function under set -e. Calls go to the log.
sed -n -e '/^cleanup_stopped() {$/,/^}$/p' -e '/^handback() {$/,/^}$/p' -e '/^t9_clean() {$/,/^}$/p' \
  -e '/^t9_release() {$/,/^}$/p' -e '/^t9() {$/,/^}$/p' -e '/^t9_disconnect() {$/,/^}$/p' "$session" >"$t/clean.sh"
mkdir -p "$t/cbin"
cat >"$t/cbin/inject" <<'EOF'
#!/bin/sh
echo "inject $*" >>"$T/clean.log"
EOF
cat >"$t/cbin/prepare" <<'EOF'
#!/bin/sh
echo "prepare $*" >>"$T/clean.log"
if [ "$1" = t9-hold ]; then
  if [ -n "${HOLD_INT:-}" ]; then kill -INT "$PPID"; sleep 1; fi
  [ -z "${HOLD_FAIL:-}" ] || exit 1
fi
[ -z "${PREP_FAIL:-}" ]
EOF
chmod +x "$t/cbin/inject" "$t/cbin/prepare"
clean_case() {
  : >"$t/clean.log"
  # shellcheck disable=SC2016 # the inner shell's own text
  bash -c 'set -uo pipefail
    log=$T/clean.log task=t4 bin=$T/cbin here=$T/cbin part=$T/part t9_reader="" t9_op="" BW_A=a BW_B=b BW_C=c
    die() { echo "die: $*" >&2; exit 1; }
    setup() { :; }
    sql() { [ -z "${SQL_FAIL:-}" ]; }
    worker() { echo mch_w; }
    running() { [ -z "${A_STOPPED:-}" ]; }
    idp_reached() { [ -z "${INT_PROBE:-}" ] || kill -INT $$; [ -z "${NO_IDP:-}" ]; }
    restart_a() { echo restart_a; [ -z "${RESTART_FAIL:-}" ] || exit 1; }
    forward_idp() { echo forward_idp; [ -z "${FWD_FAIL:-}" ] || exit 1; }
    set_issuer() { echo "set_issuer $1"; [ -z "${ISSUER_FAIL:-}" ] || exit 1; }
    docker() {
      [ "${*: -1}" != "${INSPECT_FAIL:-}" ] || return 1
      [ "${*: -1}" != "${INT_ON:-}" ] || kill -INT $$
      case " ${PAUSED:-} " in *" ${*: -1} "*) echo true ;; *) echo false ;; esac
    }
    # As t9 disconnect does; a setup has no INT trap, and SIGINT ends the shell.
    [ "$1" != t9_clean ] || trap "exit 130" INT TERM
    # ERREXIT runs the function under set -e, as the session does.
    [ -z "${ERREXIT:-}" ] || set -e
    . "$0"; "$1"' "$t/clean.sh" "$1" 2>&1
}
if [ "$(grep -cE '^(handback|t9_clean|t9_release|t9|t9_disconnect)\(\) \{' "$t/clean.sh")" = 5 ]; then
  out=$(PAUSED="a c" clean_case handback)
  calls=$(tr '\n' ' ' <"$t/clean.log")
  if [ "$calls" = "inject unpause A inject unpause C set_issuer zitadel restart_a " ]; then
    ok "handback unpauses every paused instance, whoever paused it, then hands A back on Zitadel"
  else bad "handback, A and C paused: $calls ($out)"; fi
  for f in ISSUER_FAIL RESTART_FAIL; do
    out=$(export "$f=1" && clean_case handback)
    if [[ $out == *"A could not be handed back on Zitadel; recreate the fixture"* ]]; then
      ok "handback says to recreate the fixture when ${f%_FAIL} ends the shell"
    else bad "handback, $f: $out"; fi
  done
  out=$(PAUSED="a b c" INSPECT_FAIL=b clean_case handback)
  calls=$(tr '\n' ' ' <"$t/clean.log")
  if [[ $out == *"B could not be inspected, so it may still be paused; recreate the fixture"* ]] &&
    [ "$calls" = "inject unpause A inject unpause C set_issuer zitadel restart_a " ]; then
    ok "handback reports an instance it cannot inspect, and unpauses the others"
  else bad "handback, B not inspected: $calls ($out)"; fi
  # A second Ctrl-C ends a cleanup that hangs, for example on a Docker call, and says to recreate
  # the fixture.
  out=$(PAUSED="a b c" INT_ON=a clean_case handback)
  if [ ! -s "$t/clean.log" ] && [[ $out == *"the cleanup was interrupted; recreate the fixture"* ]]; then
    ok "handback ends on a second Ctrl-C and says to recreate the fixture"
  else bad "handback, Ctrl-C during the cleanup: $(tr '\n' ' ' <"$t/clean.log") ($out)"; fi
  out=$(NO_IDP=1 INT_PROBE=1 clean_case t9_clean)
  if [ ! -s "$t/clean.log" ] && [[ $out == *"the cleanup was interrupted; recreate the fixture"* ]]; then
    ok "t9_clean ends on a second Ctrl-C and says to recreate the fixture"
  else bad "t9_clean, Ctrl-C during the cleanup: $(cat "$t/clean.log") ($out)"; fi
  out=$(A_STOPPED=1 clean_case t9_clean)
  calls=$(tr '\n' ' ' <"$t/clean.log")
  if [ "$calls" = "restart_a prepare t9-release " ] && [[ $out == *"A is started again"* ]]; then
    ok "t9_clean starts a stopped A again, then releases T9's hold"
  else bad "t9_clean, A stopped: $calls ($out)"; fi
  out=$(NO_IDP=1 clean_case t9_clean)
  calls=$(tr '\n' ' ' <"$t/clean.log")
  if [ "$calls" = "forward_idp prepare t9-release " ] && [[ $out == *"the forwarder is started again"* ]]; then
    ok "t9_clean starts the forwarder again when A runs but its namespace does not reach Zitadel"
  else bad "t9_clean, no forwarder: $calls ($out)"; fi
  out=$(NO_IDP=1 FWD_FAIL=1 clean_case t9_clean)
  if [[ $out == *"forwarder to Zitadel could not be started again; recreate the fixture"* ]] &&
    [ "$(tail -n 1 "$t/clean.log")" = "prepare t9-release" ]; then
    ok "t9_clean says to recreate the fixture when the forwarder cannot start, and still releases T9's hold"
  else bad "t9_clean, forwarder fails: $(tr '\n' ' ' <"$t/clean.log") ($out)"; fi
  # T9's apply is held on C until t9 disconnect releases it (ginsys/bronzeward#512): a disconnect
  # that ends early must not leave it held, or the next setup waits on an apply that never ends.
  out=$(clean_case t9_clean)
  if [ "$(cat "$t/clean.log")" = "prepare t9-release" ] && [ -z "$out" ]; then
    ok "t9_clean releases T9's hold, and leaves a running A that reaches Zitadel alone"
  else bad "t9_clean, nothing to repair: $(cat "$t/clean.log") ($out)"; fi
  out=$(PREP_FAIL=1 clean_case t9_clean)
  if [[ $out == *"the hold on T9's apply could not be released; recreate the fixture"* ]]; then
    ok "t9_clean says to recreate the fixture when T9's hold cannot be released"
  else bad "t9_clean, release fails: $(cat "$t/clean.log") ($out)"; fi
  out=$(clean_case t9_release)
  if [ "$(cat "$t/clean.log")" = "prepare t9-release" ] && [ -z "$out" ]; then
    ok "t9_release asks prepare to release the hold, and says nothing when it is released"
  else bad "t9_release: $(cat "$t/clean.log") ($out)"; fi
  # The hold is released on every way out of the t9 setup but success, and of t9 disconnect from
  # its first read on.
  out=$(clean_case t9)
  if [ "$(cat "$t/clean.log")" = "prepare t9-hold" ] && [[ $out == *"held on C"* ]]; then
    ok "the t9 setup keeps a hold that succeeded"
  else bad "t9, hold succeeds: $(tr '\n' ' ' <"$t/clean.log") ($out)"; fi
  out=$(HOLD_FAIL=1 clean_case t9)
  calls=$(tr '\n' ' ' <"$t/clean.log")
  if [ "$calls" = "prepare t9-hold prepare t9-release " ] && [[ $out == *"the apply was not held on C"* ]]; then
    ok "the t9 setup releases a hold that failed"
  else bad "t9, hold fails: $calls ($out)"; fi
  out=$(HOLD_INT=1 clean_case t9)
  calls=$(tr '\n' ' ' <"$t/clean.log")
  if [ "$calls" = "prepare t9-hold prepare t9-release " ] && [[ $out != *"held on C"* ]]; then
    ok "the t9 setup releases the hold when a Ctrl-C stops it during the hold"
  else bad "t9, Ctrl-C during the hold: $calls ($out)"; fi
  # Under set -e, as in the session, a failed first read ends the shell at once, before any die.
  rc=0
  out=$(ERREXIT=1 SQL_FAIL=1 clean_case t9_disconnect) || rc=$?
  if [ "$(cat "$t/clean.log")" = "prepare t9-release" ] && [ "$rc" != 0 ]; then
    ok "t9 disconnect releases the hold and fails when its first read fails"
  else bad "t9 disconnect, first read fails: status $rc, $(tr '\n' ' ' <"$t/clean.log") ($out)"; fi
  # The setup after T9 releases the hold first, for a session that skipped t9 disconnect: its
  # `prepare applied` waits for T9's apply to complete.
  if [ "$(sed -n '/^setup_t2() {$/,/^}$/p' "$session" | grep -oE 't9-release|prepare applied' | tr '\n' ' ')" = "t9-release prepare applied " ]; then
    ok "the t2 setup releases T9's hold before it waits for T9's apply"
  else bad "the t2 setup does not release T9's hold before prepare applied"; fi
  # A joined link lets A or B dispatch the apply, which then ends in about 50 ms, before any read.
  t9_body=$(sed -n '/^t9() {$/,/^}$/p' "$session")
  if [[ $t9_body == *'t9-hold'* ]] && [[ $t9_body != *'links linkjoin'* ]]; then
    ok "the t9 setup holds the apply on C and joins no other instance's link to the worker"
  else bad "the t9 setup joins every link to the worker, or does not hold the apply on C"; fi
  # A release with no hold, as t9 disconnect's cleanup before the t9 setup does, must leave T8's
  # cut links cut. prepare needs a fixture, so its steps are read in order: t9-hold records the hold
  # before it writes C's control file, and t9-release joins the links only under that record.
  prep=$repo_src/fixtures/participants/prepare
  hold_order=$(sed -n '/^  t9-hold)$/,/^    ;;$/p' "$prep" | grep -oE 'participants-t9-hold|interrupt dispatch-committed' | tr '\n' ' ')
  # shellcheck disable=SC2016 # $STATE is prepare's text, not this shell's
  release_order=$(sed -n '/^  t9-release)$/,/^    ;;$/p' "$prep" |
    grep -oE '! -e "\$STATE/participants-t9-hold"|^ +else$|interrupt -|worker_links linkjoin|rm -f -- "\$STATE/participants-t9-hold"' |
    sed 's/^ *//' | tr '\n' ' ')
  # shellcheck disable=SC2016 # $STATE is prepare's text, not this shell's
  if [ "$hold_order" = "participants-t9-hold interrupt dispatch-committed " ] &&
    [ "$release_order" = '! -e "$STATE/participants-t9-hold" else interrupt - worker_links linkjoin rm -f -- "$STATE/participants-t9-hold" ' ]; then
    ok "prepare t9-release joins the links only after t9-hold recorded a hold"
  else bad "prepare: the hold record does not guard the release (hold: $hold_order; release: $release_order)"; fi
else bad "handback, t9_clean and t9_release were not found in the session"; fi

# idp_reached with a docker client that ignores TERM, as one waiting on a daemon that hangs can. The
# timeout's KILL ends it; the test shortens the timeout to 1 s and its KILL fallback to 1 s more.
if ! grep -nE '(^|[ ;(])timeout( -s [A-Z]+)? [0-9]' "$session" >"$t/nokill"; then
  ok "every timeout in the session has a KILL fallback"
else bad "a timeout in the session has no KILL fallback: $(cat "$t/nokill")"; fi
sed -n '/^idp_reached() {$/,/^}$/p' "$session" | sed 's/timeout -k 5 60 /timeout -k 1 1 /' >"$t/probe.sh"
mkdir -p "$t/hbin"
cat >"$t/hbin/docker" <<'EOF'
#!/bin/sh
trap '' TERM
sleep 10
EOF
chmod +x "$t/hbin/docker"
s0=$SECONDS
# shellcheck disable=SC2016 # the inner shell's own text
PATH=$t/hbin:$PATH bash -c 'BW_A=a BW_DEV_IDP_PORT=1 ZITADEL_LOGIN_IMAGE=i discovery_get=""
  . "$0"; idp_reached' "$t/probe.sh" 2>/dev/null
rc=$?
if [ "$rc" != 0 ] && [ $((SECONDS - s0)) -lt 6 ]; then
  ok "idp_reached ends a docker client that ignores TERM"
else bad "idp_reached with a docker client that ignores TERM: rc $rc after $((SECONDS - s0)) s"; fi

exit "$fail"
