# shellcheck shell=bash
# Shared setup for the fixture commands. Sourced, never executed.

# CDPATH emptied: with it set, `cd` to a relative path prints the directory it found, and the
# substitution would capture that line as well as the one from pwd. -P on both: this path names
# the checkout in the fixture's claim, and the same checkout reached through a symlink must give
# the same name, or its own down would refuse it.
FIXTURES=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
STATE=$FIXTURES/.state
CACHE=$FIXTURES/.cache

# A BAO_TOKEN from the caller's own OpenBao or Vault work would win over the fixture's root token in
# bao() below. bin/evidence sets it on purpose, after this point. The fixture's own variables are
# cleared too: a secrets.env that lacks one of them, as an interrupted up leaves it, must not fall
# through to a value from the caller's shell.
unset BAO_TOKEN BW_CANARY BW_POSTGRES_PASSWORD BW_BAO_ROOT_TOKEN BW_BAO_METADATA_TOKEN
# Likewise defaults the caller set for tar and for the gzip it runs: a TAR_OPTIONS with an --exclude
# would make store-snapshot write, without any error, an archive that lacks part of the store.
unset TAR_OPTIONS GZIP

# bin/up only ever creates .state as a directory. A symlink there was put by someone else, and
# following it would source a secrets.env from outside the checkout, or write this run's secrets
# there. Checked here, before anything under it is read: every command loads this file first.
# The same holds one level down for the directories the commands write into: through a symlinked
# evidence or backups directory, dumps and snapshots would land where teardown does not reach.
# And for .cache: through a link, downloads would land elsewhere and `down --purge` would remove
# the link alone while reporting the cache gone. And for the Talos state directory, which names
# what `talosctl cluster destroy` acts on.
for managed in "$STATE" "$STATE/data" "$STATE/backups" "$STATE/evidence" "$STATE/talos" "$CACHE"; do
  if [ -L "$managed" ]; then
    printf 'fixtures: %s is a symlink; the fixture never creates one. Remove the link and run again\n' "$managed" >&2
    exit 1
  fi
done
unset managed

set -a
# shellcheck source-path=SCRIPTDIR source=versions.env
. "$FIXTURES/versions.env"
set +a
# The name the fixture was created under: bin/up records it first of all. With versions.env edited
# or the checkout switched since, every name and label above would be derived from another value,
# and down would look for nothing, find nothing, remove .state and report success while the
# fixture runs on under the old name. Refused here, for every command, until the value is back.
if [ -e "$STATE/up-fixture-name" ] || [ -L "$STATE/up-fixture-name" ]; then
  if [ -L "$STATE/up-fixture-name" ] || [ ! -f "$STATE/up-fixture-name" ] ||
    [ "$(stat --format=%h -- "$STATE/up-fixture-name" 2>/dev/null)" != 1 ]; then
    printf 'fixtures: %s is not the regular file bin/up writes, with that one name; the fixture never makes anything else there\n' "$STATE/up-fixture-name" >&2
    exit 1
  fi
  IFS= read -r created_as <"$STATE/up-fixture-name" || created_as=
  if [ "$created_as" != "$FIXTURE_NAME" ]; then
    printf 'fixtures: the fixture in %s was created as %s, and versions.env now names %s; restore FIXTURE_NAME, or the checkout it came from, then run fixtures/bin/down\n' "$STATE" "${created_as:-nothing}" "$FIXTURE_NAME" >&2
    exit 1
  fi
  unset created_as
fi
# The generated secrets are read as data, not sourced: a secrets.env that was replaced, or a line
# added to it, must not run as shell code here, before any ownership check. Only the four
# assignments bin/up writes are accepted, each a bare value without whitespace, and the file must
# be the regular file bin/up made, under that one name: with a second name outside .state the
# secrets would outlive teardown there.
if [ -e "$STATE/secrets.env" ] || [ -L "$STATE/secrets.env" ]; then
  if [ -L "$STATE/secrets.env" ] || [ ! -f "$STATE/secrets.env" ] ||
    [ "$(stat --format=%h -- "$STATE/secrets.env" 2>/dev/null)" != 1 ]; then
    printf 'fixtures: %s is not the regular file bin/up writes, with that one name; the fixture never makes anything else there\n' "$STATE/secrets.env" >&2
    exit 1
  fi
  while IFS= read -r secret_line || [ -n "$secret_line" ]; do
    if [[ $secret_line =~ ^(BW_CANARY|BW_POSTGRES_PASSWORD|BW_BAO_ROOT_TOKEN|BW_BAO_METADATA_TOKEN)=([^[:space:]]+)$ ]]; then
      export "${BASH_REMATCH[1]}=${BASH_REMATCH[2]}"
    else
      printf 'fixtures: %s holds a line the fixture never writes; remove the file and the fixture with it\n' "$STATE/secrets.env" >&2
      exit 1
    fi
  done <"$STATE/secrets.env"
  unset secret_line
