#!/usr/bin/env bash
# shellcheck disable=SC2034 # the E2SP_* paths set here are read by the scripts that source this file
# Shared setup for the sensitivity-provenance run. Sourced by the scripts beside it, never run on its
# own.
#
# It reuses fixtures/lib.sh for the pinned talosctl (fetched and checked against the digest in
# fixtures/versions.env, run only through the wrapper that checks it) and for the fixture state the
# fixture base is read from. Composition, validation and every redaction are offline.
#
# Phase-0 evidence for the sensitivity and provenance experiment (ginsys/bronzeward issue 4). Not
# v1 tooling.

E2SP_RUN_DIR=$(CDPATH='' cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
E2SP_DIR=$(dirname -- "$E2SP_RUN_DIR")
REPO_DIR=$(CDPATH='' cd -P -- "$E2SP_DIR/../.." && pwd -P)
# Issue 3's resolver and cases, reused unchanged: bwref is built from its directory, and a case here
# that names `from:` takes that case's fragments.
E2SP_BWREF_DIR=$REPO_DIR/experiments/e2-structural-references

# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$REPO_DIR/fixtures/lib.sh"

# The output directory is the operator's to choose. It holds both bases' secrets bundles, every
# unredacted artifact and the synthetic values, so it must be outside the checkout, where a careless
# `git add` would commit them. It is compared canonical (symlinks and `..` resolved, a missing tail
# allowed) and at a path boundary, so neither `<repo>/../<repo>/x` nor a symlink into the checkout
# passes, and `<repo>-other` is not mistaken for the checkout.
[ -n "${E2SP_OUT:-}" ] || die "set E2SP_OUT to an empty directory outside this checkout"
case $E2SP_OUT in
  /*) ;;
  *) die "E2SP_OUT must be an absolute path" ;;
esac
E2SP_OUT=$(realpath -m -- "$E2SP_OUT") || die "cannot canonicalize E2SP_OUT"
case "$E2SP_OUT/" in
  "$REPO_DIR"/*) die "E2SP_OUT is inside the checkout; the secrets under it must not be one git add away" ;;
esac

E2SP_BIN=$E2SP_OUT/bin
# The marker run/all writes first. collect-evidence refuses a directory without it (another
# investigation's output, or none), and run/all refuses one that has anything in it, so evidence
# from two runs cannot mix.
E2SP_MARK=$E2SP_OUT/e2-sensitivity-provenance.run

e2sp_talosctl_ready() {
  if [ ! -f "$CACHE/talosctl" ]; then
    local file
    file=$(fetch talosctl "$TALOSCTL_URL" "$TALOSCTL_SHA256")
    install -m 0755 "$file" "$CACHE/talosctl"
  fi
  tool_verify talosctl "$TALOSCTL_SHA256"
  talosctl_verified=1
}

# e2sp_build: issue 3's resolver and this experiment's tracker, built once per run from this checkout.
e2sp_build() {
  mkdir -p -- "$E2SP_BIN"
  go -C "$E2SP_BWREF_DIR" build -o "$E2SP_BIN/bwref" . || die "cannot build bwref"
  go -C "$E2SP_DIR" build -o "$E2SP_BIN/bwprov" . || die "cannot build bwprov"
}

bwref() { "$E2SP_BIN/bwref" "$@"; }
bwprov() { "$E2SP_BIN/bwprov" "$@"; }
