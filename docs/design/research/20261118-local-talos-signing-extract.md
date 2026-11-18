# Signing from a key read through a talosconfig

18 November 2026. Evidence for [#883](https://github.com/ginsys/bronzeward/issues/883), an
addendum to [Signing Talos admin certificates locally](20261118-local-talos-signing.md)
(evidence for [#874](https://github.com/ginsys/bronzeward/issues/874)). On the fixture, a
talosconfig whose certificate has `os:admin` is enough: one read of a control plane's machine
configuration returns the `os` CA key, a certificate signed from that key was accepted by the
node, and the same read with an `os:reader` certificate was refused. One sample per case, one
Talos version.

## What the first note left open

The first note measured two things apart: an `os:admin` read of `MachineConfig` (18627 bytes on
the fixture) and signing from keys that came from local files. It never signed from a key taken
out of what the node returned. The owner's direction of 2026-11-18 17:08 CET, answering
[#869](https://github.com/ginsys/bronzeward/issues/869), was that a talosconfig is a way in:
onboarding with one reads a control plane configuration through it and keeps `machine.ca.key`.
That rests on this step. This note closes it for the fixture.

## Method

The `extract` mode of
`docs/design/research/evidence/20261118-local-talos-signing/main.go`, run on the disposable
fixture (one control plane, one worker, Talos v1.14.5, machinery v1.14.5, Bronzeward `main` at
`19c39500`). Inputs are the fixture's synthetic talosconfig, and its secrets bundle only to
compare the CA certificate. The program:

1. dials `internal/talos.Dial` with the operator's talosconfig and reads `MachineConfig`;
2. decodes the answer with the machinery's `configloader` and takes
   `Machine().Security().IssuingCA()`; with no key in it, it stops and signs nothing;
3. signs a one-hour `os:admin` certificate from that key and presents it;
4. presents a one-hour `os:reader` certificate signed from the same key for the same read;
5. presents an `os:admin` certificate signed by a fresh unrelated CA key, as a negative
   control for step 3.

No key is printed: the program prints presence, a size, fingerprints of public certificates and
gRPC codes. Command lines and the whole output are in
`docs/design/research/evidence/20261118-local-talos-signing-extract.txt`.

## Observed

| Step | Observed |
|---|---|
| 1 read as the operator's `os:admin` talosconfig | `MachineConfig` ok, 18627 bytes |
| 2 the answer | os CA key present (135 bytes); its certificate equals the bundle's and the talosconfig's |
| 3 `os:admin` signed from the extracted key | chain verifies; `Version` ok v1.14.5; `MachineConfig` ok, 18627 bytes |
| 4 `os:reader` signed from the extracted key, same read | `Version` ok; `MachineConfig` `PermissionDenied` |
| 5 `os:admin` from a fresh unrelated CA | `Unavailable` on both calls |

What this shows: the key that signs came from the node's answer to a read an `os:admin`
talosconfig is allowed to make, and a node accepts what it signs. A talosconfig whose
certificate is `os:reader` cannot make that read, so it cannot lead to a signer.

Step 5 recorded `Unavailable`, the gRPC code of a failed connection, which an unreachable
node also returns. The node had answered step 4 seconds earlier, so a failed TLS handshake is
the likely cause, but the run recorded neither the TLS alert nor a reachability call after
step 5. The control is therefore consistent with the node checking the issuer and does not
isolate it.

## Limits

- One run, one sample per case, one control plane, Talos v1.14.5, machinery v1.14.5, the
  fixture's synthetic configuration. A cluster with more `machine.acceptedCAs` than the issuing
  CA, a rotated CA or another Talos version is unmeasured.
- The unrelated-CA control (step 5) has no TLS alert and no reachability call after it, as
  described under the table.
- The read returns the key because the node's `MachineConfig` resource is the whole
  configuration. That is the node's behavior at this version and not a stated contract; a later
  Talos version may redact it, which would end the path, so a contract that relies on it names
  the version it was measured on.
- A talosconfig whose endpoints reach no control plane was not run here. The first note
  measured that a worker configuration file holds no CA key (finding 1); what a worker node
  answers to a `MachineConfig` read was not measured. Refusing such an endpoint set is a rule
  of the contract, not a measurement of the node.
- The first note's limits still hold: revocation is untested and CA rotation is unmeasured.
