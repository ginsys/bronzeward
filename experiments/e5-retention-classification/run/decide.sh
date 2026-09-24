#!/usr/bin/env bash
# The classification rules, as pure functions over a provider's answer: no fixture, no network, so
# run/test-decide can exercise each rule on its own. Sourced by run/classify and run/test-decide.
#
# Every function prints one line, `class=<retained|blocked|lost|unknown> reason=<text>`, the
# states of design §7.6. The rules follow that section's two constraints:
#   - lost needs evidence of an irreversible removal in the answer itself (destroyed, a version
#     below the oldest kept, a version below the trimmed floor). An absent path, a denied read, a
#     missing listing or no answer is unknown, never lost.
#   - unknown is never promoted: the monitor below alerts on it, and does not change it.
#
# Phase-0 evidence for the retention-classification experiment (ginsys/bronzeward issue 9). Not v1
# tooling.

say_class() { printf 'class=%s reason=%s\n' "$1" "$2"; }

# version_ok <version>: a provider version is a positive integer without a leading zero. Anything
# else names no version the provider has, and would otherwise compare below a floor and read as
# lost. The answer on stdin is drained either way, so that its writer never sees a closed pipe.
version_ok() {
  [[ $1 =~ ^[1-9][0-9]*$ ]] && return 0
  cat >/dev/null
  say_class unknown "'$1' is not a version number"
  return 1
}

# decide_kv <version> <now-rfc3339>: stdin is the data object of a KV v2 metadata read
# (GET secret/metadata/<path>). Deletion times carry fractional seconds, which jq's date parser
# does not take, so they are cut to whole seconds before comparing.
decide_kv() {
  local version=$1 now=$2 out
  version_ok "$version" || return 0
  out=$(jq -r --arg v "$version" --arg now "$now" '
    def secs: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
    if (.versions | type) != "object" or (.current_version | type) != "number" or (.oldest_version | type) != "number"
    then "unknown\tmetadata has no version map"
    elif .versions[$v] != null then
      .versions[$v] as $m
      | if $m.destroyed == true then "lost\tdestroyed"
        elif ($m.deletion_time // "") == "" then "retained\tversion present, not deleted"
        elif ($m.deletion_time | secs) <= ($now | secs) then "blocked\tsoft-deleted at \($m.deletion_time | sub("\\.[0-9]+Z$"; "Z")) (reversible by undelete)"
        else "retained\tdeletion scheduled for \($m.deletion_time | sub("\\.[0-9]+Z$"; "Z"))"
        end
    elif ($v | tonumber) > .current_version then "unknown\tversion \($v) is above current_version \(.current_version)"
    elif .oldest_version > 0 and ($v | tonumber) < .oldest_version then "lost\tpruned: below oldest_version \(.oldest_version)"
    else "unknown\tversion \($v) is missing from the metadata without a recorded removal"
    end' 2>/dev/null) || out=$'unknown\tanswer could not be read as KV v2 metadata'
  say_class "${out%%$'\t'*}" "${out#*$'\t'}"
}

# decide_transit <key-version>: stdin is the data object of a Transit key read
# (GET transit/keys/<key>). The key's version list is not used: it hides versions below the
# decryption floor (ginsys/bronzeward#8 hand-off), so the floors decide. A key flagged
# soft_deleted is unknown, not blocked: that it can be restored was not observed here.
decide_transit() {
  local version=$1 out
  version_ok "$version" || return 0
  out=$(jq -r --argjson v "$version" '
    if (.soft_deleted // false) != false then "unknown\tkey soft_deleted is \(.soft_deleted | tojson) (reversibility not established)"
    elif (.latest_version | type) != "number" or (.min_decryption_version | type) != "number"
       or (.min_available_version | type) != "number"
    then "unknown\tkey metadata has no version floors"
    elif .min_available_version > 0 and $v < .min_available_version then "lost\ttrimmed: below min_available_version \(.min_available_version)"
    elif $v < .min_decryption_version then "blocked\tbelow min_decryption_version \(.min_decryption_version) (reversible)"
    elif $v > .latest_version then "unknown\tversion \($v) is above latest_version \(.latest_version)"
    else "retained\tkey version \($v) within latest \(.latest_version), decryption floor \(.min_decryption_version)"
    end' 2>/dev/null) || out=$'unknown\tanswer could not be read as Transit key metadata'
  say_class "${out%%$'\t'*}" "${out#*$'\t'}"
}

# answer_class <curl-exit> <http-code>: the class of an answer that is not metadata, or nothing
# for an HTTP 200, which the callers above decide.
answer_class() {
  local rc=$1 code=$2
  case $rc in
    0) ;;
    7) say_class unknown "unreachable: curl exit 7"; return ;;
    28) say_class unknown "no answer: curl exit 28"; return ;;
    *) say_class unknown "request failed: curl exit $rc"; return ;;
  esac
  case $code in
    200) ;;
    403) say_class unknown "denied: HTTP 403" ;;
    404) say_class unknown "absent: HTTP 404" ;;
    503) say_class unknown "sealed or unavailable: HTTP 503" ;;
    *) say_class unknown "unexpected answer: HTTP $code" ;;
  esac
}

# The monitor. A persistent unknown is alertable after a defined interval (design §7.6); the
# interval is not specified yet, so RC_ALERT_AFTER is this experiment's parameter, not a policy.
RC_ALERT_AFTER=${RC_ALERT_AFTER:-600}

# monitor <state-file> <now-epoch> <class>: record one observation of one dependency and print
# `class=<class> alert=<no|regression|persistent|blocked|lost>[ unknown_since=<epoch>]`. The class
# printed is always the class observed: time only changes the alert, never the class.
monitor() {
  local f=$1 now=$2 class=$3 seen_retained=no since='' alert=no
  if [ -f "$f" ]; then
    seen_retained=$(sed -n 's/^seen_retained=//p' "$f")
    since=$(sed -n 's/^unknown_since=//p' "$f")
  fi
  case $class in
    retained) seen_retained=yes since='' ;;
    blocked | lost) alert=$class since='' ;;
    unknown)
      [ -n "$since" ] || since=$now
      if [ $((now - since)) -ge "$RC_ALERT_AFTER" ]; then
        alert=persistent
      elif [ "$seen_retained" = yes ]; then
        alert=regression
      fi
      ;;
    *) die "monitor: no such class '$class'" ;;
  esac
  printf 'seen_retained=%s\nunknown_since=%s\n' "$seen_retained" "$since" >"$f"
  printf 'class=%s alert=%s%s\n' "$class" "$alert" "${since:+ unknown_since=$since}"
}