fi

# Keep every generated client config inside .state; never touch the caller's own.
export TALOSCONFIG=$STATE/talosconfig
export KUBECONFIG=$STATE/kubeconfig

# The daemon-wide claim: a container that is created and never started. Docker refuses a second
# container of the same name atomically, which no look-before check across checkouts can do, and
# its label names the checkout the fixture belongs to.
CLAIM=$FIXTURE_NAME-claim
CLAIM_LABEL=bronzeward.fixture=$FIXTURE_NAME
CLAIM_OWNER_LABEL=bronzeward.fixture.checkout
# The up that made the claim, which two up of one checkout started together share nothing else
# with: the one Docker refused must not take the winner's claim for its own.
CLAIM_ATTEMPT_LABEL=bronzeward.fixture.attempt
# On the Compose volumes bin/up makes: the run that made them, which bin/down holds each to.
# shellcheck disable=SC2034  # read by bin/up and bin/down, which source this file
RUN_LABEL=bronzeward.fixture.run

PG=$FIXTURE_NAME-postgres
BAO=$FIXTURE_NAME-openbao
CP=$FIXTURE_NAME-controlplane-1
WORKER=$FIXTURE_NAME-worker-1

say() { printf '%s\n' "$*" >&2; }
die() {
  say "fixtures: $*"
  exit 1
}

need() {
  local tool
  for tool in "$@"; do
    # type -P: an executable on PATH. `command -v` is also satisfied by a shell function, and curl
    # below is wrapped in one.
    type -P "$tool" >/dev/null || die "missing prerequisite: $tool"
  done
}

# need_state: this checkout has a fixture, and the fixture on the daemon is that one. The state
# directory alone does not show it: container names are fixed, so after this checkout's resources
# were removed by hand another checkout's fixture answers to the same names, and the stale
# secrets.env here would send a kill or a restore to it.
need_state() {
  local owner
  [ -f "$STATE/secrets.env" ] || die "no running fixture: run fixtures/bin/up first"
  # All four, from the file: an up interrupted between writing them leaves fewer, and the
  # variables were cleared above, so a missing one is empty here rather than the caller's.
  if [ -z "${BW_CANARY:-}" ] || [ -z "${BW_POSTGRES_PASSWORD:-}" ] ||
    [ -z "${BW_BAO_ROOT_TOKEN:-}" ] || [ -z "${BW_BAO_METADATA_TOKEN:-}" ]; then
    die "$STATE/secrets.env is incomplete, as an interrupted up leaves it. Run fixtures/bin/down, then up"
  fi
  need docker
  daemon_own
  owner=$(claim_owner) || die "could not ask Docker who holds the fixture claim"
  [ -n "$owner" ] ||
    die "$STATE exists but no fixture claim does: the state is stale. Run fixtures/bin/down, then up"
  [ "$owner" = "$FIXTURES" ] ||
    die "the fixture on this daemon belongs to $owner, so $STATE is stale. Remove that directory by hand: fixtures/bin/down refuses here, rightly, because the running fixture is not this checkout's"
  state_files_own
  containers_own
}

# state_files_own: every credential file bin/up generated is the regular file it wrote, under that
# one name. Hard-linked outside .state, a bao-init.json or talosconfig keeps the root token or the
# client key past teardown, and the scan roots never reach the source itself. secrets.env is
# checked above, before it is read; the rest here, by need_state and by down, before anything is
# scanned or removed. The node-volume record has its own check in down. injections.log too: its
# arguments name snapshots, and bin/evidence copies it into the bundle as the timeline. And the
# records of the Talos container and network IDs, which containers_own and down trust with what
# a fixture name or label may act on.
state_files_own() {
  local file
  for file in bao-init.json talosconfig kubeconfig talos-secrets.yaml controlplane.yaml scan-patterns.txt injections.log down-node-containers down-node-networks down-compose-containers down-compose-volumes down-compose-networks up-manifest up-fixtures-diff.txt up-fixture-name up-daemon; do
    [ -e "$STATE/$file" ] || [ -L "$STATE/$file" ] || continue
    if [ -L "$STATE/$file" ] || [ ! -f "$STATE/$file" ] || [ "$(stat --format=%h -- "$STATE/$file" 2>/dev/null)" != 1 ]; then
      die "$STATE/$file is not the regular file bin/up writes, with that one name; the fixture never makes anything else there"
    fi
  done
}

