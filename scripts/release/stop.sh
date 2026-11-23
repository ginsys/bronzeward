#!/usr/bin/env bash
# The two stops before a release that the shared release workflows of ginsys/.github do not make
# (ginsys/bronzeward#262): the VERSION file holds a pre-release of the decided form, v0.N.0-alpha.M
# (or -beta.M, -rc.M, M from 0 as the shared scheme counts), and the repository carries a LICENSE.
# Every failure is named, not only the first. With DRY_RUN=1 a missing LICENSE is a warning, so a
# dry run still checks and builds everything else; a real run stops on it, since no release is
# published until the licence is chosen (ginsys/bronzeward#16). Run from the repository root.
set -uo pipefail

failed=0
# GitHub reads these prefixes as annotations; locally they are plain lines.
fail() { echo "::error::$*" >&2; failed=1; }

# VERSION is the next version a release cuts, so it is the one that must have the decided form.
v=$(cat VERSION 2>/dev/null) || v=
[[ $v =~ ^v0\.[1-9][0-9]*\.0-(alpha|beta|rc)\.(0|[1-9][0-9]*)$ ]] ||
	fail "VERSION '$v' is not a pre-release v0.N.0-alpha.M (or -beta.M, -rc.M)"

if [ ! -s LICENSE ]; then
	msg="the repository has no LICENSE: no release is published until the licence is chosen (ginsys/bronzeward#16)"
	if [ "${DRY_RUN:-0}" = 1 ]; then
		echo "::warning::$msg; a real run stops here" >&2
	else
		fail "$msg"
	fi
fi
exit "$failed"
