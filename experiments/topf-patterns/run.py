#!/usr/bin/env python3
"""Run pinned topf unit tests and synthetic compatibility probes without a cluster."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import time

PIN = "7aeead7545d6a715e719e2e6241027792f7e1a89"
HERE = Path(__file__).resolve().parent
CASES = [
    ("composition", "pkg/config", ["TestLoadFileTemplate", "TestParsePatches",
        "TestBronzewardCompositionOrder", "TestBronzewardTemplateInputCanChange"], None),
    ("masking", "internal/maskedwriter", ["TestMaskedWriter", "TestMaskedWriterConcurrency"], None),
    ("secrets-and-version", "internal/topf", ["TestCollectCurrentConfigSecrets",
        "TestEncodingVariationsPEM", "TestCollectCurrentConfigSecretsNilSafe",
        "TestBronzewardRunningContractAndDesiredImage"], None),
    ("dispatch", "internal/nodepool", ["TestRun", "TestBronzewardSerialFailureStopsAdmission"], None),
    ("write-branch", "pkg/providers", ["TestBronzewardEncryptedWriteBranch"], None),
    ("version-controls", "internal/cmd/upgrade", ["TestBronzewardVersionControls"], None),
    ("encryption-failure", "pkg/providers", ["TestBronzewardEncryptionFailureMustRefusePersistence"],
        "BW_PLAINTEXT_PERSISTED"),
    ("digest-version", "internal/cmd/upgrade", ["TestBronzewardDigestMustNotBecomeVersion"],
        "BW_DIGEST_IN_VERSION"),
    ("unknown-version", "internal/cmd/upgrade", ["TestBronzewardUnknownVersionMustNotSelectAPI"],
        "BW_UNPROVEN_API_SELECTION"),
]
OVERLAYS = {"pkg/config": "config_test.go", "pkg/providers": "providers_test.go",
            "internal/cmd/upgrade": "upgrade_test.go", "internal/nodepool": "nodepool_test.go",
            "internal/topf": "node_test.go"}


def main():
    deadline = time.monotonic() + 1180
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--go", type=Path, required=True, help="Native Go binary, not a version-manager shim")
    args = parser.parse_args()
    source, output, go = args.source.resolve(), args.output.resolve(), args.go.resolve(strict=True)

    def git(*argv):
        return subprocess.check_output(["git", "-C", str(source), *argv], text=True).strip()

    def dirty():
        return git("status", "--porcelain", "--untracked-files=all", "--ignored")

    if git("rev-parse", "HEAD") != PIN or dirty():
        raise SystemExit("Source must be pristine at " + PIN)
    if output == source or source in output.parents:
        raise SystemExit("Output must be outside the upstream checkout")
    scratch = {}
    for key in ("TMPDIR", "GOTMPDIR", "GOCACHE", "GOMODCACHE"):
        value = os.environ.get(key)
        if not value or not Path(value).is_absolute() or not Path(value).is_dir():
            raise SystemExit(key + " must name existing absolute disk-backed scratch")
        scratch[key] = value
    output.mkdir(parents=True, exist_ok=False)
    # Do not inherit credentials, user Go settings, workspace selection or GOROOT.
    env = dict(scratch, PATH=str(go.parent) + os.pathsep + os.defpath,
               GOPATH=str(output / "gopath"), GOENV="off", GOTOOLCHAIN="local",
               GOWORK="off", GOFLAGS="", GOMAXPROCS="2", CGO_ENABLED="1")
    overlay = {"Replace": {str(source / package / "bronzeward_probe_test.go"):
                           str(HERE / "probes" / filename) for package, filename in OVERLAYS.items()}}
    overlay_path = output / "overlay.json"
    overlay_path.write_text(json.dumps(overlay, indent=2) + "\n")

    def sanitize(value):
        replacements = [(str(go), "$GO"), (str(source), "$SOURCE"),
                        (str(output), "$OUTPUT"), (str(HERE), "$FIXTURE")]
        replacements += [(v, "$" + k) for k, v in scratch.items()]
        for path, token in replacements:
            value = value.replace(path, token)
        return value

    report = {
        "source_commit": PIN, "started_utc": datetime.now(timezone.utc).isoformat(),
        "python_version": platform.python_version(), "platform": platform.platform(),
        "go_version": subprocess.check_output([str(go), "version"], env=env, text=True, timeout=30).strip(),
        "git_version": git("--version"),
        "timeout_version": subprocess.check_output(["timeout", "--version"], env=env, text=True, timeout=30).splitlines()[0],
        "cc_version": subprocess.check_output(["cc", "--version"], env=env, text=True, timeout=30).splitlines()[0],
        "environment": {k: sanitize(v) for k, v in env.items() if k != "PATH"},
        "fixture_sha256": {str(p.relative_to(HERE)): hashlib.sha256(p.read_bytes()).hexdigest()
                           for p in [Path(__file__).resolve(), *sorted((HERE / "probes").glob("*.go"))]},
        "source_files_sha256": {p: hashlib.sha256((source / p).read_bytes()).hexdigest()
                                for p in ("go.mod", "go.sum", ".github/workflows/e2e.yml", ".github/workflows/test.yml")},
        "cases": [], "limits": "Pure/synthetic tests and subprocess stubs; no real encryption, API, upgrade or recovery proof.",
    }
    for name, package, tests, marker in CASES:
        remaining = int(deadline - time.monotonic())
        if remaining < 1:
            raise SystemExit("Overall fixture deadline exhausted; retain partial evidence")
        command = ["timeout", "--kill-after=10s", str(min(600, remaining)) + "s", str(go), "test", "-mod=readonly",
                   "-count=1", "-p=2", "-parallel=2", "-timeout=90s", "-json"]
        if name == "masking":
            command.append("-race")
        command += ["-overlay=" + str(overlay_path), "./" + package,
                    "-run", "^(" + "|".join(map(re.escape, tests)) + ")$"]
        result = subprocess.run(command, cwd=source, env=env, text=True, capture_output=True)
        stdout, stderr = sanitize(result.stdout), sanitize(result.stderr)
        (output / (name + ".jsonl")).write_text(stdout)
        (output / (name + ".stderr")).write_text(stderr)
        events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        terminal = {e["Test"]: e["Action"] for e in events
                    if e.get("Test") and e["Action"] in ("pass", "fail", "skip")}
        expected = "fail" if marker else "pass"
        observed = {test: terminal.get(test, "missing") for test in tests}
        unexpected = {test: action for test, action in terminal.items()
                      if test not in tests and action in ("fail", "skip")}
        matched = (result.returncode == (1 if marker else 0)
                   and all(state == expected for state in observed.values()) and not unexpected
                   and (marker is None or marker in stdout))
        report["cases"].append({"case": name, "command": sanitize(" ".join(command)), "exit": result.returncode,
                                "expected_test_result": expected, "observed": observed,
                                "unexpected": unexpected, "matched": matched})
        (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"{name}: exit={result.returncode}, matched={matched}", flush=True)
        if not matched:
            raise SystemExit("Unexpected result; retain evidence and investigate: " + name)
    report["source_clean_after"] = not dirty()
    report["fixture_unchanged_after"] = all(
        hashlib.sha256((HERE / path).read_bytes()).hexdigest() == digest
        for path, digest in report["fixture_sha256"].items())
    report["finished_utc"] = datetime.now(timezone.utc).isoformat()
    (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    if not report["source_clean_after"]:
        raise SystemExit("Source checkout changed during experiment")
    if not report["fixture_unchanged_after"]:
        raise SystemExit("Fixture changed during experiment")


if __name__ == "__main__":
    main()
