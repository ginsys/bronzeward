#!/usr/bin/env bash
# fixtures/scenarios/s5 cli mode (#360, #778): the two checks that read bronzewardctl's kept output decide on
# its text alone. cli_unseen wants the unseen-overwrite warning as one whole line, and unfreeze_refused_cli
# wants exit status 1 and the approver-role refusal. The real functions are cut out of the scenario and run
# through common.sh's own ctl and cli_kept against a stub bronzewardctl that prints a chosen text and exits
# with a chosen status. No fixture, no Docker, no server: the database answer is a variable.
#
# Two proofs. The cases (unseen_cases, refusal_cases) change the sentence: every word replaced or deleted,
# every mark removed or swapped, every gap closed or filled, and the scenario must refuse each one. The
# pattern mutants change the scenario: each pattern is read out of the cut function, one relaxed copy is
# made per word, mark and gap, and the cases of that function must refuse a text the mutant accepts. The
# mutants relax one place of one pattern each: a change to anything else in the two functions (the exit
# status test, the database test, the call to ctl_rm) is not generated here.
set -uo pipefail
repo_src=$(CDPATH='' cd -- "$(dirname "$0")/../.." && pwd)
t=$(mktemp -d)
trap 'rm -rf -- "$t"' EXIT
failed=0

cat >"$t/bronzewardctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$STUB_TEXT"
exit "${STUB_RC:-0}"
EOF
chmod 0755 "$t/bronzewardctl"

# common.sh reads A's port, the state directory and the scenario's name when it is sourced.
export SERVER_A_PORT=1 STATE=$t/state
export scenario=s5-cli-test
# shellcheck source=/dev/null
. "$repo_src/fixtures/scenarios/common.sh"

# The functions under test, cut from the scenario by name. A name that is not cut fails the run at once.
s5=$repo_src/fixtures/scenarios/s5
ctl_rm_text=$(sed -n '/^ctl_rm() {/,/^}/p' "$s5")
unseen_text=$(sed -n '/^cli_unseen() {/,/^}/p' "$s5")
refusal_text=$(sed -n '/^unfreeze_refused_cli() {/,/^}/p' "$s5")
eval "$ctl_rm_text"
eval "$unseen_text"
eval "$refusal_text"
for fn in ctl_rm cli_unseen unfreeze_refused_cli; do
  declare -F "$fn" >/dev/null || { echo "FAIL $fn was not found in fixtures/scenarios/s5"; exit 1; }
done

# What the functions use from the scenario: the run's directories, a worker, and a token and a database
# that answer without a fixture.
# shellcheck disable=SC2034
cli_dir=$t/cli
# shellcheck disable=SC2034
ctl_bin=$t/bronzewardctl
# shellcheck disable=SC2034
EV=$t/ev
# shellcheck disable=SC2034
machine_w=m_worker
mkdir -p "$cli_dir" "$EV"
mint() { printf 'synthetic-token'; }
keep_secret() { :; }
sql() { printf '%s' "$SQL_ANSWER"; }

warning='It overwrites the drifted configuration, which no reviewer has seen: this approval records that you approve overwriting it.'
role='Needs one of the roles [approver].'
read -r -a words <<<"$warning"
read -r -a role_words <<<"$role"
export STUB_TEXT STUB_RC SQL_ANSWER

# run <want> <name> <stub text> <stub exit> <database answer> <function> <args>...
# quiet=1 prints nothing and counts: accepted_bad is a case that wanted a refusal and passed, rejected_good
# a case that wanted a pass and failed.
quiet=0
accepted_bad=0
rejected_good=0
run() {
  local want=$1 name=$2 rc=0
  STUB_TEXT=$3 STUB_RC=$4 SQL_ANSWER=$5
  shift 5
  # ctl keeps the output as cli-<name>.txt, which cli_unseen reads under the same name.
  case $1 in
    cli_unseen) ctl_rm "approve-$3" h-approver approve "$3" >/dev/null 2>&1 || true ;;
  esac
  "$@" >/dev/null 2>&1 || rc=$?
  if { [ "$want" = pass ] && [ "$rc" -eq 0 ]; } || { [ "$want" = fail ] && [ "$rc" -ne 0 ]; }; then
    [ "$quiet" -eq 1 ] || echo "ok   $name"
  else
    [ "$want" = fail ] && accepted_bad=$((accepted_bad + 1))
    [ "$want" = pass ] && rejected_good=$((rejected_good + 1))
    [ "$quiet" -eq 1 ] || { echo "FAIL $name: want $want, the function exited $rc"; failed=$((failed + 1)); }
  fi
}

