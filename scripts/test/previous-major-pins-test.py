#!/usr/bin/env python3
"""Tests for scripts/ci/previous-major-pins.py (ginsys/bronzeward#907): the pins for the previous major of
Chrome, Edge and Firefox are read from the three vendor sources, served here by a local fixture server
through the script's one override, PINS_MIRROR. Exit 0 is equal, 1 is drift, 2 is any failure to read a
source or a pin that would move backwards, and 2 never writes."""

import http.server
import json
import os
import subprocess
import sys
import tempfile
import threading

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SCRIPT = os.path.join(ROOT, "scripts", "ci", "previous-major-pins.py")

EDGE_ROOT = "https://packages.microsoft.com/repos/edge/"
SOURCES = {"chrome": "c", "edge": "e", "firefox": "f"}


def stanza(package, version, sha):
    return (
        f"Package: {package}\nVersion: {version}-1\nArchitecture: amd64\n"
        f"Filename: pool/main/m/{package}/{package}_{version}-1_amd64.deb\nSHA256: {sha}\n"
    )


GOOD = {
    "/cft/last-known-good-versions.json": json.dumps({"channels": {"Stable": {"version": "158.0.8127.36"}, "Beta": {"version": "159.0.8154.13"}}}),
    "/cft/known-good-versions.json": json.dumps(
        {"versions": [{"version": v} for v in ("156.0.8078.80", "157.0.8100.1", "157.0.8103.100", "157.0.8103.78", "157.0.8103.5", "158.0.8127.36", "159.0.8154.13")]}
    ),
    "/firefox/firefox_versions.json": json.dumps({"LATEST_FIREFOX_VERSION": "160.0.1"}),
    "/firefox/firefox.json": json.dumps(
        {
            "releases": {
                "firefox-158.0": {"category": "major", "version": "158.0"},
                "firefox-159.0": {"category": "major", "version": "159.0"},
                "firefox-159.0.1": {"category": "stability", "version": "159.0.1"},
                "firefox-159.0esr": {"category": "esr", "version": "159.0esr"},
                "firefox-160.0b3": {"category": "dev", "version": "160.0b3"},
                "firefox-159.9": {"category": "dev", "version": "159.9"},
                "firefox-160.0.1": {"category": "stability", "version": "160.0.1"},
                "firefox-3.6.3plugin1": {"category": "stability", "version": "3.6.3plugin1"},
            }
        }
    ),
    "/edge/dists/stable/main/binary-amd64/Packages": "\n".join(
        [
            stanza("microsoft-edge-stable", "156.0.4314.58", "a" * 64),
            stanza("microsoft-edge-beta", "159.0.4500.1", "b" * 64),
            stanza("microsoft-edge-stable", "157.0.4341.60", "f" * 64),
            stanza("microsoft-edge-stable", "157.0.4341.100", "c" * 64),
            stanza("microsoft-edge-stable", "157.0.4338.9", "d" * 64),
            stanza("microsoft-edge-stable", "158.0.4370.33", "e" * 64),
        ]
    ),
}

SOURCES_BLOCK = {"chrome": "ccc", "edge": "eee", "firefox": "fff"}
OLD = {
    "chrome": "156.0.8078.80",
    "edge": {"version": "156.0.4314.58", "debUrl": EDGE_ROOT + "pool/main/m/microsoft-edge-stable/microsoft-edge-stable_156.0.4314.58-1_amd64.deb", "debSha256": "a" * 64},
    "firefox": "159.0.1",
    "sources": SOURCES_BLOCK,
}
NEW = {
    "chrome": "157.0.8103.100",
    "edge": {"version": "157.0.4341.100", "debUrl": EDGE_ROOT + "pool/main/m/microsoft-edge-stable/microsoft-edge-stable_157.0.4341.100-1_amd64.deb", "debSha256": "c" * 64},
    "firefox": "159.0.1",
    "sources": SOURCES_BLOCK,
}

# The script's environment: the whole one (a toolcache Python needs its LD_LIBRARY_PATH to start), without a
# proxy, so the fixture server on 127.0.0.1 is reached directly.
CHILD_ENV = {k: v for k, v in os.environ.items() if "proxy" not in k.lower()}

