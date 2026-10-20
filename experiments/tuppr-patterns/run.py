#!/usr/bin/env python3
"""Run selected pinned tuppr tests and two expected-negative compatibility probes."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from datetime import datetime, timezone

PIN = "377fbf03df05420ca833230233e67877e5f343d4"
HERE = Path(__file__).resolve().parent
CASES = [
    ("recovery", "internal/controller/talosupgrade", [
        "TestTalosReconcile_CreatesJobForNextNode",
        "TestTalosReconcile_HealthCheckFailure",
        "TestTalosReconcile_FailedState_ResetsOnRetry",
        "TestTalosReconcile_HandleJobSuccess_PatchInstallImageFails_Continues",
        "TestTalosReconcile_FailedJobButNodeUpgraded_TreatedAsSuccess",
        "TestTalosReconcile_RebootingNodeRecordsDeadline",
        "TestTalosReconcile_RebootingNodeTimesOut_JobStillPresent",
        "TestTalosReconcile_RebootingNodeAfterJobGC_KeepsWaiting",
        "TestTalosReconcile_RebootingNodeAfterJobGC_TimesOut",
        "TestTalosReconcile_RebootingNodeAfterJobGC_CompletesWhenReady",
        "TestTalosReconcile_StaleRebootTrackingCleared",
    ], None),
    ("coordination", "internal/controller/coordination", ["TestIsAnotherUpgradeActive"], None),
    ("config", "internal/talos", ["TestInstallImageFromConfig", "TestSetInstallImage"], None),
    ("health", "internal/healthcheck", [
        "TestHealthChecker_ValidateHealthChecks",
        "TestHealthChecker_EvaluateSpecificResource_NotFound",
        "TestHealthChecker_EvaluateAllResources_OneFails",
        "TestHealthChecker_ListSelectorEmpty",
        "TestHealthChecker_EvaluateAllResources_EmptyList",
    ], None),
    ("foreign-cordon", "internal/controller/drain", [
        "TestBronzewardForeignCordonMustSurvive",
    ], "BW_FOREIGN_CORDON_CLEARED"),
    ("suspend-job", "internal/controller/talosupgrade", [
        "TestBronzewardSuspendMustStopActiveJob",
    ], "BW_JOB_STILL_ENABLED"),
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    source, output = args.source.resolve(), args.output.resolve()

    def git(*argv):
        return subprocess.check_output(["git", "-C", str(source), *argv], text=True).strip()

    if git("rev-parse", "HEAD") != PIN or git("status", "--porcelain", "--untracked-files=all"):
        raise SystemExit("Source must be clean at pinned commit " + PIN)
    if output == source or source in output.parents:
        raise SystemExit("Evidence output must be outside the source checkout")
    output.mkdir(parents=True, exist_ok=False)
    for key in ("TMPDIR", "GOTMPDIR", "GOCACHE", "GOMODCACHE"):
        if not os.environ.get(key):
            raise SystemExit(key + " must name disposable disk-backed scratch")
    env = dict(os.environ, GOTOOLCHAIN="local", GOWORK="off", GOFLAGS="", GOMAXPROCS="2")
    # Let the selected Go binary locate its own installation, not an inherited mise root.
    env.pop("GOROOT", None)
    overlay = {"Replace": {
        str(source / "internal/controller/drain/bronzeward_probe_test.go"): str(HERE / "probes/cordon_test.go"),
        str(source / "internal/controller/talosupgrade/bronzeward_probe_test.go"): str(HERE / "probes/suspend_test.go"),
    }}
    overlay_path = output / "overlay.json"
    overlay_path.write_text(json.dumps(overlay, indent=2) + "\n")

    def sanitize(value):
        for path, token in ((str(source), "$SOURCE"), (str(output), "$OUTPUT"), (str(HERE), "$FIXTURE")):
            value = value.replace(path, token)
        return value

    report = {
        "source_commit": PIN,
        "started_utc": datetime.now(timezone.utc).isoformat(),
        "go_version": subprocess.check_output(["go", "version"], env=env, text=True).strip(),
        "fixture_sha256": {str(p.relative_to(HERE)): hashlib.sha256(p.read_bytes()).hexdigest()
                           for p in [Path(__file__).resolve(), *sorted((HERE / "probes").glob("*.go"))]},
        "source_files_sha256": {p: hashlib.sha256((source / p).read_bytes()).hexdigest()
                                for p in ("go.mod", "go.sum", ".mise/config.toml")},
        "cases": [],
        "limits": "Fake clients only; no cluster, real RPC, quorum, reboot or credential custody proof.",
    }
    for name, package, tests, marker in CASES:
        command = ["timeout", "--kill-after=10s", "600s", "go", "test", "-mod=readonly",
                   "-count=1", "-p=2", "-parallel=2", "-timeout=90s", "-json",
                   "-overlay=" + str(overlay_path), "./" + package,
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
        bad_extra = {test: action for test, action in terminal.items()
                     if test not in tests and action in ("fail", "skip")}
        ok = (result.returncode == (1 if marker else 0)
              and all(state == expected for state in observed.values())
              and not bad_extra and (marker is None or marker in stdout))
        report["cases"].append({"case": name, "command": sanitize(" ".join(command)),
                                "exit": result.returncode, "expected_test_result": expected,
                                "observed": observed, "unexpected": bad_extra, "matched": ok})
        (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"{name}: exit={result.returncode}, matched={ok}", flush=True)
        if not ok:
            raise SystemExit("Unexpected result; retain evidence and investigate: " + name)
    report["source_clean_after"] = not git("status", "--porcelain", "--untracked-files=all")
    report["finished_utc"] = datetime.now(timezone.utc).isoformat()
    (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    if not report["source_clean_after"]:
        raise SystemExit("Source checkout changed during experiment")


if __name__ == "__main__":
    main()
