#!/usr/bin/env python3
"""List the failing leaf tests of a `go test -v` log with the cause each one's own output shows.
Usage: failures.py <log>. A leaf is a failed test with no failed subtest. The output of a test is
every line printed while it was the running test. "contract" means that output holds the
contract rule's refusal; everything else is "other", shown with the first 12 lines of its output."""

import pathlib
import re
import sys

RUN = re.compile(r"^=== (?:RUN|CONT|NAME)\s+(\S+)")
END = re.compile(r"^\s*--- (PASS|FAIL|SKIP): (\S+)")
PACKAGE = re.compile(r"^(ok|FAIL)\s+(github\.com/\S+)")
CONTRACT = re.compile(
    r"is not supported: this server renders only v1\.15|refused \(contract\)"
    r"|not the renderer.s minor"
)


def main():
    current, output, failed, rows = None, {}, [], []
    counts = {"PASS": 0, "FAIL": 0, "SKIP": 0}
    for line in pathlib.Path(sys.argv[1]).read_text(errors="replace").splitlines():
        if m := RUN.match(line):
            current = m.group(1)
        elif m := END.match(line):
            counts[m.group(1)] += 1
            if m.group(1) == "FAIL":
                failed.append(m.group(2))
            current = None
        elif m := PACKAGE.match(line):
            package = m.group(2).removeprefix("github.com/ginsys/bronzeward/")
            for name in sorted(failed):
                if not any(other.startswith(name + "/") for other in failed):
                    rows.append((package, name, output.get(name, [])))
            current, output, failed = None, {}, []
        elif current:
            output.setdefault(current, []).append(line)
    tally = {}
    for package, name, lines in rows:
        cause = "contract" if any(CONTRACT.search(x) for x in lines) else "other"
        tally[(package, cause)] = tally.get((package, cause), 0) + 1
        print(f"{cause:8} {package} {name}")
        if cause == "other":
            # The first lines of the test's own output, each cut at 400 characters.
            shown = [x.strip() for x in lines if x.strip()]
            for x in shown[:12]:
                print(f"           {x[:400].rstrip()}")
    print("--- result lines (tests and subtests):", " ".join(f"{k}={v}" for k, v in counts.items()))
    for (package, cause), n in sorted(tally.items()):
        print(f"--- failing leaves: {package} {cause}={n}")
    print(f"--- failing leaves in all: {len(rows)}")


if __name__ == "__main__":
    main()
