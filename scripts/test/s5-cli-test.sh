#!/usr/bin/env bash
# fixtures/scenarios/s5 cli mode (#360): the two checks that read bronzewardctl's kept output decide on its
# text alone. cli_unseen wants the unseen-overwrite warning as one whole line, and unfreeze_refused_cli wants
# exit status 1 and the approver-role refusal. The real functions are cut out of the scenario and run through
# common.sh's own ctl and cli_kept against a stub bronzewardctl that prints a chosen text and exits with a
# chosen status. No fixture, no Docker, no server: the database answer is a variable.
set -uo pipefail
repo_src=$(CDPATH='' cd -- "$(dirname "$0")/../.." && pwd)
t=$(mktemp -d)
trap 'rm -rf -- "$t"' EXIT
failed=0

cat >"$t/bronzewardctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$STUB_TEXT"
exit "${STUB_RC:-0}"
EOF
chmod 0755 "$t/bronzewardctl"

# common.sh reads A's port, the state directory and the scenario's name when it is sourced.
export SERVER_A_PORT=1 STATE=$t/state
export scenario=s5-cli-test
# shellcheck source=/dev/null
. "$repo_src/fixtures/scenarios/common.sh"

# The functions under test, cut from the scenario by name. A name that is not cut fails the run at once.
s5=$repo_src/fixtures/scenarios/s5
eval "$(sed -n '/^ctl_rm() {/,/^}/p; /^cli_unseen() {/,/^}/p; /^unfreeze_refused_cli() {/,/^}/p' "$s5")"
for fn in ctl_rm cli_unseen unfreeze_refused_cli; do
  declare -F "$fn" >/dev/null || { echo "FAIL $fn was not found in fixtures/scenarios/s5"; exit 1; }
done

# What the functions use from the scenario: the run's directories, a worker, and a token and a database
# that answer without a fixture.
# shellcheck disable=SC2034
cli_dir=$t/cli
# shellcheck disable=SC2034
ctl_bin=$t/bronzewardctl
# shellcheck disable=SC2034
EV=$t/ev
# shellcheck disable=SC2034
machine_w=m_worker
mkdir -p "$cli_dir" "$EV"
mint() { printf 'synthetic-token'; }
keep_secret() { :; }
sql() { printf '%s' "$SQL_ANSWER"; }

warning='It overwrites the drifted configuration, which no reviewer has seen: this approval records that you approve overwriting it.'
export STUB_TEXT STUB_RC SQL_ANSWER

# run <want> <name> <stub text> <stub exit> <database answer> <function> <args>...
run() {
  local want=$1 name=$2 rc=0
  STUB_TEXT=$3 STUB_RC=$4 SQL_ANSWER=$5
  shift 5
  # ctl keeps the output as cli-<name>.txt, which cli_unseen reads under the same name.
  case $1 in
    cli_unseen) ctl_rm "approve-$3" h-approver approve "$3" >/dev/null 2>&1 || true ;;
  esac
  "$@" >/dev/null 2>&1 || rc=$?
  if { [ "$want" = pass ] && [ "$rc" -eq 0 ]; } || { [ "$want" = fail ] && [ "$rc" -ne 0 ]; }; then
    echo "ok   $name"
  else
    echo "FAIL $name: want $want, the function exited $rc"
    failed=$((failed + 1))
  fi
}

run pass "cli_unseen accepts the warning as a whole line, with the approval entry marked" \
  "$warning" 0 1 cli_unseen plan_x s
run pass "cli_unseen accepts the same line when the table's padding surrounds it" \
  "  $warning  " 0 1 cli_unseen plan_x s
run fail "cli_unseen refuses a warning that sits inside a longer line" \
  "Note: $warning" 0 1 cli_unseen plan_x s
run fail "cli_unseen refuses the whole line when the approval entry is not marked" \
  "$warning" 0 0 cli_unseen plan_x s
run fail "cli_unseen refuses an approval output without the warning" \
  "Plan plan_x approved." 0 1 cli_unseen plan_x s

role='Needs one of the roles [approver].'
run pass "unfreeze_refused_cli accepts exit 1 with the approver-role refusal and a frozen scope" \
  "$role" 1 t unfreeze_refused_cli h-viewer
run fail "unfreeze_refused_cli refuses exit 2 with the same text" \
  "$role" 2 t unfreeze_refused_cli h-viewer
run fail "unfreeze_refused_cli refuses a refusal that names no approver role" \
  "Refused." 1 t unfreeze_refused_cli h-viewer
run fail "unfreeze_refused_cli refuses a scope that is no longer frozen" \
  "$role" 1 f unfreeze_refused_cli h-viewer

[ "$failed" -eq 0 ] || { echo "s5-cli-test: $failed failed"; exit 1; }
echo "s5-cli-test: ok"