# daemon_own: the Docker daemon this shell reaches is the one bin/up created the fixture on. With
# DOCKER_HOST or the context changed since, every check above would be made against a daemon that
# holds none of it, down would verify that one clean and remove .state while the fixture, and its
# credentials, run on. The daemon's ID is what bin/up recorded; no record (an up interrupted
# before it) is not a mismatch.
daemon_own() {
  local recorded reached
  [ -e "$STATE/up-daemon" ] || [ -L "$STATE/up-daemon" ] || return 0
  if [ -L "$STATE/up-daemon" ] || [ ! -f "$STATE/up-daemon" ] || [ "$(stat --format=%h -- "$STATE/up-daemon" 2>/dev/null)" != 1 ]; then
    die "$STATE/up-daemon is not the regular file bin/up writes, with that one name; the fixture never makes anything else there"
  fi
  IFS= read -r recorded <"$STATE/up-daemon" || recorded=
  reached=$(docker info --format '{{.ID}}' 2>/dev/null) || die "the Docker daemon does not answer; nothing can be verified"
  [ -n "$recorded" ] && [ "$recorded" = "$reached" ] ||
    die "the fixture in $STATE was created on the Docker daemon ${recorded:-recorded as nothing}, and this shell reaches $reached (DOCKER_HOST or the Docker context changed); point it back at that daemon and run again"
}

# containers_own: every container that answers to one of the fixture's names is the fixture's. The
# names are fixed, so once the real one was removed by hand anything can take the name while the
# claim still stands, and a kill or a restore would go to it. The Compose containers carry the
# project and its directory, which any container can be labelled with too, and the Talos nodes
# only the cluster name; each is held to the ID bin/up recorded under that name once it had
# created it. Under the name, not among the recorded: with the two nodes' names swapped each is
# still one recorded, and a pause of the worker would go to the control plane. One that is gone
# is what a kill or a teardown that could not finish leaves, and is not refused here; any other
# failure to ask is not that, and must not read as it, or the container would be acted on
# unverified.
containers_own() {
  local name answer
  for name in "$PG" "$BAO"; do
    if ! answer=$(docker inspect --type container --format \
      '{{.Id}} {{index .Config.Labels "com.docker.compose.project"}} {{index .Config.Labels "com.docker.compose.project.working_dir"}}' \
      "$name" 2>&1); then
      case $answer in
        *[Nn]o\ such\ container*) continue ;;
        *) die "could not inspect the container $name: $answer; the fixture will not act on what it cannot verify" ;;
      esac
    fi
    [ "${answer#* }" = "$FIXTURE_NAME $FIXTURES" ] ||
      die "the container named $name is not this fixture's: its labels do not name this project and checkout. Remove it by hand; the fixture will not act on it"
    recorded_under "$name" "${answer%% *}" "$STATE/down-compose-containers" Compose
  done
  for name in "$CP" "$WORKER"; do
    if ! answer=$(docker inspect --type container --format '{{.Id}}' "$name" 2>&1); then
      case $answer in
        *[Nn]o\ such\ container*) continue ;;
        *) die "could not inspect the container $name: $answer; the fixture will not act on what it cannot verify" ;;
      esac
    fi
    recorded_under "$name" "$answer" "$STATE/down-node-containers" Talos
  done
}
# recorded_under <name> <id> <record> <mark>: the record bin/up wrote, one `<id> <name>` per line,
# names <id> under <name>.
recorded_under() {
  [ -f "$3" ] ||
    die "a container named $1 exists but bin/up left no record of the $4 containers it created, as an up interrupted right after creating them leaves it. Run fixtures/bin/down --adopt, then up"
  grep --quiet --line-regexp --fixed-strings -- "$2 $1" "$3" ||
    die "the container named $1 is not this fixture's: it is not the one bin/up created under that name. Remove or rename it by hand; the fixture will not act on it"
}

# The project name is forced: a COMPOSE_PROJECT_NAME in the caller's shell wins over the `name` in
# compose.yaml, and every ownership check and teardown probe selects by that name's label.
compose() {
  docker compose --project-name "$FIXTURE_NAME" --project-directory "$FIXTURES" \
    --file "$FIXTURES/compose.yaml" \
    --env-file "$FIXTURES/versions.env" --env-file "$STATE/secrets.env" "$@"
}

