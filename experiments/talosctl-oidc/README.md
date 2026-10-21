# talosctl-oidc credential research fixture

Work in progress for [#162](https://github.com/ginsys/bronzeward/issues/162).
No local execution result is claimed yet.

The original `rpc.go` probe uses an explicitly supplied disposable talosconfig
and endpoint. It reports certificate metadata and gRPC status without printing
private keys. `mint` writes the returned synthetic credential to a new mode-0600
file; that file is private run state and must never enter retained evidence.
`lifetime` compares calls on one held connection with a new connection after
the certificate's actual expiry, counting TCP dials to detect reconnection.

This fixture is separate from the accepted PoC and its running demo. It does not
change Bronzeward authentication, approval or provider policy. Reproduction,
complete scenario results and limitations will be added after execution.

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
