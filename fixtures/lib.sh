# shellcheck shell=bash
# Shared setup for the fixture commands. Sourced, never executed.

# CDPATH emptied: with it set, `cd` to a relative path prints the directory it found, and the
# substitution would capture that line as well as the one from pwd. -P on both: this path names
# the checkout in the fixture's claim, and the same checkout reached through a symlink must give
# the same name, or its own down would refuse it.
FIXTURES=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
STATE=$FIXTURES/.state
CACHE=$FIXTURES/.cache
# The digests of this file and of the command running, as they are on disk at load: the tree
# check later ties the fixture to a commit by the files as they are then, and a script edited
# before launch and put back before that inventory would have run as edited while the inventory
# says the commit's. Taken again at the inventory and at the end of up (scripts_unchanged_since_load),
# and a difference refuses the run; what remains is the moment between the shell opening a script
# and this line. The command's path is made absolute here, so that a later cd does not change it.
loaded_scripts=("$FIXTURES/lib.sh" "$(CDPATH='' cd -P -- "$(dirname "$0")" && pwd -P)/$(basename "$0")")
loaded_digests=$(sha256sum -- "${loaded_scripts[@]}") || {
  printf 'fixtures: cannot read the scripts loaded, %s and %s\n' "${loaded_scripts[0]}" "${loaded_scripts[1]}" >&2
  exit 1
}
scripts_unchanged_since_load() {
  [ "$(sha256sum -- "${loaded_scripts[@]}" 2>/dev/null)" = "$loaded_digests" ] ||
    die "${loaded_scripts[0]} or ${loaded_scripts[1]} changed since it was loaded; what ran is not what the files say. Run again"
}

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

# Read once, as bytes, and sourced from that reading: bin/up records the same bytes, and the guard
# below holds them, not a second reading of the file, to the record; an edit between two readings
# would have the values in use and the record disagree without a word.
versions_env=$(cat -- "$FIXTURES/versions.env") || {
  printf 'fixtures: cannot read %s\n' "$FIXTURES/versions.env" >&2
  exit 1
}
# Every value the manifest must set, cleared before the file is read and required after: a
# value exported by the caller's shell would otherwise stand in for one the file omits, used by
# every command and recorded nowhere, since the record is the file's bytes.
versions_keys=(TALOS_VERSION TALOSCTL_URL TALOSCTL_SHA256 TALOS_IMAGE KUBERNETES_VERSION OPENBAO_IMAGE
  POSTGRES_IMAGE SOPS_VERSION SOPS_URL SOPS_SHA256 AGE_VERSION AGE_URL AGE_SHA256 AGE_BINARY_SHA256
  AGE_KEYGEN_SHA256 FIXTURE_NAME TALOS_SUBNET TALOS_CONTROLPLANE_IP TALOS_WORKER_IP POSTGRES_PORT OPENBAO_PORT)