# fixtures_manifest_diff <status>: how fixtures/ differs from the commit, as bin/up records it at
# creation and bin/evidence at capture: the status given, the diff of the tracked files and the
# whole content of the untracked ones (as a diff against nothing), so that the commit plus this
# says what ran. Git's own diff: --no-ext-diff and --no-textconv keep a helper or filter from the
# caller's configuration, which may print nothing with the status git would give, from being
# handed it; --binary carries a file that is not text whole, not as "differ". Ignored files,
# .state and .cache, are left out: they are the run's secrets and the pinned binaries.
fixtures_manifest_diff() {
  local file
  printf '%s\n\n' "$1"
  git -C "$FIXTURES" -c core.autocrlf=false diff --no-ext-diff --no-textconv --binary HEAD -- "$FIXTURES" || return 1
  # A pipe, not a substitution: a substitution drops the NULs that end each name. With pipefail
  # a failing listing fails the pipeline, as does the loop when a diff cannot be written.
  git -C "$FIXTURES" ls-files --others --exclude-standard -z -- "$FIXTURES" |
    while IFS= read -r -d '' file; do
      # --no-index exits 1 when the two differ, which a file against /dev/null always does.
      # core.autocrlf off here too: with it on, git warns on an untracked LF file it would convert.
      git -C "$FIXTURES" -c core.autocrlf=false diff --no-index --no-ext-diff --no-textconv --binary -- /dev/null "$file" || [ $? -eq 1 ] || exit 1
    done
}

# tree_name <path>: a name as bin/up may print it. bin/evidence passes its own sanitizer instead,
# which withholds a name that holds one of the run's secrets; before up generated them there is
# nothing of the fixture's to withhold.
tree_name() { printf '%s' "$1"; }

