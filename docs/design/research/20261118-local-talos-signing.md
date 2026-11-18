# Signing Talos admin certificates locally

18 November 2026. Evidence for [#874](https://github.com/ginsys/bronzeward/issues/874).
Bronzeward can sign its own Talos admin certificates from a cluster's `os` CA key, and a
node accepts them: every case run on the fixture behaved as the machinery source says.
No node refused a locally signed certificate, so the stop condition of the issue did not
fire. The note records evidence only; the contract changes follow in a second pull
request.

## Scope and decision enabled

The owner decided on 18 November 2026, 17:08 CET, answering the access-model question of
[#869](https://github.com/ginsys/bronzeward/issues/869): "I think we must support all
"first keys": TALOSCONFIG, controlplane node configs, or even an ip + secrets.yaml.
Whatever the user provides, bw gets access and saves the signing keys". That reverses the
2026-10-21 deferral of [#266](https://github.com/ginsys/bronzeward/issues/266) (design
§13.2), which kept the CA signing key out of Bronzeward for lack of signing evidence.

This note supplies that evidence: which first keys can yield a signer, what a locally
signed certificate carries, whether a node accepts it, and what expiry and revocation
mean. It enables the contract rewrite (design §13.2, persistence and API §3.3 and choice
§17.29, compilation, the grants test list and `bronzewardctl cluster onboard`). It
settles nothing about who may supply or replace a first key.

Out of scope: product code, a real cluster, CA rotation, any Talos version other than
v1.14.5.

## Pinned sources and inputs

- Talos machinery `github.com/siderolabs/talos/pkg/machinery` v1.14.5 (`go.mod`), the module
  the program is built with. Source read at that tag.
- Offline inputs made with the host's `talosctl` v1.14.6 and `--talos-version v1.14.5`;
  the fixture run used the fixture's pinned `talosctl` and Talos v1.14.5 nodes
  (`fixtures/versions.env`).
- Bronzeward `main` at `9f009866`.
- Every input is synthetic. No key, token or secret value is printed or quoted; the program
  prints subjects, validity, key usage, fingerprints of public certificates and gRPC codes.

## Reproduction

The program is `docs/design/research/evidence/20261118-local-talos-signing/main.go`: one
`package main` behind `//go:build ignore`, so `go build ./...` skips it and nothing imports
it. Its output, with both command lines, is
`docs/design/research/evidence/20261118-local-talos-signing.txt`.

```sh
# offline: synthetic bundle and configurations, then signing from each source
talosctl gen secrets --output-file "$D/secrets.yaml" --talos-version v1.14.5
talosctl gen config synthetic https://192.0.2.10:6443 \
  --with-secrets "$D/secrets.yaml" --output "$D" --talos-version v1.14.5 --force
go run docs/design/research/evidence/20261118-local-talos-signing/main.go offline "$D"

# node: on the disposable fixture cluster (a heavy run)
fixtures/bin/up
go run docs/design/research/evidence/20261118-local-talos-signing/main.go node "$D2" 10.55.0.2
fixtures/bin/down
```

`$D2` holds the fixture's `controlplane.yaml`, `talos-secrets.yaml` renamed `secrets.yaml`,
and `talosconfig`. The node mode dials through `internal/talos.Dial`, the path the server
would use, and reads `Version` and `MachineConfig`.

## Findings

### 1. Which first keys yield a signer

`NewAdminCertificateAndKey(currentTime, ca, roles, ttl)`
(`pkg/machinery/config/generate/secrets/ca.go:67-92`) needs the `os` CA certificate and
its key. Of the three first keys the owner named:

| First key | Holds the `os` CA key | Observed |
| --- | --- | --- |
| Control plane machine configuration | yes, `machine.ca.key` | signs; certificate chains to the CA |
| Endpoint plus secrets bundle | yes, `Certs.OS` | signs; the same CA certificate as the configuration |
| Talosconfig | no: the CA certificate, a client certificate and its key | signing from the CA certificate alone is refused |
| Worker machine configuration | no: `machine.ca.key` is empty | refused |

So a talosconfig is a way in and not a signer. To obtain the key from one, Bronzeward must
read a control plane configuration through it once, which needs `os:admin`
(`internal/talos/live_test.go`, `TestLiveRoleProbe`; observed again below: `os:reader` is
refused `MachineConfig`). The talosconfig's CA certificate, the control plane
configuration's and the bundle's were the same certificate in both runs.

The refusals read "failed to parse PEM block": the machinery fails when it parses the empty
key, not with a message naming the missing key. A contract that refuses a talosconfig or a
worker configuration as a signer must say so in its own words.

### 2. What a signed certificate carries

Every certificate has subject organisation equal to the role set (sorted), an empty common
name, `KeyUsage = digitalSignature`, `ExtKeyUsage = clientAuth`, `IsCA = false`, an
Ed25519 key, and a random serial; two signatures with the same inputs differ in
certificate, key and serial.

- An empty role set is an error (`ca.go:69`).
- Any role string signs, including one no Talos version defines (`os:bogus`): the library
  checks only that the set is not empty. Whether a node accepts or ignores an unknown role
  was not run.
- There is no lifetime maximum: 60 s, ten years and about 114 years all signed. The CA's own
  validity is ten years (`secrets.go:15`, `CAValidityTime`).
- `NotBefore` is the `currentTime` the caller passes and `NotAfter` is that plus the TTL.

### 3. Clock

A certificate signed at `now+5 s` did not verify at `now+1 s` and a node refused it
(`Unavailable`) when presented at once, then accepted it once the node's clock passed
`NotBefore` (presented 7 s later). A certificate signed with `currentTime = now-1 h` and a
TTL of 2 h was accepted. So the signer's clock and the node's clock must agree to within
the margin the signer builds in; back-dating `NotBefore` is possible and works.

### 4. Node acceptance (fixture, Talos v1.14.5)

| Case | Result |
| --- | --- |
| `os:admin`, CA key from a control plane configuration | `Version` ok; `MachineConfig` ok |
| `os:admin`, CA key from the secrets bundle | `Version` ok; `MachineConfig` ok |
| `os:reader` | `Version` ok; `MachineConfig` `PermissionDenied` |
| `os:admin` signed by an unrelated CA | `Unavailable` |
| `NotBefore` +5 s, presented at once | `Unavailable`; ok 7 s later |
| Expired (signed at now-2 h, TTL 1 h) | `Unavailable` |
| Back-dated (signed at now-1 h, TTL 2 h) | ok |
| Operator's own certificate and a local one, held at the same moment | both ok, both read `MachineConfig` |

The node answers an untrusted or out-of-window certificate with `Unavailable`, the same
code for a foreign CA, a not-yet-valid and an expired certificate: the caller cannot tell
which from the code.

### 5. Expiry and revocation

- Local signing does not retire the operator's access: the operator's own certificate kept
  working beside the local one.
- Talos has no per-certificate revocation (the [OIDC note](20261021-talosctl-oidc.md),
  read again against v1.14.5 source: the certificate is checked against the CA and its
  validity window, not against a list). The only ways to end a certificate's use are its
  `NotAfter`, or replacing the CA, which ends every certificate and every machine
  configuration that carries the old one. CA replacement was not run.
- A held connection outlives `NotAfter`: a connection opened with a 60 s certificate still
  answered 5 s past it, while a new connection with the same certificate was refused.
  One sample; it matches the OIDC note's single samples. So a short TTL bounds new
  connections, and a connection already open stays usable until it closes. Expiry ends
  a certificate's use only where the holder closes its own connection at or before
  `NotAfter`; the node does not.
- An `os:admin` holder, which Bronzeward is once it holds the CA key, can sign any role set
  for any lifetime. Short lifetimes narrow what a leaked certificate grants. They do not
  narrow what the CA key grants.

## Alternatives compared

| | Hold the CA key and sign per use | Hold one admin certificate | Remint through a remote `os:admin` call |
| --- | --- | --- | --- |
| Needs after onboarding | the `os` CA key | a stored admin client key | an admin certificate to start |
| Created and imported clusters | the same | an admin certificate must exist first | an admin certificate must exist first |
| Lifetime and roles | the signer's choice, per use | whatever the stored certificate says | up to the caller's `os:admin` role, no maximum |
| Revocation | expiry, or CA replacement | expiry of the stored certificate | the same |
| Newly held secret | the CA key (ten-year validity) | a one-year client key | none beyond the first certificate |

The owner chose the first column on 17:08 CET. The evidence supports it technically; what
it does to the exposure is in finding 5.

## Expected and observed

| Check | Expected | Observed |
| --- | --- | --- |
| Signing from a control plane configuration | signs | signs |
| Signing from a secrets bundle | signs | signs |
| Signing from a talosconfig's CA certificate | refused | refused |
| Signing from a worker configuration | refused | refused |
| Empty role set | refused | refused |
| `os:reader` reads `MachineConfig` | refused | `PermissionDenied` |
| Foreign CA | refused | `Unavailable` |
| NotBefore ahead of the node clock | refused until it passes | refused, then accepted |
| Expired certificate on a new connection | refused | refused |
| Held connection past `NotAfter` | open | open (one sample) |

## Failure cases and limits

- One Talos version (v1.14.5) on Docker nodes of the fixture, one sample per case. A real
  cluster at another minor version is not covered.
- The fixture's node and the signer share a host clock, so clock skew was only simulated
  by choosing `currentTime`.
- Unknown roles were signed offline and not presented to a node.
- CA rotation and the effect of replacing the CA on a held connection were not run.
- A held connection's survival is one sample; the machinery's gRPC server was not read for
  a deadline that could close it. Because of it, "expiry is the only revocation" holds
  for new connections only: a design that relies on expiry must bound how long it holds a
  connection per certificate, and no measurement here covers how a client does that.
- Whether the machinery's signer is the only signer a node accepts is not claimed: a
  certificate that chains to the CA and carries a role is what the node checked here.
- Who may supply or replace a first key is a decision for the owner and is not settled by
  this note.