unset -v "${versions_keys[@]}"
# Parsed, never sourced: sourced, a value that is an expansion (`$RANDOM`, `${X:-58200}`) would
# be evaluated, and the same bytes could give another value at another command, with the record
# of the bytes holding. Each line is blank, a comment, or one plain KEY=value whose value is made
# of the characters a version, an address, a URL or a digest needs; anything else is refused.
versions_line=0
while IFS= read -r line; do
  versions_line=$((versions_line + 1))
  [[ ! $line =~ ^[[:space:]]*(#.*)?$ ]] || continue
  if [[ $line =~ ^([A-Z][A-Z0-9_]*)=([A-Za-z0-9._:/@+-]*)$ ]]; then
    # Only the keys above, each once: any other name would be assigned too, STATE, CACHE, PATH or
    # a key of the commands', and a key set twice would run under its last value while the record
    # of the bytes shows both. Every key above was unset before the file is read, so one that is
    # set now was set by an earlier line.
    case " ${versions_keys[*]} " in
      *" ${BASH_REMATCH[1]} "*) ;;
      *)
        printf 'fixtures: versions.env line %d sets %s, which is not a value the manifest defines; the file sets only those the commands need\n' "$versions_line" "${BASH_REMATCH[1]}" >&2
        exit 1
        ;;
    esac
    if [ -n "${!BASH_REMATCH[1]+set}" ]; then
      printf 'fixtures: versions.env line %d sets %s a second time; a key is set once, so that the value that runs is the one the record shows\n' "$versions_line" "${BASH_REMATCH[1]}" >&2
      exit 1
    fi
    printf -v "${BASH_REMATCH[1]}" '%s' "${BASH_REMATCH[2]}"
    export "${BASH_REMATCH[1]}"
  else
    printf 'fixtures: versions.env line %d is not a plain KEY=value assignment (a value of letters, digits and ._:/@+-); the manifest is read literally, never evaluated\n' "$versions_line" >&2
    exit 1
  fi
done <<<"$versions_env"
unset versions_line line
for versions_key in "${versions_keys[@]}"; do
  [ -n "${!versions_key:-}" ] || {
    printf 'fixtures: versions.env does not set %s; every command needs it, and a value from the shell would not be recorded\n' "$versions_key" >&2
    exit 1
  }
done
unset versions_key versions_keys
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
# The rest of versions.env, likewise: the ports, the node addresses, the image pins and the
# requested versions every command uses come from the file as it is now, and bin/up records the
# whole file as it was. An edit since, the name kept, would have inject send a token to another
# port, netjoin put a node back at another address, or evidence report a version that was never
# requested. Refused, byte for byte, until the file is back.
if [ -e "$STATE/up-versions.env" ] || [ -L "$STATE/up-versions.env" ]; then
  if [ -L "$STATE/up-versions.env" ] || [ ! -f "$STATE/up-versions.env" ] ||
    [ "$(stat --format=%h -- "$STATE/up-versions.env" 2>/dev/null)" != 1 ]; then
    printf 'fixtures: %s is not the regular file bin/up writes, with that one name; the fixture never makes anything else there\n' "$STATE/up-versions.env" >&2
    exit 1
  fi
  if ! printf '%s\n' "$versions_env" | cmp --silent -- - "$STATE/up-versions.env"; then
    printf 'fixtures: versions.env is not the file the fixture in %s was created with (recorded as %s); restore it, or the checkout it came from, then run fixtures/bin/down\n' "$STATE" "$STATE/up-versions.env" >&2
    exit 1
  fi
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
  for file in bao-init.json talosconfig kubeconfig talos-secrets.yaml controlplane.yaml scan-patterns.txt injections.log down-node-containers down-node-networks down-compose-containers down-compose-volumes down-compose-networks up-manifest up-fixtures-diff.txt up-fixture-name up-daemon up-versions.env up-compose.yaml; do
    [ -e "$STATE/$file" ] || [ -L "$STATE/$file" ] || continue
    if [ -L "$STATE/$file" ] || [ ! -f "$STATE/$file" ] || [ "$(stat --format=%h -- "$STATE/$file" 2>/dev/null)" != 1 ]; then
      die "$STATE/$file is not the regular file bin/up writes, with that one name; the fixture never makes anything else there"
    fi
  done
}