# fixtures_tree_check <workdir> <diff-file> <name-fn>: the commit fixtures/ is at, in `manifest`,
# and how it differs from it, in `differs` and, when it does, as fixtures_manifest_diff in
# <diff-file>. bin/up runs it before recording what the fixture is made from and bin/evidence
# before tying a bundle to what ran, the same checks in both, so that the creation record stands
# by what the bundle stands by: a commit plus a diff say what ran only if the tree holds nothing
# they leave out. Refused: an untracked file that only .git/info/exclude or a global excludes file
# hides, since neither the status nor the diff sees it (the repository's own .gitignore files are
# the one exclusion stood by: .state and .cache); a symlink anywhere else under fixtures/, tracked
# or not, since a diff carries the target's name and not the bytes that run, and on another host
# the name points at something else or at nothing; and any warning git or find print while
# listing, since a directory they cannot open is left out of the listing with status 0, and named
# in the warning as it is. Every name printed goes through <name-fn>: a name may hold a secret.
# Listings and the collected stderr go into <workdir> and are removed; a listing that fails is not
# an empty one.
fixtures_tree_check() {
  local workdir=$1 diff_file=$2 name_fn=$3 warn file attribute value hidden top ignores untracked_all flagged filtered linked find_rc=0
  warn=$workdir/.git-stderr
  : >"$warn" || die "cannot collect git's warnings in $workdir"
  # Assigned first: a git that fails inside a printf argument would leave the line empty.
  manifest=$(git -C "$FIXTURES" rev-parse HEAD 2>>"$warn") ||
    die "cannot read the manifest commit from git; the fixture could not be tied to a fixture version"
  # core.autocrlf off for the status and the diff: with it on, a tracked script turned to CRLF
  # reads as unmodified (both sides normalised), or as modified with an empty diff, and the bytes
  # that ran, a CRLF shebang among them, would be in neither. The text, eol, ident and
  # working-tree-encoding attributes do the same whatever the setting, and are refused below with
  # the clean filter. --untracked-files=all: a status.showUntrackedFiles=no in the caller's
  # configuration would leave a tree whose only change is an untracked file reading as the commit's.
  differs=$(git -C "$FIXTURES" -c core.autocrlf=false status --porcelain --untracked-files=all -- "$FIXTURES" 2>>"$warn") ||
    die "cannot ask git whether fixtures/ differs from commit $manifest"
  # A tracked file git is told to skip, assume-unchanged or skip-worktree (git update-index): a
  # modification there is in no status and no diff. ls-files -v tags each file, a lowercase tag
  # for assume-unchanged and S for skip-worktree.
  flagged=$(git -C "$FIXTURES" ls-files -v -z -- "$FIXTURES" 2>>"$warn" |
    while IFS= read -r -d '' file; do
      case $file in
        [a-z]\ * | S\ *) printf '%s ' "$("$name_fn" "$FIXTURES/${file#??}")" ;;
      esac
    done) ||
    die "cannot ask git which files under fixtures/ it is told to skip; the commit could not be tied to what runs"
  # The ignore rules the listings below apply are the working tree's: a .gitignore modified, or
  # an untracked one (which may hide itself and its directory), hides an untracked file from
  # every listing, and the diff would carry the rule and not the file. The root .gitignore applies
  # under fixtures/ too. Refused while any of them is not the commit's.
  top=$(git -C "$FIXTURES" rev-parse --show-toplevel 2>>"$warn") ||
    die "cannot find the repository root; the commit could not be tied to what runs"
  ignores=$(git -C "$FIXTURES" -c core.autocrlf=false status --porcelain --untracked-files=all -- "$top/.gitignore" "$FIXTURES" 2>>"$warn") ||
    die "cannot ask git about the ignore files; the commit could not be tied to what runs"
  untracked_all=$(git -C "$FIXTURES" ls-files --others -- "$FIXTURES" 2>>"$warn") ||
    die "cannot list the files under fixtures/; the commit could not be tied to what runs"
  ignores=$(grep -E '(^|[ /])\.gitignore"?$' <<<"$ignores"$'\n'"$untracked_all" || [ $? -eq 1 ]) ||
    die "cannot look for ignore files under fixtures/; the commit could not be tied to what runs"
  ignores=$(while IFS= read -r file; do
    [ -n "$file" ] || continue
    printf '%s ' "$("$name_fn" "$file")"
  done <<<"$ignores")
  unset top untracked_all
  git -C "$FIXTURES" ls-files --others --exclude-standard -z -- "$FIXTURES" 2>>"$warn" | sort -z >"$workdir/.untracked-seen" ||
    die "cannot list the untracked files under fixtures/; the commit could not be tied to what runs"
  git -C "$FIXTURES" ls-files --others --exclude-per-directory=.gitignore -z -- "$FIXTURES" 2>>"$warn" | sort -z >"$workdir/.untracked-all" ||
    die "cannot list the untracked files under fixtures/; the commit could not be tied to what runs"
  hidden=$(comm --zero-terminated -13 "$workdir/.untracked-seen" "$workdir/.untracked-all" |
    while IFS= read -r -d '' file; do
      printf '%s ' "$("$name_fn" "$FIXTURES/$file")"
    done) ||
    die "cannot compare the untracked listings of fixtures/; the commit could not be tied to what runs"
  rm -- "$workdir/.untracked-seen" "$workdir/.untracked-all" || die "cannot remove the untracked listings from $workdir"
  # A clean filter from the attributes has git compare the filter's output, not the bytes that
  # run: one that maps a modified script back to its committed content leaves the status empty
  # and the diff with it, and --no-textconv does not turn it off. A file under fixtures/ that any
  # attributes file gives a filter is refused, untracked ones included: the diff against nothing
  # that carries an untracked file applies the attributes too. The output is path, attribute,
  # value.
  filtered=$({ git -C "$FIXTURES" ls-files -z -- "$FIXTURES" &&
    git -C "$FIXTURES" ls-files --others --exclude-standard -z -- "$FIXTURES"; } 2>>"$warn" |
    git -C "$FIXTURES" check-attr --stdin -z filter text eol ident working-tree-encoding 2>>"$warn" |
    while IFS= read -r -d '' file && IFS= read -r -d '' attribute && IFS= read -r -d '' value; do
      case $value in
        unspecified | unset) ;;
        *) printf '%s (%s) ' "$("$name_fn" "$FIXTURES/$file")" "$attribute" ;;
      esac
    done) ||
    die "cannot ask git which attributes apply under fixtures/; the commit could not be tied to what runs"
  # find's status is looked at after its warnings: a directory it cannot open fails it, and the
  # warning names that directory. Besides symlinks, what git records nothing of: a named pipe, a
  # socket, a device, an empty directory. Code that runs may read from any of them, and the commit
  # plus a diff would not show it.
  linked=$(find "$FIXTURES" \( -path "$STATE" -o -path "$CACHE" \) -prune -o \
    \( -type l -o -type p -o -type s -o -type b -o -type c -o \( -type d -empty \) \) -print -quit 2>>"$warn") || find_rc=$?
  tree_warnings_refuse "$warn" "$name_fn"
  [ "$find_rc" -eq 0 ] || die "cannot look for symlinks and special files under fixtures/; the commit could not be tied to what runs"
  [ -z "$hidden" ] ||
    die "untracked files under fixtures/ are hidden from git by an excludes file outside the repository: ${hidden% }; the commit plus a diff could not say what runs. Track, remove or unhide them"
  [ -z "$ignores" ] ||
    die "the ignore rules are not the commit's: ${ignores% }; an untracked file they hide would be in no listing, and the commit plus a diff would not say what runs. Commit, revert or remove the .gitignore change"
  [ -z "$flagged" ] ||
    die "git is told to skip ${flagged% }(assume-unchanged or skip-worktree); a change there is in no status and no diff, and the commit plus a diff would not say what runs. Clear the flag (git update-index --no-assume-unchanged, --no-skip-worktree)"
  [ -z "$filtered" ] ||
    die "a filter, text, eol, ident or working-tree-encoding attribute from git's attributes applies to ${filtered% }; git would compare the attribute's output, not the bytes that run. Remove the attribute"
  [ -z "$linked" ] ||
    die "$("$name_fn" "$linked") is a symlink, a special file or an empty directory under fixtures/; git records nothing of it, and the commit plus a diff would not say what runs. Replace or remove it"
  # Written when the tree is the commit's too, empty then: a record that is absent could not be
  # told from one removed, and bin/evidence requires the one bin/up wrote.
  if [ -n "$differs" ]; then
    fixtures_manifest_diff "$differs" >"$diff_file" 2>>"$warn" ||
      die "cannot record how fixtures/ differs from commit $manifest; the commit could not be tied to what runs"
  else
    : >"$diff_file" || die "cannot record that fixtures/ is commit $manifest; the commit could not be tied to what runs"
  fi
  tree_warnings_refuse "$warn" "$name_fn"
  rm -f -- "$warn"
}
# tree_warnings_refuse <file> <name-fn>: refuse on what git and find said on stderr, each line
# through <name-fn>, the file removed first.
tree_warnings_refuse() {
  local line warned=
  [ -s "$1" ] || return 0
  warned=$(while IFS= read -r line; do printf '%s; ' "$("$2" "$line")"; done <"$1")
  rm -f -- "$1"
  die "git or find warned while listing fixtures/, so the listing is incomplete: ${warned%; }. The commit could not be tied to what runs"
}

