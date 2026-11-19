#!/usr/bin/env bash
# PostgreSQL, OpenBao and the test identity provider, run natively on a GitHub-hosted macOS arm64
# runner, which has no Docker (ginsys/bronzeward#892, ginsys/bronzeward#262). They stand where the
# Docker fixture's stand, so that `mise run browser-auth` runs unchanged: PostgreSQL on
# 127.0.0.1:11432 (role bronzeward, trust auth), OpenBao on 127.0.0.1:11200 (root token
# bw-dev-root-token), the identity provider on localhost:11080 (fixtures/dev-idp, BW_DEV_RUNTIME=native).
# Usage: scripts/dev/native-services.sh up | down
#   OpenBao and Zitadel are the vendors' release binaries at the fixtures/versions.env pins, checked
#   against their sha256; PostgreSQL 17 is Homebrew's (brew cannot pin a patch release, so the run
#   logs the version it got). Zitadel has only a port setting, so it listens on every interface of the
#   runner VM, which has no inbound route; OpenBao and PostgreSQL listen on 127.0.0.1.
# It installs software and starts trust-auth services, so it refuses anywhere but a GitHub Actions
# macOS arm64 runner: `uname` alone is true on every developer's Apple-silicon Mac.
#   scripts/dev/native-services.sh guard <system> <machine> <GITHUB_ACTIONS value>   (the guard alone)
set -euo pipefail

# guard SYSTEM MACHINE ACTIONS: the host may run this script. Arguments, not uname, so that a test
# can drive every case on any host.
guard() {
  if [ "$1" != Darwin ] || [ "$2" != arm64 ]; then
    echo "native-services: runs on a macOS arm64 runner only, not $1 $2" >&2
    return 1
  fi
  if [ "$3" != true ]; then
    echo "native-services: refusing to install and start services outside GitHub Actions: GITHUB_ACTIONS is '$3', not 'true'" >&2
    return 1
  fi
}

die() {
  echo "native-services: $*" >&2
  exit 1
}

up() {
  cd "$(dirname "$0")/../.."
  local pin_file=fixtures/versions.env state=$PWD/.dev/native pgport=11432 baoport=11200
  pin() { sed -n "s/^$1=//p" "$pin_file"; }
  rm -rf -- "$state"
  mkdir -p "$state"

  # fetch NAME: the vendor archive pinned as <NAME>_URL and <NAME>_SHA256, checked before it is
  # unpacked; prints the directory it was unpacked into.
  fetch() {
    local url sha dir
    url=$(pin "$1_URL") sha=$(pin "$1_SHA256")
    [ -n "$url" ] && [ -n "$sha" ] || die "no $1_URL and $1_SHA256 in $pin_file"
    dir=$state/$(printf %s "$1" | tr '[:upper:]' '[:lower:]')
    mkdir -p "$dir"
    curl -fsSL --retry 3 -o "$dir.tar.gz" "$url"
    printf '%s  %s\n' "$sha" "$dir.tar.gz" | shasum -a 256 -c - >&2 || die "$1: the download does not match the pinned sha256"
    tar -xzf "$dir.tar.gz" -C "$dir"
    printf %s "$dir"
  }
  # binary DIR NAME: the executable file NAME somewhere under DIR.
  binary() {
    local f
    f=$(find "$1" -type f -name "$2" | head -n 1)
    [ -n "$f" ] || die "no $2 in $1"
    chmod +x "$f"
    printf %s "$f"
  }

  # PostgreSQL 17: keg-only, so its tools are not on PATH. Later steps of the job find them through
  # GITHUB_PATH, and this script through the PATH it sets here.
  HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 brew install postgresql@17 >&2
  local pgbin
  pgbin=$(brew --prefix postgresql@17)/bin
  [ -x "$pgbin/initdb" ] || die "no initdb under $pgbin"
  export PATH="$pgbin:$PATH"
  [ -z "${GITHUB_PATH:-}" ] || echo "$pgbin" >>"$GITHUB_PATH"
  "$pgbin/postgres" --version >&2
  initdb -D "$state/pgdata" -U postgres --auth=trust -E UTF8 >"$state/initdb.log" 2>&1 || { tail -n 30 "$state/initdb.log" >&2; die "initdb failed"; }
  # max_locks_per_transaction as dev-db sets it: each DB test migrates a fresh database in one
  # transaction, and the lock table runs out at the default 64.
  pg_ctl -D "$state/pgdata" -l "$state/pg.log" -w -o "-p $pgport -h 127.0.0.1 -c max_locks_per_transaction=256" start >&2 ||
    { tail -n 30 "$state/pg.log" >&2; die "PostgreSQL did not start"; }
  # The Linux role is POSTGRES_USER, a superuser; the cases create and drop databases as it.
  createuser -h 127.0.0.1 -p "$pgport" -U postgres -s bronzeward
  createdb -h 127.0.0.1 -p "$pgport" -U postgres -O bronzeward bronzeward

  local bao
  bao=$(binary "$(fetch OPENBAO_DARWIN_ARM64)" bao)
  "$bao" version >&2
  nohup "$bao" server -dev -dev-root-token-id=bw-dev-root-token -dev-listen-address="127.0.0.1:$baoport" \
    >"$state/bao.log" 2>&1 </dev/null &
  echo $! >"$state/bao.pid"
  local ready=""
  for _ in $(seq 30); do
    if BAO_ADDR="http://127.0.0.1:$baoport" "$bao" status >/dev/null 2>&1; then ready=1 && break; fi
    sleep 1
  done
  [ -n "$ready" ] || { tail -n 30 "$state/bao.log" >&2; die "OpenBao was not unsealed within 30s"; }

  local zitadel
  zitadel=$(binary "$(fetch ZITADEL_DARWIN_ARM64)" zitadel)
  "$zitadel" --version >&2 || true
  BW_DEV_RUNTIME=native BW_NATIVE_ZITADEL=$zitadel BW_DEV_PG_PORT=$pgport fixtures/dev-idp up
}

down() {
  cd "$(dirname "$0")/../.."
  local state=$PWD/.dev/native
  BW_DEV_RUNTIME=native fixtures/dev-idp down || true
  if [ -f "$state/bao.pid" ]; then kill "$(cat "$state/bao.pid")" 2>/dev/null || true; fi
  if [ -d "$state/pgdata" ]; then pg_ctl -D "$state/pgdata" -m immediate stop >/dev/null 2>&1 || true; fi
  rm -rf -- "$state"
}

case ${1:-} in
guard)
  [ "$#" -eq 4 ] || die "usage: native-services.sh guard <system> <machine> <GITHUB_ACTIONS value>"
  guard "$2" "$3" "$4"
  ;;
up)
  guard "$(uname -s)" "$(uname -m)" "${GITHUB_ACTIONS:-}"
  up
  ;;
down)
  guard "$(uname -s)" "$(uname -m)" "${GITHUB_ACTIONS:-}"
  down
  ;;
*)
  echo "usage: native-services.sh up | down" >&2
  exit 2
  ;;
esac
