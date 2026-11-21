#!/usr/bin/env python3
"""The pins for the previous major of Chrome, Edge and Firefox (ginsys/bronzeward#907).

    previous-major-pins.py FILE            print the pin file the sources call for
    previous-major-pins.py --check FILE    exit 0 when FILE equals it, 1 when it differs
    previous-major-pins.py --write FILE    replace FILE with it

FILE is web/e2e/previous-majors.json; its `sources` block is kept. Each rule reads the vendor source the
run downloads from: Chrome for Testing (the Stable channel's major, minus one, and the highest known-good
build of it), Mozilla's release list (the latest release's major, minus one, and the highest major or
stability release of it) and Microsoft's apt index for Edge (the highest stable major, minus one, and its
highest build, with that stanza's file name and sha256). A differing pin is printed as `name: old -> new`.

Exit 2 is any failure to read a source or to find what a rule needs, and a pin that would move backwards
from FILE's; it prints the reason on stderr and writes nothing, so a vendor outage or a changed source never
reads as "no drift". PINS_MIRROR, a base URL, replaces the three vendor roots with <base>/cft/,
<base>/firefox/ and <base>/edge/, for the test.
"""

import json
import os
import re
import sys
import urllib.request

CFT = "https://googlechromelabs.github.io/chrome-for-testing/"
FIREFOX = "https://product-details.mozilla.org/1.0/"
EDGE = "https://packages.microsoft.com/repos/edge/"


class Failure(Exception):
    pass


def roots():
    mirror = os.environ.get("PINS_MIRROR")
    if mirror:
        return mirror + "/cft/", mirror + "/firefox/", mirror + "/edge/"
    return CFT, FIREFOX, EDGE


def get(url):
    with urllib.request.urlopen(url, timeout=60) as r:
        return r.read().decode()


def ver(s):
    return tuple(int(p) for p in s.split("."))


def previous(versions, current_major, what):
    """The highest of `versions` in the major before `current_major`."""
    got = [v for v in versions if ver(v)[0] == current_major - 1]
    if not got:
        raise Failure(f"{what}: no build of major {current_major - 1} (current is {current_major})")
    return max(got, key=ver)


def chrome(root):
    stable = json.loads(get(root + "last-known-good-versions.json"))["channels"]["Stable"]["version"]
    known = [v["version"] for v in json.loads(get(root + "known-good-versions.json"))["versions"]]
    return previous(known, ver(stable)[0], "chrome")


def firefox(root):
    latest = json.loads(get(root + "firefox_versions.json"))["LATEST_FIREFOX_VERSION"]
    releases = [r["version"] for r in json.loads(get(root + "firefox.json"))["releases"].values() if r["category"] in ("major", "stability")]
    releases = [v for v in releases if re.fullmatch(r"\d+(\.\d+)*", v)]  # not an old "3.6.3plugin1"
    return previous(releases, ver(latest)[0], "firefox")


def edge(root):
    stanzas = []
    for text in get(root + "dists/stable/main/binary-amd64/Packages").split("\n\n"):
        f = dict(line.split(": ", 1) for line in text.splitlines() if ": " in line and not line.startswith(" "))
        if f.get("Package") == "microsoft-edge-stable":
            stanzas.append((f["Version"].rsplit("-", 1)[0], f["Filename"], f["SHA256"]))
    if not stanzas:
        raise Failure("edge: no microsoft-edge-stable stanza in the index")
    current = max(ver(v)[0] for v, _, _ in stanzas)
    build = previous([v for v, _, _ in stanzas], current, "edge")
    _, filename, sha = next(s for s in stanzas if s[0] == build)
    # The URL is the vendor's own, whatever PINS_MIRROR served the index from.
    return {"version": build, "debUrl": EDGE + filename, "debSha256": sha}


def build(old):
    c, f, e = roots()
    new = {"chrome": chrome(c), "edge": edge(e), "firefox": firefox(f), "sources": old["sources"]}
    for name, was, now in (("chrome", old["chrome"], new["chrome"]), ("edge", old["edge"]["version"], new["edge"]["version"]), ("firefox", old["firefox"], new["firefox"])):
        if ver(now) < ver(was):
            raise Failure(f"{name}: the source offers {now}, below the pinned {was}")
    return new


def differences(old, new):
    flat = (("chrome", old["chrome"], new["chrome"]), ("firefox", old["firefox"], new["firefox"]))
    flat += tuple((f"edge.{k}", old["edge"][k], new["edge"][k]) for k in ("version", "debUrl", "debSha256"))
    return [f"{name}: {was} -> {now}" for name, was, now in flat if was != now]


def main(argv):
    mode, path = ("print", argv[0]) if len(argv) == 1 else (argv[0], argv[1]) if len(argv) == 2 and argv[0] in ("--check", "--write") else (None, None)
    if mode is None:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        with open(path, encoding="utf-8") as fh:
            old = json.load(fh)
        new = build(old)
    except Exception as e:  # noqa: BLE001  anything that stops the read is exit 2: an uncaught exception would exit 1, which reads as drift
        print(f"previous-major-pins: {type(e).__name__}: {e}", file=sys.stderr)
        return 2
    text = json.dumps(new, indent=2) + "\n"
    if mode == "print":
        sys.stdout.write(text)
        return 0
    diff = differences(old, new)
    if mode == "--check":
        print("\n".join(diff))
        return 1 if diff else 0
    if diff:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)
    print("\n".join(diff))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
