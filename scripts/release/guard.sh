#!/usr/bin/env bash
# The checks Release / Create and Release / Publish make before a tag or a release
# (ginsys/bronzeward#262 P1, P2): the version is a pre-release tag of the decided form, CHANGELOG.md
# has a section headed by it, and the repository carries a LICENSE. Every failure is named, not only
# the first. With DRY_RUN=1 a missing LICENSE is a warning, so a dry run still checks and builds
# everything else; a real run stops on it, since no release is published until the licence is
# chosen (ginsys/bronzeward#16). With a second argument, writes the section's body there as the
# release notes. Run from the repository root; the checks that need the network (CI passed on the
# commit, no such tag or release yet) are the workflows' own.
set -uo pipefail

v=${1:?usage: guard.sh <version> [notes-file]}
notes=${2:-}
failed=0
# GitHub reads these prefixes as annotations; locally they are plain lines.
fail() { echo "::error::$*" >&2; failed=1; }

[[ $v =~ ^v0\.[1-9][0-9]*\.0-(alpha|beta|rc)\.[1-9][0-9]*$ ]] ||
	fail "$v is not a pre-release tag v0.N.0-alpha.M (or -beta.M, -rc.M)"

if [ ! -s LICENSE ]; then
	msg="the repository has no LICENSE: no release is published until the licence is chosen (ginsys/bronzeward#16)"
	if [ "${DRY_RUN:-0}" = 1 ]; then
		echo "::warning::$msg; a real run stops here" >&2
	else
		fail "$msg"
	fi
fi

# The section runs from its exact heading to the next second-level heading.
body=$(awk -v h="## $v" '$0 == h {on = 1; next} on && /^## / {exit} on' CHANGELOG.md)
if ! grep -qxF -- "## $v" CHANGELOG.md; then
	fail "CHANGELOG.md has no section headed '## $v'"
elif [ -z "${body//[[:space:]]/}" ]; then
	fail "CHANGELOG.md's section '## $v' is empty"
fi

if [ "$failed" = 0 ] && [ -n "$notes" ]; then
	printf '%s\n' "$body" >"$notes" || exit 1
fi
exit "$failed"
