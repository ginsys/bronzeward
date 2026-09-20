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
  for file in bao-init.json talosconfig kubeconfig talos-secrets.yaml controlplane.yaml scan-patterns.txt injections.log down-node-containers down-node-networks down-compose-volumes; do
    [ -e "$STATE/$file" ] || [ -L "$STATE/$file" ] || continue
    if [ -L "$STATE/$file" ] || [ ! -f "$STATE/$file" ] || [ "$(stat --format=%h -- "$STATE/$file" 2>/dev/null)" != 1 ]; then
      die "$STATE/$file is not the regular file bin/up writes, with that one name; the fixture never makes anything else there"
    fi
  done
}

# containers_own: every container that answers to one of the fixture's names is the fixture's. The
# names are fixed, so once the real one was removed by hand anything can take the name while the
# claim still stands, and a kill or a restore would go to it. The Compose containers carry the
# project and its directory. The Talos nodes carry only the cluster name, which anything can be
# labelled with, so they are held to the IDs bin/up recorded once it had created them. One that is
# gone is what a kill or a teardown that could not finish leaves, and is not refused here.
containers_own() {
  local name labels id
  for name in "$PG" "$BAO"; do
    labels=$(docker inspect --format \
      '{{index .Config.Labels "com.docker.compose.project"}} {{index .Config.Labels "com.docker.compose.project.working_dir"}}' \
      "$name" 2>/dev/null) || continue
    [ "$labels" = "$FIXTURE_NAME $FIXTURES" ] ||
      die "the container named $name is not this fixture's: its labels do not name this project and checkout. Remove it by hand; the fixture will not act on it"
  done
  for name in "$CP" "$WORKER"; do
    id=$(docker inspect --format '{{.Id}}' "$name" 2>/dev/null) || continue
    [ -f "$STATE/down-node-containers" ] ||
      die "a container named $name exists but bin/up left no record of the Talos containers it created, as an up interrupted right after creating the cluster leaves it. Run fixtures/bin/down --adopt, then up"
    grep --quiet --line-regexp --fixed-strings -- "$id" "$STATE/down-node-containers" ||
      die "the container named $name is not this fixture's: it is not one bin/up created. Remove it by hand; the fixture will not act on it"
  done
}

# The project name is forced: a COMPOSE_PROJECT_NAME in the caller's shell wins over the `name` in
# compose.yaml, and every ownership check and teardown probe selects by that name's label.
compose() {
  docker compose --project-name "$FIXTURE_NAME" --project-directory "$FIXTURES" \
    --file "$FIXTURES/compose.yaml" \
    --env-file "$FIXTURES/versions.env" --env-file "$STATE/secrets.env" "$@"
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

# claim_take: fails when any checkout on this daemon already holds the claim.
claim_take() {
  docker create --quiet --name "$CLAIM" --label "$CLAIM_LABEL" --label "$CLAIM_OWNER_LABEL=$FIXTURES" \
    --network none "$POSTGRES_IMAGE" true >/dev/null
}
# claim_names: the claim container, if any (its name, or nothing).
claim_names() { docker ps --all --filter "label=$CLAIM_LABEL" --format '{{.Names}}'; }
# claim_owner: the checkout that took the claim (nothing when there is no claim).
claim_owner() {
  docker ps --all --quiet --filter "label=$CLAIM_LABEL" |
    xargs --no-run-if-empty docker inspect --format "{{index .Config.Labels \"$CLAIM_OWNER_LABEL\"}}"
}
# claim_drop: --volumes, because the image declares a data volume and Docker creates an anonymous
# one for the claim although it never starts. Nothing else would ever find that volume again.
claim_drop() {
  docker ps --all --quiet --filter "label=$CLAIM_LABEL" |
    xargs --no-run-if-empty docker rm --force --volumes >/dev/null
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