# compose_volume_names: the daemon-side names of the named volumes compose.yaml declares,
# <project>_<key>, as Compose derives them. Read from the file, not repeated here. The secrets
# file may not exist yet, so the password is only interpolated.
compose_volume_names() {
  local keys key
  # Into a variable first: a failed `config` inside a `for` word list would be an empty list and
  # a clean exit, and an empty list is not what compose.yaml declares.
  keys=$(BW_POSTGRES_PASSWORD=${BW_POSTGRES_PASSWORD:-unused} docker compose --project-name "$FIXTURE_NAME" \
    --project-directory "$FIXTURES" --file "$FIXTURES/compose.yaml" --env-file "$FIXTURES/versions.env" \
    config --volumes) || return 1
  [ -n "$keys" ] || return 1
  for key in $keys; do
    printf '%s_%s\n' "$FIXTURE_NAME" "$key"
  done
}

talosctl() { "$CACHE/talosctl" "$@"; }

# Every curl of the fixture ignores the caller's curlrc: an `output` or a `proxy` set there would
# send a snapshot somewhere else, or a token through a proxy, with curl still exiting 0. --disable
# only counts as the first argument.
curl() { command curl --disable "$@"; }

# Every tar of the fixture takes its names literally: GNU tar unquotes backslash escapes in the
# directory given to -C (`\b` read as a backspace), so an archive or a checkout whose name holds
# one would be expanded somewhere that does not exist.
tar() { command tar --no-unquote "$@"; }

# Every request to a service is bounded. The fixture exists to be broken on purpose, and a provider
# that accepts a request and never answers would otherwise hang the command for good; a request
# that times out fails like any other. wait_for shortens the limits to what is left of its own.
REQUEST_LIMIT=20
PG_LIMIT=60

# bao <args>: the OpenBao CLI inside the pinned container, as root token unless BAO_TOKEN is set.
bao() {
  timeout "$REQUEST_LIMIT" docker exec -e BAO_TOKEN="${BAO_TOKEN:-${BW_BAO_ROOT_TOKEN:-}}" "$BAO" bao "$@"
}

pg() { timeout "$PG_LIMIT" docker exec -e PGPASSWORD="$BW_POSTGRES_PASSWORD" "$PG" "$@"; }

# pg_client <psql args>: psql from a throwaway container on the fixture network. The image trusts
# every connection that starts inside the server's own container, so pg() proves nothing about the
# password; only a connection from another host is asked for it.
pg_client() {
  PGPASSWORD=$BW_POSTGRES_PASSWORD timeout "$PG_LIMIT" docker run --rm --network "${FIXTURE_NAME}_default" \
    --env PGPASSWORD "$POSTGRES_IMAGE" psql --host=postgres --username=bronzeward --dbname=bronzeward "$@"
}

