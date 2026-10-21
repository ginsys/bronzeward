#!/usr/bin/env python3
"""Project private fixture evidence, scan it, and assemble a reproducible bundle."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess

import yaml


def encoded(value):
    return (json.dumps(value, indent=2) + "\n").encode()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def export_run(args):
    run = args.run.resolve()
    private = run / "private"
    events = json.loads((run / "events.json").read_text())
    assert events[-1]["event"] == "completed", "Do not export an unfinished chain"
    assert any(row["event"] == "cleanup" for row in events)
    replacements = {str(getattr(args, name).resolve()): "$" + label for name, label in (
        ("talosctl", "TALOSCTL"), ("go", "GO"), ("tools", "TOOLS"),
        ("source", "SOURCE"), ("checkout", "BRONZEWARD"))}
    replacements[str(private)] = "$PRIVATE"
    assert len(replacements) == 6, "Use distinct source, checkout and tool paths"
    text = encoded(events).decode()
    for old, new in sorted(replacements.items(), key=lambda item: -len(item[0])):
        text = text.replace(old, new)

    values = []
    realm = private / "fixture-realm.json"
    if realm.exists():
        for user in json.loads(realm.read_text())["users"]:
            values += [c["value"] for c in user["credentials"]]
    env = private / "keycloak.env"
    if env.exists():
        values += [line.split("=", 1)[1] for line in env.read_text().splitlines()
                   if line.startswith("KC_BOOTSTRAP_ADMIN_PASSWORD=")]
    tokens = private / "config/talosctl-oidc/tokens.json"
    if tokens.exists():
        for entry in json.loads(tokens.read_text()).values():
            values += [entry[k] for k in ("access_token", "id_token", "refresh_token") if entry.get(k)]
    for path in private.glob("*.talosconfig"):
        for context in yaml.safe_load(path.read_text()).get("contexts", {}).values():
            if context.get("key"):
                values += [context["key"], base64.b64decode(context["key"]).decode()]
    for path in private.glob("*.json"):
        document = json.loads(path.read_text())
        if isinstance(document, dict) and isinstance(document.get("key"), str):
            values.append(document["key"])
    known = [v.encode() for v in set(values) if len(v) >= 16]
    private_key = re.compile(rb"-----BEGIN [A-Z ]*PRIVATE KEY-----")
    jwt = re.compile(rb"eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+")

    def leaks(data):
        return any(v in data for v in known) or bool(private_key.search(data)) or bool(jwt.search(data))

    assert known, "Expected synthetic credentials in retained private state"
    assert all(leaks(value) for value in known)
    assert leaks(b"-----BEGIN PRIVATE KEY-----")
    assert leaks(b"eyJhbGciOiJub25lIn0.eyJzdWIiOiJ0ZXN0In0.synthetic")
    files = {"events.json": text.encode()}
    browser = {path.name: json.loads(path.read_text()) for path in sorted(private.glob("browser-*.json"))}
    if browser:
        files["browser.json"] = encoded(browser)
    audit = private / "audit.jsonl"
    if audit.exists():
        files["audit.json"] = encoded([{k: v for k, v in json.loads(line).items() if k != "error"}
                                       for line in audit.read_text().splitlines()])
    retention = {"run": run.name, "outcome": events[-1]["result"],
                 "projection": "events plus audit without raw error strings; credential state and raw command output remain private",
                 "commands": []}
    diagnostics = []
    # Fixed allowlist: copy only these diagnostic fields, never a whole log line.
    signatures = (
        {"type": "LOGIN_ERROR", "error": "cookie_not_found"},
        {"type": "CODE_TO_TOKEN_ERROR", "error": "not_allowed",
         "reason": "Offline tokens not allowed for the user or client"},
        {"type": "REFRESH_TOKEN_ERROR", "error": "invalid_token", "reason": "User disabled"},
    )
    for path in sorted((private / "commands").glob("*")):
        record = {"path": "private/commands/" + path.name, "bytes": path.stat().st_size, "sha256": digest(path)}
        retention["commands"].append(record)
        if path.name.endswith("-keycloak-logs.out"):
            for number, line in enumerate(path.read_text().splitlines(), 1):
                for fields in signatures:
                    if all(f'{key}="{value}"' in line for key, value in fields.items()):
                        diagnostics.append({**record, "line": number, "fields": fields})
    files["retention.json"] = encoded(retention)
    files["diagnostics.json"] = encoded({"projection": "Fixed allowlisted Keycloak event fields; all other fields omitted",
                                          "events": diagnostics})
    for name, data in files.items():
        assert not leaks(data), "Credential value detected in " + name
        assert str(Path.home()).encode() not in data, "Host-home path detected in " + name
        assert not any(path.encode() in data for path in replacements), "Local path detected in " + name
    scan = {"scope": sorted(files), "known_synthetic_values": len(known), "matches": 0,
            "positive_controls": "every known value, private-key marker and synthetic JWT detected",
            "limitations": "This checks retained projections only. Private run state intentionally contains synthetic credentials and is not publishable."}
    files["leak-scan.json"] = encoded(scan)
    args.output.mkdir(mode=0o700)  # Never overwrite a previous export.
    for name, data in files.items():
        (args.output / name).write_bytes(data)
    print(json.dumps({"outcome": retention["outcome"], "events": len(events), "scan": scan}))


def bundle(args):
    results = []
    for number, (directory, receipt_path) in enumerate(args.run, 1):
        source = Path(directory)
        events = json.loads((source / "events.json").read_text())
        pins = next(row for row in events if row["event"] == "pins")
        receipt = json.loads(Path(receipt_path).read_text())
        assert receipt["state"] == "completed" and receipt["head_unchanged"] and receipt["checkout_clean"]
        assert receipt["head"] == pins["bronzeward_revision"]
        assert events[-1]["event"] == "completed"
        assert events[-1]["result"] in ("pass", "failed")
        assert (receipt["rc"] == 0) == (events[-1]["result"] == "pass")
        assert any(row["event"] == "cleanup" for row in events)
        for name, expected in pins["fixture_hashes"].items():
            raw = subprocess.check_output(["git", "-C", str(args.checkout), "show",
                                           pins["bronzeward_revision"] + ":experiments/talosctl-oidc/" + name])
            assert hashlib.sha256(raw).hexdigest() == expected, name
        scan = json.loads((source / "leak-scan.json").read_text())
        assert scan["matches"] == 0
        assert sorted(p.name for p in source.iterdir()) == sorted(scan["scope"] + ["leak-scan.json"])
        commands = [row for row in events if row["event"] == "command"]
        results.append({"run": f"run{number}", "revision": pins["bronzeward_revision"],
                        "result": events[-1]["result"], "seconds": receipt["seconds"], "exit": receipt["rc"],
                        "events": len(events), "commands": len(commands),
                        "matched_command_exits": sum(row["rc"] == row["expected"] for row in commands),
                        "expected_nonzero_commands": sum(row["expected"] != 0 for row in commands),
                        "skips": 0, "cleanup": "pass",
                        "scope_limit": "Later scenarios unexecuted after first failure" if receipt["rc"] else
                        "One complete disposable chain; see report for untested modes"})
    args.output.mkdir(mode=0o700)
    for number, (directory, _) in enumerate(args.run, 1):
        shutil.copytree(directory, args.output / f"run{number}")
    (args.output / "results.json").write_bytes(encoded(results))
    retained = [{"path": str(path.relative_to(args.output)), "sha256": digest(path), "bytes": path.stat().st_size}
                for path in sorted(args.output.rglob("*")) if path.is_file()]
    (args.output / "retention.json").write_bytes(encoded({"retained_sha256": retained,
        "private_state": "All raw logs and usable synthetic credentials excluded; original runs retained separately for acceptance."}))
    print(json.dumps({"runs": len(results), "files": len(retained) + 1}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    run = commands.add_parser("run")
    for name in ("run", "output", "talosctl", "go", "tools", "source", "checkout"):
        run.add_argument("--" + name, type=Path, required=True)
    run.set_defaults(action=export_run)
    combined = commands.add_parser("bundle")
    combined.add_argument("--run", nargs=2, action="append", required=True, metavar=("EXPORT", "RECEIPT"))
    combined.add_argument("--output", type=Path, required=True)
    combined.add_argument("--checkout", type=Path, required=True)
    combined.set_defaults(action=bundle)
    args = parser.parse_args()
    args.action(args)


if __name__ == "__main__":
    main()
