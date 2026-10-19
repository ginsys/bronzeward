#!/usr/bin/env bash
# Exercise only the extracted released_once function, never the scenario's top-level code.
# Every sql() invocation creates nullable synthetic TEMP tables inside BEGIN/ROLLBACK;
# search_path excludes public, and all setup/mutations explicitly name pg_temp.
set -euo pipefail

source_script=${1:?usage: harness.sh source-script artifact-directory}
artifacts=${2:?usage: harness.sh source-script artifact-directory}
dsn=${BW_TEST_PG_DSN:-postgres://bronzeward@127.0.0.1:55433/bronzeward?sslmode=disable}
mkdir -p -- "$artifacts"
function_file=$artifacts/released_once.sh
awk '/^released_once\(\) \{/ { copy=1 } copy { print } copy && /^}$/ { exit }' "$source_script" >"$function_file"
[ -s "$function_file" ]
bash -n "$function_file"
# shellcheck source=/dev/null
. "$function_file"
pub_op=op_publication
draft=drf_publication
# These variables are consumed by the extracted scenario function.
export pub_op draft
: >"$artifacts/queries.sql"
: >"$artifacts/sql-errors.txt"

cat >"$artifacts/setup.sql" <<'SQL'
BEGIN;
SET LOCAL search_path = pg_temp;
SET LOCAL statement_timeout = '5s';
CREATE TEMP TABLE release (id text, operation text, cluster text, contract text);
CREATE TEMP TABLE draft (id text, state text, release text);
CREATE TEMP TABLE release_machine (release text, machine text, publication_observation text);
CREATE TEMP TABLE observation (id text, machine text, basis bigint, access_path text, running_version text);
CREATE TEMP TABLE observation_start (machine text, revision bigint, purpose text, operation text, publication_generation bigint);
CREATE TEMP TABLE operation (id text, owner_gen bigint);
INSERT INTO pg_temp.release VALUES ('rel_publication', 'op_publication', 'cl_fixture', 'v1.13');
INSERT INTO pg_temp.draft VALUES ('drf_publication', 'published', 'rel_publication');
INSERT INTO pg_temp.release_machine VALUES ('rel_publication', 'mch_worker', 'obs_worker');
INSERT INTO pg_temp.observation VALUES ('obs_worker', 'mch_worker', 10, 'access/talos/cl_fixture', 'v1.13.6');
INSERT INTO pg_temp.observation_start VALUES ('mch_worker', 10, 'publication', 'op_publication', 2);
INSERT INTO pg_temp.operation VALUES ('op_publication', 2);
SQL

mutation() {
  case $case_name in
    matching) ;;
    matching_two_machines)
      cat <<'SQL'
INSERT INTO pg_temp.release_machine VALUES ('rel_publication', 'mch_controlplane', 'obs_controlplane');
INSERT INTO pg_temp.observation VALUES ('obs_controlplane', 'mch_controlplane', 11, 'access/talos/cl_fixture', 'v1.13.7');
INSERT INTO pg_temp.observation_start VALUES ('mch_controlplane', 11, 'publication', 'op_publication', 2);
SQL
      ;;
    no_release) printf '%s\n' 'DELETE FROM pg_temp.release;' ;;
    duplicate_release) printf '%s\n' "INSERT INTO pg_temp.release SELECT 'rel_duplicate', operation, cluster, contract FROM pg_temp.release;" ;;
    draft_not_published) printf '%s\n' "UPDATE pg_temp.draft SET state = 'open';" ;;
    draft_wrong_release) printf '%s\n' "UPDATE pg_temp.draft SET release = 'rel_other';" ;;
    missing_draft) printf '%s\n' 'DELETE FROM pg_temp.draft;' ;;
    zero_machines) printf '%s\n' 'DELETE FROM pg_temp.release_machine;' ;;
    missing_observation) printf '%s\n' 'DELETE FROM pg_temp.observation;' ;;
    missing_start) printf '%s\n' 'DELETE FROM pg_temp.observation_start;' ;;
    missing_operation) printf '%s\n' 'DELETE FROM pg_temp.operation;' ;;
    null_observation_link) printf '%s\n' 'UPDATE pg_temp.release_machine SET publication_observation = NULL;' ;;
    null_generation) printf '%s\n' 'UPDATE pg_temp.observation_start SET publication_generation = NULL;' ;;
    null_owner_generation) printf '%s\n' 'UPDATE pg_temp.operation SET owner_gen = NULL;' ;;
    null_access_path) printf '%s\n' 'UPDATE pg_temp.observation SET access_path = NULL;' ;;
    null_purpose) printf '%s\n' 'UPDATE pg_temp.observation_start SET purpose = NULL;' ;;
    null_operation_binding) printf '%s\n' 'UPDATE pg_temp.observation_start SET operation = NULL;' ;;
    null_running_version) printf '%s\n' 'UPDATE pg_temp.observation SET running_version = NULL;' ;;
    wrong_operation_binding) printf '%s\n' "UPDATE pg_temp.observation_start SET operation = 'op_other';" ;;
    wrong_machine_binding) printf '%s\n' "UPDATE pg_temp.observation SET machine = 'mch_other';" ;;
    wrong_start_basis) printf '%s\n' 'UPDATE pg_temp.observation_start SET revision = 9;' ;;
    wrong_generation) printf '%s\n' 'UPDATE pg_temp.observation_start SET publication_generation = 1;' ;;
    wrong_access_path) printf '%s\n' "UPDATE pg_temp.observation SET access_path = 'access/talos/cl_other';" ;;
    wrong_purpose) printf '%s\n' "UPDATE pg_temp.observation_start SET purpose = 'drift';" ;;
    wrong_minor) printf '%s\n' "UPDATE pg_temp.observation SET running_version = 'v1.12.4';" ;;
    malformed_minor) printf '%s\n' "UPDATE pg_temp.observation SET running_version = 'invalid';" ;;
    mixed_missing_observation)
      printf '%s\n' "INSERT INTO pg_temp.release_machine VALUES ('rel_publication', 'mch_controlplane', 'obs_missing');"
      ;;
    mixed_null_generation)
      cat <<'SQL'
