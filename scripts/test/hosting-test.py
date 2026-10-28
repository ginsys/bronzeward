#!/usr/bin/env python3
"""Serve a release web tarball through its rendered Caddyfile and check what Caddy answers
(ginsys/bronzeward#401, #262 P5; client contracts §10.1-§10.2).

Usage: hosting-test.py <directory holding one bronzeward-web_*.tar.gz>

The pinned Caddy image (packaging/versions.env) runs the archive's Caddyfile on a private Docker
network, with a second Caddy standing in for the Bronzeward server. Every page path must answer
with exactly web/headers.json's set, its placeholders filled with the test's values, and the
application as fallback; /api/v1/ and /livez must reach the server. Needs Docker.
"""

import glob
import json
import os
import shutil
import ssl
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
ISSUER = "https://idp.example.invalid"
PROVIDERS = "https://idp.example.invalid https://token.example.invalid"
UPSTREAM_MARK = "X-Bw-Test-Upstream"


def pin(name):
    with open(os.path.join(ROOT, "packaging", "versions.env"), encoding="utf-8") as f:
        for line in f:
            if line.startswith(name + "="):
                return line.split("=", 1)[1].strip()
    raise SystemExit(f"hosting-test: no {name} in packaging/versions.env")


def docker(*args):
    return subprocess.run(["docker", *args], check=True, capture_output=True, text=True).stdout.strip()


def fetch(base, path, headers=None):
    ctx = ssl.create_default_context()
    ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE  # Caddy's internal CA, for localhost
    req = urllib.request.Request(base + path, headers=headers or {})
    try:
        with urllib.request.urlopen(req, context=ctx, timeout=5) as r:
            return r.status, r.headers, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.headers, e.read()


def read(path):
    with open(path, "rb") as f:
        return f.read()


def check(base, web, expected):
    failures = []
    index = read(os.path.join(web, "dist", "index.html"))
    asset = "/" + os.path.relpath(glob.glob(os.path.join(web, "dist", "assets", "*.js"))[0], os.path.join(web, "dist"))
    pages = {
        "/": index,
        "/index.html": index,
        asset: read(os.path.join(web, "dist", asset.lstrip("/"))),
        "/bronzeward-client.json": read(os.path.join(web, "bronzeward-client.example.json")),
        "/machines/m1/history": index,  # a client route: the application
        "/api/v1": index,  # not under /api/v1/
        "/livez/x": index,
        "/assets/missing.js": index,
    }
    for path, body in pages.items():
        for extra in ({}, {"Range": "bytes=0-0"}):
            status, got, data = fetch(base, path, extra)
            where = f"GET {path}" + (" with a Range" if extra else "")
            if status != 200 or data != body:
                failures.append(f"{where}: {status}, {len(data)} bytes, not the expected {len(body)}")
            for name, value in expected.items():
                if got.get(name) != value:
                    failures.append(f"{where}: {name} is {got.get(name)!r}, want {value!r}")
            if got.get(UPSTREAM_MARK):
                failures.append(f"{where}: answered by the server, not the web files")
    for path in ("/livez", "/api/v1/server", "/api/v1/machines?limit=1"):
        status, got, _ = fetch(base, path)
        if got.get(UPSTREAM_MARK) != "server":
            failures.append(f"GET {path}: {status}, not routed to the server")
    return failures


def main(argv):
    if len(argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    tarballs = glob.glob(os.path.join(argv[1], "bronzeward-web_*.tar.gz"))
    if len(tarballs) != 1:
        print(f"hosting-test: want one bronzeward-web_*.tar.gz in {argv[1]}, found {len(tarballs)}", file=sys.stderr)
        return 1
    with open(os.path.join(ROOT, "web", "headers.json"), encoding="utf-8") as f:
        expected = {k: v.replace("{providerOrigins}", PROVIDERS).replace("{issuerOrigin}", ISSUER) for k, v in json.load(f).items()}
    image = pin("CADDY_IMAGE")
    work = tempfile.mkdtemp(prefix="w3-hosting-")
    name = f"bw-hosting-test-{os.getpid()}"
    try:
        with tarfile.open(tarballs[0]) as t:
            t.extractall(work, filter="data")
        web = glob.glob(os.path.join(work, "bronzeward-web_*"))[0]
        os.chmod(work, 0o755)
        shutil.copy(os.path.join(web, "bronzeward-client.example.json"), os.path.join(web, "dist", "bronzeward-client.json"))
        docker("network", "create", name)
        docker("run", "-d", "--name", name + "-server", "--network", name, image,
               "caddy", "respond", "--listen", ":8080", "--header", f"{UPSTREAM_MARK}: server", "server")
        docker("run", "-d", "--name", name + "-web", "--network", name, "-p", "127.0.0.1::443",
               "-v", f"{web}:/srv:ro",
               "-e", "BW_SITE_ADDRESS=https://localhost", "-e", f"BW_SERVER={name}-server:8080",
               "-e", "BW_WEB_ROOT=/srv/dist", "-e", f"BW_ISSUER_ORIGIN={ISSUER}", "-e", f"BW_PROVIDER_ORIGINS={PROVIDERS}",
               image, "caddy", "run", "--config", "/srv/Caddyfile", "--adapter", "caddyfile")
        port = docker("port", name + "-web", "443/tcp").splitlines()[0].rsplit(":", 1)[1]
        base = f"https://localhost:{port}"
        for _ in range(60):
            try:
                fetch(base, "/")
                break
            except (OSError, ssl.SSLError):
                time.sleep(0.5)
        failures = check(base, web, expected)
        if failures:
            print(docker("logs", name + "-web"), file=sys.stderr)
        for f in failures:
            print("FAIL:", f, file=sys.stderr)
        print(f"hosting-test: {image.split('@')[0]}: {'failed' if failures else 'every page has the header set; /api/v1/ and /livez reach the server'}")
        return 1 if failures else 0
    finally:
        # Best effort: what was never created has nothing to remove.
        subprocess.run(["docker", "rm", "-f", name + "-web", name + "-server"], check=False, capture_output=True)
        subprocess.run(["docker", "network", "rm", name], check=False, capture_output=True)
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
