#!/usr/bin/env bash
# Loads the completion script of bronzewardctl in bash, zsh and fish, each in a container, and completes a
# command, a verb and a flag. Needs docker and the network once, to build the image of the three shells.
#   BW_COMPLETION_IMAGE   the image tag (default bronzeward-completion-shells)
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
image=${BW_COMPLETION_IMAGE:-bronzeward-completion-shells}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

docker build -q -t "$image" -f "$root/scripts/test/completion-shells.Dockerfile" "$root/scripts/test" >/dev/null
(cd "$root" && CGO_ENABLED=0 GOOS=linux go build -o "$work/bronzewardctl" ./cmd/bronzewardctl)

# complete_in <shell> <the words before the one being completed> <that word>: the candidates, one per line.
complete_in() {
  local shell=$1 prior=$2 cur=$3
  docker run --rm -v "$work/bronzewardctl:/usr/local/bin/bronzewardctl:ro" "$image" "${shell%-fpath}" -c "$(body "$shell")" _ "$prior" "$cur"
}

body() {
  case $1 in
    bash) cat <<'EOF'
source <(bronzewardctl completion bash)
COMP_WORDS=(bronzewardctl $1 "$2")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
_bronzewardctl
printf '%s\n' "${COMPREPLY[@]}"
EOF
      ;;
    zsh-fpath) cat <<'EOF'
# installed as $fpath/_bronzewardctl, the documented way: the first completion autoloads the file
mkdir /fp && bronzewardctl completion zsh >/fp/_bronzewardctl
fpath=(/fp $fpath)
autoload -Uz compinit && compinit -u
[[ ${_comps[bronzewardctl]} == _bronzewardctl ]] || { echo "not registered by compinit" >&2; exit 1; }
compadd() { local m; shift; for m in "$@"; do [[ $m == ${PREFIX}* ]] && print -r -- $m; done }
words=(bronzewardctl ${=1} "$2")
CURRENT=$#words
PREFIX=$2
_bronzewardctl
true # the stand-in compadd ends on the status of its last comparison
EOF
      ;;
    zsh) cat <<'EOF'
autoload -Uz compinit && compinit -u
source <(bronzewardctl completion zsh)
[[ ${_comps[bronzewardctl]} == _bronzewardctl ]] || { echo "not registered with compdef" >&2; exit 1; }
compadd() { local m; shift; for m in "$@"; do [[ $m == ${PREFIX}* ]] && print -r -- $m; done }
words=(bronzewardctl ${=1} "$2")
CURRENT=$#words
PREFIX=$2
_bronzewardctl
true # the stand-in compadd ends on the status of its last comparison
EOF
      ;;
    fish) cat <<'EOF'
bronzewardctl completion fish | source
complete -C "bronzewardctl $argv[2] $argv[3]" | cut -f1
EOF
      ;;
  esac
}

failed=0
# check <shell> <prior> <cur> <must be offered> [<must not be offered>]
check() {
  local got
  got=$(complete_in "$1" "$2" "$3")
  if ! grep -qxF -- "$4" <<<"$got"; then
    echo "FAIL $1: '$2 $3' does not offer $4; offered: $(tr '\n' ' ' <<<"$got")"
    failed=1
  elif [ -n "${5:-}" ] && grep -qxF -- "$5" <<<"$got"; then
    echo "FAIL $1: '$2 $3' offers $5"
    failed=1
  else
    echo "ok   $1: '$2 $3' offers $4"
  fi
}

for shell in bash zsh fish; do
  check "$shell" "" mach machine
  check "$shell" machine ad add
  check "$shell" "machine add" --cl --cluster
  check "$shell" "" --con --context
  check "$shell" "cluster onboard" --no- --no-import --no-wait
  check "$shell" "-o json machine" ad add
  # an argument that is another command's verb does not switch to that command's flags
  check "$shell" "fragment set history" -- --if-match --limit
done
for shell in bash zsh fish; do
  check "$shell" "--context x" mach machine
done
# the value of a flag may look like a flag: it is the value, so the cursor is at the command, not at a value
# (fish reads the second --context as a flag of its own before any condition runs, so it is left out)
for shell in bash zsh; do
  check "$shell" "--context --context" mach machine
done
# bash splits --context=x into three words, zsh and fish keep one
check bash "--context = x" mach machine
check bash "--context = x machine" ad add
check bash "--no-input = true" mach machine
check bash "-v = false machine" ad add
check zsh-fpath "" mach machine
check zsh-fpath "machine" ad add
check zsh "--context=x machine" ad add
check fish "--context=x machine" ad add
check zsh "--no-input=true" mach machine
check fish "--no-input=true" mach machine
# the cursor is the value of a flag: nothing is offered
refuse() {
  local got
  got=$(complete_in "$1" "$2" "$3")
  if [ -n "$got" ]; then
    echo "FAIL $1: '$2 $3' offers something at a flag's value: $(tr '\n' ' ' <<<"$got")"
    failed=1
  else
    echo "ok   $1: '$2 $3' offers nothing"
  fi
}
for shell in bash zsh fish; do
  refuse "$shell" "--context" mach
done
# fish offers the flag itself here, as its own parser does
for shell in bash zsh; do
  refuse "$shell" "--context" --con
done
refuse bash "--context =" mach
exit "$failed"
