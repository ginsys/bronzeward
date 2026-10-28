#!/usr/bin/env python3
"""Tests for scripts/release/render-caddy.py: web/headers.json renders with its two placeholders
as Caddy environment placeholders, and a header the Caddyfile could not carry verbatim is refused.
mise run hosting checks what Caddy then serves."""

import importlib.util
import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
spec = importlib.util.spec_from_file_location("render_caddy", os.path.join(ROOT, "scripts", "release", "render-caddy.py"))
rc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rc)

failures = []


def refused(name, headers):
    try:
        rc.render(headers)
    except ValueError:
        return
    failures.append(f"{name}: rendered, want a refusal")


with open(os.path.join(ROOT, "web", "headers.json"), encoding="utf-8") as f:
    real = json.load(f)
out = rc.render(real)
for name in real:
    if out.count(f"\t\t\t{name} \"") != 1:
        failures.append(f"web/headers.json: {name} is not rendered once")
for want in ("connect-src 'self' {$BW_PROVIDER_ORIGINS};", "form-action {$BW_ISSUER_ORIGIN};"):
    if want not in out:
        failures.append(f"web/headers.json: the rendering lacks {want!r}")
if "{providerOrigins}" in out or "{issuerOrigin}" in out:
    failures.append("web/headers.json: a placeholder is left unfilled")

refused("a double quote", {"X-A": 'a "b"'})
refused("a backslash", {"X-A": "a\\b"})
refused("a newline", {"X-A": "a\nb"})
refused("a backtick", {"X-A": "a`b"})
refused("a non-string value", {"X-A": 1})
refused("a name with a space", {"X A": "b"})
refused("an unknown placeholder", {"X-A": "{host}"})
refused("a Caddy environment placeholder", {"X-A": "{$HOME}"})
refused("a near miss of one", {"X-A": "{$BW_ISSUER_ORIGINS}"})

for f in failures:
    print("FAIL:", f, file=sys.stderr)
print(f"render-caddy: {'failed' if failures else 'every case passed'}")
sys.exit(1 if failures else 0)