failures = []
served = {}


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        status, body = served.get(self.path, (404, "not served"))
        self.send_response(status)
        self.end_headers()
        self.wfile.write(body.encode())

    def log_message(self, *_):
        pass


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
MIRROR = f"http://127.0.0.1:{server.server_address[1]}"


def serve(override=None):
    """The good fixtures, with a path replaced by (status, body)."""
    served.clear()
    for path, body in GOOD.items():
        served[path] = (200, body)
    served.update(override or {})


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def run(mode, content, mirror=MIRROR):
    with tempfile.TemporaryDirectory() as d:
        pin = os.path.join(d, "previous-majors.json")
        with open(pin, "w", encoding="utf-8") as f:
            f.write(json.dumps(content, indent=2) + "\n")
        before = read(pin)
        env = {**CHILD_ENV, "PINS_MIRROR": mirror}
        args = [sys.executable, "-I", SCRIPT] + ([mode] if mode else []) + [pin]
        p = subprocess.run(args, check=False, capture_output=True, text=True, env=env, timeout=60)
        return p, before, read(pin)


def expect(name, cond, detail=""):
    if not cond:
        failures.append(f"{name}: {detail}")


# A run that reads equal, one that finds drift, and one that writes.
serve()
p, _, _ = run("--check", NEW)
expect("equal file", p.returncode == 0, f"rc={p.returncode} {p.stderr}")

p, before, after = run("--check", OLD)
expect("drift exit", p.returncode == 1, f"rc={p.returncode} {p.stderr}")
for want in (
    "chrome: 156.0.8078.80 -> 157.0.8103.100",
    "edge.version: 156.0.4314.58 -> 157.0.4341.100",
    "edge.debSha256: " + "a" * 64 + " -> " + "c" * 64,
):
    expect("drift lines", want in p.stdout, f"missing {want!r} in {p.stdout!r}")
expect("drift names no unchanged pin", "firefox" not in p.stdout, p.stdout)
expect("check writes nothing", before == after)

p, before, after = run("--write", OLD)
expect("write exit", p.returncode == 0, f"rc={p.returncode} {p.stderr}")
expect("write result", json.loads(after) == NEW, after)
expect("write keeps the sources block and the layout", after == json.dumps(NEW, indent=2) + "\n", after)

# --write replaces the file whole: its mode stays, nothing is left beside it, and a directory it cannot
# write into leaves the file as it was and exits 2.
def write_in(dir_mode, file_mode):
    with tempfile.TemporaryDirectory() as d:
        pin = os.path.join(d, "previous-majors.json")
        with open(pin, "w", encoding="utf-8") as f:
            f.write(json.dumps(OLD, indent=2) + "\n")
        os.chmod(pin, file_mode)
        before = read(pin)
        os.chmod(d, dir_mode)
        try:
            p = subprocess.run([sys.executable, "-I", SCRIPT, "--write", pin], check=False, capture_output=True, text=True, env={**CHILD_ENV, "PINS_MIRROR": MIRROR}, timeout=60)
            return p, before, read(pin), oct(os.stat(pin).st_mode & 0o777), sorted(os.listdir(d))
        finally:
            os.chmod(d, 0o755)


serve()
p, before, after, mode_after, left = write_in(0o755, 0o640)
expect("write keeps the mode", p.returncode == 0 and mode_after == "0o640" and json.loads(after) == NEW and left == ["previous-majors.json"], f"rc={p.returncode} mode={mode_after} left={left}")
if os.geteuid() != 0:  # root writes into a 0555 directory anyway
    p, before, after, mode_after, left = write_in(0o555, 0o644)
    expect("write into a directory it cannot write", p.returncode == 2 and before == after and left == ["previous-majors.json"] and p.stderr.startswith("previous-major-pins: "), f"rc={p.returncode} left={left} {p.stderr}")

p, _, _ = run(None, OLD)
expect("print mode", p.returncode == 0 and json.loads(p.stdout) == NEW, p.stdout + p.stderr)

# "equal file" above is also the rules check: the fixtures hold a beta Edge of a higher major, a dev Firefox,
# an old Firefox "3.6.3plugin1", builds that sort higher as strings than the highest (8103.78, 4341.60) and
# an earlier stanza of the previous major, and none of them is picked.