# daemon_own: the Docker daemon this shell reaches is the one bin/up created the fixture on. With
# DOCKER_HOST or the context changed since, every check above would be made against a daemon that
# holds none of it, down would verify that one clean and remove .state while the fixture, and its
# credentials, run on. The daemon's ID is what bin/up recorded, before it made anything else under
# the directory, and a directory it could not record it in it removed again; so a state directory
# without the record is not one bin/up left, and is not taken for a fixture on whichever daemon
# the shell reaches now. No state directory at all is nothing to hold to.
daemon_own() {
  local recorded reached
  [ -d "$STATE" ] || return 0
  if [ ! -e "$STATE/up-daemon" ] && [ ! -L "$STATE/up-daemon" ]; then
    # An empty directory is what a removal cut short between the record, which down removes last,
    # and the directory itself leaves: nothing to hold to, and down removes it.
    [ -n "$(find "$STATE" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ] || return 0
    die "no record in $STATE of the Docker daemon the fixture was created on; bin/up writes it before it makes anything else there, so the directory is not one it left. Find the fixture's daemon by hand, run down there once the record is restored, or remove $STATE yourself"
  fi
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
# The ID each name was verified under, for a command to act on: a mutation by name would act on
# whatever carries the name by then, a replacement made after the check included.
# shellcheck disable=SC2034  # read by bin/inject
declare -A verified_container_ids=()
containers_own() {
  local name answer
  # shellcheck disable=SC2034  # read by bin/inject
  verified_container_ids=()
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
    # shellcheck disable=SC2034  # read by bin/inject
    verified_container_ids["$name"]=${answer%% *}
  done
  for name in "$CP" "$WORKER"; do
    if ! answer=$(docker inspect --type container --format '{{.Id}}' "$name" 2>&1); then
      case $answer in
        *[Nn]o\ such\ container*) continue ;;
        *) die "could not inspect the container $name: $answer; the fixture will not act on what it cannot verify" ;;
      esac
    fi
    recorded_under "$name" "$answer" "$STATE/down-node-containers" Talos
    # shellcheck disable=SC2034  # read by bin/inject
    verified_container_ids["$name"]=$answer
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

# compose_inputs: the compose file and the environment file Compose is given, in compose_file and
# compose_env. The copies bin/up recorded under .state once they exist, the checkout's files
# before: the services are created well after the records are written (the tools are fetched in
# between), and Compose reading the checkout's files then would create them from bytes the records
# do not hold, an image or a port among them, with the checkout put back afterwards passing every
# guard. Without the records nothing was composed yet, and the checkout's files are what an up
# would compose.
compose_inputs() {
  if [ -f "$STATE/up-compose.yaml" ]; then
    compose_file=$STATE/up-compose.yaml
    compose_env=$STATE/up-versions.env
  else
    compose_file=$FIXTURES/compose.yaml
    compose_env=$FIXTURES/versions.env
  fi
}

# The project name is forced: a COMPOSE_PROJECT_NAME in the caller's shell wins over the `name` in
# compose.yaml, and every ownership check and teardown probe selects by that name's label. The
# project directory stays the checkout, so that a path in the file resolves as it always did.
compose() {
  local compose_file compose_env
  compose_inputs
  docker compose --project-name "$FIXTURE_NAME" --project-directory "$FIXTURES" \
    --file "$compose_file" \
    --env-file "$compose_env" --env-file "$STATE/secrets.env" "$@"
}

# fixtures_git <args...>: git on the fixtures checkout, for every inventory and diff below, with two
# of the caller's settings turned off. core.autocrlf: with it on, a tracked script turned to CRLF
# reads as unmodified (both sides normalised), or as modified with an empty diff, and the bytes
# that ran, a CRLF shebang among them, would be in neither; and git warns on an untracked LF file
# it would convert. core.ignoreCase: with it on, an untracked file whose name differs from a
# tracked one's only by case is taken for the tracked one, in no status and no listing, while
# find sees an ordinary file (observed: README.MD beside README.md, every inventory empty). And
# the stat cache at its strictest: core.trustctime=false or core.checkStat=minimal let a tracked
# file changed to bytes of the same length, its mtime put back, pass for the commit's without its
# content being read; an fsmonitor or an untracked cache would have git ask a daemon or a
# directory's mtime what changed, in place of looking. core.fileMode=false has git ignore the
# executable bit, so a command made non-executable is in no status and no diff, while running it
# fails. And replacement refs (git replace) have status and diff compare against the replacement
# commit's tree while rev-parse names the original, so the manifest would name a commit whose
# bytes did not run: --no-replace-objects reads every object as it is. And core.symlinks=false has
# git check a committed symlink out as a regular file holding the target's name, and read that file
# back as the symlink, so the tree is clean while a regular file runs where the commit has a link:
# core.symlinks=true has git see the regular file for what it is, a type change, in the diff.
fixtures_git() {
  git --no-replace-objects -C "$FIXTURES" -c core.autocrlf=false -c core.ignoreCase=false -c core.trustctime=true \
    -c core.checkStat=default -c core.fsmonitor=false -c core.untrackedCache=false -c core.fileMode=true \
    -c core.symlinks=true "$@"
}

# fixtures_manifest_diff <status> <commit>: how fixtures/ differs from the commit, as bin/up
# records it at creation and bin/evidence at capture: the status given, the diff of the tracked
# files against the commit named, not HEAD, which a checkout meanwhile would have moved, and the
# whole content of the untracked ones (as a diff against nothing), so that the commit plus this
# says what ran. Git's own diff: --no-ext-diff and --no-textconv keep a helper or filter from the
# caller's configuration, which may print nothing with the status git would give, from being
# handed it; --binary carries a file that is not text whole, not as "differ". Ignored files,
# .state and .cache, are left out: they are the run's secrets and the pinned binaries.
fixtures_manifest_diff() {
  local file
  printf '%s\n\n' "$1"
  fixtures_git diff --no-ext-diff --no-textconv --binary "$2" -- "$FIXTURES" || return 1
  # A pipe, not a substitution: a substitution drops the NULs that end each name. With pipefail
  # a failing listing fails the pipeline, as does the loop when a diff cannot be written.
  fixtures_git ls-files --others --exclude-standard -z -- "$FIXTURES" |
    while IFS= read -r -d '' file; do
      # --no-index exits 1 when the two differ, which a file against /dev/null always does.
      fixtures_git diff --no-index --no-ext-diff --no-textconv --binary -- /dev/null "$file" || [ $? -eq 1 ] || exit 1
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
  local workdir=$1 diff_file=$2 name_fn=$3 warn file attribute value hidden top ignores untracked_all flagged gitlinks filtered linked find_rc=0 tab=$'\t' first_manifest first_differs
  warn=$workdir/.git-stderr
  scripts_unchanged_since_load
  : >"$warn" || die "cannot collect git's warnings in $workdir"
  fixtures_tree_inventory
  first_manifest=$manifest
  first_differs=$differs
  # Written when the tree is the commit's too, empty then: a record that is absent could not be
  # told from one removed, and bin/evidence requires the one bin/up wrote.
  fixtures_tree_diff "$diff_file"
  # The inventory and the diff describe the tree as it was at their moments, one after the other;
  # a file changed, added, linked or flagged, or HEAD moved, between two of them would have the
  # diff describe a tree the inventory did not pass, or the inventory pass a tree the diff does
  # not describe. Both are taken a second time and held to the first: the same commit, the same
  # status, the same diff byte for byte, or the run is refused. What is not seen is a change made
  # and undone between the two takes of the same listing.
  fixtures_tree_inventory
  [ "$manifest" = "$first_manifest" ] ||
    die "HEAD moved from $first_manifest to $manifest while fixtures/ was being inventoried; run again"
  [ "$differs" = "$first_differs" ] ||
    die "fixtures/ changed while it was being inventoried (git's status differs between two listings); run again"
  fixtures_tree_diff "$workdir/.diff-again"
  cmp --silent -- "$diff_file" "$workdir/.diff-again" ||
    die "fixtures/ changed while it was being inventoried (the diff from commit $manifest differs between two takes); run again"
  rm -f -- "$workdir/.diff-again" "$warn"
}
# fixtures_tree_diff <file>: fixtures_manifest_diff of the inventory just taken into <file>, empty
# when the tree is the commit's. Called from fixtures_tree_check, whose variables it uses.
fixtures_tree_diff() {
  if [ -n "$differs" ]; then
    fixtures_manifest_diff "$differs" "$manifest" >"$1" 2>>"$warn" ||
      die "cannot record how fixtures/ differs from commit $manifest; the commit could not be tied to what runs"
  else
    : >"$1" || die "cannot record that fixtures/ is commit $manifest; the commit could not be tied to what runs"
  fi
  tree_warnings_refuse "$warn" "$name_fn"
}
# fixtures_tree_inventory: one take of every listing fixtures_tree_check stands by, `manifest` and
# `differs` set, refused on anything the commit plus a diff would leave out. Called from
# fixtures_tree_check, whose variables it uses.
fixtures_tree_inventory() {
  find_rc=0
  # Assigned first: a git that fails inside a printf argument would leave the line empty.
  manifest=$(fixtures_git rev-parse HEAD 2>>"$warn") ||
    die "cannot read the manifest commit from git; the fixture could not be tied to a fixture version"
  # The text, eol, ident and working-tree-encoding attributes normalise like core.autocrlf does
  # (turned off in fixtures_git) whatever the setting, and are refused below with the clean
  # filter. --untracked-files=all: a status.showUntrackedFiles=no in the caller's configuration
  # would leave a tree whose only change is an untracked file reading as the commit's.
  differs=$(fixtures_git status --porcelain --untracked-files=all -- "$FIXTURES" 2>>"$warn") ||
    die "cannot ask git whether fixtures/ differs from commit $manifest"
  # A tracked file git is told to skip, assume-unchanged or skip-worktree (git update-index): a
  # modification there is in no status and no diff. ls-files -v tags each file, a lowercase tag
  # for assume-unchanged and S for skip-worktree.
  flagged=$(fixtures_git ls-files -v -z -- "$FIXTURES" 2>>"$warn" |
    while IFS= read -r -d '' file; do
      case $file in
        [a-z]\ * | S\ *) printf '%s ' "$("$name_fn" "$FIXTURES/${file#??}")" ;;
      esac
    done) ||
    die "cannot ask git which files under fixtures/ it is told to skip; the commit could not be tied to what runs"
  # A gitlink, a submodule or an embedded repository staged as one (mode 160000): the diff carries
  # "Subproject commit <hash>", with -dirty at most, never the bytes under it, and the untracked
  # listing stops at its directory. ls-files --stage gives mode, object, stage, a tab, the path.
  gitlinks=$(fixtures_git ls-files --stage -z -- "$FIXTURES" 2>>"$warn" |
    while IFS= read -r -d '' file; do
      case $file in
        160000\ *) printf '%s ' "$("$name_fn" "$FIXTURES/${file#*"$tab"}")" ;;
      esac
    done) ||
    die "cannot ask git for the index entries under fixtures/; the commit could not be tied to what runs"
  # The ignore rules the listings below apply are the working tree's: a .gitignore modified, or
  # an untracked one (which may hide itself and its directory), hides an untracked file from
  # every listing, and the diff would carry the rule and not the file. The root .gitignore applies
  # under fixtures/ too. Refused while any of them is not the commit's.
  top=$(fixtures_git rev-parse --show-toplevel 2>>"$warn") ||
    die "cannot find the repository root; the commit could not be tied to what runs"
  ignores=$(fixtures_git status --porcelain --untracked-files=all -- "$top/.gitignore" "$FIXTURES" 2>>"$warn") ||
    die "cannot ask git about the ignore files; the commit could not be tied to what runs"
  untracked_all=$(fixtures_git ls-files --others -- "$FIXTURES" 2>>"$warn") ||
    die "cannot list the files under fixtures/; the commit could not be tied to what runs"
  ignores=$(grep -E '(^|[ /])\.gitignore"?$' <<<"$ignores"$'\n'"$untracked_all" || [ $? -eq 1 ]) ||
    die "cannot look for ignore files under fixtures/; the commit could not be tied to what runs"
  ignores=$(while IFS= read -r file; do
    [ -n "$file" ] || continue
    printf '%s ' "$("$name_fn" "$file")"
  done <<<"$ignores")
  unset top untracked_all
  fixtures_git ls-files --others --exclude-standard -z -- "$FIXTURES" 2>>"$warn" | sort -z >"$workdir/.untracked-seen" ||
    die "cannot list the untracked files under fixtures/; the commit could not be tied to what runs"
  fixtures_git ls-files --others --exclude-per-directory=.gitignore -z -- "$FIXTURES" 2>>"$warn" | sort -z >"$workdir/.untracked-all" ||
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
  filtered=$({ fixtures_git ls-files -z -- "$FIXTURES" &&
    fixtures_git ls-files --others --exclude-standard -z -- "$FIXTURES"; } 2>>"$warn" |
    fixtures_git check-attr --stdin -z filter text eol ident working-tree-encoding 2>>"$warn" |
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
  # plus a diff would not show it. And a nested repository, staged as a gitlink or not: git lists
  # nothing under one it recognises, and what is under one it does not is a repository all the same.
  linked=$(find "$FIXTURES" \( -path "$STATE" -o -path "$CACHE" \) -prune -o \
    \( -type l -o -type p -o -type s -o -type b -o -type c -o \( -type d -empty \) -o -name .git \) -print -quit 2>>"$warn") || find_rc=$?
  tree_warnings_refuse "$warn" "$name_fn"
  [ "$find_rc" -eq 0 ] || die "cannot look for symlinks and special files under fixtures/; the commit could not be tied to what runs"
  [ -z "$hidden" ] ||
    die "untracked files under fixtures/ are hidden from git by an excludes file outside the repository: ${hidden% }; the commit plus a diff could not say what runs. Track, remove or unhide them"
  [ -z "$ignores" ] ||
    die "the ignore rules are not the commit's: ${ignores% }; an untracked file they hide would be in no listing, and the commit plus a diff would not say what runs. Commit, revert or remove the .gitignore change"
  [ -z "$flagged" ] ||
    die "git is told to skip ${flagged% }(assume-unchanged or skip-worktree); a change there is in no status and no diff, and the commit plus a diff would not say what runs. Clear the flag (git update-index --no-assume-unchanged, --no-skip-worktree)"
  [ -z "$gitlinks" ] ||
    die "${gitlinks% } is a gitlink (a submodule, or a repository staged as one); the diff would carry a commit hash for it and not the bytes under it, and the commit plus a diff would not say what runs. Remove it from the index"
  [ -z "$filtered" ] ||
    die "a filter, text, eol, ident or working-tree-encoding attribute from git's attributes applies to ${filtered% }; git would compare the attribute's output, not the bytes that run. Remove the attribute"
  [ -z "$linked" ] ||
    die "$("$name_fn" "$linked") is a symlink, a special file, an empty directory or a nested repository under fixtures/; git records nothing of it, or a commit hash at most, and the commit plus a diff would not say what runs. Replace or remove it"
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
# <project>_<key>, as Compose derives them. Read from the file (the recorded copy, once there is
# one), not repeated here. The secrets file may not exist yet, so the password is only
# interpolated.
compose_volume_names() {
  local keys key compose_file compose_env
  compose_inputs
  # Into a variable first: a failed `config` inside a `for` word list would be an empty list and
  # a clean exit, and an empty list is not what compose.yaml declares.
  keys=$(BW_POSTGRES_PASSWORD=${BW_POSTGRES_PASSWORD:-unused} docker compose --project-name "$FIXTURE_NAME" \
    --project-directory "$FIXTURES" --file "$compose_file" --env-file "$compose_env" \
    config --volumes) || return 1
  [ -n "$keys" ] || return 1
  for key in $keys; do
    printf '%s_%s\n' "$FIXTURE_NAME" "$key"
  done
}

# tool_verify <name> <sha256>: the cached tool is a regular file whose digest is the one the
# manifest pins. The cache is writable, and fetch checked the download once: a binary replaced or
# damaged since would be run as the pinned one, and could report the pinned version, while every
# bundle cites the manifest. Checked by every command before its first use of the tool (a tenth
# of a second for talosctl), by evidence for all four, and by up once they are installed.
tool_verify() {
  local digest
  [ -f "$CACHE/$1" ] && [ ! -L "$CACHE/$1" ] || die "$CACHE/$1 is not a regular file; run fixtures/bin/down --purge, then up"
  digest=$(sha256sum <"$CACHE/$1") || die "cannot read $CACHE/$1 to check it against the manifest"
  [ "${digest%% *}" = "$2" ] ||
    die "$CACHE/$1 is not the pinned $1 (sha256 ${digest%% *}, the manifest pins $2); run fixtures/bin/down --purge, then up"
}
talosctl_verified=0
tools_verify() {
  tool_verify talosctl "$TALOSCTL_SHA256"
  talosctl_verified=1
  tool_verify sops "$SOPS_SHA256"
  tool_verify age "$AGE_BINARY_SHA256"
  tool_verify age-keygen "$AGE_KEYGEN_SHA256"
}
talosctl() {
  if [ "$talosctl_verified" -eq 0 ]; then
    tool_verify talosctl "$TALOSCTL_SHA256"
    talosctl_verified=1
  fi
  "$CACHE/talosctl" "$@"
}
# talosctl_timed <seconds> <args...>: the same, bounded. timeout runs a program, not a function, so
# a bounded call names the binary itself; the check goes before it here, so that no call site runs
# the cached file unchecked.
talosctl_timed() {
  if [ "$talosctl_verified" -eq 0 ]; then
    tool_verify talosctl "$TALOSCTL_SHA256"
    talosctl_verified=1
  fi
  timeout "$1" "$CACHE/talosctl" "${@:2}"
}

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

# The server is given the same limit, a second less: timeout ends the docker client, and the
# process inside the container runs on without it, so a rename waiting on a lock would still commit
# once the client was given up on, after the log had said the action failed. With the statement and
# lock timeouts set for the session, the server cancels the statement itself, before the client is
# given up on. pg_dump and pg_restore set both to zero for their own session, and are not bounded
# this way: a dump that runs on only reads, and a restore that runs on fills the scratch database
# the next db-restore drops.
pg_options() { printf -- '-c statement_timeout=%ds -c lock_timeout=%ds' "$((PG_LIMIT > 1 ? PG_LIMIT - 1 : 1))" "$((PG_LIMIT > 1 ? PG_LIMIT - 1 : 1))"; }
pg() {
  timeout "$PG_LIMIT" docker exec -e PGPASSWORD="$BW_POSTGRES_PASSWORD" -e PGOPTIONS="$(pg_options)" "$PG" "$@"
}

# pg_client <psql args>: psql from a throwaway container on the fixture network. The image trusts
# every connection that starts inside the server's own container, so pg() proves nothing about the
# password; only a connection from another host is asked for it.
pg_client() {
  PGPASSWORD=$BW_POSTGRES_PASSWORD PGOPTIONS=$(pg_options) timeout "$PG_LIMIT" docker run --rm --network "${FIXTURE_NAME}_default" \
    --env PGPASSWORD --env PGOPTIONS "$POSTGRES_IMAGE" psql --host=postgres --username=bronzeward --dbname=bronzeward "$@"
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
  tools_verify
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
