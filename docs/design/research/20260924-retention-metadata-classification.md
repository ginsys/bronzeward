# Retention and metadata classification: retained, blocked, lost and unknown

| | |
|---|---|
| **Date** | 24 September 2026 |
| **Work item** | [Experiment E5 - test retention and metadata classification](https://github.com/ginsys/bronzeward/issues/9) |
| **Design reference** | [§7.5 Rotation and retention contract](../Talos_Configuration_and_Machine_Management_Design.md#75-rotation-and-retention-contract), [§7.6 Metadata-only dependency checks](../Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts) |
| **Artifacts** | [`experiments/e5-retention-classification/`](../../../experiments/e5-retention-classification/README.md), evidence under [`experiments/e5-retention-classification/evidence/`](../../../experiments/e5-retention-classification/evidence/) |
| **Decision enabled** | Which metadata each candidate exposes for classifying a dependency, where that metadata runs out, and what alert policy the evidence supports. Input to [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13) and [retention and recovery policy](https://github.com/ginsys/bronzeward/issues/15). No provider, interval or policy is selected here. |

## 1. Question

Design §7.6 requires a provider to expose enough metadata, without secret values, to classify every
secret or key version a release depends on as one of four states:

| State | §7.6 meaning |
|---|---|
| retained | present with no known retention block; says nothing about whether the caller can read or decrypt |
| blocked | present with a known reversible restriction (a soft delete, a decryption floor) |
| lost | evidence establishes irreversible removal from the current provider (destruction, pruning, trim) |
| unknown | the check could not be completed, or the metadata cannot tell the other states apart |

It adds three constraints. An absent listing, denied access or unreachable provider "is not by
itself proof of loss". Unknown "never counts as a pass or becomes lost by timeout", and a persistent
unknown is alertable after an interval the design leaves open. And the check "establishes retention
status only": it neither authorizes dispatch nor replaces the point-of-use checks.

The issue asks for four things, each answered in §6: the mapping from metadata to state per
candidate, the proof that nothing uncertain becomes lost, the proof that a retained verdict grants
no authority, and the provider limits plus the alert policy the evidence supports.

## 2. Candidates and identities

The candidates are those of the [provider-capability comparison](20260924-provider-capability-comparison.md):

- **OpenBao** 2.6.1 KV v2 and Transit, as the [investigation fixtures](20260919-investigation-fixtures.md)
  run it (single node, integrated Raft, real init and unseal).
- **The local age store**, in the layout the provider-capability run built: one ciphertext per
  generation, with a metadata file beside it recording the recipient and the ciphertext's SHA-256.
- **SOPS files** (sops 3.13.3 with age 1.3.2), whose metadata is the `sops` block, readable without a
  key, including the MAC.

| Identity | Holds | Used for |
|---|---|---|
| metadata | the fixtures' metadata-only token: read and list on `secret/metadata/*` and `transit/keys/*` | every classification |
| compiler | `transit/encrypt/*` | encrypting the synthetic artifact |
| executor | `transit/decrypt/*` | decrypting it |
| other | read on `secret/data/other/*` only: a compiler for another scope | authority checks |
| admin | the root token, standing for the operator's administrator | setting states the fixture has no injection for; reading ground truth |
| operator | the fixtures' `inject` command | soft delete, destroy, key deletion, partition, pause |

For the local stores, the classifier reads only the metadata file or `sops` block and the
ciphertext's digest. It never opens a key file.

## 3. What was built

### 3.1 Rules, classifier, rows

`run/decide.sh` holds the rules as pure functions over one provider answer. `run/test-decide`
checks them on 28 synthetic answers, written out in the script, before any capture uses them (the
rules were written after those checks had been seen to fail against a stub). `run/classify` asks
the provider and applies the rules. For OpenBao it sends one GET per dependency over HTTP to the
published port, from the host, with the metadata token. It does not use the fixtures' `bao` wrapper,
which runs inside the container, where a partition cannot be seen (fixtures report §7).

The rules:

| Candidate | Answer | Class |
|---|---|---|
| KV v2 | version present, no `deletion_time` | retained |
| KV v2 | `deletion_time` in the future (`delete_version_after`) | retained, reason names the scheduled time |
| KV v2 | `deletion_time` now or past | blocked (undelete reverses it) |
| KV v2 | `destroyed: true` | lost |
| KV v2 | version below `oldest_version` (when that is above 0) | lost (pruned) |
| KV v2 | version above `current_version`, or missing with no recorded removal | unknown |
| Transit | version below `min_available_version` (when above 0) | lost (trimmed) |
| Transit | version below `min_decryption_version` | blocked |
| Transit | version above `latest_version`, or a floor missing from the answer | unknown |
| Transit | otherwise | retained |
| age store | metadata readable, ciphertext digest as recorded | retained |
| age store | ciphertext present with another digest | lost (replaced; the store keeps no history) |
| SOPS | MAC as referenced | retained |
| SOPS | another MAC | lost (replaced; sops keeps no versions) |
| any | HTTP 403 or a file permission error | unknown (denied) |
| any | HTTP 404 or no such file | unknown (absent) |
| any | HTTP 503 | unknown (sealed or unavailable) |
| any | no connection, no answer in time, any other failure | unknown |

Transit's own `keys` map is not used. It hides versions below the decryption floor, as the
provider-capability run found, and it also hides trimmed versions (027: after the trim, the map
holds only version 2). The floors are what distinguish blocked from lost.

A monitor (`monitor` in `decide.sh`) records each observation of a dependency. It prints the class
it was given and an alert: `regression` when a dependency seen retained turns unknown,
`persistent` once unknown has lasted `RC_ALERT_AFTER` seconds, and `blocked` or `lost` for those
classes. It never changes the class.

`run/openbao` and `run/local` put the dependencies into each state and record one row per
observation in `verdicts.tsv`: the expected class, written before the command runs; the observed
class and reason, taken from the classifier's last line; and the evidence bundle of that state. A
row that differs from its expectation is a mismatch and the run carries on.

### 3.2 States and their ground truth

Every OpenBao state has its own `fixtures/bin/evidence` bundle, copied out of `.state` before the
next state is set. From each bundle, `evidence/states/<state>/` keeps `openbao-metadata.jsonl` (the
provider's metadata, read inside the container), `openbao-client-view.txt` (whether the published
port answered) and `versions.txt` (the container's state and networks):

| State | Set by | Client view | Out-of-band metadata |
|---|---|---|---|
| baseline | populating KV and Transit | HTTP 200 | complete |
| removed | `inject bao-soft-delete`, `bao-destroy`, `bao-delete-key`; admin metadata delete, rotate + floor, trim | HTTP 200 | complete |
| reversed | admin undelete, floor lowered; a trim reversal attempted | HTTP 200 | complete |
| partitioned | `inject netsplit openbao` | curl exit 7 | complete; `networks=[]` |
| paused | `inject pause openbao` | curl exit 28 | unknown: nothing could be read |
| after-uncertain | a `bao-destroy` sent while paused, then `unpause` | HTTP 200 | complete |
| sealed | admin `bao operator seal` | HTTP 503 | unknown: nothing could be read |
| authority | unsealed again | HTTP 200 | complete |
| final | after the local runs; its leak scan covers the local stores | HTTP 200 | complete |

The bundle's KV records carry each version's `deletion_time` and `destroyed`, but not
`current_version` or `oldest_version`. Its Transit records carry `latest` and `min_decryption`,
but not `min_available_version`. The admin rows read what the bundle lacks: 005 for the pruned
path, 028 for the trimmed key, 051 for the path the unanswered destroy was aimed at.

### 3.3 Pinned versions

OpenBao `ghcr.io/openbao/openbao:2.6.1@sha256:5b2486ab…a67e0`, sops v3.13.3 and age v1.3.2, all
from `fixtures/versions.env`, each verified by the fixture before use. The classifier's HTTP client
is the host's curl (8.14.1 here), not a pinned image: it has to be a client outside the container.
It needs curl 8.3 or later for `--variable` and `--expand-header`, which keep the token off the
command line.

### 3.4 Synthetic values and the leak scan

Every value is `BWSYNTH-rc-<name>-<random hex>`, generated per run under `RC_OUT`, and reaches a
tool only over stdin. Every value read back is reduced to a digest verdict before it reaches a
transcript. The final bundle's leak scan walked `.state/data`, where the local stores live, and
found its positive control and nothing else (`summary.txt`). `run/collect-evidence` refused
anything matching the fixture's scan patterns, an age secret key, an OpenBao token or a home path.

### 3.5 Reproduction

```sh
experiments/e5-retention-classification/run/test-decide
fixtures/bin/up
RC_OUT=<empty directory on disk> experiments/e5-retention-classification/run/all
RC_OUT=<the same directory> experiments/e5-retention-classification/run/collect-evidence
fixtures/bin/down
```

The capture took about nine minutes after `up`: 99 rows, 0 mismatches, 28 of 28 rule checks.

## 4. Results

Rows are numbered as in `verdicts.tsv`; each has its transcript under `evidence/transcripts/`.

### 4.1 OpenBao

| Dependency and state | Row | Provider answer | Class |
|---|---|---|---|
| KV version, live | 001 | version present, no `deletion_time` | retained |
| KV version, deletion scheduled in an hour | 002 | `deletion_time` in the future | retained |
| KV version 2 of a path written 11 times | 003 | present | retained |
| KV version 1 of that path | 004 | `oldest_version: 2`, `current_version: 11` (005) | lost |
| KV version 12 of that path | 006 | above `current_version` | unknown |
| Transit key version 1, live | 007 | within both floors | retained |
| KV path on another mount | 008 | HTTP 403 | unknown |
| Transit key on another mount | 009 | HTTP 403 | unknown |
| KV path that never existed | 010 | HTTP 404 | unknown |
| KV version, soft-deleted | 021 | `deletion_time` in the past | blocked |
| KV version, destroyed | 022 | `destroyed: true` | lost |
| Transit key, deleted | 023 | HTTP 404 | unknown |
| KV path, metadata deleted | 024 | HTTP 404 | unknown |
| Transit version 1 under a floor of 2 | 025 | `min_decryption_version: 2` | blocked |
| Transit version 2 of the same key | 026 | within the floors | retained |
| Transit version 1, trimmed | 027 | `min_available_version: 2` | lost |
| soft delete undone | 034 | `deletion_time` cleared | retained |
| floor lowered to 1 | 035 | within the floors | retained |
| destroyed version, after the reversals | 036 | `destroyed: true` | lost |
| trimmed version, after the reversal was refused | 037 | `min_available_version: 2` | lost |
| three dependencies, partitioned | 040-042 | curl exit 7 | unknown |
| after `netjoin` | 045 | version present | retained |
| paused | 047 | curl exit 28 after 5 s | unknown |
| sealed | 053 | HTTP 503 "Vault is sealed" | unknown |
| unsealed | 055 | version present | retained |

The reversals are the difference between blocked and lost in action. The undelete (031) and the
lowered floor (032) were accepted, and the executor decrypted version 1 again (038). Lowering
`rc-trim`'s floor below the trimmed one was refused: "min decryption version should not be less
then min available version" (033). Before a trim, OpenBao requires `min_encryption_version` to be
raised as well as the decryption floor. The first attempt, without it, was refused (§5.1).

The deleted Transit key (023) and the path whose metadata was deleted (024) answer exactly as a name
that never existed (010) does: HTTP 404 with an empty error list. The rule puts all three at
unknown. Whether a Bronzeward reference to that name ever resolved is Bronzeward's own record, not
the provider's.

### 4.2 Local stores

| Dependency and state | Row | Answer | Class |
|---|---|---|---|
| age generation, as written | 065-066 | digest matches | retained |
| age generation never written | 068 | no metadata file | unknown |
| ciphertext removed, metadata kept | 070 | no such file | unknown |
| ciphertext replaced under the same name | 072 | digest differs | lost |
| metadata file mode 000 | 074 | permission denied | unknown |
| ciphertext mode 000 | 077 | permission denied | unknown |
| modes restored | 079 | digest matches | retained |
| key file deleted | 081 | digest matches | retained |
| SOPS file, as referenced | 085 | same MAC | retained |
| SOPS file edited in place | 088 | another MAC | lost |
| SOPS file removed | 090 | no such file | unknown |
| SOPS file mode 000 | 092 | permission denied | unknown |
| mode restored | 094 | same MAC | retained |
| key file deleted | 096 | same MAC | retained |

Neither local candidate has a blocked or an unreachable state, and rows 083-084 and 098-099 record
that rather than leaving the cells blank. Nothing hides a file reversibly while keeping it. A local
file has no remote end: an unreadable file is either denied or absent.

## 5. Failures hit while building it

### 5.1 A trim needs the encryption floor raised first

A first capture set `rc-trim`'s decryption floor to 2 and then asked for a trim to 2. OpenBao refused
it with HTTP 400: "minimum available version cannot be set when minimum encryption version is not
set". The key stayed untrimmed, so the classifier correctly reported it blocked, and the three trim
rows were mismatches. The script now raises `min_encryption_version` along with the decryption
floor. That capture was discarded and the fixture rebuilt; the committed evidence is the second,
complete capture.

### 5.2 A denied ciphertext read as absent

The age-store classifier first took the ciphertext's digest through a redirection
(`sha256sum <file`). A redirection that fails reports on the shell's own stderr, before the
command's `2>&1` applies. A mode-000 ciphertext therefore produced no captured error and would have
been reported as absent instead of denied. Both are unknown, but the reason was wrong. It now passes
the file by name. The mistake was caught on review of the script, before any capture.

### 5.3 Verbatim transcripts fail `git diff --check`

As in the provider-capability run, bao's trailing blank lines trip the whitespace check.
`.gitattributes` exempts this experiment's `evidence/transcripts/*.txt` alone.

## 6. What this decides

### 6.1 Criterion 1: metadata and permissions mapped to the four states

**OpenBao** supplies every state from the metadata token alone: read on `secret/metadata/*` and
`transit/keys/*`, plus list. Retained, blocked (soft delete, decryption floor) and lost
(destruction, pruning, trim) each rest on a field of the answer (§3.1, §4.1), and each was
observed against its ground truth. Scheduled deletion is retained until its time and blocked after
it. The reason names the time, so a monitor can warn before it arrives.

**The local age store** supplies retained and one form of lost: a replaced ciphertext, detected by
the digest its metadata recorded. It supplies no blocked state. Its lost is only as good as its
metadata: a writer that updates the metadata along with the ciphertext, as the
provider-capability run's rotation does, leaves nothing to detect.

**SOPS** supplies retained and lost against a pinned MAC, and no blocked state. The reference has to
carry the MAC: the file itself has no version number to refer to.

For both local candidates, **retained means the metadata file and the ciphertext are there, and
nothing about the key** (081, 096 retained with the key file gone; 082, 097 the decrypt failing).

### 6.2 Criterion 2: nothing uncertain becomes lost

Each cause of uncertainty was produced and each landed on unknown:

| Cause | Rows |
|---|---|
| denied metadata | 008, 009 (OpenBao); 074, 077 (age store); 092 (SOPS) |
| missing path or listing entry | 010, 023, 024 (OpenBao); 068, 070 (age store); 090 (SOPS) |
| unreachable | 040-042 (partition, curl exit 7) |
| no answer | 047 (paused, curl exit 28) |
| sealed | 053 (HTTP 503) |
| insufficient evidence | 006 (a version above `current_version`); the rule checks for missing floors, missing version maps and unparseable answers |

No timeout moves unknown to lost. Row 043 is three observations of a partitioned dependency, 4 s
apart, with the alert interval set to 5 s. The monitor raised `regression` at once, because the
dependency had been retained, and `persistent` at the third observation. The class stayed unknown
throughout. The rule checks cover the same with an unknown observed 99,900 s after it began, and
with a dependency never seen retained.

The uncertain mutation: a `bao-destroy` sent while OpenBao was paused got no answer, and the fixture
logged it `unknown rc=28` (048, `injections.log`). After `unpause`, the version was not destroyed in
this run (050, with the admin read 051 agreeing). The classifier reported what the provider then
held, and the fixture's `unknown` log line is the only record that a destroy may have been sent.
That is a single observation, not proof that OpenBao never applies such a request later.

### 6.3 Criterion 3: a retained verdict grants nothing

In the `authority` state (056-064), each dependency was classified retained and then used:

- The metadata token itself could not read the value it had just classified (057, HTTP 403).
- A compiler for another scope could not read it (058).
- The compiler could not decrypt the retained artifact (060, denied).
- The executor could not decrypt a damaged copy of it (061).
- After the executor's token was revoked, the key was still retained (063) and the executor was
  denied (064).

The same holds with a floor in the way: the executor's decrypt of version 1 failed while it was
blocked (029), and succeeded once the floor was lowered (038). Retention is a property of the provider's state. Authority is the identity's policy at the moment
of use. The experiment observed them change independently.

### 6.4 Criterion 4: provider limits and the alert policy the evidence supports

**Provider limits.**

- OpenBao: a deleted Transit key, a KV path whose metadata was deleted, and a name that never
  existed all answer HTTP 404 (010, 023, 024). There is no tombstone. The provider cannot say
  "lost" for either removal, so the classifier cannot either.
- OpenBao: the KV `max_versions` default prunes past ten versions (004, `max_versions: 0` in the
  answer, as §7.5 says). Pruning is visible only as `oldest_version`, never as a per-version flag.
- OpenBao: Transit trim needs `min_encryption_version` raised as well (§5.1). A trim is visible only
  as `min_available_version`, and the key's `keys` map hides trimmed and blocked versions alike.
- OpenBao: a sealed or paused provider hides everything. The fixture's out-of-band read failed too,
  so for those states the ground truth is the state set, not a reading (§3.2).
- OpenBao: whether an unanswered mutation was applied can only be learned by asking afterwards.
- Local stores: no blocked state, and no removal that the metadata can see except a replacement
  against a recorded digest or MAC. A deleted key is invisible.
- Every candidate: presence is not usability (§6.1, §6.3).

**Alert policy the evidence supports.** It is a proposal for
[retention and recovery policy](https://github.com/ginsys/bronzeward/issues/15), not a setting:

1. **lost**: alert at once. It rests on a positive field of the answer.
2. **blocked**: alert at once, and say it is reversible. The admin's undelete or lowered floor
   restored use (034-035, 038).
3. **retained → unknown**: alert at once as a regression. A 404 for a name that resolved before is
   what a deleted key or deleted metadata looks like (023-024), and only the transition gives it
   away.
4. **unknown that persists**: alert after the interval. The evidence does not fix the interval; it
   shows only that a partition, a pause and a seal all look alike from the client, apart from the
   reason.
5. **retained with a scheduled deletion**: warn before the time in the reason (002).

## 7. Limits

- **One OpenBao topology**, as in the provider-capability run: single node, one unseal share.
  Standby nodes, performance replicas and auto-unseal were not exercised. HTTP 503 was seen only as
  "sealed".
- **The administrator ran as root.** Rows 015-020 and 031-033 used the root token. A least-privilege
  administrator policy is still unwritten.
- **One pass per state.** In particular, the unanswered destroy was not applied in this one run.
- **The alert interval is a parameter** (`RC_ALERT_AFTER`, 5 s in row 043). It shows how the monitor
  behaves, not what the interval should be.
- **KV `delete_version_after` was observed only before its time.** Blocked after the time is the
  same rule as a past `deletion_time` and is covered by the rule checks, not by a capture.
- **Host curl.** The classifier's client is the host's curl, not a pinned image.
- **Local stores ran as one uid**, as before. Denial was produced with file modes, not separate
  users.
- **OpenBao's Transit `soft_deleted` field** is present in the 2.6.1 key answer (027) and false
  throughout. Transit soft deletion was not exercised.

## 8. Recommendation

This selects nothing. It tells [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13)
and [retention and recovery policy](https://github.com/ginsys/bronzeward/issues/15) what classification
can rest on.

1. **OpenBao's metadata is sufficient for §7.6 under a metadata-only policy**, with one exception:
   whole-key and whole-path deletion come out as unknown, not lost. §7.5 already makes restricting
   them a deployment requirement. The classifier needs Bronzeward's own record of what was
   referenced to turn that 404 into a regression alert.
2. **The local candidates meet §7.6 only as far as their metadata goes.** They have no blocked state
   and cannot see a key loss. Their lost depends on the writer keeping the recorded digest honest.
   This supports the provider-capability run's conclusion: on this evidence, §7.1's condition for a
   local provider is not met.
3. **Retention and authority must stay separate checks**, as §7.6 says. Nothing observed here would
   let a retained verdict stand in for a point-of-use read or decrypt.

## 9. Hand-off

**To [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13):** §8, and §6.4's
provider limits.

**To [retention and recovery policy](https://github.com/ginsys/bronzeward/issues/15):** the alert
policy of §6.4. The interval is still open. Two deployment requirements follow from §6.4: restrict
Transit key deletion and KV metadata deletion, whose results the classifier cannot tell from a name
that never existed; and set KV `max_versions` explicitly wherever a path is overwritten.

**To [key loss and restoration](https://github.com/ginsys/bronzeward/issues/10):** a deleted key
file leaves both local candidates retained (081, 096). Key loss has to be found by a use-time check
or a key inventory, not by this classifier.
