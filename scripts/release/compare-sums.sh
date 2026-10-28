#!/usr/bin/env bash
# Compare two builds' SHA256SUMS (ginsys/bronzeward#262 P6): every file each names, with its hash,
# must be in the other, unless packaging/reproducibility-exceptions.txt (or $3) lists the file with
# its cause. Prints each difference; exits 1 on an unlisted one.
set -euo pipefail
a=$1 b=$2 exceptions=${3:-packaging/reproducibility-exceptions.txt}
for f in "$a" "$b"; do
	[ -s "$f" ] || { echo "compare-sums: $f is missing or empty" >&2; exit 1; }
done
listed=$(sed -e 's/#.*//' "$exceptions" | awk 'NF {print $1}')
# A line in one file only is a file whose hash differs or that the other build lacks.
differing=$(LC_ALL=C sort "$a" "$b" | uniq -u | awk '{print $2}' | LC_ALL=C sort -u)
status=0
while IFS= read -r name; do
	[ -n "$name" ] || continue
	if grep -qxF -- "$name" <<<"$listed"; then
		echo "compare-sums: $name differs, a listed exception"
	else
		echo "compare-sums: $name differs between the two builds and is not a listed exception" >&2
		status=1
	fi
done <<<"$differing"
[ "$status" -ne 0 ] || echo "compare-sums: $(wc -l <"$a") files, every unlisted one identical"
exit "$status"