# claim_take [attempt]: fails when any checkout on this daemon already holds the claim. The image
# declares a data volume, and Docker would make an anonymous, unlabelled one for the claim although
# it never starts; removed outside claim_drop without --volumes, it would outlive every record. A
# tmpfs at that path takes the volume's place, and nothing is made.
claim_take() {
  docker create --quiet --name "$CLAIM" --label "$CLAIM_LABEL" --label "$CLAIM_OWNER_LABEL=$FIXTURES" \
    --label "$CLAIM_ATTEMPT_LABEL=${1:-}" --tmpfs /var/lib/postgresql/data --network none "$POSTGRES_IMAGE" true >/dev/null
}
# claim_names: every container carrying the claim label (names, or nothing).
claim_names() { docker ps --all --filter "label=$CLAIM_LABEL" --format '{{.Names}}'; }
# claim_strangers: containers carrying the claim label under another name than the claim's. Not
# the claim, whatever their labels say, and someone's: nothing here removes them.
claim_strangers() {
  claim_names | { grep --invert-match --line-regexp --fixed-strings -- "$CLAIM" || [ $? -eq 1 ]; }
}
# claim_label <key>: that label of the container of the claim's name, if it carries the claim
# label; nothing when there is no such container or it is not the claim. Fails only when Docker
# cannot say, on "no such container" it says nothing.
claim_label() {
  local out
  if out=$(docker inspect --type container --format "{{index .Config.Labels \"${CLAIM_LABEL%%=*}\"}} {{index .Config.Labels \"$1\"}}" "$CLAIM" 2>&1); then
    [ "${out%% *}" = "${CLAIM_LABEL#*=}" ] || return 0
    printf '%s\n' "${out#* }"
    return 0
  fi
  case $out in
    *[Nn]o\ such\ container*) return 0 ;;
    *) return 1 ;;
  esac
}
# claim_owner: the checkout that took the claim (nothing when there is no claim).
claim_owner() { claim_label "$CLAIM_OWNER_LABEL"; }
# claim_drop: the container of the claim's name, once its label shows it to be the claim, and only
# that one: the label alone selects any container given it. --volumes, because the image declares
# a data volume and Docker creates an anonymous one for the claim although it never starts.
# Nothing else would ever find that volume again.
claim_drop() {
  local strangers held
  strangers=$(claim_strangers) || die "could not list the containers carrying the fixture claim label"
  [ -z "$strangers" ] ||
    die "$(tr '\n' ' ' <<<"$strangers")carries the fixture claim label without being the claim $CLAIM; not the fixture's, not removed"
  held=$(claim_label "$CLAIM_OWNER_LABEL") || die "could not ask Docker about the claim $CLAIM"
  [ -n "$held" ] || return 0
  docker rm --force --volumes "$CLAIM" >/dev/null
}

# The exact content of the leak scan's positive control. bin/up writes it and bin/evidence compares
# against it: a control file that holds anything else is not the control any more.
control_content() { printf 'planted on purpose: %s\n' "$BW_CANARY"; }

# Talos node volumes are anonymous and unlabelled: once their container is gone nothing ties them to
# the fixture. bin/up records these names right after creating the cluster, bin/down adds what it
# still sees, and the list in .state is what teardown removes and verifies.
talos_volume_names() { # talos_volume_names [container-id ...]: of every labelled container when none is given
  local ids
  if [ $# -gt 0 ]; then
    ids=$(printf '%s\n' "$@")
  else
    ids=$(docker ps --all --quiet --filter "label=talos.cluster.name=$FIXTURE_NAME") || return 1
  fi
  # shellcheck disable=SC2016  # a Go template, not a shell expansion
  xargs --no-run-if-empty docker inspect \
    --format '{{range .Mounts}}{{if eq .Type "volume"}}{{println .Name}}{{end}}{{end}}' <<<"$ids"
}

# container <target>: map an injection target to a container name.
container() {
  case $1 in
    postgres) printf '%s\n' "$PG" ;;
    openbao) printf '%s\n' "$BAO" ;;
    controlplane) printf '%s\n' "$CP" ;;
    worker) printf '%s\n' "$WORKER" ;;
    *) die "unknown target '$1' (postgres, openbao, controlplane, worker)" ;;
  esac
}

