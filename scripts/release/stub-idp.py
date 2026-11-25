#!/usr/bin/env python3
"""A test double for an OIDC identity provider, for scripts/release/run-packaged-darwin.sh
(ginsys/bronzeward#405). It is not Zitadel and says nothing about a provider's behaviour: it answers
exactly what bronzewardctl's device sign-in and logout ask for (discovery, device authorization,
the device-code token request, revocation), on a loopback port, with synthetic tokens. It also answers
GET /livez with 204, so that `context add` accepts it as a server; the script then stops it, and the
server is gone.

    stub-idp.py <port file> <log file>

It writes its port to <port file> once it listens, and appends one line to <log file> for every
request: "<path>" and, for the revocation endpoint, " token=<the revoked token>".
"""
import http.server
import json
import sys
import urllib.parse

ACCESS, REFRESH = "BWSYNTH-access", "BWSYNTH-refresh"
port_file, log_file = sys.argv[1], sys.argv[2]


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send_json(self, body, status=200):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        self.record("")
        base = f"http://127.0.0.1:{self.server.server_port}"
        if self.path == "/.well-known/openid-configuration":
            self.send_json({
                "issuer": base,
                "authorization_endpoint": base + "/authorize",
                "token_endpoint": base + "/token",
                "device_authorization_endpoint": base + "/device",
                "revocation_endpoint": base + "/revoke",
                "jwks_uri": base + "/jwks",
                "response_types_supported": ["code"],
                "subject_types_supported": ["public"],
                "id_token_signing_alg_values_supported": ["RS256"],
            })
        elif self.path == "/jwks":
            self.send_json({"keys": []})
        elif self.path == "/livez":
            # The server stand-in: `context add` wants a 204 here, and nothing else of a server is answered.
            self.send_response(204)
            self.end_headers()
        else:
            self.send_json({"error": "not_found"}, 404)

    def do_POST(self):
        form = urllib.parse.parse_qs(self.rfile.read(int(self.headers.get("Content-Length", 0))).decode())
        self.record(" token=" + form["token"][0] if self.path == "/revoke" and "token" in form else "")
        if self.path == "/device":
            self.send_json({"device_code": "BWSYNTH-device", "user_code": "BWSYNTH-CODE",
                            "verification_uri": f"http://127.0.0.1:{self.server.server_port}/verify",
                            "expires_in": 300, "interval": 1})
        elif self.path == "/token" and form.get("device_code") == ["BWSYNTH-device"]:
            self.send_json({"access_token": ACCESS, "refresh_token": REFRESH, "token_type": "Bearer", "expires_in": 300})
        elif self.path == "/revoke":
            self.send_json({})
        else:
            self.send_json({"error": "invalid_request"}, 400)

    def record(self, extra):
        with open(log_file, "a") as f:
            f.write(self.path + extra + "\n")


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
with open(port_file, "w") as f:
    f.write(str(server.server_port))
server.serve_forever()
