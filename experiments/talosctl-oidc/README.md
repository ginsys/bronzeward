# talosctl-oidc credential research fixture

Research for [#162](https://github.com/ginsys/bronzeward/issues/162); see the
[report](../../docs/design/research/20261021-talosctl-oidc.md) for interpretation,
alternatives and limits. One complete chain passed on 21 October 2026; two
earlier login failures remain recorded at their original revisions.

The original `rpc.go` probe uses an explicitly supplied disposable talosconfig
and endpoint. It reports certificate metadata and gRPC status without printing
private keys. `mint` writes the returned synthetic credential to a new mode-0600
file; that file is private run state and must never enter retained evidence.
`lifetime` compares calls on one held connection with a new connection after
the certificate's actual expiry, counting TCP dials to detect reconnection.

This fixture is separate from the accepted PoC and its running demo. It does not
change Bronzeward authentication, approval or provider policy.

## Recorded results

| Revision | Result | Coverage |
| --- | --- | --- |
| `faae408` | Failed, 191.380 seconds | First CLI login timed out because the Python browser withheld Secure loopback cookies. Later cases did not run; scoped teardown passed. |
| `aaceff9` | Failed, 142.382 seconds | Browser callback passed; Keycloak rejected the CLI's default offline-access request. Later cases did not run; scoped teardown passed. |
| `3ddf133` | Passed, 564.516 seconds | 81 events; all 30 command exits matched (25 zero, five nonzero), HTTP/certificate checks passed, no skips; scoped teardown passed. |

[Results](evidence/results.json), [successful events](evidence/run3/events.json),
[issuance audit](evidence/run3/audit.json), [browser metadata](evidence/run3/browser.json)
and [retained hashes](evidence/retention.json) preserve the evidence. Each run
directory also records its projection leak-scan result and private-log hashes.
Known synthetic-value, private-key and JWT checks detected no exported secrets;
positive controls passed. Raw logs and usable synthetic credentials are excluded.

The tests demonstrate certificate renewal, role denial, cached-token behavior
after removal, existing connections beyond certificate expiry, administrator
reminting, issuer/exchange outages and Talos interruption/recovery. They do not
establish immediate revocation, a hard administrator-access duration bound,
direct-command audit or production readiness. Automatic ID-token refresh after
natural expiry, offline access and background watch mode were not exercised.

## Reproduction

Prerequisites: Linux amd64, Docker, Python 3 with PyYAML, Go 1.27.1, and the
Talos 1.13.6 CLI. Use a disposable host with capacity for one 2 GiB Talos node
and one 1 GiB Keycloak container. The fixed `10.61.162.0/24` subnet must not
overlap an existing Docker network; the runner refuses an overlap. Container
names are unique and published ports bind to loopback. Keycloak 26.8.1 is a
test dependency, not a selected Bronzeward identity provider.

Clone `qjoly/talosctl-oidc` and check out
`d3cd122341537d3235bd5dfe7ced2171ec765227` without modifying it. With
`SOURCE`, `TOOLS`, `TMPDIR` and `GOTMPDIR` pointing to disposable disk-backed
directories, build from that source and this repository:

```sh
go -C "$SOURCE" build -mod=readonly -trimpath -p=2 -o "$TOOLS/talosctl-oidc" .
go build -mod=readonly -trimpath -p=2 -o "$TOOLS/rpc" experiments/talosctl-oidc/rpc.go
python3 experiments/talosctl-oidc/run.py \
  --output "$RUN" --tmp "$GOTMPDIR" --talosctl "$TALOSCTL" \
  --rpc "$TOOLS/rpc" --exchange "$TOOLS/talosctl-oidc" \
  --source "$SOURCE" --go "$GO"
```

`RUN` must not exist. The runner creates private mode-0700 state beneath it;
synthetic passwords, token cache, certificates, private keys and raw command
logs stay there. Never commit that directory. The separate `events.json` and
`audit.json` projections omit credential values, but still require inspection
and a leak scan before retention. Commands retain expected and actual exits,
elapsed time and hashes of their private output. A failure stops the chain and
preserves its evidence; run again only in a fresh directory.
`GO` names the Go 1.27.1 executable. The runner checks the upstream commit and
cleanliness, built exchange revision/toolchain, probe machinery version and
Talos CLI checksum before starting services. It records the Bronzeward revision
and verifies both checkouts and fixture hashes remain unchanged after teardown.

The browser helper completes the upstream CLI's authorization-code/PKCE flow
with synthetic users against the fixture's loopback origins. HOME, XDG config
and the D-Bus address are isolated from the host's keyring. The exchange uses
its own TLS CA; the IdP uses loopback HTTP in this disposable profile.
The CLI explicitly requests `openid,profile,email`, using normal session refresh
tokens. Its default additional `offline_access` request was rejected by the
imported test realm; this profile does not grant offline access.
Keycloak marks loopback cookies `Secure`; the helper permits those cookies only
on the exact fixture IdP origin, including its port. It refuses other origins
and never sends IdP cookies to the CLI callback. Safe browser diagnostics retain
cookie names/flags and callback success, without cookie values or URL queries.
Run the cookie regression checks without starting services:

```sh
python3 -m unittest discover -s experiments/talosctl-oidc -p 'test_*.py' -v
```

The chain checks login/renewal, explicit reader access and mint denial,
unmatched groups, cached versus fresh credentials after group/account removal,
new versus held connections after expiry, and admin reminting. It measures a
306-second IdP outage across the upstream five-minute key cache, then a cold
exchange restart and recovery. Pausing only its own Talos node checks timeout
and recovery; emergency access cannot work while that same node is paused,
but is checked during the independent IdP/exchange outages and after recovery.
Issuance audit and a direct version RPC are compared separately.

Normal and failed runs attempt scoped teardown in `finally`. Retain the run
directory until evidence is assessed. An interrupted process or failed teardown
requires checking its recorded unique resource names before any cleanup; never
use broad Docker cleanup. Keep fixture files unchanged during a chain.
