#!/usr/bin/env python3
"""Run one disposable Talos/Keycloak research chain; retain private state separately."""

import argparse
import base64
import datetime as dt
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import ssl
import subprocess
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener, HTTPSHandler

import yaml


SOURCE = "d3cd122341537d3235bd5dfe7ced2171ec765227"
TALOS = "ghcr.io/siderolabs/talos:v1.13.6@sha256:f2e2b7e5812b2b59c1acfe6af7516231aeeef79fb1ffff6b57ad987f8dd47a6e"
KEYCLOAK = "quay.io/keycloak/keycloak:26.8.1@sha256:ed1cab21814c7d18ce8f0e11d5a1fa1c6923de12dd4c7625a18c5d34452a57a4"


def utc():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Fixture:
    def __init__(self, args):
        self.args = args
        self.root = args.output.resolve()
        self.root.mkdir(mode=0o700)  # A new directory is mandatory, including after failure.
        self.private = self.root / "private"
        self.private.mkdir(mode=0o700)
        self.logs = self.private / "commands"
        self.logs.mkdir()
        self.events = []
        self.name = "bw-oidc162-" + secrets.token_hex(4)
        self.idp_name = self.name + "-idp"
        self.idp_id = None
        self.node_id = None
        self.exchange_process = None
        self.exchange_log = None
        self.cluster_attempted = False
        self.env = {"PATH": os.environ["PATH"], "HOME": str(self.private / "home"),
                    "XDG_CONFIG_HOME": str(self.private / "config"),
                    "DBUS_SESSION_BUS_ADDRESS": "unix:path=" + str(self.private / "absent-dbus"),
                    "TMPDIR": str(args.tmp), "GOTMPDIR": str(args.tmp), "LANG": "C.UTF-8"}
        (self.private / "home").mkdir()
        (self.private / "config").mkdir()
        self.idp_url = "http://127.0.0.1:" + str(port())
        self.issuer = self.idp_url + "/realms/fixture"
        self.exchange_url = "https://127.0.0.1:" + str(port())
        self.callback_port = port()
        self.callback = "http://127.0.0.1:" + str(self.callback_port) + "/callback"
        self.password = secrets.token_urlsafe(24)
        self.admin_password = secrets.token_urlsafe(24)
        self.emergency = self.private / "emergency.talosconfig"
        self.audit = self.private / "audit.jsonl"
        self.ca = self.private / "exchange-tls/ca.crt"
        self.no_proxy = build_opener(ProxyHandler({}))
        self.before_ids = set(self.docker("ps", "-aq").split())
        self.fixture_hashes = {p.name: digest(p) for p in Path(__file__).parent.iterdir() if p.is_file()}
        self.checkout = Path(__file__).resolve().parents[2]
        self.revision = self.git(self.checkout, "rev-parse", "HEAD")
        assert not self.git(self.checkout, "status", "--porcelain=v1"), "fixture checkout must be clean"
        assert self.git(args.source, "rev-parse", "HEAD") == SOURCE
        assert not self.git(args.source, "status", "--porcelain=v1"), "upstream checkout must be clean"

    def git(self, path, *args):
        return subprocess.check_output(["git", "-C", str(path), *args], env=self.env, text=True).strip()

    def event(self, kind, **data):
        self.events.append({"event": kind, "utc": utc(), **data})
        (self.root / "events.json").write_text(json.dumps(self.events, indent=2) + "\n")
        print(kind + ": " + str(data.get("name", data.get("result", "recorded"))), flush=True)

    def command(self, name, argv, expected=0, timeout=90, extra_env=None):
        number = len(self.events)
        out = self.logs / f"{number:03d}-{name}.out"
        err = out.with_suffix(".err")
        started = time.monotonic()
        with out.open("wb") as stdout, err.open("wb") as stderr:
            proc = subprocess.Popen([str(a) for a in argv], env=self.env | (extra_env or {}),
                                    stdout=stdout, stderr=stderr, start_new_session=True)
            try:
                rc = proc.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGTERM)
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait(timeout=5)
                rc = 124
            except BaseException:
                os.killpg(proc.pid, signal.SIGTERM)
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait(timeout=5)
                raise
        self.event("command", name=name, argv=[str(a).replace(str(self.private), "$PRIVATE") for a in argv],
                   rc=rc, expected=expected, seconds=round(time.monotonic()-started, 3),
                   stdout_sha256=digest(out), stderr_sha256=digest(err))
        assert rc == expected, f"{name}: exit {rc}, expected {expected}; private logs retained"
        return out.read_text()

    def docker(self, *args):
        return subprocess.check_output(["docker", *args], env=self.env, text=True).strip()

    def http(self, url, method="GET", data=None, form=None, bearer=None, timeout=15):
        headers = {}
        if form is not None:
            raw = urlencode(form).encode()
            headers["Content-Type"] = "application/x-www-form-urlencoded"
        elif data is not None:
            raw = json.dumps(data).encode()
            headers["Content-Type"] = "application/json"
        else:
            raw = None
        if bearer:
            headers["Authorization"] = "Bearer " + bearer
        opener = self.no_proxy
        if url.startswith(self.exchange_url):
            opener = build_opener(ProxyHandler({}), HTTPSHandler(context=ssl.create_default_context(cafile=self.ca)))
        try:
            response = opener.open(Request(url, data=raw, headers=headers, method=method), timeout=timeout)
        except HTTPError as error:
            response = error
        with response:
            content = response.read()
            if not content:
                result = None
            elif "application/json" in response.headers.get("Content-Type", ""):
                result = json.loads(content)
            else:
                result = content.decode()
            return response.status, result

    def wait_http(self, url, seconds=120):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            try:
                if self.http(url)[0] == 200:
                    return
            except (OSError, URLError):
                pass
            time.sleep(1)
        raise RuntimeError("fixture endpoint did not become ready")

    def admin(self, method, path, data=None):
        code, token = self.http(self.idp_url + "/realms/master/protocol/openid-connect/token", "POST",
                                form={"grant_type": "password", "client_id": "admin-cli",
                                      "username": "fixture-admin", "password": self.admin_password})
        assert code == 200
        code, response = self.http(self.idp_url + "/admin/realms/fixture/" + path,
                                   method, data=data, bearer=token["access_token"])
        assert code in (200, 204), f"IdP fixture administration returned {code}"
        self.event("idp-admin", name=method + " " + path, status=code)
        return response

    def setup(self):
        build_info = self.command("exchange-build-info", [self.args.go, "version", "-m", self.args.exchange])
        assert "go1.27.1" in build_info and "vcs.revision=" + SOURCE in build_info
        assert "vcs.modified=false" in build_info
        probe_info = self.command("probe-build-info", [self.args.go, "version", "-m", self.args.rpc])
        assert "go1.27.1" in probe_info and "github.com/siderolabs/talos/pkg/machinery\tv1.13.6" in probe_info
        assert digest(self.args.talosctl) == "540c5e7cb0d3fa3a9b2e1c717ced212727b73bcaf0cf9cf9ba2472ec381041d4"
        subnet = ipaddress.ip_network("10.61.162.0/24")
        networks = json.loads(self.docker("network", "inspect", *self.docker("network", "ls", "-q").split()))
        for network in networks:
            for cfg in network["IPAM"]["Config"] or []:
                if cfg.get("Subnet"):
                    other = ipaddress.ip_network(cfg["Subnet"])
                    assert other.version != subnet.version or not subnet.overlaps(other), "fixture subnet overlaps existing network"
        self.event("pins", upstream=SOURCE, talos_image=TALOS, keycloak_image=KEYCLOAK,
                   bronzeward_revision=self.revision, kubernetes="1.36.2", go="1.27.1", talosctl_sha256=digest(self.args.talosctl),
                   rpc_sha256=digest(self.args.rpc), exchange_binary_sha256=digest(self.args.exchange),
                   fixture_hashes=self.fixture_hashes, cluster=self.name, subnet=str(subnet))
        self.command("keycloak-pull", ["docker", "pull", KEYCLOAK], timeout=300)
        self.cluster_attempted = True
        self.command("talos-create", [self.args.talosctl, "cluster", "create", "docker", "--name", self.name,
                     "--state", self.private / "cluster", "--workers", "0", "--subnet", str(subnet),
                     "--image", TALOS, "--kubernetes-version", "1.36.2", "--host-ip", "127.0.0.1",
                     "--cpus-controlplanes", "2", "--memory-controlplanes", "2GiB",
                     "--talosconfig-destination", self.emergency], timeout=600)
        node = json.loads(self.docker("inspect", self.name + "-controlplane-1"))[0]
        self.node_id = node["Id"]
        binding = node["NetworkSettings"]["Ports"]["50000/tcp"][0]
        assert binding["HostIp"] == "127.0.0.1"
        self.endpoint = "127.0.0.1:" + binding["HostPort"]
        self.probe("emergency-baseline", self.emergency)
        users = [{"username": name, "enabled": True, "emailVerified": True,
                  "email": name + "@example.test", "firstName": "Synthetic", "lastName": name,
                  "credentials": [{"type": "password", "value": self.password, "temporary": False}],
                  "groups": groups} for name, groups in
                 [("reader", ["readers"]), ("admin", ["admins"]), ("unmatched", []),
                  ("disabled", ["readers"]), ("outage", ["readers"])]]
        realm = {"realm": "fixture", "enabled": True, "sslRequired": "none",
                 "accessTokenLifespan": 900, "ssoSessionIdleTimeout": 1800, "ssoSessionMaxLifespan": 1800,
                 "groups": [{"name": "readers"}, {"name": "admins"}], "users": users,
                 "clients": [{"clientId": "talos-fixture", "enabled": True, "publicClient": True,
                              "protocol": "openid-connect", "standardFlowEnabled": True,
                              "directAccessGrantsEnabled": False, "redirectUris": [self.callback],
                              "attributes": {"pkce.code.challenge.method": "S256"},
                              "protocolMappers": [{"name": "groups", "protocol": "openid-connect",
                                                   "protocolMapper": "oidc-group-membership-mapper",
                                                   "config": {"claim.name": "groups", "full.path": "false",
                                                              "id.token.claim": "true", "access.token.claim": "true"}}]}]}
        realm_file = self.private / "fixture-realm.json"
        realm_file.write_text(json.dumps(realm))
        env_file = self.private / "keycloak.env"
        env_file.write_text("KC_BOOTSTRAP_ADMIN_USERNAME=fixture-admin\nKC_BOOTSTRAP_ADMIN_PASSWORD=" + self.admin_password + "\n")
        self.idp_id = self.command("keycloak-start", ["docker", "run", "-d", "--name", self.idp_name,
                           "--memory", "1g", "--cpus", "1.5", "--env-file", env_file,
                           "-p", self.idp_url.removeprefix("http://") + ":8080",
                           "--mount", f"type=bind,src={realm_file},dst=/opt/keycloak/data/import/fixture-realm.json,readonly",
                           KEYCLOAK, "start-dev", "--import-realm", "--hostname", self.idp_url]).strip()
        self.wait_http(self.issuer + "/.well-known/openid-configuration")
        cfg = {"talos_config": str(self.emergency), "issuer_url": self.issuer, "client_id": "talos-fixture",
               "listen": self.exchange_url.removeprefix("https://"), "endpoints": [self.endpoint],
               "cert_ttl": "45s", "roles": [], "data_dir": str(self.private / "exchange-tls"),
               "audit_log": str(self.audit), "rbac": {"rules": [
                   {"claim": "groups", "value": "readers", "roles": ["os:reader"]},
                   {"claim": "groups", "value": "admins", "roles": ["os:admin"]}]}}
        (self.private / "exchange.yaml").write_text(yaml.safe_dump(cfg))
        self.start_exchange()

    def start_exchange(self):
        self.exchange_log = (self.private / "exchange.log").open("ab")
        self.exchange_process = subprocess.Popen([str(self.args.exchange), "serve", "--config", str(self.private / "exchange.yaml")],
                                                 env=self.env, stdout=self.exchange_log, stderr=self.exchange_log)
        self.wait_http(self.exchange_url + "/healthz", 30)
        self.event("exchange-start", pid=self.exchange_process.pid)

    def stop_exchange(self):
        if self.exchange_process:
            self.exchange_process.terminate()
            try:
                self.exchange_process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.exchange_process.kill()
                self.exchange_process.wait(timeout=5)
            self.exchange_process = None
            self.exchange_log.close()

    def login(self, user, name=None, expected=0):
        name = name or user
        path = self.private / (name + ".talosconfig")
        output = self.command("login-" + name, [self.args.exchange, "login", "--provider", self.issuer,
                              "--client-id", "talos-fixture", "--server", self.exchange_url, "--server-ca", self.ca,
                              "--talosconfig", path, "--context-name", name, "--callback-port", self.callback_port,
                              "--browser-command", Path(__file__).with_name("browser.py")], expected=expected, timeout=45,
                              extra_env={"BW_IDP_URL": self.idp_url, "BW_CALLBACK_URL": self.callback,
                                         "BW_BROWSER_DIAGNOSTIC": str(self.private / f"browser-{len(self.events)}-{name}.json"),
                                         "BW_LOGIN_USER": user, "BW_LOGIN_PASSWORD": self.password})
        token_file = self.private / "config/talosctl-oidc/tokens.json"
        assert token_file.stat().st_mode & 0o777 == 0o600
        token = json.loads(token_file.read_text())[name]
        payload = token["id_token"].split(".")[1]
        claims = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
        self.event("login", name=name, expected=expected, cached="Using cached OIDC token" in output,
                   token_claims={k: claims.get(k) for k in ["iss", "sub", "iat", "exp", "groups"]},
                   token_cache_mode="0600", refresh_present=bool(token.get("refresh_token")))
        return path, token

    def probe(self, name, cfg, mode="version", expected=0, *extra):
        raw = self.command(name, [self.args.rpc, "-config", cfg, "-endpoint", self.endpoint, "-mode", mode, *extra],
                           expected=expected, timeout=180)
        rows = [json.loads(line) for line in raw.splitlines()]
        self.event("probe", name=name, rows=rows)
        return rows

    def exchange(self, name, token, expected=200):
        start = time.monotonic()
        code, result = self.http(self.exchange_url + "/exchange", "POST", data={"id_token": token["id_token"]})
        self.event("exchange", name=name, status=code, expected=expected, seconds=round(time.monotonic()-start, 3))
        assert code == expected
        return result

    def write_config(self, name, response):
        path = self.private / (name + ".talosconfig")
        context = {"endpoints": [self.endpoint], "ca": base64.b64encode(response["ca"].encode()).decode(),
                   "crt": base64.b64encode(response["cert"].encode()).decode(),
                   "key": base64.b64encode(response["key"].encode()).decode()}
        with path.open("x") as f:
            yaml.safe_dump({"context": name, "contexts": {name: context}}, f)
        return path

    def scenarios(self):
        reader, reader_token = self.login("reader")
        first = self.probe("reader-version", reader)
        assert first[0]["roles"] == ["os:reader"] and first[1]["code"] == "OK"
        reader, _ = self.login("reader")
        renewed = self.probe("renewed-reader-version", reader)
        assert renewed[0]["sha256"] != first[0]["sha256"]
        denied = self.probe("reader-cannot-mint-admin", reader, "mint", 1, "-roles", "os:admin", "-output", str(self.private / "denied.json"))
        assert denied[1]["code"] == "PermissionDenied" and not (self.private / "denied.json").exists()
        self.login("unmatched", expected=1)
        before = len(self.audit.read_text().splitlines())
        self.probe("direct-command-no-issuance-event", reader)
        after = len(self.audit.read_text().splitlines())
        assert before == after
        self.event("direct-audit", before=before, after=after)

        user = self.admin("GET", "users?username=reader&exact=true")[0]
        group = self.admin("GET", "groups?search=readers&exact=true")[0]
        self.admin("DELETE", f"users/{user['id']}/groups/{group['id']}")
        removed_at = time.monotonic()
        self.login("reader")
        self.exchange("removed-group-cached-token", reader_token)
        _, fresh = self.login("reader", "reader-fresh", expected=1)
        self.exchange("removed-group-fresh-token", fresh, 403)
        self.event("group-removal", seconds=round(time.monotonic()-removed_at, 3))

        disabled_cfg, disabled = self.login("disabled")
        user = self.admin("GET", "users?username=disabled&exact=true")[0]
        self.admin("PUT", "users/" + user["id"], {"enabled": False})
        disabled_at = time.monotonic()
        self.exchange("disabled-account-cached-token", disabled)
        status_code, _ = self.http(self.issuer + "/protocol/openid-connect/token", "POST",
                                   form={"grant_type": "refresh_token", "client_id": "talos-fixture", "refresh_token": disabled["refresh_token"]})
        assert status_code == 400
        self.event("disabled-account-refresh", status=status_code, seconds=round(time.monotonic()-disabled_at, 3))
        self.probe("disabled-account-existing-certificate", disabled_cfg)

        issued = self.exchange("reader-expiry-issue", reader_token)
        expiry_cfg = self.write_config("expiry", issued)
        rows = self.probe("certificate-expiry", expiry_cfg, "lifetime")
        by_event = {row["event"]: row for row in rows}
        assert by_event["before_expiry"]["code"] == "OK"
        assert by_event["connection"]["past_not_after"]
        assert by_event["connection"]["dials_before"] == by_event["connection"]["dials_after"]
        assert by_event["held_after_expiry"]["code"] == "OK"
        assert by_event["new_after_expiry"]["code"] != "OK"

        admin_cfg, _ = self.login("admin")
        minted_path = self.private / "reminted.json"
        minted = self.probe("admin-remint-one-hour", admin_cfg, "mint", 0, "-roles", "os:admin", "-ttl", "1h", "-output", str(minted_path))
        original_end = dt.datetime.fromisoformat(minted[0]["not_after"].replace("Z", "+00:00"))
        reminted = self.write_config("reminted", json.loads(minted_path.read_text()))
        wait = max(0, original_end.timestamp() - time.time() + 2)
        assert wait < 60
        time.sleep(wait)
        self.probe("original-admin-expired", admin_cfg, expected=1)
        self.probe("reminted-admin-after-original-expiry", reminted)

        _, outage_token = self.login("outage")
        self.docker("stop", "--time", "10", self.idp_id)
        stopped = time.monotonic()
        self.event("idp-stopped", container_id=self.idp_id)
        self.exchange("issuer-outage-warm-cache", outage_token)
        self.probe("emergency-during-issuer-outage", self.emergency)
        self.event("cache-expiry-wait", seconds=306)
        time.sleep(max(0, 306 - (time.monotonic() - stopped)))
        self.exchange("issuer-outage-expired-cache", outage_token)
        self.event("issuer-outage-elapsed", seconds=round(time.monotonic()-stopped, 3))
        self.stop_exchange()
        self.probe("emergency-during-exchange-outage", self.emergency)
        self.start_exchange()
        self.exchange("issuer-outage-cold-restart", outage_token, 401)
        self.probe("emergency-after-cold-restart", self.emergency)
        self.docker("start", self.idp_id)
        self.wait_http(self.issuer + "/.well-known/openid-configuration")
        self.exchange("issuer-restored", outage_token)

        self.docker("pause", self.node_id)
        self.event("talos-paused", container_id=self.node_id)
        try:
            self.probe("emergency-while-talos-unavailable", self.emergency, expected=1)
            try:
                code, _ = self.http(self.exchange_url + "/exchange", "POST", data={"id_token": outage_token["id_token"]}, timeout=8)
            except (TimeoutError, URLError):
                code = 0
            self.event("talos-unavailable-exchange", status=code, client_timeout_seconds=8)
            assert code in (0, 500)
        finally:
            self.docker("unpause", self.node_id)
        self.probe("emergency-after-talos-recovery", self.emergency)
        self.exchange("talos-restored", outage_token)
        audit = [json.loads(line) for line in self.audit.read_text().splitlines()]
        issued = [row for row in audit if row["type"] == "cert_issued"]
        assert issued and all(all(row.get(k) for k in ["subject", "issuer", "email", "request.id", "cert_fingerprint", "roles"]) for row in issued)
        safe = [{key: value for key, value in row.items() if key != "error"} for row in audit]
        (self.root / "audit.json").write_text(json.dumps(safe, indent=2) + "\n")
        self.event("audit", rows=len(audit), issued=len(issued), direct_rpc_events=0,
                   limitation="Issuance identifies a token subject; it does not record subsequent direct Talos commands.")

    def cleanup(self):
        self.stop_exchange()
        if self.node_id:
            node = json.loads(self.docker("inspect", self.node_id))[0]
            if node["State"]["Paused"]:
                self.docker("unpause", self.node_id)
        if self.idp_id:
            assert json.loads(self.docker("inspect", self.idp_id))[0]["Name"] == "/" + self.idp_name
            self.command("keycloak-logs", ["docker", "logs", self.idp_id])
            self.docker("rm", "-f", self.idp_id)
        if self.cluster_attempted:
            self.command("talos-destroy", [self.args.talosctl, "cluster", "destroy", "--name", self.name,
                                           "--state", self.private / "cluster"], timeout=90)
        after_ids = set(self.docker("ps", "-aq").split())
        assert self.before_ids <= after_ids, "a pre-existing container disappeared"
        assert not self.docker("ps", "-aq", "--filter", "name=" + self.name)
        assert not self.docker("network", "ls", "-q", "--filter", "name=" + self.name)
        assert self.fixture_hashes == {p.name: digest(p) for p in Path(__file__).parent.iterdir() if p.is_file()}
        assert self.git(self.checkout, "rev-parse", "HEAD") == self.revision
        assert not self.git(self.checkout, "status", "--porcelain=v1")
        assert self.git(self.args.source, "rev-parse", "HEAD") == SOURCE
        assert not self.git(self.args.source, "status", "--porcelain=v1")
        self.event("cleanup", result="owned resources removed; pre-existing container IDs preserved; fixture unchanged")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ["output", "talosctl", "rpc", "exchange", "tmp", "go", "source"]:
        parser.add_argument("--" + name, required=True, type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    for binary in [args.talosctl, args.rpc, args.exchange, args.go]:
        assert binary.is_file() and os.access(binary, os.X_OK)
    def interrupted(_signal, _frame):
        raise SystemExit("fixture interrupted")
    signal.signal(signal.SIGTERM, interrupted)
    fixture = Fixture(args)
    success = False
    try:
        fixture.setup()
        fixture.scenarios()
        success = True
    except BaseException as error:
        fixture.event("failure", result=type(error).__name__, detail=str(error) if isinstance(error, AssertionError) else "See private logs; no exception payload published")
        raise
    finally:
        fixture.cleanup()
        fixture.event("completed", result="pass" if success else "failed")


if __name__ == "__main__":
    main()
