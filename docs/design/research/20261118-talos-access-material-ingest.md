# Ingesting a cluster's Talos access material

18 November 2026. Research for [#869](https://github.com/ginsys/bronzeward/issues/869).
The recommendation is alternative B, ingesting the talosconfig through the existing
extraction pipeline, with alternative C kept as a later step that reopens
[#266](https://github.com/ginsys/bronzeward/issues/266). The note decides nothing: the
owner decides. No fixture, cluster or OpenBao was run; every measurement is offline.

## Scope and decision enabled

The owner's direction of 18 November 2026, 15:21 CET, is the hypothesis tested:
"importing a cluster means importing a talos config, sanitziing it, and saving the
secrets to openbao, then forgrtting it", and "at this point there's no difference any
more between a created cluster and an imported one".

Today the cluster's talosconfig is written to OpenBao by the operator and is read by
the ingestion and executor identities ([persistence and API
§3.3](../../spec/persistence-api.md#33-talos-access), choice §17.29). The note
compares three alternatives and answers the six questions of the issue. It enables one
owner decision: keep §17.29 (A), ingest the talosconfig (B), or ingest the whole secrets
bundle (C).

Out of scope: multi-tenancy and user-coupled OpenBao identities
([#870](https://github.com/ginsys/bronzeward/issues/870)), and any specification or code
change. A specification change follows the owner's decision.

## Pinned sources and inputs

- Talos v1.14.5, `talosctl` from `TALOSCTL_URL` in `fixtures/versions.env`, checked
  against its `TALOSCTL_SHA256`; machinery source read at the same tag.
- Bronzeward `main` at `84b7eaab`.
- Inputs are synthetic and generated offline: no cluster, no fixture, no real
  credential. Every value below is a key name, a document kind or a certificate
  subject; no key, token or secret value is quoted.

Reproduction (all offline, under a scratch directory, mode 0700):

```sh
talosctl gen secrets --output-file secrets.yaml
talosctl gen config bw-synth https://10.55.0.2:6443 \
  --with-secrets secrets.yaml --output-dir cfg
# cfg/ now holds controlplane.yaml, worker.yaml and talosconfig
for k in crt ca; do   # the talosconfig's client and CA certificates, base64 PEM
  awk -v k="$k:" '$1 == k {print $2}' cfg/talosconfig | base64 -d |
    openssl x509 -noout -subject -dates
done
```

## What a talosconfig, a bundle and a configuration hold

**Finding 1 (read and observed): the cluster's CA keys are already inside a control
plane's machine configuration; only the admin client certificate and key are outside
every machine configuration.**

| Artifact | Holds | Observed (v1.14.5, synthetic) |
| --- | --- | --- |
| Secrets bundle (`secrets.yaml`) | cluster id and secret, bootstrap token, secretbox secret, trustd token, and a certificate and key for each of `etcd`, `k8s`, `k8saggregator`, `k8sserviceaccount` (key only) and `os` | 5 certificate groups; `os` is the Talos API CA |
| Control plane `controlplane.yaml` | `machine.ca.key` (the `os` CA key), `machine.token`, `cluster.token`, `cluster.etcd.ca.key`, and one document each of kind `KubeAPIServerCAConfig`, `KubeAggregatorCAConfig`, `KubeServiceAccountConfig`, `KubeEtcdEncryptionConfig`, `DiscoveryIdentityConfig`, each with its secret | the `os` CA key is a non-empty value |
| Worker `worker.yaml` | `machine.ca.crt`, `machine.token`, `cluster.token`; `machine.ca.key` is present and empty | the worker holds no CA key |
| `talosconfig` | `Context{Endpoints, Nodes, CA, Crt, Key, Auth, Cluster, ProxyURL}`; `Auth` may hold a Basic password or a SideroV1 identity | the client certificate has subject `O=os:admin`, 1 year validity; the CA certificate subject `O=talos`, 10 years |

Sources: `pkg/machinery/client/config/config.go:52-78`,
`pkg/machinery/config/generate/secrets/secrets.go:21-59`, and the synthetic output
above.

The machinery signs the admin client certificate locally: `NewAdminCertificateAndKey`
(`pkg/machinery/config/generate/secrets/ca.go:67`) takes the `os` CA certificate and
key, a role set and a time to live chosen by the caller, and returns a certificate; no
Talos node is called. This is a different route from the one the [OIDC
note](20261021-talosctl-oidc.md) measured, which issues a certificate through a remote
`os:admin` call. Its findings (no maximum lifetime, admin reminting, the executor
contracts) are about that remote call and do not transfer unchanged to local signing.

## The six questions

### 1. What is ingested

Three artifacts can carry the access: the talosconfig (A and B), a secrets bundle, or a
control plane's machine configuration (the last two for C). What exists today is
extracted values, not an assembled bundle: the compiler "never assembles a secrets
bundle" ([compilation §6](../../spec/compilation.md#6-resolution-order-and-composition),
import-base passage). Alternative C therefore needs a bundle assembled from references,
which is new.

A worker's configuration holds no CA key, so only a control plane (or a bundle) can
supply the `os` CA key.

### 2. How it passes the pipeline

For a machine configuration the pipeline already does what the hypothesis says.
Identification "diffs its encoding with and without `RedactSecrets`" through the pinned
machinery (`internal/ingest/doc.go:20-21`; `schemaPointers`,
`internal/ingest/identify.go:225-284`), so every field Talos itself redacts becomes a
reference into a create-only provider generation
([compilation §2.3](../../spec/compilation.md#23-pipeline) steps 3 to 6). At v1.14.5
the redaction covers the machine token, the `os` CA key (`machine.ca.key`), the cluster
token, the etcd CA key, the Kubernetes API server and aggregator CA keys, the service
account key and the discovery cluster secret
(`pkg/machinery/config/types/v1alpha1/v1alpha1_redact_test.go`, `TestRedactSecrets`).
For a registry's TLS client identity it leaves the certificate and the CA bundle in
clear and redacts the key (`TestRedactExtendedSecrets`), the same split a talosconfig
would need.
Design §9.1 step 3 already says adoption extracts "the Talos bundle" into the provider.

**A talosconfig is refused today (read, not run).** It has no `kind` and none of the
keys `machine`, `cluster`, `debug` or `persist`, so the pinned decoder returns
`ErrMissingKind` (`configloader/internal/decoder/decoder.go:179-183`), which is not
`ErrNoConfig` (returned only for zero documents, `configloader.go:43-45`).
`schemaPointers` maps any other error to `RuleSchemaUnloadable`
(`internal/ingest/identify.go:244-249`; the existing cases at
`internal/ingest/identify_test.go:360-362`). So the input is refused rather than read as
secret-free. The consequence for B: it needs a new input kind with its own
identification rule (extract `key`, and `Auth.Basic.Password` and the SideroV1
identity where set; keep endpoints, nodes, `ca` and `crt`). Redaction has no field list
for a talosconfig, so the list is Bronzeward's to own and to keep current. This is
the cost B carries and the compiler section of the specification does not yet name.

### 3. Which identity reads it, and what replaces choice §17.29

Under B no person needs an OpenBao token for the credential: a request carries the
talosconfig and ingestion extracts it into a create-only generation. But the identity
that creates it cannot be today's ingestion identity as it stands. Compilation
[§1](../../spec/compilation.md#1-scope-roles-and-interfaces) splits ingestion from the
compiler "so that the one that creates secrets never reads them" (choice §16.1), and
keeps the talosconfig path disjoint from `secret/data/gen/*` so that "the ingestion
identity still never reads a secret it creates". The same identity also reads the
credential for a `source: machine` ingestion. B therefore needs either a new
create-only identity for access generations (the same kind of narrow identity as the
orphan-report identity, choice §17.30), or an amendment of choice §16.1 for that path.
The note recommends the first. The executor and ingestion identities then read a
generation by reference, recording its path and version, instead of reading the fixed
path `secret/data/access/talos/<cluster id>`. What changes:

- §3.3 stops saying "no request accepts it". A request would carry the talosconfig once,
  as `source: document` already carries a machine configuration with its secrets.
- The operator-written secret, the `bronzewardctl cluster onboard` OpenBao write and the
  `bw-onboard` create-only policy are replaced by the extraction.
- The property "no request can point a read at another secret" is replaced by a
  reference held in the database: a database write could redirect a read to another
  generation. Whether the ingestion and executor grants limit that is not read here
  (limits).
- **The authority to choose which credential Bronzeward uses moves from whoever holds an
  OpenBao write token to an `author`.** This is a design decision inside B, not a side
  effect. It is the question to settle in the specification that follows (who may
  ingest an access credential, and whether it needs an approval step).

### 4. What happens to the 2026-10-21 decision of ginsys/bronzeward#266

The decision ([design §13.2](../Talos_Configuration_and_Machine_Management_Design.md))
keeps "the per-cluster, operator-written, provider-held talosconfig" and says Bronzeward
"does not issue Talos certificates"; issuance is "deferred, not rejected", reopened only
on a Talos lifetime cap, a read-only configuration role, or an exposure requirement that
rotation cannot meet.

- A keeps it as written.
- B changes "operator-written" to "operator-supplied, extracted by ingestion". It issues
  no certificate and holds no CA signing key in the executor path, so the decision's
  second sentence stands. The first sentence is amended.
- C is what the decision deferred. Holding the `os` CA key lets Bronzeward sign any
  role for any validity, and the note's observed CA validity is 10 years. The owner's
  15:21 CET direction is new instruction, but the three conditions are not met, so C
  needs the owner to reopen the decision, and it needs its own evidence (local signing
  with the pinned machinery against a fixture), which this note did not gather.

The blast radius of C is smaller than it first reads: the compiler identity has `read`
on `secret/data/gen/*` (compilation
[§1](../../spec/compilation.md#1-scope-roles-and-interfaces)), where adoption already
puts a control plane's CA keys, so the application already reads them to render
configurations. What C adds is possession of the CA signing key and a local signing
capability in the executor path. It does not contrast with an expiry-bounded
administrator credential, because none exists: design §13.2 rejects that bound and the OIDC note observed
that an `os:admin` holder can mint a longer-lived certificate before expiry and that an
established connection survives expiry
([OIDC note](20261021-talosctl-oidc.md), certificate expiry and administrator reminting
rows).

### 5. Rotation and evidence binding

Today a dispatch binds evidence to the credential's path, KV version and `created_time`
([execution and recovery §3.1](../../spec/execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction),
[persistence and API §3.3](../../spec/persistence-api.md#33-talos-access)), because a
version number alone is not an identity after a metadata delete or a restore.

An access generation is create-only at a path of its own with a random component
([compilation §2.3](../../spec/compilation.md#23-pipeline) step 6), and rotation is a
new generation with the old one not deleted by the PoC
([design §7.8](../Talos_Configuration_and_Machine_Management_Design.md) item 1). That
makes a path collision on re-creation unlikely, but it does not remove the need for
`created_time`: an administrator can still delete a version or the metadata, and a
restore can recreate it, which is why compilation
[§9](../../spec/compilation.md#9-dependency-records) pins every generation to its
`created_time`. B keeps that binding: path, version and `created_time` of the access
generation, read at use as today. Dropping it would be a separate contract change with
its own safety requirement, and the note does not propose it. What B adds is a pointer
on the cluster record to the current access generation, and the pointer's history is
evidence a dispatch can bind.

### 6. Effect on ginsys/bronzeward#855, ginsys/bronzeward#856 and the held PR ginsys/bronzeward#867

[#855](https://github.com/ginsys/bronzeward/issues/855) specified a provider read of
the credential's metadata through a new narrow identity (choice §17.41, owner answer
"A"); [#856](https://github.com/ginsys/bronzeward/issues/856) shows the result in the
web console; [#867](https://github.com/ginsys/bronzeward/pull/867) carries the
specification and is open and idle.

- A: all three stand.
- B: a database record of the access generation says the material was ingested, not
  that the provider still holds it: an administrative deletion or a provider restore
  leaves the record intact. ginsys/bronzeward#867 defines presence from current
  provider metadata, and ginsys/bronzeward#856 asks for distinct present, absent and
  provider-unreachable displays. So B has two ways to go, an owner choice inside it:
  keep the provider metadata read for the current generation (the access-presence
  identity, the deleted-versions answer and the `503` rows of ginsys/bronzeward#867
  stay, aimed at the generation's path instead of the fixed path), or redefine the
  display as "ingested" and amend the scope of ginsys/bronzeward#856. Only the second
  retires most of ginsys/bronzeward#867, and it changes what the console reports. The
  note recommends keeping the provider read, so B changes ginsys/bronzeward#867's
  target path and little else.
- C: as B.

## Alternatives compared

| | A: keep §17.29 | B: ingest the talosconfig | C: ingest the full bundle |
| --- | --- | --- | --- |
| Person touches OpenBao | yes: own token for the write (`cluster onboard` does it) | no, for the credential | no |
| Created cluster same as imported | no: a created cluster has no admin talosconfig unless something signs one | no, unless creation also signs one | yes: both end as a bundle plus signing |
| Newly held by Bronzeward | nothing new | an admin certificate and key, as today, now written by an API | the CA keys in the executor path |
| Rotation | operator writes a new KV version | new generation by ingestion, old kept | mint a new certificate, no operator step |
| Evidence binding | path, version, `created_time` | the same, on the access generation | the same, on the CA key generation, plus the issue record of each certificate |
| Contracts changed | none | §3.3, compilation (new input kind, a new create-only identity), `cluster onboard`, and the path that ginsys/bronzeward#855 and ginsys/bronzeward#867 read | all of B, plus a bundle assembler and a signer, and ginsys/bronzeward#266 reopened |
| Cost | none | a new input kind and identification list; an author-authority decision | B plus new evidence and an owner reopening |
| First read of an existing cluster | needs the operator's admin talosconfig | needs it (the ingested input) | needs a control plane configuration file or an admin talosconfig once |

Reading a node's configuration needs `os:admin` (the access row of the persistence
traceability table). So an existing cluster needs one credential from outside before
Bronzeward can read anything, under every alternative; C only removes the stored
talosconfig after that. If the operator already holds a control plane's configuration
file, `source: document` ingests it today with its CA key extracted and no credential is
needed at all for that step.

On the hypothesis: "no difference between a created and an imported cluster" holds only
if Bronzeward can produce the admin certificate for a cluster it generated, which means
signing locally. That is C's capability. B alone makes an imported cluster's access
look like a created one's only after creation also yields a talosconfig.

## Recommendation

Alternative B, with C kept as the follow-on that created clusters
([design §9.3](../Talos_Configuration_and_Machine_Management_Design.md) step 1) need.

- B tests the hypothesis with the smallest step: the pipeline already extracts and
  forgets; B adds one input kind. It removes the person-with-an-OpenBao-token from
  access ingestion. It leaves
  [#855](https://github.com/ginsys/bronzeward/issues/855) and
  [#867](https://github.com/ginsys/bronzeward/pull/867) standing with a new target
  path, unless the owner also wants the display redefined (section 6).
- It leaves [#266](https://github.com/ginsys/bronzeward/issues/266) standing in its
  second sentence, so no CA signing key joins the executor path on a decision that has
  no signing evidence.
- C is deferred, not rejected, as ginsys/bronzeward#266 itself deferred it, and the reason to reopen it
  is created clusters, not imports.

What B does not solve: it does not make a created cluster the same as an imported one,
it does not remove the first credential an existing cluster needs, and it moves the
choice of the credential to an `author` until the specification adds a guard.

## Expected and observed

| Check | Expected | Observed |
| --- | --- | --- |
| `talosctl version --client` | v1.14.5 | v1.14.5 |
| Worker `machine.ca.key` | empty | empty |
| Control plane `machine.ca.key` | non-empty | non-empty |
| `talosconfig` client certificate subject | `O=os:admin` | `O=os:admin` |
| A talosconfig loaded as a machine configuration | refused | refused by reading (`ErrMissingKind`); not run |

## Failure cases and limits

- The refusal of a talosconfig is read from source, not run. A run through the
  ingestion path would confirm it and is a heavy run; the reading is specific enough
  that the note does not ask for one.
- No read grant on an access generation was measured (compilation §1 says the same of
  the present path), so whether a database write could redirect a read to another
  generation is open.
- Only `os:admin` roles and a synthetic one-year certificate were observed. Role
  restriction, certificate revocation and CA rotation under C were not measured.
- Both control-plane bases of the sensitivity work were control planes
  ([compilation §2.4](../../spec/compilation.md#24-what-identification-does-not-promise));
  the worker observation here is one synthetic file.
- No claim is made about OIDC or Omni contexts: the `Auth` field is read from the type
  only.
