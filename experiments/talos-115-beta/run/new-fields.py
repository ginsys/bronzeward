#!/usr/bin/env python3
"""List the yaml-tagged struct fields that one machinery module's config types have and another's
lack. Usage: new-fields.py <older module directory> <newer module directory>. Output, one line per
field: <package>.<struct> <yaml key>. It reads source text; it runs nothing from the modules."""

import pathlib
import re
import sys

STRUCT = re.compile(r"^type (\w+) struct \{")
TAG = re.compile(r'`[^`]*yaml:"([^",]*)[^"]*"[^`]*`')


def fields(root):
    found = set()
    types = pathlib.Path(root) / "config" / "types"
    for path in sorted(types.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        struct = None
        for line in path.read_text().splitlines():
            m = STRUCT.match(line)
            if m:
                struct = m.group(1)
                continue
            if line.startswith("}"):
                struct = None
                continue
            m = TAG.search(line)
            if struct and m and m.group(1) not in ("", "-"):
                found.add((str(path.parent.relative_to(types)), struct, m.group(1)))
    return found


def main():
    older, newer = fields(sys.argv[1]), fields(sys.argv[2])
    old_structs = {(p, s) for p, s, _ in older}
    for p, s, key in sorted(newer - older):
        mark = "" if (p, s) in old_structs else " (new struct)"
        print(f"{p}.{s} {key}{mark}")
    for p, s, key in sorted(older - newer):
        print(f"removed: {p}.{s} {key}")
    print(f"counts: older={len(older)} newer={len(newer)}", file=sys.stderr)


if __name__ == "__main__":
    main()