# The cases of cli_unseen. Every word of the sentence is a clause the scenario's match must hold: a match that
# stops checking one word passes the first cases and accepts a sentence that says something else. So each word
# is replaced and deleted, each mark is removed and the final period swapped, and a negation is inserted and a
# space closed at each gap, and the scenario must refuse each one.
unseen_cases() {
  local -a alt
  local i w core punct
  run pass "cli_unseen accepts the warning as a whole line, with the approval entry marked" \
    "$warning" 0 1 cli_unseen plan_x s
  run pass "cli_unseen accepts the same line when the table's padding surrounds it" \
    "  $warning  " 0 1 cli_unseen plan_x s
  run fail "cli_unseen refuses a warning that sits inside a longer line" \
    "Note: $warning" 0 1 cli_unseen plan_x s
  run fail "cli_unseen refuses a warning that is followed by more text on its line" \
    "$warning NOT APPROVED" 0 1 cli_unseen plan_x s
  run fail "cli_unseen refuses the whole line when the approval entry is not marked" \
    "$warning" 0 0 cli_unseen plan_x s
  run fail "cli_unseen refuses an approval entry counted twice" \
    "$warning" 0 2 cli_unseen plan_x s
  run fail "cli_unseen refuses a warning with other wording" \
    "It overwrites the drifted configuration, which one reviewer has seen: this approval records that you approve overwriting it." 0 1 cli_unseen plan_x s
  run fail "cli_unseen refuses an approval output without the warning" \
    "Plan plan_x approved." 0 1 cli_unseen plan_x s
  for i in "${!words[@]}"; do
    w=${words[$i]}
    core=${w%[,:.]}
    punct=${w#"$core"}
    alt=("${words[@]}")
    alt[i]="XX$punct"
    run fail "cli_unseen refuses the warning with the word '$core' replaced" \
      "${alt[*]}" 0 1 cli_unseen plan_x s
    # The word goes and its mark stays, as a pattern that makes only the word optional would accept it.
    alt=("${words[@]:0:i}" ${punct:+"$punct"} "${words[@]:i+1}")
    run fail "cli_unseen refuses the warning with the word '$core' deleted" \
      "${alt[*]}" 0 1 cli_unseen plan_x s
    if [ -n "$punct" ]; then
      alt=("${words[@]}")
      alt[i]=$core
      run fail "cli_unseen refuses the warning without the '$punct' after '$core'" \
        "${alt[*]}" 0 1 cli_unseen plan_x s
    fi
  done
  alt=("${words[@]}")
  alt[${#alt[@]} - 1]="${words[${#words[@]} - 1]%.}!"
  run fail "cli_unseen refuses the warning with its final period replaced by '!'" \
    "${alt[*]}" 0 1 cli_unseen plan_x s
  for ((i = 1; i < ${#words[@]}; i++)); do
    alt=("${words[@]:0:i}" not "${words[@]:i}")
    run fail "cli_unseen refuses the warning with 'not' inserted before '${words[$i]}'" \
      "${alt[*]}" 0 1 cli_unseen plan_x s
  done
  for ((i = 0; i < ${#words[@]} - 1; i++)); do
    alt=("${words[@]:0:i}" "${words[i]}${words[i + 1]}" "${words[@]:i+2}")
    run fail "cli_unseen refuses the warning with no space between '${words[i]}' and '${words[i + 1]}'" \
      "${alt[*]}" 0 1 cli_unseen plan_x s
  done
}

# The cases of unfreeze_refused_cli, shaped the same way. The refusal's pattern has no anchor, since the CLI
# may follow the sentence with its signed-in suffix, so a gap or an end gets no insertion: only the gaps
# inside the sentence do.
refusal_cases() {
  local -a alt
  local i w core punct
  run pass "unfreeze_refused_cli accepts exit 1 with the approver-role refusal and a frozen scope" \
    "$role" 1 t unfreeze_refused_cli h-viewer
  run pass "unfreeze_refused_cli accepts the refusal as the CLI prints it, with the signed-in suffix on its line" \
    "$role You are signed in as h-viewer." 1 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses exit 2 with the same text" \
    "$role" 2 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses a command that succeeded (exit 0) with the same text" \
    "$role" 0 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses a refusal that names another role" \
    "Needs one of the roles [publisher]." 1 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses a refusal that names no approver role" \
    "Refused." 1 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses a scope that is no longer frozen" \
    "$role" 1 f unfreeze_refused_cli h-viewer
  for i in "${!role_words[@]}"; do
    w=${role_words[$i]}
    core=${w%.}
    punct=${w#"$core"}
    alt=("${role_words[@]}")
    alt[i]="XX$punct"
    run fail "unfreeze_refused_cli refuses the refusal with the word '$core' replaced" \
      "${alt[*]}" 1 t unfreeze_refused_cli h-viewer
    alt=("${role_words[@]:0:i}" ${punct:+"$punct"} "${role_words[@]:i+1}")
    run fail "unfreeze_refused_cli refuses the refusal with the word '$core' deleted" \
      "${alt[*]}" 1 t unfreeze_refused_cli h-viewer
  done
  run fail "unfreeze_refused_cli refuses the refusal with 'approver' deleted from its brackets" \
    "Needs one of the roles []." 1 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses the refusal without its final period" \
    "Needs one of the roles [approver]" 1 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses the refusal with its final period replaced by '!'" \
    "Needs one of the roles [approver]!" 1 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses the refusal without its opening bracket" \
    "Needs one of the roles approver]." 1 t unfreeze_refused_cli h-viewer
  run fail "unfreeze_refused_cli refuses the refusal without its closing bracket" \
    "Needs one of the roles [approver." 1 t unfreeze_refused_cli h-viewer
  for ((i = 1; i < ${#role_words[@]}; i++)); do
    alt=("${role_words[@]:0:i}" except "${role_words[@]:i}")
    run fail "unfreeze_refused_cli refuses the refusal with 'except' inserted before '${role_words[$i]}'" \
      "${alt[*]}" 1 t unfreeze_refused_cli h-viewer
  done
  for ((i = 0; i < ${#role_words[@]} - 1; i++)); do
    alt=("${role_words[@]:0:i}" "${role_words[i]}${role_words[i + 1]}" "${role_words[@]:i+2}")
    run fail "unfreeze_refused_cli refuses the refusal with no space between '${role_words[i]}' and '${role_words[i + 1]}'" \
      "${alt[*]}" 1 t unfreeze_refused_cli h-viewer
  done
}

echo "-- the cases against the real scenario"
unseen_cases
refusal_cases

# The sentence above is the CLI's own, so the cases that change it start from the real text.
if grep -qF -- "\"$warning\"" "$repo_src/cmd/bronzewardctl/approvals.go"; then
  echo "ok   the warning in this test is the sentence cmd/bronzewardctl/approvals.go prints"
else
  echo "FAIL the warning in this test is not the sentence cmd/bronzewardctl/approvals.go prints"
  failed=$((failed + 1))
fi

# pattern_of <function text>: the quoted pattern its cli_kept call holds.
pattern_of() {
  local re="cli_kept \"[^\"]*\" '([^']*)'"
  [[ $1 =~ $re ]] && printf '%s' "${BASH_REMATCH[1]}"
}

# gen_mutants <pattern>: fills mut_pat[] with the pattern relaxed at one place, and mut_why[] with where.
# The pattern is read as tokens: a gap ([[:space:]]+ or one space), the padding ([[:space:]]*), an escaped
# character, a word, or any other single character (an anchor, a comma, a colon). A word is made optional and
# made any letters, a gap is made optional, a mark is made optional, and an escaped period is made any
# character.
# The tokens put back together must be the pattern, or the run fails: a token lost would be a place not tried.
gen_mutants() {
  local pat=$1 rest=$1 before="" tok kind gap n=0
  local re_space='^(\[\[:space:\]\][*+])' re_esc='^(\\.)' re_word='^([A-Za-z]+)' letters='[A-Za-z]+'
  mut_pat=()
  mut_why=()
  while [ -n "$rest" ]; do
    if [[ $rest =~ $re_space ]]; then
      tok=${BASH_REMATCH[1]}
      kind=gap
      [ "${tok: -1}" = '*' ] && kind=pad
    elif [[ $rest =~ $re_esc ]]; then
      tok=${BASH_REMATCH[1]}
      kind=mark
    elif [[ $rest =~ $re_word ]]; then
      tok=${BASH_REMATCH[1]}
      kind=word
    else
      tok=${rest:0:1}
      kind=other
      case $tok in
        , | :) kind=mark ;;
        ' ') kind=gap ;;
      esac
    fi
    rest=${rest#"$tok"}
    n=$((n + 1))
    case $kind in
      gap)
        if [ "$tok" = ' ' ]; then
          mut_pat+=("${before} ?${rest}")
        else
          mut_pat+=("${before}[[:space:]]*${rest}")
        fi
        mut_why+=("gap $n made optional")
        ;;
      word)
        # A word followed by a gap is made optional with that gap, as a pattern that makes only the word
        # optional keeps one gap too many and cannot accept the sentence with the word deleted.
        if [[ $rest =~ $re_space ]] && [ "${BASH_REMATCH[1]: -1}" = '+' ]; then
          gap=${BASH_REMATCH[1]}
          mut_pat+=("${before}(${tok}${gap})?${rest#"$gap"}")
        elif [[ $rest == ' '* ]]; then
          mut_pat+=("${before}(${tok} )?${rest# }")
        else
          mut_pat+=("${before}(${tok})?${rest}")
        fi
        mut_pat+=("${before}${letters}${rest}")
        mut_why+=("word '$tok' made optional" "word '$tok' made any letters")
        ;;
      mark)
        mut_pat+=("${before}${tok}?${rest}")
        mut_why+=("mark '$tok' made optional")
        if [ "$tok" = '\.' ]; then
          mut_pat+=("${before}.${rest}")
          mut_why+=("mark '$tok' made any character")
        fi
        ;;
    esac
    before+=$tok
  done
  if [ "$before" != "$pat" ]; then
    echo "FAIL the tokens of the pattern do not rebuild it: $before"
    failed=$((failed + 1))
    mut_pat=()
    mut_why=()
  fi
}

# mutate_check <function> <function text> <cases function>: each relaxed copy of the function's pattern is
# loaded in place of the real one, and the cases must refuse something it accepts (accepted_bad) while every
# case that wants a pass still passes (rejected_good, so the mutant is a relaxation that runs and not an
# error). The real function is loaded again afterwards.
mutate_check() {
  local fn=$1 text=$2 cases=$3 pat mutated i
  pat=$(pattern_of "$text")
  if [ -z "$pat" ]; then
    echo "FAIL $fn: no pattern was found in its cli_kept call"
    failed=$((failed + 1))
    return
  fi
  gen_mutants "$pat"
  echo "-- $fn: ${#mut_pat[@]} pattern mutants of $pat"
  [ "${#mut_pat[@]}" -gt 0 ] || { echo "FAIL $fn: no pattern mutant was made"; failed=$((failed + 1)); return; }
  for i in "${!mut_pat[@]}"; do
    mutated=${text/"$pat"/"${mut_pat[$i]}"}
    if [ "$mutated" = "$text" ]; then
      echo "FAIL $fn: the mutant with ${mut_why[$i]} did not change the function"
      failed=$((failed + 1))
      continue
    fi
    eval "$mutated"
    quiet=1 accepted_bad=0 rejected_good=0
    "$cases"
    quiet=0
    if [ "$accepted_bad" -gt 0 ] && [ "$rejected_good" -eq 0 ]; then
      echo "ok   $fn with ${mut_why[$i]} is caught: it accepts $accepted_bad text(s) the scenario must refuse"
    else
      echo "FAIL $fn with ${mut_why[$i]} is not caught: it accepts $accepted_bad text(s) the scenario must refuse, and refuses $rejected_good text(s) it must accept"
      failed=$((failed + 1))
    fi
  done
  eval "$text"
}

mutate_check cli_unseen "$unseen_text" unseen_cases
mutate_check unfreeze_refused_cli "$refusal_text" refusal_cases

[ "$failed" -eq 0 ] || { echo "s5-cli-test: $failed failed"; exit 1; }
echo "s5-cli-test: ok"
