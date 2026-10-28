#!/usr/bin/env bash
# The browser tasks' TMPDIR (#430): wdio runs with a directory of its own under TMPDIR, and the task
# fails when a browser profile is left in it.
#   browser-tmp.sh start <task>         makes the run's directory and prints it
#   browser-tmp.sh finish <task> <dir>  removes it; fails, naming what was left, when a profile was,
#                                       or when the directory cannot be read or removed
# Chrome and Edge bind a socket at <dir>/.org.chromium.Chromium.XXXXXX/SingletonSocket (46 bytes
# under <dir>, the longest of their names), and a Unix socket path holds at most 107 bytes: past that
# every session fails as "user data directory is already in use". So `start` refuses a TMPDIR that
# long when BW_BROWSERS (default chrome,firefox, as in web/e2e/wdio.conf.ts) names chrome or edge.
set -euo pipefail
case ${1:-}:${2:-} in
  start:?*)
    tmp=${TMPDIR:-/tmp}
    dir=$(mktemp -d "$tmp/bw.XXXXXX")
    case ,${BW_BROWSERS:-chrome,firefox}, in
      *,chrome,* | *,edge,*)
        if [ $((${#dir} + 46)) -gt 107 ]; then
          rmdir "$dir"
          printf '%s: TMPDIR %s is too long: the Chrome socket path under it would be %s bytes, over the 107 a Unix socket allows; use a TMPDIR of at most %s bytes\n' \
            "$2" "$tmp" $((${#dir} + 46)) $((61 - ${#dir} + ${#tmp})) >&2
          exit 1
        fi
        ;;
    esac
    printf '%s\n' "$dir"
    ;;
  finish:?*)
    dir=${3:?browser-tmp.sh finish needs the run directory}
    left=$(find "$dir" -mindepth 1 -maxdepth 1 \( -name '.org.chromium.Chromium.*' -o -name 'bw-browser-profiles-*' \) 2>&1) ||
      left="$left (cannot read $dir)"
    rm -rf "$dir" || left="$left (cannot remove $dir)"
    [ -z "$left" ] || { printf '%s: the run left browser profiles behind:\n%s\n' "$2" "$left" >&2; exit 1; }
    ;;
  *)
    echo "usage: browser-tmp.sh start <task> | finish <task> <dir>" >&2
    exit 2
    ;;
esac