INSERT INTO pg_temp.release_machine VALUES ('rel_publication', 'mch_controlplane', 'obs_controlplane');
INSERT INTO pg_temp.observation VALUES ('obs_controlplane', 'mch_controlplane', 11, 'access/talos/cl_fixture', 'v1.13.7');
INSERT INTO pg_temp.observation_start VALUES ('mch_controlplane', 11, 'publication', 'op_publication', NULL);
SQL
      ;;
    *) printf 'unknown case: %s\n' "$case_name" >&2; return 2 ;;
  esac
}

# Same signature as scenarios/common.sh. The scenario's exact query is executed
# against a fresh copy of this case each time; no setup/query can resolve public.
sql() {
  printf -- '-- %s\n%s;\n' "$case_name" "$1" >>"$artifacts/queries.sql"
  {
    cat "$artifacts/setup.sql"
    mutation
    printf '%s;\nROLLBACK;\n' "$1"
  } | psql "$dsn" -X -v ON_ERROR_STOP=1 -Atq || {
    printf 'SQL_ERROR %s\n' "$case_name" | tee -a "$artifacts/sql-errors.txt" >&2
    return 1
  }
}

printf 'UTC start: %s\n' "$(date -u +%FT%TZ)"
server_version=$(psql "$dsn" -X -v ON_ERROR_STOP=1 -Atqc 'SHOW server_version') || {
  printf 'HARNESS_ERROR: PostgreSQL connection failed before the case matrix\n' >&2
  exit 2
}
printf 'PostgreSQL: %s\n' "$server_version"
printf 'Extracted function: '
sha256sum "$function_file"
printf 'Harness: '
sha256sum "$0"
passed=0 failed=0
for case_name in matching matching_two_machines no_release duplicate_release draft_not_published draft_wrong_release missing_draft \
  zero_machines missing_observation missing_start missing_operation null_observation_link null_generation null_owner_generation \
  null_access_path null_purpose null_operation_binding null_running_version wrong_operation_binding wrong_machine_binding \
  wrong_start_basis wrong_generation wrong_access_path wrong_purpose wrong_minor malformed_minor mixed_missing_observation mixed_null_generation; do
  want=1
  case $case_name in matching | matching_two_machines) want=0 ;; esac
  errors_before=$(wc -l <"$artifacts/sql-errors.txt")
  got=0
  released_once || got=$?
  if [ "$(wc -l <"$artifacts/sql-errors.txt")" != "$errors_before" ]; then
    printf 'HARNESS_ERROR\t%s\tSQL failed; no assertion result\n' "$case_name"
    failed=$((failed + 1))
  elif [ "$got" = "$want" ]; then
    printf 'PASS\t%s\texit=%s\n' "$case_name" "$got"
    passed=$((passed + 1))
  else
    printf 'FAIL\t%s\texit=%s expected=%s\n' "$case_name" "$got" "$want"
    failed=$((failed + 1))
  fi
done
printf 'Cases: %s PASS, %s FAIL; SQL errors: %s\n' "$passed" "$failed" "$(wc -l <"$artifacts/sql-errors.txt")"
printf 'UTC finish: %s\n' "$(date -u +%FT%TZ)"
[ "$failed" = 0 ] && [ ! -s "$artifacts/sql-errors.txt" ]