# Exit 2: a source that cannot be read or does not say what the script needs. Nothing is written.
BAD = {
    "chrome unreachable": {"/cft/known-good-versions.json": (500, "boom")},
    "chrome not json": {"/cft/last-known-good-versions.json": (200, "<html>")},
    "chrome no Stable channel": {"/cft/last-known-good-versions.json": (200, json.dumps({"channels": {"Beta": {"version": "159.0.1.1"}}}))},
    "chrome no build of the previous major": {"/cft/known-good-versions.json": (200, json.dumps({"versions": [{"version": "158.0.8127.36"}]}))},
    "firefox unreachable": {"/firefox/firefox.json": (503, "down")},
    "firefox not json": {"/firefox/firefox_versions.json": (200, "nope")},
    "firefox no release": {"/firefox/firefox.json": (200, json.dumps({"releases": {}}))},
    "firefox no build of the previous major": {"/firefox/firefox.json": (200, json.dumps({"releases": {"f": {"category": "major", "version": "160.0"}}}))},
    "edge unreachable": {"/edge/dists/stable/main/binary-amd64/Packages": (502, "bad gateway")},
    "edge no stable stanza": {"/edge/dists/stable/main/binary-amd64/Packages": (200, stanza("microsoft-edge-beta", "158.0.4400.1", "b" * 64))},
    "edge empty index": {"/edge/dists/stable/main/binary-amd64/Packages": (200, "")},
    "edge no build of the previous major": {"/edge/dists/stable/main/binary-amd64/Packages": (200, stanza("microsoft-edge-stable", "158.0.4370.33", "e" * 64))},
    # An exception no handler names must still be exit 2: Python's own exit for it is 1, which reads as drift.
    "chrome nested too deeply": {"/cft/last-known-good-versions.json": (200, "[" * 200000)},
}
for name, override in BAD.items():
    for mode in ("--check", "--write"):
        serve(override)
        p, before, after = run(mode, OLD)
        expect(f"{name} {mode}", p.returncode == 2, f"rc={p.returncode} stdout={p.stdout!r} stderr={p.stderr!r}")
        expect(f"{name} {mode} writes nothing", before == after)
        # Python itself exits 2 for a script it cannot open, so the script's own message is the proof.
        expect(f"{name} {mode} says why", p.stderr.startswith("previous-major-pins: "), f"stderr={p.stderr!r}")

# A pin file that lacks a field the comparison reads is a failure to read too, not drift.
serve()
broken = json.loads(json.dumps(OLD))
del broken["edge"]["debUrl"]
for mode in ("--check", "--write"):
    p, before, after = run(mode, broken)
    expect(f"pin file without debUrl {mode}", p.returncode == 2 and before == after and p.stderr.startswith("previous-major-pins: "), f"rc={p.returncode} {p.stderr}")

serve()
p, before, after = run("--check", OLD, mirror="http://127.0.0.1:1")
expect("no server", p.returncode == 2 and before == after and p.stderr.startswith("previous-major-pins: "), f"rc={p.returncode} {p.stderr}")

# A pin that would move backwards is a failure to read, never drift.
serve()
ahead = json.loads(json.dumps(OLD))
ahead["chrome"] = "158.0.8127.36"
for mode in ("--check", "--write"):
    p, before, after = run(mode, ahead)
    expect(f"downgrade {mode}", p.returncode == 2 and before == after and p.stderr.startswith("previous-major-pins: "), f"rc={p.returncode} {p.stderr}")
ahead = json.loads(json.dumps(OLD))
ahead["firefox"] = "159.0.2"
p, before, after = run("--write", ahead)
expect("firefox downgrade", p.returncode == 2 and before == after and p.stderr.startswith("previous-major-pins: "), f"rc={p.returncode} {p.stderr}")
ahead = json.loads(json.dumps(OLD))
ahead["edge"]["version"] = "157.0.4399.1"
p, before, after = run("--write", ahead)
expect("edge downgrade", p.returncode == 2 and before == after and p.stderr.startswith("previous-major-pins: "), f"rc={p.returncode} {p.stderr}")

server.shutdown()
if failures:
    print("FAIL:\n  " + "\n  ".join(failures), file=sys.stderr)
    sys.exit(1)
print("previous-major-pins-test: ok")
