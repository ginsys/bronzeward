# Key loss and restoration: applying a retained artifact versus regenerating it

| | |
|---|---|
| **Date** | 24 September 2026 |
| **Work item** | [Experiment E5 - test key loss and restoration](https://github.com/ginsys/bronzeward/issues/10) |
| **Design reference** | [§7.5 Rotation and retention contract](../Talos_Configuration_and_Machine_Management_Design.md#75-rotation-and-retention-contract), [§14.4 Recovery dependencies](../Talos_Configuration_and_Machine_Management_Design.md#144-recovery-dependencies), [§14.6 Explicit recovery mode after restoration](../Talos_Configuration_and_Machine_Management_Design.md#146-explicit-recovery-mode-after-restoration), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts); [execution and recovery contract §7](../../spec/execution-recovery.md#7-recovery-after-management-state-restoration) |
| **Artifacts** | [`experiments/e5-key-loss-restoration/`](../../../experiments/e5-key-loss-restoration/README.md), evidence under [`experiments/e5-key-loss-restoration/evidence/`](../../../experiments/e5-key-loss-restoration/evidence/) |
| **Decision enabled** | What each backup family must hold, and in what order and age relative to the others, for a release to stay applicable or regenerable after a restore; what the recovery check must verify and how it stops. Input to [retention and recovery policy](https://github.com/ginsys/bronzeward/issues/15), [execution and recovery contracts](https://github.com/ginsys/bronzeward/issues/19) and [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13). No policy, window or provider is selected here. |

## 1. Question

The design keeps two recovery paths apart. Applying a retained release uses its exact encrypted
artifact; regenerating compiles a new release from source versions
([design:287](../Talos_Configuration_and_Machine_Management_Design.md#65-renderer-and-contract-pinning),
[design:404](../Talos_Configuration_and_Machine_Management_Design.md#75-rotation-and-retention-contract)).
"Losing a source secret version can block regeneration while a retained encrypted artifact remains
applicable. Losing the only usable artifact decryption key blocks application of that artifact.
Preserving ciphertext alone does not preserve executability" (design:404). The recovery set
includes releases, secret versions, artifact keys, credentials and provider unlock material, and
"backups need not have identical timestamps, but every dependency required by the selected
recovery action must be available" (design:866). After a restore the operator enters recovery
mode, verifies decryption and credentials under the actual recovery identities, and marks each
scope ready, blocked or unresolved (design:876-880; spec §7 steps 3 and 6, spec:448 and spec:454).

The issue asks for three things:

1. restoration cases for older and newer application and provider backups, missing keys and
   missing secret versions;
2. applying a stored encrypted artifact kept apart from regenerating the configuration, with
   ciphertext presence alone not counted as executability;
3. the recoverable, blocked and irrecoverable outcomes, the custody and unlock prerequisites, and
   the safe stopping behaviour.

## 2. What was built

### 2.1 Three backup families, three generations

The [investigation fixtures](20260919-investigation-fixtures.md#6-what-each-experiment-gets)
snapshot three families independently and restore them in any combination. This experiment gives
each a role:

| Family | Snapshot, restore | Holds here |
|---|---|---|
| management database (PostgreSQL) | `db-snapshot`, `db-restore` | table `kl_release`: id, generation, source path and version, key name, stored ciphertext, artifact SHA-256 |
| provider (OpenBao, integrated Raft) | `bao-snapshot`, `bao-restore` | KV v2 source versions under `secret/kl/`, Transit keys `kl-*`, policies, and every token it has issued |
| local credential store (`.state/data`) | `store-snapshot`, `store-restore` | the compiler's and the executor's tokens (0600), with their accessors |

`run/scenario` builds three generations:

| Generation | Built | Snapshotted |
|---|---|---|
| g1 | KV `kl/app` v1; key `kl-art` v1; release R1 = `kl/app@1` under `kl-art` v1; compiler and executor1 issued | all three families (row 001) |
| g2 | KV `kl/app` v2; `kl-art` rotated to v2; R2 = `kl/app@2` under v2; R1 rewrapped to v2 in the database (002); decryption floor raised to 2 (003); executor2 issued (004), executor1 revoked (005, shown effective in L, 102) | all three families (006) |
| g3 | KV `kl/late` v1; key `kl-late`; R3 = `kl/late@1` under `kl-late` v1 | never |

Row 007 is the release table at the end of g3: R1 and R2 under `kl-art` v2, R3 under `kl-late` v1.
The injection log (`evidence/injections.log`) fixes which snapshot each restore used.

### 2.2 Identities and the three actions

| Identity | Policy |
|---|---|
| compiler | read `secret/data/kl/*`; `update` on `transit/encrypt/kl-*` |
| executor | `update` on `transit/decrypt/kl-*` |
| admin | the root token, standing for the operator's administrator |
| operator | runs the fixture's injections and the recovery check |

`run/release` takes a release id and reads everything else from the restored state: the record
from the database, the credential from the store, the key and source from the provider.

- **apply** (executor): decrypt the stored ciphertext and compare the plaintext's digest with the
  recorded one. This is application *eligibility*; nothing is sent to a machine.
- **regen** (compiler): read the recorded source version, render the artifact, compare its digest,
  and encrypt it under the release's key. Nothing is stored: a regenerated release is a new release
  that needs its own approval.
- **check** (operator): run both, then stop. It prints `stopped: nothing dispatched, no release
  stored`, confirms that the release table's digest is unchanged, and ends with one class:
  `applicable` (the stored artifact decrypts to its digest), `regenerable` (it does not, and the
  source does regenerate it), `blocked` (neither), or `absent` (the restored database has no such
  release).
  The check is not read-only at the provider. Its regeneration step sends a real Transit encrypt,
  and Transit turns an encrypt under a missing key into a key creation for an identity that holds
  `create` (§3.2, case D). The scenario therefore reads the provider's Transit key state as the
  administrator before and after every check, and records a change as the class
  `provider-changed`. The comparison brackets `check` rows only (`run/scenario:111-124`, called
  from `judge_release`, 130-134). A standalone `regen` row is not bracketed. Row 037, for one,
  sends a Transit encrypt to the deleted `kl-art` outside any check. A key created there would
  show only as part of the next check's "before" state. None did: the checks either side of 037
  (035, 038) both count one key.

The artifact is a one-field YAML document, `machine:\n  token: <value>\n`, rendered
deterministically so that a regeneration from the same source version reproduces its digest.

### 2.3 Rows

Every observation is one row of `evidence/verdicts.tsv`: the case, release, action and identity,
the state bundle that is its ground truth, the expected outcome written into the script before the
run, and the outcome observed from the command's exit status and output alone. For `apply` and
`regen` the observation is `ok`, `denied` (the CLI's `Code: 403`, matched case-sensitively so that
a local "Permission denied" on a file is not taken for the provider's) or `fail`; for `check` it is
the class printed. A mismatch is recorded and the run continues. `fail` is matched on the exit
status alone, so a row expecting `fail` would also match a failure for another reason; the reason
each such row failed is quoted in §3.2 from its transcript.

The state column names the last bundle taken, not always the state the row ran in. Rows 040-043,
065-067, 084-086 and 110-112 name a bundle taken before an in-case change: the administrator's
upsert of `kl-art` (039), the floor changes (064, 066) and the executor reissues (083, 109). The
bundle does not show that change; the change's own row and its transcript do.

### 2.4 Pinned versions

From `fixtures/versions.env`: OpenBao `ghcr.io/openbao/openbao:2.6.1@sha256:5b2486ab…`, PostgreSQL
`docker.io/library/postgres:17-alpine@sha256:f02121de…`, one-share unseal, single-node Raft. Talos
v1.13.6 runs as part of the fixture and is not used here.

### 2.5 Synthetic values and the leak scan

The three source values are `BWSYNTH-kl-<name>-<24 hex>`, generated per run under `KL_OUT`, outside
the checkout and outside every backup family, and handed to the CLIs over stdin. A rendered
artifact exists only in a temporary file for the one call that needs it. Every decryption is
reduced to a digest verdict. Tokens pass through the environment; only accessors are printed.

The fixture's leak scan (`fixtures/lib.sh:923`, patterns including `BWSYNTH-`) ran on each of the
14 bundles, over the bundle, `.state/backups` and `.state/data`. Each scan found its positive
control three times (the live store and the two store archives the bundle expands) and nothing
else: 42 control hits, 0 other hits (`evidence/leak-scans.txt`, `evidence/summary.txt`).

### 2.6 Reproduction

```sh
fixtures/bin/up
KL_OUT=<empty directory on disk> experiments/e5-key-loss-restoration/run/all
KL_OUT=<the same directory> experiments/e5-key-loss-restoration/run/collect-evidence
fixtures/bin/down
```

The committed capture: 112 rows, 0 mismatches, 14 state bundles.

The fixture records the commit it was brought up at in each bundle's `versions.txt`: `created at
5b29fed`, in all 14. The capture ran on 24 September 2026. The fixture wrote its own secret
`fixture/inline` at 22:02 CEST (20:02:37Z), the scenario wrote its first source version at
20:04:23Z, and the final bundle was taken at 20:07:57Z (`openbao-metadata.jsonl` and
`versions.txt` of each bundle). It ran from a working tree whose `run/` scripts were last modified
at 22:01:47 CEST, before the capture began. Those scripts are committed unchanged in 3c715e6,
together with the evidence. The evidence itself does not record the scripts' commit or their
hashes, so the tie between the capture and the committed scripts rests on those times and that
commit, not on anything the capture recorded. A future capture should record `git rev-parse HEAD`
and the hashes of `run/`.

## 3. Results

### 3.1 Case table

"Live" is the state of each family at that point; `gN` names the snapshot a family was restored
from. A live family carries every earlier case's loss until a restore undoes it: in D, `kl/app@1`
is still destroyed from B and `kl-late` still deleted from C.

| Case | Database | Provider | Store | Release | apply | regen | check | Outcome | Rows |
|---|---|---|---|---|---|---|---|---|---|
| A nothing lost | live | live | live | R1, R2, R3 | ok | ok | applicable | recoverable | 009-017 |
| B source version destroyed (`kl/app@1`) | live | live | live | R1 | ok | fail | applicable | recoverable by apply; regeneration blocked | 020-022 |
| | | | | R2 | ok | ok | applicable | recoverable | 023-025 |
| C key and source lost after the last snapshot (`kl-late`, `kl/late@1`) | live | live | live | R3 | fail | fail | blocked | irrecoverable: no snapshot holds either (E) | 028-030 |
| D artifact key deleted (`kl-art`) | live | live | live | R1 | fail | fail | blocked | blocked; recovered by E | 033-035 |
| | | | | R2 | fail | denied | blocked | blocked; recovered by E | 036-038 |
| E all restored | g2 | g2 | g2 | R1, R2 | ok | ok | applicable | recoverable | 052-057 |
| | | | | R3 | — | — | absent | irrecoverable | 058 |
| G database older than provider | g1 | g2 | g2 | R1 | fail (below floor) | ok | regenerable | recoverable: regeneration, or an administrator lowers the floor | 060-067 |
| | | | | R2 | — | — | absent | no record to apply | 063 |
| H provider older than database | g2 | g1 | g1 | R1 | fail (key version too new) | ok | regenerable | recoverable by regeneration | 072-074 |
| | | | | R2 | fail | fail | blocked | blocked in this combination | 075-077 |
| I store newer than provider | g1 | g1 | g2 | R1 | denied | ok | regenerable | recoverable: an administrator reissues the executor | 080-086 |
| J credentials lost | g1 | g1 | none, then g1 | R1 | fail, then ok | fail, then ok | blocked, then applicable | recoverable from the store backup that matches the provider | 088-094 |
| K provider sealed | g1 | g1 (sealed) | g1 | R1 | — | — | blocked, then applicable | recoverable with the unseal key share | 096-098 |
| L store older than provider | g2 | g2 | g1 | R1, R2 | denied | ok | regenerable | recoverable: an administrator reissues the executor | 102-108 |
| | | | | R2 after reissue | ok | ok | applicable | recoverable | 110-112 |

### 3.2 What each case showed

**B — a destroyed source version.** The admin read shows `kl/app` version 1 `destroyed: true`
(019). R1's stored artifact still decrypts to its digest (020), while its regeneration fails: "No
data found at secret/data/kl/app" (021). The check classes R1 applicable (022). This is
design:404 observed: the artifact stays applicable while regeneration is blocked. A later provider
restore brought the version back (048), and R1 regenerated again (053).

**C and E — irrecoverable, and why.** After `kl-late` was deleted and `kl/late@1` destroyed, R3
neither applies ("encryption key not found", 028) nor regenerates (029). Restoring all three
families to g2 brings back what B destroyed and what D deleted: `kl/app@1` undestroyed (048) and
`kl-art` at latest version 2 with its floor (047); version 1, below the floor there, is shown
decryptable again in G (064, 065). But `kl-late` and `kl/late` are absent from that snapshot (049,
050), and so is R3's record (051, 058). R3 is irrecoverable because no snapshot holds its key, its
source or its record, not because they were deleted: the same restore undid a destruction and a key
deletion.

**D — a deleted artifact key, and a key recreated under its name.** With `kl-art` deleted (032),
no stored artifact applies (033, 036). R1's regeneration fails for a reason carried over from B,
not from the deletion: `kl/app@1` is still destroyed ("No data found at secret/data/kl/app", 034). R2's source is still readable and renders to its digest, but
encryption is refused with HTTP 403 (037). Transit's encrypt endpoint creates a missing key, which
needs the `create` capability, and the compiler holds `update` only. Rows 039-043 show what that
refusal prevents. The administrator, who holds `create`, encrypted a probe under the missing name
(039). That minted a new `kl-art` whose only version is 1 (040). Against that key, R2's stored
artifact still fails ("invalid ciphertext: version is too new", 041). R2's regeneration now goes
through and encrypts under "kl-art key version 1" (042), a label the lost key's own version 1 also
carried. The check classes R2 regenerable (043). Nothing in the ciphertext's `vault:v1:` prefix tells
the two keys apart.

**G — the database older than the provider.** The g1 database holds R1 as it was before the
rewrap, under key version 1, and the g2 provider enforces a decryption floor of 2. Apply fails:
"ciphertext or signature version is disallowed by policy (too old)" (060). Regeneration from
`kl/app@1` succeeds (061), so the check says regenerable (062). R2 was recorded after the g1
database snapshot, so the restored database has no record of it (063), although the provider still
holds its source and key. The administrator lowered the floor to 1 (064). The same artifact then
applied (065), and failed again once the floor was put back (066, 067).

**H — the provider older than the database.** The g2 database holds R1 rewrapped and R2 encrypted
under `kl-art` version 2. The g1 provider has only version 1 (071). Both applications fail with
"invalid ciphertext: version is too new" (072, 075). R1 regenerates from `kl/app@1` (073), but R2
cannot: the compiler's read of `kl/app` version 2 answers "No value found at secret/data/kl/app"
(076). Case H has no `kv_truth` row; the H bundle's provider metadata lists version 1 of `kl/app`
only. The rewrap in g2 is what cost R1 its applicability here. Before it, R1's artifact was under
version 1, which this provider holds.

**I — credentials newer than the provider.** The g2 store holds executor2, which the g1 provider
never issued: apply is denied with HTTP 403 (080). The compiler token dates from g1 and still
works, so R1 regenerates (081, 082). The administrator issued a new executor token on the restored
provider (083), after which R1 applies (084, 086). The artifact and its key were intact; only the
credential was missing.

**J — credentials lost.** With the credential store removed, neither path opens: "no executor
credential in the restored store" (088), "no compiler credential" (089). Restoring the g1 store,
the one matching the g1 provider, restores both (092-094).

**K — a sealed provider.** With every dependency retained and every credential valid, the check is
blocked: both paths got HTTP 503 "Vault is sealed" (096). After unsealing with the fixture's key
share (097), R1 is applicable again (098).

**L — credentials older than the provider.** The g1 store holds executor1, which the g2 provider
revoked in setup (005). The provider has no token for its accessor ("invalid accessor", 102), and
apply is denied with HTTP 403 for both releases (103, 106), although key, artifact and source are
all present. The compiler token dates from g1, was never revoked and still works, so both
regenerate (104, 107). After the administrator issued a new executor token (109), R2 applies
again (110, 112). This is I in the other direction: a store backup that does not match the
provider loses the credential whichever of the two is older.

### 3.3 Custody and unlock prerequisites per case

| Case | What was missing | Where it is kept | What made the release usable again |
|---|---|---|---|
| B | source `kl/app@1` | provider KV; provider backup | a provider restore (E, 048); apply was never affected (020) |
| C | key `kl-late`, source `kl/late@1`, R3's record | provider and database; no backup holds them | nothing |
| D | key `kl-art` | provider Transit; provider backup | a provider restore (E, 047) |
| G | key version 1 above the floor; R2's record | the provider's key configuration; database backup | an administrator floor change (064); R2 needs a database backup no older than R2 |
| H | key version 2; `kl/app@2` | provider backup no older than the database backup | a newer provider backup; R1 regenerates meanwhile (073) |
| I | an executor credential this provider issued | credential store backup matching the provider | an administrator reissue (083) |
| J | every credential | credential store backup | the store restore matching the provider (091) |
| K | an unsealed provider | the unseal key share, held apart from every backup family | unsealing (097) |
| L | an executor credential this provider has not revoked | credential store backup matching the provider | an administrator reissue (109) |

## 4. Failures hit while building it

### 4.1 The expected outcome of regenerating against a deleted key

The first full capture expected row 037 to `fail` and observed `denied`: it was the one mismatch in
93 rows. The script was right and the expectation was wrong. Transit's encrypt treats a missing key
as an upsert, and the compiler's policy lacks `create`, so the provider refused the request with a
403 rather than reporting a missing key. That capture was discarded and is not committed; the
mismatch count comes from its run, not from evidence in this repository. Later captures expect
`denied` and add the administrator's upsert and its consequences (039-043) as the control for what
the refusal prevents.

### 4.2 The check was described as read-only

A review of the second capture found that the report, `run/release` and the README called the
check read-only, while its regeneration step sends a real encrypt (row 038 sends one to the deleted
`kl-art`). The third, committed capture compares the provider's key state across every check,
adds case L (a store older than the provider, which also shows the executor1 revocation took
effect) and matches `denied` on the CLI's `Code: 403` only. Rows 001-098 have the same case,
release, action, identity, state, expectation, observation and verdict in both captures. The
second capture is not part of the current evidence: it was committed in 5b29fed and replaced by
this one in 3c715e6, so the comparison rests on that earlier commit's `verdicts.tsv`, not on
anything under `evidence/` now.

### 4.3 Verbatim transcripts fail `git diff --check`

The indented error text inside the check transcripts carries trailing whitespace. `.gitattributes`
exempts this experiment's `evidence/transcripts/*.txt`, as it already does for the
provider-capability run's.

## 5. What this decides

### 5.1 Criterion 1: restoration cases

Each combination the issue's run design names was restored, and both paths were tried after it:
application older than provider, provider older than application, a destroyed secret version, a
deleted Transit key and missing management credentials. Of the eight database/provider/store age
combinations, six were set up: g2/g2/g2 (E), g1/g2/g2 (G), g2/g1/g1 (H), g1/g1/g2 (I), g1/g1/g1
(J after the store restore, K) and g2/g2/g1 (L). The other two were not (§6).

| Asked for | Case |
|---|---|
| application backup older than provider | G (database g1, provider g2) |
| application backup newer than provider | H (database g2, provider g1) |
| provider backup older | H |
| provider backup newer | G |
| missing key | C (never snapshotted), D (deleted, restorable) |
| missing secret version | B (destroyed, restorable), C (destroyed, never snapshotted) |
| missing management credentials | I (store newer than the provider), L (store older), J (credentials gone) |
| explicit restoration | E (all three families to g2), J (store alone) |

### 5.2 Criterion 2: applying is not regenerating, and ciphertext is not executability

The two paths failed independently in both directions:

- **Artifact applies, regeneration blocked:** B, where the source version was destroyed (020, 021).
- **Artifact does not apply, regeneration works:** G (060, 061), H for R1 (072, 073), I (080, 081),
  L (103, 104), and D after the key was recreated (041, 042).
- **Both blocked:** C, D, H for R2, J and K.

In every "does not apply" row the stored ciphertext was present: each transcript's first line
names its key version. The release then applied again once the missing piece came back: the key
version (E, 052), the floor (065) or the credential (084, 110). That it was the same ciphertext is
by construction, not observed: the scenario writes a release's ciphertext only when it makes the
release and in the rewrap (002), and the evidence records its key-version prefix (007, 051), not
its bytes. What made it executable was the key version, the provider's floor, the executor's
credential and an unsealed provider. The ciphertext alone never did.

### 5.3 Criterion 3: outcomes, prerequisites and stopping

**Outcomes.** In this run, recoverable meant that a restore, an administrator action or a
regeneration made the release usable again. Blocked meant a missing piece that another backup or
action could supply. Irrecoverable meant that no snapshot held the piece:

- *recoverable by apply*: A, B (R1), E (R1, R2), I and L after reissue, J after the store restore,
  K after unsealing;
- *recoverable by regeneration*: H (R1), and I and L before the executor was reissued; each makes
  a new release that needs its own approval;
- *recoverable by regeneration, or by apply once an administrator lowers the floor*: G (R1, 064,
  065);
- *blocked, then recovered by a restore*: D (by E);
- *blocked in this combination*: H (R2), which needs a provider backup at least as new as g2;
- *irrecoverable*: R3 (C, E). Its key, its source version and its record were all made after the
  last snapshot.

**Custody and unlock prerequisites observed**, by path (per case: §3.3):

| Path | Needs |
|---|---|
| apply | the release record (database); the key version it names, present and at or above the decryption floor (provider); an executor credential the *restored* provider recognises (store matching provider); an unsealed provider (the unseal share) |
| regen | the source version (provider KV); a key to encrypt under (provider); a compiler credential the restored provider recognises; an unsealed provider; then a new approval |
| repair actions seen | lowering a decryption floor (064), reissuing a credential (083, 109), creating a key (039): all administrator operations on the restored provider |

**Safe stopping.** The check stores no release and dispatches nothing. Each of the 25 `check` rows
prints `stopped: nothing dispatched, no release stored`, and for every release present the release
table's digest was the same before and after the check (for example 030 and 043). The check is not
read-only at the provider, though. Its regeneration step sends a real Transit encrypt, and Transit
creates a missing key on encrypt for an identity that holds `create` (039). So the scenario read
the provider's Transit key state, as the administrator, before and after every check. It was
unchanged in the 24 checks where the provider could answer, and not compared in the one where the
provider was sealed (096). For the irrecoverable R3 the check reports `absent` (058) or `blocked`
(030) and stops. It creates no new release and no substitute record. The one way this run found to
"recover" past a lost key without a backup was the recreated key in D (042). There a regeneration
succeeds under a different key that carries the same name and version number. The recovery check
must not do that on its own. The compiler's lack of `create` is what kept D's checks (035, 038)
from doing it (037); a recovery identity that held `create` would have minted the key.

### 5.4 Alternative recovery paths compared

| Path | Works when | Costs | Seen in |
|---|---|---|---|
| apply the retained artifact | the record exists, its key version is at or above the floor, the executor credential is one the provider honours, and the provider is unsealed | nothing beyond the original approval; the bytes approved are the bytes applied (design:287) | A, B, E, J, K, and I and L after reissue |
| regenerate from source as a new release | the source version, a key to encrypt under and a compiler credential | a new release and a new approval; under another renderer the bytes could differ (not exercised: the renderer is fixed here) | G, H (R1), I, L |
| restore another backup generation | a snapshot holds the missing piece | everything after that snapshot in the restored family is lost (R3 in E), and the pairing with the other families can break (G, H, I, L) | E, which undid B's and D's losses |
| administrator repair on the restored provider | lowering the floor works while the key version still exists below it; reissuing a credential works while the policy exists | an administrator action outside the check, to be recorded (and, for the floor, reverted) | G (064-066), I (083), L (109) |
| recreate a lost key under its name | for apply, never: the new key decrypts nothing stored (041). A regeneration does succeed under it (042) | the regenerated ciphertext reuses the lost key's name and its `vault:v1:` label, so a record of key name and version cannot tell the new key from the lost one | D (039-043) |

Inference: apply loses least. Next come a restore or repair that makes apply possible again, then
regeneration with re-approval. Recreating a key under the lost key's name, reusing its name and
`vault:vN` labels, is not a recovery path. Regenerating under a new key with a new name, as a new
release, was not exercised as a recovery path.

## 6. Limits

- **One provider and one topology.** OpenBao 2.6.1, single node, one unseal share. Every provider
  restore went back onto the same cluster, which keeps its unseal key. Restoring onto a new cluster,
  where unseal or recovery keys come from another custody path, was not exercised.
- **The administrator ran as root** for every change it made (002-005, 039, 064, 066, 083, 095, 109)
  and every read of the ground truth. A least-privilege administrator policy for floor changes,
  credential reissue and key creation is still unwritten.
- **Apply is eligibility, not application.** Nothing was sent to a Talos machine. The artifact is a
  one-field synthetic document, not a rendered machine configuration.
- **The release table is a stand-in.** It has no approvals, operation journal or recovery epoch.
  The check models spec §7 steps 2, 3 and 6 for one release. It does not model recovery-mode entry,
  executor quiescence or approval invalidation.
- **Token expiry was not exercised.** Tokens used the provider's default TTL, well beyond the run.
  A restored provider whose tokens have expired, or a lease revoked after a snapshot, was not
  tested.
- **One pass per case, in sequence, on one fixture.** Each case's ground truth is its bundle, and the
  injection log fixes the order. Case G's floor change was reverted within the case (066).
- **Two age combinations were not set up.** Database/provider/store at g1/g2/g1 and at g2/g1/g2
  (§5.1). The issue's verification method asks for each backup-age combination; these two are
  open.
- **The capture does not record its scripts.** Each bundle records the fixture's commit (5b29fed,
  §2.6), not the commit or the hashes of the `run/` scripts that drove it. The tie to 3c715e6 rests
  on the scripts' modification times, before the capture began. A future capture should record
  `git rev-parse HEAD` and the `run/` hashes.
- **The key-state comparison brackets `check` rows only** (§2.2). A key created by a standalone
  `regen` row, such as 037, would show only in the next check's "before" state.
- **`run/all` tells the control by its file name.** It counts a leak-scan hit on any path ending in
  `/data/canary-control.txt` as the control (`run/all:32-33`). The fixture's own control test also
  checks the file's content and links, but its verdict goes to `states-<label>.stderr`, which
  `run/all` does not read and `run/collect-evidence` does not copy.
- **Smaller harness properties.** `revoked()` (`run/scenario:278-285`) takes any lookup error as
  revoked; row 102 shows the error was `invalid accessor`, so the result stands. `key_state` (`run/scenario:96-101`) takes a failed listing with empty output as no keys.
  `make_release` (`run/scenario:66-80`) would run as the root token if the compiler's token file
  were empty, since the fixture's `bao` falls back to it (`fixtures/lib.sh:687`).
  `run/collect-evidence:38` rewrites the checkout path before `KL_OUT`, so a sibling
  `<checkout>-evidence` output, which `run/lib.sh:28-31` allows, would come out as
  `<repo>-evidence`. The committed evidence holds neither `<KL_OUT>` nor `<repo>-evidence`, and does
  not record which `KL_OUT` the capture used. That guard also compares `KL_OUT` as given, not
  canonicalized, so a path reaching the checkout through `..` or a symlink would pass it.
  `key_state` folds the listing's stderr into its output, so a mount with no key whose listing
  prints an error would read as `unreadable`, not as no keys. No check row had an empty mount:
  every check compared one to three keys, except the sealed row 096.
- **The store is a file copy.** It held the token and accessor files and the fixture's own leak-scan
  control (`canary-control.txt`); no process held a database open in it.
- **What stays out of the repository.** The bundles under `KL_OUT` include the expanded store
  archives, which hold the run's tokens. `run/collect-evidence` copies none of them and refuses
  collected evidence that looks like an OpenBao token. The command column of `verdicts.tsv` holds
  the probe ciphertext's base64 plaintext (`kl-probe`, 039) and no synthetic value.

## 7. Recommendation

The recommendations below are inferences from the cases above. They are not observations.

1. **Record a release's key by identity, not by name and version alone.** D shows that a key
   recreated under a lost key's name produces ciphertexts labelled `vault:v1:` like the lost key's
   (042). Contracts for [issue 19](https://github.com/ginsys/bronzeward/issues/19) and the provider
   profile for [issue 13](https://github.com/ginsys/bronzeward/issues/13) should pin something the
   provider cannot reissue, such as the key's creation time for that version. The compiler should
   also be kept without `create` on the key path (037).
2. **Pair backups by age.** A provider backup older than the database backup it is restored with
   loses what the database references (H). A database backup older than the provider loses release
   records and can fall below a raised floor (G). For
   [issue 15](https://github.com/ginsys/bronzeward/issues/15): take the provider backup no earlier
   than the database backup it pairs with. Keep the credential store's backup matched to the
   provider's: a store newer than the provider (I) and one older (L) each lost the executor
   credential, and a lost store needed the matching backup (J).
3. **Tie floor raises and rewraps to the database retention window.** Rewrapping made every
   rewrapped artifact depend on the newest key version (H), and raising the floor made every older
   database's artifacts undecryptable (G). While a database backup that references a key version
   may still be restored, keep that version decryptable, or accept regeneration and re-approval
   as the recovery path for those releases.
4. **Make the recovery check verify, stop, and name the missing piece.** The check in this run
   tells applicable from regenerable from blocked by testing decryption and the source under the
   actual recovery identities, which spec §7 step 3 asks for. Its output names what failed. That
   is enough to mark a scope blocked with the missing dependency (spec:454). Administrator repairs
   (floor, reissue) stay explicit and outside the check.
5. **Irrecoverability rests on the backup inventory.** A restore undid destruction and deletion
   here (047, 048). Anything created after the newest snapshot of any family is what can be lost for
   good (R3). The backup interval bounds that exposure.

## 8. Hand-off

- **[Issue 15](https://github.com/ginsys/bronzeward/issues/15), retention and recovery policy:**
  the pairing rule and the floor and rewrap window (§7 items 2 and 3); the exposure bounded by the
  newest snapshot (item 5); the case table (§3.1) as the restoration matrix to keep.
- **[Issue 19](https://github.com/ginsys/bronzeward/issues/19), execution and recovery contracts:**
  the prerequisites table (§5.3), the check's stop-and-name behaviour, key identity in the release
  record (item 1), and that a regeneration is a new release needing approval (G, H, I).
- **[Issue 13](https://github.com/ginsys/bronzeward/issues/13), deployment profile:** Transit's
  upsert-on-encrypt and the `create` capability (D); the unseal share as a custody item separate
  from every backup family (K).
- **Not covered, for whoever takes it up:** the age combinations g1/g2/g1 and g2/g1/g2, restore
  onto a new provider cluster, token expiry across a restore, a least-privilege administrator
  policy, and a capture that records its own scripts (§6).
