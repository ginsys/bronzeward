# Multi-tenancy: can a signed-in user's identity carry the tenant boundary into OpenBao?

18 November 2026. Research for [#870](https://github.com/ginsys/bronzeward/issues/870).
The note decides nothing: the owner decides. Measurements ran on a standalone throwaway
OpenBao 2.6.1 in dev mode; no fixture, cluster or browser was used.

**Result.** The line the hypothesis reopens is a derived one: [design §13.7 item
1](../Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)
marks "no human or automation identity is given an OpenBao identity" as *derived*, stronger
than the owner's words and than §13.1, so the owner's own decisions are not in question.
OpenBao can enforce a tenant boundary from a signed-in user's identity: a login with a
group claim gives that user's tokens access to the tenant's paths and nothing else (M1),
and a namespace per tenant separates logins, mounts and Transit keys completely (M5). It
cannot do what Bronzeward needs most from the executor path. A user token that may decrypt,
decrypts: OpenBao has no approval concept, so nothing checks the plan (M6). Four of the ten
rows of operations below also run with no user present, and decryption at dispatch runs
after an approval that may be somebody else's, and possibly after a restart. So the user-held identity
fits only what a person does with their own hands, writing a talosconfig into their own
tenant; every other operation keeps a component identity. The recommendation is a
component identity per tenant on one OpenBao namespace per tenant, with the user-held
identity kept for the operator's own writes.

## Scope and decision enabled

The owner's words of 18 November 2026, 15:21 CET: "multi-tenancy: research if identities
can be coupled to the logged-in user, yielding access only to it's own tenants(s), and
avoiding bronzeward needing identities of it's own? of course bw will at some point need an
identity for certain operations, but we could investigate the use cases?"

The note answers the five questions of the issue and enables one decision: which of three
identity models a future multi-tenant design takes, and whether the derived line of §13.7
item 1 changes. Hard multi-tenant isolation stays deferred from version 1
([design §17.2](../Talos_Configuration_and_Machine_Management_Design.md#172-explicitly-deferred-from-version-1)),
and grants are installation-wide in the PoC (§13.7 item 2). Nothing here changes the
specification or code.

Out of scope: the shared fixture, a cluster, a browser, performance and load, and any
real identity provider.

## Pinned sources and inputs

- OpenBao 2.6.1, the image of `OPENBAO_IMAGE` in `fixtures/versions.env`, by digest, in
  dev mode on loopback. The API documentation of the same tag was read for parameters.
- Bronzeward `main` at `9e861683`.
- Inputs are synthetic: a throwaway RSA key signs the tokens of a made-up identity
  provider, and the tenants are "a" and "b". Every measurement call uses a token that a
  `jwt` login minted, never the dev root token; the root token appears only in steps
  labelled ADMIN (setup and the one revoke in M3).

Reproduction: the full scripts and every observed line are in
[the evidence file](evidence/20261118-tenancy-identities.txt). In short:

```sh
docker run -d --rm --name bw870-bao -e BAO_DEV_ROOT_TOKEN_ID=<synthetic> \
  -e BAO_DEV_LISTEN_ADDRESS=0.0.0.0:8200 -p 127.0.0.1:18870:8200 <OPENBAO_IMAGE>
# then, from a directory holding the scripts of the evidence file:
bash setup.sh && bash m6.sh && bash m1_m3.sh && bash m3b.sh && bash m3c.sh && bash m3d.sh \
  && bash m4.sh && bash m4b.sh && bash m5.sh && bash m7.sh && bash m8.sh
```

## What Bronzeward does today

Read from the specification and `internal/config/config.go`, not run.

- Humans sign in to Bronzeward with OIDC. Each request is verified on its own: the
  configured audience (`bronzeward` in the specification's example), at most 60 seconds of
  skew, at most the configured maximum lifetime between `iat` and `exp` (15 minutes by
  default), roles from group claims evaluated per request
  ([persistence and API §10.1](../../spec/persistence-api.md)).
- Four component identities (ingestion, compiler, metadata, executor) are static token
  files in the server's configuration; the orphan-report identity is a fifth, used by the
  `bronzeward orphans` command and not by the server
  ([compilation §1](../../spec/compilation.md#1-scope-roles-and-interfaces),
  [persistence and API §6.4](../../spec/persistence-api.md#64-orphans)). "No role executes:
  the controller dispatches approved plans" (§13.7 item 2).
- Paths already carry the cluster: generations are `gen/<cluster>/<claim>/<value>` and the
  talosconfig is `access/talos/<cluster>`. A tenant would be a set of clusters.
- The database keys most records by `cluster` (18 column definitions named `cluster` in
  `internal/migrate/migrations/0001_schema.sql`). A few tables are installation-wide by
  design: `installation_state` is a singleton row holding the current recovery epoch and
  mode, and `principal`, `act`, `automation_token` and `identity_revocation` carry no
  cluster.

## Measurements

Each line is one sample; see the limits below. Details and exact output are in the
evidence file.

| # | Question | Expected | Observed |
| --- | --- | --- | --- |
| M6 | Can a user token that may decrypt a tenant's artifact key decrypt with no plan or approval? | yes: OpenBao has no approval check | decrypt returned the plaintext (200); the other tenant's key and the other tenant's user were refused (403) |
| M1 | Do group claims map to tenant policies? | own tenant allowed, other refused | own 200; other read and write 403; a user in two tenants 200 and 200; a login with no group 403 on tenant paths |
| M2 | Must the token's audience name OpenBao, and can the token be replayed? | audience required; no replay check | `aud` of `bronzeward` alone refused; with `openbao` accepted; the same token logged in twice, both accepted |
| M3 | How long does the OpenBao token live against the token's `exp`? | follows the role | `exp` 60 s and 3600 s gave 900 s and 899 s, the role's 15 minutes; a role `token_ttl` of 2 minutes gave 119 s |
| M3 | Does removing a group reach an existing token? | at the next login | after a login without the group, the old token was refused and its `identity_policies` read null; an admin `revoke-accessor` also stopped it |
| M3 | Expiry leeway | the documented 150 s default | a token expired 55 s ago logged in, one expired 65 s ago did not (boundary between 55 s and 65 s, cause not established) |
| M4, M4b | Can a user token mint a narrower child per request, and does it matter how the parent holds the policy? | child policies a subset of the parent's | the same policy name `tenant-a`: refused (400, "child policies must be subset of parent") for a parent that held it through an identity group, minted for a parent that held it directly on the role; a direct-role child with `num_uses` 1 read once (200) and was refused the second time (403). In M4 a child of two uses read its one named path (200) and not another (403), and a response-wrapped child unwrapped once and was refused the second time; M4 changed the policy set as well as its source, so M4b is the control |
| M5 | Does a namespace per tenant separate everything? | yes | user a logged in to namespace `ta` and was refused in `tb`; a `ta` token was refused in `tb` and the root namespace; the same key name in both namespaces gave "message authentication failed" across them; a tenant token could not create a namespace |
| M7 | Does create-only KV hold for a token from a login? | create once, no overwrite, no read | `cas=0` create 200; second write, write without `cas`, read, metadata read, delete, destroy and list all 403; a reader token read the value |
| M8 | Does one templated policy serve every tenant? | follows the claim | tenant `a` read own, `b` refused; claim values `*`, `a/../b` and empty logged in and read nothing; a later login of the same subject with tenant `b` made the earlier token lose `a` and gain `b` |

M6 ran first, as planned: it rules the user-held identity out for the executor path, so
everything after it compares component identities.

## The five questions

### 1. Can a user's identity reach OpenBao and enforce the boundary?

Yes for what the user does themselves; the boundary is real. With JWT auth and external
identity groups mapped to per-tenant policies (M1), or one namespace per tenant (M5), the
user's token reaches only the tenant's paths, and a user in two tenants gets both. Three
properties limit what it buys Bronzeward:

- **The audience.** OpenBao accepted a token only when `aud` named it (M2). Bronzeward's own
  API requires its configured audience, so one token valid at both names both audiences and
  is a bearer for either service. It was accepted twice (M2): OpenBao did not refuse a
  replayed token, and the checks of Bronzeward's contract (§10.1) name none either. A
  separate token for OpenBao needs a second client or a token exchange; no exchange was
  measured.
- **The lifetime.** The OpenBao token lives as long as the role says, not as long as the
  JWT (M3): a 60-second JWT gave a 15-minute token. Bronzeward's maximum lifetime is
  configurable (15 minutes by default, §10.1), so the role's ttl would have to be set to
  match it.
- **Revocation.** A removed group reached an existing token at the user's next login, or at
  once by an admin revoking the accessor (M3). A user who never logs in again keeps the
  policies until the token's ttl ends.

Where the token would have to travel is itself a decision. If the client holds it and
talks to OpenBao directly, a client writes to OpenBao outside the extraction pipeline that
sanitises input before persistence, and §13.1 says a client "never receives broad OpenBao
access"; a tenant-scoped token is narrow, but the pipeline is the only door the design
allows for ingestion. If the server holds it, the user's token sits in the controller's
memory beside the component tokens, with the replay and lifetime properties above.

### 2. Which operations need an identity of their own?

| # | Operation | Runs | A user present? | Needs from OpenBao | Narrowest tenant-scoped identity | Source |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | Artifact decryption at dispatch | after approval; queued, retried, after a restore | not necessarily: the approver may differ from the creator (self-approval is allowed, §13.7 item 3), and a retry comes later | decrypt on the artifact key; read `access/talos/<cluster>` | executor of the tenant | [execution and recovery §3.1](../../spec/execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction) |
| 2 | Drift and other observations | every drift interval, on every controller with an executor | no | read `access/talos/<cluster>` | the executor's read-only client, same credential | execution and recovery §6.1; design §13.2 |
| 3 | Dependency monitor | every pass, on every instance | no | read `secret/metadata/*` and `transit/keys/*` | metadata identity of the tenant | [dependency monitor §4](../../spec/dependency-monitor.md#4-the-metadata-identity), §6.1 |
| 4a | Staging sweep | at startup and periodically | no | nothing from OpenBao: it writes the claim state and clears the payload in the database | none needed | compilation §3.5 |
| 4b | Staging takeover | on an explicit operator recovery request, by an ingestion principal | an operator | decrypt with the staging key | ingestion identity of the tenant | compilation §3.1, §3.4 |
| 5 | Ingestion | a user's request | yes, but the plaintext passes through the server either way | create-only on `gen/*`; HMAC; staging and baseline key; for a `source: machine` ingestion, read `access/talos/<cluster>` (an `os:admin` credential used through a read-only client) | ingestion identity of the tenant | compilation §1; persistence and API §3.3 |
| 6 | Compilation | at publication | the requester, but it runs as a job | read the pinned generations; encrypt with the artifact key | compiler of the tenant | compilation §1 |
| 7 | Orphan report | an operator's command on the server host | an operator | list and read `secret/metadata/gen/*` | report identity of the tenant | persistence and API §6.4 |
| 8 | Writing a cluster's talosconfig | the operator's own act | yes | write `access/talos/<cluster>` | the operator's own login, scoped to the tenant | persistence and API §3.3 |
| 9 | Signing an admin certificate (new, see below) | at observation and dispatch | no | the cluster's CA key | a signer of the tenant | the owner's answer on [#869](https://github.com/ginsys/bronzeward/issues/869), specification [#874](https://github.com/ginsys/bronzeward/issues/874) |

Only row 8 is a person's own act with a person's own token, and it bypasses the Bronzeward
server and its component identities today: the operator writes with `bao`, or with
`bronzewardctl cluster onboard`, which writes once with the operator's own token. Rows 2, 3 and 9 have no user at the time they run, so no user
token exists to use, and row 1 may have none; row 4a needs no OpenBao identity at all.
Rows 4b, 5, 6 and 7 have a person present, but giving that person's token the staging key,
the create path or the metadata would make the person hold what the specification gives
only a component identity (compilation §3.1, "only ingestion identities can decrypt" the
staging key). Row 1 adds the approval: OpenBao cannot check it (M6), and a user token that
could decrypt would let that user bypass the application check by calling OpenBao.

### 3. Layout per tenant

Two separate choices, measured separately.

**Where OpenBao separates tenants.**

- *Paths with policies.* One mount, one policy per tenant. Because paths hold the cluster,
  not the tenant, a policy must list the tenant's clusters, or a tenant segment must join
  the path grammar (a change to choice §17.8 and the orphan listing). A single templated
  policy (M8) avoids the list but takes the path from a claim, so the identity provider
  decides what the policy opens; the three values tried read nothing, yet the metadata
  belongs to the identity and a later login changed an earlier token's reach.
- *Namespaces.* Each tenant gets its own mounts, auth methods, policies and Transit keys,
  and the path grammar stays as it is (M5). Tokens did not cross, the same key name in two
  namespaces held separate keys, and a tenant token could not create a namespace.
  Namespace creation and its mounts are an OpenBao administrator's act, which §13.7 item 5
  already leaves outside Bronzeward's roles.

**The database.** `cluster` already keys most records, so a `tenant` owning a set of
clusters would scope rows. A schema per tenant could share the
singleton `installation_state`, and then a restore and a recovery mode stay per
installation and reach every tenant; recovery of one tenant alone would need its own epoch,
which needs a further design and comes close to a per-tenant installation (analysis, not
measured; [persistence and API §12.1](../../spec/persistence-api.md) holds the epoch). A separate installation per tenant already gives hard isolation with no change
to the specification, at the cost of running one controller and one database per tenant;
it is the baseline the three models below are weighed against.

### 4. What a user-held token changes in the existing boundaries

- **Create-only generations** hold for a token from a login (M7), so the creator
  cannot read or overwrite. But policies from groups add up (M1, a user in two tenants):
  "the creator never reads" becomes a property of how groups map to policies instead of
  of which component holds the token, and one person in an author group and in a group
  that reads generations breaks it.
- **The approval check before decryption** has no counterpart in OpenBao (M6; the policy
  documentation of the pinned version lists no approval-style primitive, and a stale
  mention of one sits on the namespaces page only). A user-held token that decrypts is a
  second path that skips the check. A user token must therefore never carry decrypt.
- **The one-process trust boundary** (design §13.2, "naming separate roles does not prove
  isolation within one process") is unchanged by a component identity per tenant held in
  one process, and worsened by user tokens held there. Per-tenant identities in separate
  processes would change it; that is a deployment choice no model here requires.
- **Per-request child tokens** (M4, M4b) can narrow one user's reach to one policy and one
  use, but in these samples only a policy the parent holds directly on its role could be
  named: the same name held through an identity group was refused. The group mapping of M1
  and a child named after the group's policy therefore do not combine on one role. A child
  binds nothing to a plan: the plan binding stays the application's.

### 5. The alternatives compared

Two axes: who holds the identity, and where tenants are separated.

| | U: the user's identity | T: a component identity per tenant | S: a shared component identity and an application check (today) |
| --- | --- | --- | --- |
| Who holds it | the person, via an OpenBao login | each component, one token per tenant | each component, one token for the installation |
| Layout | paths and groups, or namespaces | namespaces (paths work with the cost in question 3) | paths as today |
| OpenBao enforces the tenant | yes (M1, M5) | yes (M5) | no: the application does |
| Approval before decryption | impossible (M6) | the application, as today | the application, as today |
| Operations with no user (rows 2, 3, 9; row 4a needs no OpenBao identity under any model) | cannot serve | serves | serves |
| A leaked or buggy controller reaches | the tokens it holds | the tokens it holds: every tenant, in one process | every tenant and every key |
| What it needs | a client or exchange for the audience; a ttl policy | a namespace, mounts, policies and tokens per tenant; configuration for many tokens; an OpenBao administrator | nothing |
| Measured | M1 to M5, M7, M8 | M5, M7 | not new |
| Change to the specification | high: new token path; §13.7 item 1 and §13.1 reopened | medium: configuration of many token files, `bronzeward orphans` per tenant, §13.2 | none |

U is ruled out for rows 1, 2, 3 and 9 (M6, or no user at the time), and for rows 4b to 7 by
the specification's own rule that a component identity holds those grants. T is S with the
tenant boundary also drawn in OpenBao: it adds nothing against a flaw in the controller
process, and it bounds an operator's mistake in one tenant's policies and gives the audit
log a tenant per entry.

## Where the owner's answer on ginsys/bronzeward#869 changes a result

On 18 November 2026, 17:08 CET, the owner answered
[#869](https://github.com/ginsys/bronzeward/issues/869): "I think we must support all
"first keys": TALOSCONFIG, controlplane node configs, or even an ip + secrets.yaml.
Whatever the user provides, bw gets access and saves the signing keys" (recorded in [the
closing comment of that issue](https://github.com/ginsys/bronzeward/issues/869#issuecomment-6984191100),
read at source). The comment's own reading: with an endpoint and a secrets bundle there is
no client certificate, so Bronzeward signs its own admin certificate from the stored CA
key, which reverses the 2026-10-21 decision of
[#266](https://github.com/ginsys/bronzeward/issues/266) and retires choice §17.29. The
specification work is tracked in [#874](https://github.com/ginsys/bronzeward/issues/874).

For tenancy this raises the stakes of S and fixes the shape of T:

- Under S one shared identity could read every tenant's CA key, and a CA key signs an
  `os:admin` certificate that reaches every node of the cluster. That is the largest
  credential in the system joining the shared identity.
- Under T a signer exists per tenant, with its tokens in the tenant's namespace, so a token
  leaked from that tenant's signer reaches that tenant's clusters only.
- U cannot sign: signing happens at observation and dispatch with no user (row 9).
- Whether signing is done with a key Bronzeward reads or by OpenBao (a Transit key, or the
  PKI engine) was not examined; it changes what the signer identity needs, not the
  conclusion above.

The earlier options would have read differently: keeping the operator-written talosconfig
(option A of the note on ginsys/bronzeward#869) is row 8 and fits a user-held identity; ingesting the
talosconfig (option B) adds a per-tenant create-only ingestion identity.

## Failure cases and limits

- Each line is one sample from one run on one container; none shows variance. A 403 carries
  no reason, so a refusal's cause is read from the setup, not from the answer.
- Dev mode has in-memory storage and no seal: seal, restart, storage and high availability
  were not measured, and the root token of the dev container is a synthetic string.
- Authentication ran on a public key (`jwt_validation_pubkeys`), not through OIDC
  discovery or a real sign-in; real group-claim shapes and a token exchange were not
  tried.
- The expiry leeway behaved unlike the pinned API documentation (150 s documented; the
  boundary was between 55 s and 65 s). An earlier run stayed refused because a role update
  merges fields and left a leeway of -1; the evidence file explains the order.
- M8 tried three claim values; it is not a test of path injection.
- Response wrapping was tried for a child token only. No audit device, no performance, no
  load, no namespace delegation beyond the refusal in M5, and no PKI or Transit signing.
- Nothing here measures Bronzeward's own code: the application checks, the pipeline and the
  database layout are read from the documents and the schema.

## Recommendation

Keep S for the PoC and record **T on one OpenBao namespace per tenant** as the
multi-tenancy design, with U kept only for the operator's own writes into their tenant's
namespace (row 8).

- It answers the owner's hypothesis directly: identities can be coupled to the signed-in
  user, and that coupling works for what the user does themselves. It cannot remove
  Bronzeward's own identities for the operations that run without a user or after another
  person's approval, which are most of them.
- It needs no change to the path grammar, to choice §17.8 or to the orphan report beyond a
  tenant list, and the namespaces are administered outside Bronzeward.
- The reversal on ginsys/bronzeward#869 makes the per-tenant signer the first reason to build it.
- §13.7 item 1's derived line needs a clarifying sentence rather than a reversal:
  Bronzeward issues no OpenBao identity to the users of its API, while operators already
  write with their own OpenBao token, through `bao` or `bronzewardctl cluster onboard`
  and not through the server ([persistence and API
  §3.3](../../spec/persistence-api.md#33-talos-access)).

What it does not solve: it does not isolate tenants from a flaw in the controller process,
it does not move the approval check into OpenBao, and it does not give a user-visible
tenant model, tenant-scoped roles or a per-tenant recovery epoch. Those need their own
specification work after the owner's decision.
