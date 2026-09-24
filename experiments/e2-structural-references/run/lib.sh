#!/usr/bin/env bash
# shellcheck disable=SC2034 # the E2_* paths set here are read by the scripts that source this file
# Shared setup for the E2 run. Sourced by the scripts beside it, never run on its own.
#
# It reuses fixtures/lib.sh for the one thing E2 needs from it: the pinned talosctl, fetched and
# checked against the digest in fixtures/versions.env, and run only through the wrapper that checks
# it. The composition and validation E2 measures are offline talosctl commands; run/all reads the
# running fixture's .state only for one of its two bases. Loading the file needs the docker CLI.
#
# Phase-0 evidence for the structural-reference experiment (ginsys/bronzeward issue 3). Not v1 tooling.

E2_RUN_DIR=$(CDPATH='' cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
E2_DIR=$(dirname -- "$E2_RUN_DIR")
REPO_DIR=$(CDPATH='' cd -P -- "$E2_DIR/../.." && pwd -P)

# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$REPO_DIR/fixtures/lib.sh"

# The output directory is the operator's to choose. It holds the generated base configuration with
# its synthetic secrets bundle, so it must not be inside the checkout, where a careless `git add`
# would commit them; only run/collect-evidence copies anything back, and it refuses those secrets.
[ -n "${E2_OUT:-}" ] || die "set E2_OUT to an empty directory outside this checkout"
case $E2_OUT in
  /*) ;;
  *) die "E2_OUT must be an absolute path" ;;
esac
[ "${E2_OUT#"$REPO_DIR"/}" = "$E2_OUT" ] && [ "$E2_OUT" != "$REPO_DIR" ] ||
  die "E2_OUT is inside the checkout; the synthetic secrets under it must not be one git add away"

E2_BIN=$E2_OUT/bin/bwref
E2_MATRIX=$E2_OUT/matrix.tsv
E2_PATTERNS=$E2_OUT/patterns.txt
# The marker run/all writes first; collect-evidence refuses an E2_OUT without it, and run/all
# refuses one that has anything in it, so evidence from two runs cannot mix.
E2_MARK=$E2_OUT/e2-structural-references.run

e2_talosctl_ready() {
  if [ ! -f "$CACHE/talosctl" ]; then
    local file
    file=$(fetch talosctl "$TALOSCTL_URL" "$TALOSCTL_SHA256")
    install -m 0755 "$file" "$CACHE/talosctl"
  fi
  tool_verify talosctl "$TALOSCTL_SHA256"
  talosctl_verified=1
}

# e2_build: the prototype, built once per run, from this checkout.
e2_build() {
  mkdir -p -- "$E2_OUT/bin"
  go -C "$E2_DIR" build -o "$E2_BIN" . || die "cannot build the bwref prototype"
}

bwref() { "$E2_BIN" "$@"; }

# oneline <file>: a message as one TSV-safe line, for the matrix. The whole text stays in the file.
oneline() {
  tr '\t\n' '  ' <"$1" | sed -e 's/  */ /g' -e 's/^ //' -e 's/ $//'
}
