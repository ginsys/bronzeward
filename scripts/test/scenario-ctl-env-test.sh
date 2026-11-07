#!/usr/bin/env bash
# fixtures/scenarios/common.sh ctl_env (#354): the CLI a scenario runs gets no credential or context from the
# caller's environment. BRONZEWARD_TOKEN_FILE takes precedence over each context's token file, so one left
# in the caller's environment would send the caller's own credentials to the fixture. bronzewardctl is a stub
# that prints the variables it receives; no fixture, no Docker, only synthetic files.
set -uo pipefail
repo_src=$(CDPATH='' cd -- "$(dirname "$0")/../.." && pwd)
t=$(mktemp -d)
trap 'rm -rf -- "$t"' EXIT
failed=0

cat >"$t/bronzewardctl" <<'EOF'
#!/usr/bin/env bash
printf 'token_file=%s\n' "${BRONZEWARD_TOKEN_FILE-<unset>}"
printf 'context=%s\n' "${BRONZEWARD_CONTEXT-<unset>}"
printf 'config=%s\n' "${XDG_CONFIG_HOME-<unset>}"
printf 'args=%s\n' "$*"
EOF
chmod 0755 "$t/bronzewardctl"

# common.sh reads A's port, the state directory and the scenario's name when it is sourced; none is used here.
export SERVER_A_PORT=1 STATE=$t/state
export scenario=ctl-env-test
# shellcheck source=../../fixtures/scenarios/common.sh
. "$repo_src/fixtures/scenarios/common.sh"
# ctl_env reads cli_dir and ctl_bin.
# shellcheck disable=SC2034
cli_dir=$t/cli
# shellcheck disable=SC2034
ctl_bin=$t/bronzewardctl
mkdir -p "$cli_dir"

# A synthetic token file of the caller's, exported as an operator would.
printf '{"accessToken":"synthetic-caller-token"}' >"$t/caller-token.json"
export BRONZEWARD_TOKEN_FILE=$t/caller-token.json BRONZEWARD_CONTEXT=caller-context

got=$(ctl_env --context h-viewer operation show op_x)
expect() {
  if printf '%s\n' "$got" | grep -qxF -- "$1"; then
    echo "ok   $2"
  else
    echo "FAIL $2: want the line '$1' in:"
    printf '%s\n' "$got"
    failed=$((failed + 1))
  fi
}
expect 'token_file=<unset>' "the caller's BRONZEWARD_TOKEN_FILE does not reach the CLI, so the context's own token file is used"
expect 'context=<unset>' "the caller's BRONZEWARD_CONTEXT does not reach the CLI, so --context decides"
expect "config=$cli_dir/config" "the CLI reads the run directory's configuration"
expect 'args=--context h-viewer operation show op_x' "the arguments pass through unchanged"

[ "$failed" -eq 0 ] || { echo "scenario-ctl-env-test: $failed failed"; exit 1; }
echo "scenario-ctl-env-test: ok"