# fetch <name> <url> <sha256>: download once into .cache, refuse a checksum mismatch.
fetch() {
  local name=$1 url=$2 sum=$3 file=$CACHE/download-$1 partial
  mkdir -p "$CACHE"
  # Never through a link: curl writes through an existing symlink, so a linked cache entry would
  # send the download into whatever file it names. The download lands in a new file of its own
  # and takes the cache name only once its checksum passed.
  [ ! -L "$file" ] || die "$file is a symlink; the fixture never makes one. Remove it and run again"
  if [ ! -f "$file" ] || ! printf '%s  %s\n' "$sum" "$file" | sha256sum --check --status; then
    say "downloading $name"
    partial=$(mktemp "$file.partial.XXXXXX")
    curl --fail --silent --show-error --location --output "$partial" "$url" || {
      rm -f -- "$partial"
      die "$name: download of $url failed"
    }
    printf '%s  %s\n' "$sum" "$partial" | sha256sum --check --status || {
      rm -f -- "$partial"
      die "$name: sha256 mismatch for $url"
    }
    mv --no-target-directory -- "$partial" "$file"
  fi
  printf '%s\n' "$file"
}

fetch_tools() {
  local file
  file=$(fetch talosctl "$TALOSCTL_URL" "$TALOSCTL_SHA256")
  install -m 0755 "$file" "$CACHE/talosctl"
  file=$(fetch sops "$SOPS_URL" "$SOPS_SHA256")
  install -m 0755 "$file" "$CACHE/sops"
  file=$(fetch age "$AGE_URL" "$AGE_SHA256")
  tar -xzf "$file" -C "$CACHE" --strip-components=1 age/age age/age-keygen
}

# scan_patterns: every synthetic secret this run produced, one per line, from the files bin/up
# generated. An empty or very short pattern would match every line of every file, so anything
# under 8 characters is dropped. The client configs hold private keys of their own, generated for
# this cluster's admin clients and absent from the secrets bundle; collected first, so that a
# config whose key is not where it is expected fails instead of thinning the list. bin/up writes
# the list to .state/scan-patterns.txt; bin/evidence rebuilds it from the same sources and refuses
# a file that differs, since a list thinned after up would let a scan pass with a token unmatched.
# Each source is read on its own and a source that cannot be read fails the whole list: a jq that
# stops halfway through bao-init.json would otherwise leave a list that is short and looks whole.
scan_patterns() {
  local talos_client_key kube_client_key bao_keys metadata_token talos_secrets
  talos_client_key=$(awk '$1 == "key:" {print $2}' "$TALOSCONFIG") || return 1
  kube_client_key=$(awk '$1 == "client-key-data:" {print $2}' "$KUBECONFIG") || return 1
  [ -n "$talos_client_key" ] || die "no client key found in $TALOSCONFIG; it would go unscanned"
  [ -n "$kube_client_key" ] || die "no client key found in $KUBECONFIG; it would go unscanned"
  # Both encodings of the unseal key: they share no substring, and either is the credential.
  bao_keys=$(jq -r '.root_token, .unseal_keys_b64[], .unseal_keys_hex[]' "$STATE/bao-init.json") || return 1
  metadata_token=$(awk -F= '$1 == "BW_BAO_METADATA_TOKEN" {print $2}' "$STATE/secrets.env") || return 1
  talos_secrets=$(awk 'tolower($1) ~ /^(key|secret|token|bootstraptoken|secretboxencryptionsecret|aescbcencryptionsecret):$/ {print $2}' \
    "$STATE/talos-secrets.yaml") || return 1
  printf '%s\n' 'BWSYNTH-' "$talos_client_key" "$kube_client_key" "$bao_keys" "$metadata_token" "$talos_secrets" |
    awk 'length($0) >= 8' | sort -u
}

bao_unseal() {
  local key
  key=$(jq -r '.unseal_keys_b64[0]' "$STATE/bao-init.json")
  bao operator unseal "$key" >/dev/null
}

# wait_for <seconds> <description> <command...>
# The deadline holds for an attempt that hangs as well as for one that fails: each attempt gets what
# is left of the limit as its own request limit. The command is a shell function, which `timeout`
# cannot run, so it must make its requests through bao, pg or another bounded call.
wait_for() {
  local limit=$1 what=$2 start=$SECONDS left
  shift 2
  while :; do
    left=$((limit - (SECONDS - start)))
    [ "$left" -gt 0 ] || die "timed out after ${limit}s waiting for $what"
    if REQUEST_LIMIT=$((left < REQUEST_LIMIT ? left : REQUEST_LIMIT)) PG_LIMIT=$((left < PG_LIMIT ? left : PG_LIMIT)) \
      "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
}
