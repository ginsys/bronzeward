#!/usr/bin/env bash
# What a browser-auth leg uploads (ginsys/bronzeward#782).
#   browser-artifact.sh scan <status file> <listing dir> <scan.ts arguments>...
#       runs web/e2e/scan.ts on the artifact directory (the first scan argument) and writes the
#       status file from its own exit status, then exits with that status:
#         passed    the scan cleared the directory
#         listed    it did not, and <listing dir> holds files.txt, the type, size and path of every
#                   file the run left, which the same scan cleared too
#         withheld  it did not, and the listing holds a flagged value as well: nothing is kept
#   browser-artifact.sh publish <out dir> <status file> <listing dir>
#       prints `path=<dir>`, the one directory the workflow uploads, in the form $GITHUB_OUTPUT takes:
#       <out dir> after `passed`, <listing dir> after `listed`, and an empty directory in any other
#       state (the task died before the scan, no status file)
# Nothing reaches the upload that the scan did not clear.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
scan() { node "$here/../../web/e2e/scan.ts" "$@"; }
case ${1:-} in
  scan)
    status=${2:?browser-artifact.sh scan needs the status file}
    listing=${3:?browser-artifact.sh scan needs the listing directory}
    shift 3
    out=${1:?browser-artifact.sh scan needs the artifact directory}
    rm -rf "$status" "$listing"
    mkdir -p "$(dirname "$status")"
    if scan "$@"; then
      echo passed >"$status"
      exit 0
    else
      rc=$?
    fi
    mkdir -p "$listing"
    # find quotes the path of an entry it cannot read, and that name may hold a flagged value, so its
    # diagnostics are dropped and a listing that did not complete is withheld. The listing is made with
    # POSIX tools, as GNU find's -printf is missing from the BSD find of a macOS runner (ginsys/bronzeward#930):
    # the type is f (a regular file, with its size), l (a symbolic link) or o (any other kind), and only a
    # file has a size, which ls -dn reads from stat (field 5), so a file the user cannot read is still listed
    # (ginsys/bronzeward#936): a link and the others list 0, where GNU's %s gives a link its target's path
    # length and GNU's %y has one letter for each of p, s, c and b.
    names=
    complete=1
    if [ -d "$out" ]; then
      names=$(cd "$out" 2>/dev/null && find . -mindepth 1 ! -type d -exec sh -c '
        for p; do
          if [ -L "$p" ]; then t=l; n=0
          elif [ -f "$p" ]; then t=f; l=$(ls -dn -- "$p") || exit 1; n=$(printf "%s\n" "$l" | awk "{print \$5}")
          else t=o; n=0; fi
          printf "%s %s %s\n" "$t" "$((n + 0))" "${p#./}"
        done' sh {} + 2>/dev/null | LC_ALL=C sort) || complete=
    fi
    {
      echo "withheld: the artifact scan did not pass, so this lists names and sizes only (type size path)"
      if [ -n "$names" ]; then printf '%s\n' "$names"; fi
    } >"$listing/files.txt"
    if [ -n "$complete" ] && scan "$listing" "${@:2}" >/dev/null 2>&1; then
      echo listed >"$status"
    else
      rm -rf "$listing"
      echo withheld >"$status"
      echo "browser-artifact: the listing of the run's files holds a flagged value too, so nothing is uploaded" >&2
    fi
    exit "$rc"
    ;;
  publish)
    out=${2:?browser-artifact.sh publish needs the artifact directory}
    status=${3:?browser-artifact.sh publish needs the status file}
    listing=${4:?browser-artifact.sh publish needs the listing directory}
    state=
    [ ! -f "$status" ] || state=$(cat "$status")
    case $state in
      passed) printf 'path=%s\n' "$out" ;;
      listed) printf 'path=%s\n' "$listing" ;;
      *)
        rm -rf "$listing"
        mkdir -p "$listing"
        printf 'path=%s\n' "$listing"
        ;;
    esac
    ;;
  *)
    echo "usage: browser-artifact.sh scan <status file> <listing dir> <scan.ts arguments>... | publish <out dir> <status file> <listing dir>" >&2
    exit 2
    ;;
esac
