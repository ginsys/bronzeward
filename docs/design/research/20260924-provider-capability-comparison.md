# Provider capabilities: OpenBao, a local age-backed store and SOPS/age

| | |
|---|---|
| **Date** | 24 September 2026 |
| **Work item** | [Experiment E5 - compare provider capabilities](https://github.com/ginsys/bronzeward/issues/8) |
| **Design reference** | [§7.3 Secret and encryption provider candidates](../Talos_Configuration_and_Machine_Management_Design.md#73-secret-and-encryption-provider-candidates), [§7.5 Rotation and retention contract](../Talos_Configuration_and_Machine_Management_Design.md#75-rotation-and-retention-contract), [§7.6 Metadata-only dependency checks](../Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts) |
| **Artifacts** | [`experiments/e5-provider-capabilities/`](../../../experiments/e5-provider-capabilities/README.md), evidence under [`experiments/e5-provider-capabilities/evidence/`](../../../experiments/e5-provider-capabilities/evidence/) |
| **Decision enabled** | Which provider properties each candidate supplies itself, which a Bronzeward provider would have to supply by convention, and which none of them supplies. This is the input to [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13). No provider, physical layout or deployment profile is selected here. |

## 1. Question

Design §7.3 makes OpenBao KV v2 and Transit the primary profile. It also asks for an investigation
of "a local age-backed encrypted store for home installations and SOPS/age as an alternative or
import/export mechanism". The inventory to take before selecting anything is "secret
creation/read/versioning, artifact encryption, any signing needs, key custody, startup unlock,
rotation, metadata-only checks, backup/restore and migration". §7.1 says the interfaces "must also
permit a simpler local provider if the investigation proves the required properties".

The question is therefore not which candidate is better. It is which of the required properties
each candidate has itself, under what permission, and whether what it offers is an encryption
primitive or complete versioned secret behaviour. The design states the required properties in
three places:

- **Generations.** Talos bundles are "separate immutable application-level generations rather than
  implicit latest values at a mutable path" (§7.3), and publication pins every required version
  (§7.4 step 1).
- **Retention.** History limits and pruning are known and explicit (§7.5). A reversible restriction
  such as a decryption-version floor differs from permanent removal (§7.5, §7.6).
- **Metadata without values.** A provider exposes enough metadata to classify each dependency as
  retained, blocked, lost or unknown, and "a local file's presence does not prove ciphertext
  validity or possession of its private key" (§7.3, §7.6).

Two role boundaries come from the specification: the executor "holds scoped artifact decryption and
operation credentials, not compiler-level secret access"
([execution-recovery §3.1](../../spec/execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction)),
and compilation resolves values under the compiler identity (§7.4 step 2).

The §18.1 row for E5 is wider than this report. Soft deletion, destruction and trim, permanent key
loss, unknown monitor results, and restoring backups of differing ages belong to
[retention and metadata](https://github.com/ginsys/bronzeward/issues/9) and
[key loss and restoration](https://github.com/ginsys/bronzeward/issues/10). This report shows that
each capability exists, what permission it needs, and one backup round trip per candidate.

## 2. Candidates and identities

| | OpenBao KV v2 + Transit | local age-backed store | SOPS with age |
|---|---|---|---|
| Built by | the [investigation fixtures](20260919-investigation-fixtures.md): single node, integrated Raft, real init and unseal | this experiment, under `fixtures/.state/data/age-store` | this experiment, under `fixtures/.state/data/sops` |
| A secret is | a KV v2 path; one immutable generation per path, created with `cas=0` | `objects/<name>/<gen>.age`, created with `O_EXCL`, plus `meta/<name>/<gen>.json` (created, recipient, ciphertext SHA-256; no value) | one `secrets/<name>.enc.json` file per logical secret |
| An artifact is | Transit ciphertext under key `bw-artifact` | `artifacts/<id>.age`, encrypted to the executor | `artifacts/<id>.enc.json`, the whole artifact as binary input, encrypted to the executor |
| An identity is | a token with one policy | possession of one age identity file | possession of one age identity file, and the `.sops.yaml` rules that pick recipients |

The identities are the roles the design names, with one policy or key each:

| Identity | OpenBao policy | Local stores |
|---|---|---|
| `publisher` | `create` on `secret/data/gen/*` only: may create a generation, never update one | holds only public recipients |
| `compiler` | `read` on `secret/data/*`, `update` on `transit/encrypt/bw-artifact` | the key source secrets are encrypted to |
| `executor` | `update` on `transit/decrypt/bw-artifact` only | the key artifacts are encrypted to |
| `metadata` | the fixture's `bw-metadata-only`: KV metadata and Transit key reads, no data | no key |
| `admin` / `operator` | the root token, standing for the operator's administrator | whoever holds the store directory |
| `recovery` | not used | SOPS only: a second recipient of source secrets |

The OpenBao policies are in [`run/lib.sh`](../../../experiments/e5-provider-capabilities/run/lib.sh).
In the local stores every identity runs as one uid. The separation is by which key file a command is
given, not by the operating system, and each local candidate records that as an unsupported cell.

## 3. What was built

### 3.1 Cells

Every operation is one *cell*: one command, under one identity, with its expected outcome written
before it runs. [`run/lib.sh`](../../../experiments/e5-provider-capabilities/run/lib.sh) runs it,
derives the observed outcome from the exit status and the output alone (`ok`, `denied` for a 403 or
"permission denied", otherwise `fail`), keeps the whole output as a transcript, and appends one row
to [`cells.tsv`](../../../experiments/e5-provider-capabilities/evidence/cells.tsv). A mismatch
between expected and observed is recorded, not retried. Each row carries a `kind`, the distinction
acceptance criterion 2 asks for:

| kind | meaning |
|---|---|
| `primitive` | an encryption or storage primitive, with no version identity of its own |
| `versioned` | complete versioned secret or provider behaviour |
| `metadata` | an observation made from metadata alone, without the value |
| `unsupported` | the candidate has no such capability; named, never left blank |
| `-` | a procedural step (a seal, a crash, a snapshot) that later cells depend on; not a capability claim |

| Script | Cells | What it does |
|---|---|---|
| [`run/openbao`](../../../experiments/e5-provider-capabilities/run/openbao) | 001-090 | KV v2 and Transit, column by column |
| [`run/age-store`](../../../experiments/e5-provider-capabilities/run/age-store) | 091-135 | builds the local store and exercises it |
| [`run/sops`](../../../experiments/e5-provider-capabilities/run/sops) | 136-178 | SOPS as a store |
| [`run/migrate`](../../../experiments/e5-provider-capabilities/run/migrate) | 179-194 | moves generations and artifacts between candidates, and SOPS import/export |
| [`run/all`](../../../experiments/e5-provider-capabilities/run/all) | | the four above on a fresh fixture, then the fixture's evidence bundle and leak scan |

A cell number in this report is the `n` column of `cells.tsv`; its transcript is
`evidence/transcripts/<n>-<candidate>-<column>.txt`.

### 3.2 Why shell

The issue's limits say the pinned `age` and `sops` are command-line versions and "a library
integration may behave differently and is not evidenced by these runs". Driving the pinned CLIs is
therefore exactly the claim being measured. A Go program linking the libraries would measure
something else.

### 3.3 Pinned versions

| Component | Version |
|---|---|
| OpenBao | 2.6.1 (`ba7ad8861d0578cd4da4f7b9e5a6756d30484f8f`), image `ghcr.io/openbao/openbao:2.6.1@sha256:5b2486ab0fb90bbc788cc345b0a08616dfb375873ee8be5df3a2fd4d378a67e0` |
| `age`, `age-keygen` | v1.3.2 |
| `sops` | 3.13.3 |
| Pins | `fixtures/versions.env` as of c74f953; binaries checked against the manifest digests |
| Captured from | d1d2efb (`manifest commit` in [`versions.txt`](../../../experiments/e5-provider-capabilities/evidence/versions.txt)) |

### 3.4 Synthetic values and the leak scan

Every value is `BWSYNTH-e5-<name>-<24 hex digits>`, generated per run outside the checkout. Values
reach the CLIs over stdin only, never argv. A read is checked by comparing the SHA-256 of what it
printed with the expected digest, and only the verdict ("value digest matches") reaches the
transcript.

After the four scripts, `fixtures/bin/evidence` scans `.state/data` (the live local stores) and
`.state/backups` (the OpenBao snapshot and the store snapshots, with store tars expanded) for the
`BWSYNTH-` prefix and this run's fixture credentials. It plants its own positive control at
`.state/data/canary-control.txt`. `run/all` fails unless the control is found and nothing else is.
Observed ([`summary.txt`](../../../experiments/e5-provider-capabilities/evidence/summary.txt),
[`leak-scan.txt`](../../../experiments/e5-provider-capabilities/evidence/leak-scan.txt)):

```text
leak-scan lines: 3
positive control hits: 3
other hits: 0
cells: 194, mismatches: 0
```

The three hits are the control in the live tree and its copies in the two store snapshots. No
synthetic value appears in plaintext in either local store or either store snapshot. The OpenBao
snapshot was scanned too, but it is encrypted by OpenBao's barrier and holds no control, so its
clean result shows nothing.

### 3.5 Reproduction

```sh
fixtures/bin/up
E5_OUT=<empty absolute path outside the checkout> experiments/e5-provider-capabilities/run/test-lib
E5_OUT=<the same path> experiments/e5-provider-capabilities/run/all
E5_OUT=<the same path> experiments/e5-provider-capabilities/run/collect-evidence
fixtures/bin/down
```

`run/test-lib` checks the recorder and the SOPS key isolation (§5.5) and needs no running fixture.
`run/collect-evidence` copies the text evidence into the checkout, rewrites the checkout and output
paths to `<repo>` and `<E5_OUT>`, and refuses anything matching the fixture's scan patterns, an age
secret key, an OpenBao token, or a path in the operator's home directory. The transcripts are kept
byte for byte; `.gitattributes` exempts them alone from the whitespace and conflict-marker checks.

## 4. The matrix

Rows are the §7.3 inventory. Each entry gives the verdict, the `kind`, and the cells that establish
it. **Native** means the candidate enforces the property itself. **Convention** means the property
holds only because the prototype's writer behaves; the store cannot enforce it. **Unsupported** is a
named absence.

| §7.3 element | OpenBao KV v2 + Transit | local age-backed store | SOPS with age |
|---|---|---|---|
| **Create / read / versioning** | **Native, versioned.** `cas=0` creates a generation once; a second create is refused ([003](../../../experiments/e5-provider-capabilities/evidence/transcripts/003-openbao-create-read-version.txt), 400 "check-and-set parameter did not match"). A mutable path keeps 10 versions: after 11 writes `oldest=2` and version 1 reads "No value found" ([019](../../../experiments/e5-provider-capabilities/evidence/transcripts/019-openbao-create-read-version.txt), [020](../../../experiments/e5-provider-capabilities/evidence/transcripts/020-openbao-create-read-version.txt)). An explicit `max_versions=0` does the same ([034](../../../experiments/e5-provider-capabilities/evidence/transcripts/034-openbao-create-read-version.txt)). `cas_required` refuses a write without CAS ([036](../../../experiments/e5-provider-capabilities/evidence/transcripts/036-openbao-create-read-version.txt)). The `gen/` paths are not `cas_required`, so create-only is the publisher's policy, not the path's: the administrator adds version 2 to a generation path without CAS ([087](../../../experiments/e5-provider-capabilities/evidence/transcripts/087-openbao-create-read-version.txt)), the metadata shows `current=2` (088), and both versions stay readable (089-090). | **Primitive; generations by convention.** `O_EXCL` refuses a second create ([096](../../../experiments/e5-provider-capabilities/evidence/transcripts/096-age-store-create-read-version.txt)), and of two concurrent creators exactly one wins ([101](../../../experiments/e5-provider-capabilities/evidence/transcripts/101-age-store-create-read-version.txt)). A writer that deletes the file first replaces a generation, and nothing records it (102-104). No retention limit and no pruning (105, unsupported). | **Primitive.** Create-only exists only in the writer's own `noclobber` ([142](../../../experiments/e5-provider-capabilities/evidence/transcripts/142-sops-create-read-version.txt)). An update replaces the only copy (146-147). No version history (148) and no compare-and-set or locking (150), both unsupported: two concurrent `sops set` adding different keys to one file both exit 0, and in 10 of 10 attempts the file still decrypted with only one of the two keys ([149](../../../experiments/e5-provider-capabilities/evidence/transcripts/149-sops-create-read-version.txt)). |
| **Artifact encryption** | **Native, primitive.** Transit: the compiler encrypts ([038](../../../experiments/e5-provider-capabilities/evidence/transcripts/038-openbao-artifact-encryption.txt)), only the executor decrypts ([039](../../../experiments/e5-provider-capabilities/evidence/transcripts/039-openbao-artifact-encryption.txt), 040-041 denied), and neither the executor nor the metadata identity can encrypt (042-043 denied). | **Primitive.** Encrypted to the executor's recipient ([106](../../../experiments/e5-provider-capabilities/evidence/transcripts/106-age-store-artifact-encryption.txt)); the executor opens it, the compiler cannot ([107](../../../experiments/e5-provider-capabilities/evidence/transcripts/107-age-store-artifact-encryption.txt), [108](../../../experiments/e5-provider-capabilities/evidence/transcripts/108-age-store-artifact-encryption.txt)). Anyone holding the public recipient can encrypt. | **Primitive.** The whole artifact is encrypted as binary input to the executor (151-152); the compiler cannot open it ([153](../../../experiments/e5-provider-capabilities/evidence/transcripts/153-sops-artifact-encryption.txt)). |
| **Signing** | **Native, primitive.** An `ed25519` Transit key signs and verifies under the administrator ([045](../../../experiments/e5-provider-capabilities/evidence/transcripts/045-openbao-signing.txt)); the compiler is denied ([046](../../../experiments/e5-provider-capabilities/evidence/transcripts/046-openbao-signing.txt)). | **Unsupported** (109): age has no signatures. | **Unsupported** (154): the MAC authenticates the file to key holders only. |
| **Key custody** | **Native.** The Transit key reports `exportable=false deletion_allowed=false` to the metadata identity ([047](../../../experiments/e5-provider-capabilities/evidence/transcripts/047-openbao-key-custody.txt)), and export is refused even to the administrator ([048](../../../experiments/e5-provider-capabilities/evidence/transcripts/048-openbao-key-custody.txt), "private key material is not exportable"). The barrier uses one Shamir share, threshold 1 ([049](../../../experiments/e5-provider-capabilities/evidence/transcripts/049-openbao-key-custody.txt)). | **Files.** Key files are `0600` in a `0700` directory; the object and metadata directories took the caller's umask, `0775` here ([093](../../../experiments/e5-provider-capabilities/evidence/transcripts/093-age-store-key-custody.txt)). No boundary between identities in one uid (094, unsupported). | **Files.** One key file per role, recipients chosen by `.sops.yaml` path rules (136-139). No boundary in one uid (140, unsupported). By default sops also tries the caller's own SSH keys as identities (§5.5). |
| **Startup unlock** | **Native.** After a seal a read fails 503 "Vault is sealed" ([073](../../../experiments/e5-provider-capabilities/evidence/transcripts/073-openbao-startup-unlock.txt)); the operator unseals with the key share (074) and reads resume (075). A crash and restart through the fixture (076-078) comes back unsealed, because the fixture's `start` unseals (§7). | **Possession of an identity file.** An unencrypted identity reads unattended ([126](../../../experiments/e5-provider-capabilities/evidence/transcripts/126-age-store-startup-unlock.txt)). A passphrase-protected one cannot: age reads the passphrase from a terminal only ([128](../../../experiments/e5-provider-capabilities/evidence/transcripts/128-age-store-startup-unlock.txt), "/dev/tty is not available"). Fed through a pseudo-terminal it works ([129](../../../experiments/e5-provider-capabilities/evidence/transcripts/129-age-store-startup-unlock.txt)), so the feeding process holds the passphrase. | **Possession of an identity file.** Without one a read fails ([171](../../../experiments/e5-provider-capabilities/evidence/transcripts/171-sops-startup-unlock.txt)); with one it succeeds (172). |
| **Rotation** | **Native, versioned.** `rotate` adds key version 2 ([051](../../../experiments/e5-provider-capabilities/evidence/transcripts/051-openbao-rotation.txt)); version-1 ciphertext still decrypts (052). `rewrap` moves ciphertext to the new version without returning plaintext; the compiler is refused it and the administrator may ([054](../../../experiments/e5-provider-capabilities/evidence/transcripts/054-openbao-rotation.txt) denied, [055](../../../experiments/e5-provider-capabilities/evidence/transcripts/055-openbao-rotation.txt)). `min_decryption_version=2` blocks version 1 ("too old", [058](../../../experiments/e5-provider-capabilities/evidence/transcripts/058-openbao-rotation.txt)), and lowering it restores access ([061](../../../experiments/e5-provider-capabilities/evidence/transcripts/061-openbao-rotation.txt)): a reversible floor. A source secret rotates as a new generation beside the old (062-063). | **Primitive.** Re-encrypt a generation to a new key ([111](../../../experiments/e5-provider-capabilities/evidence/transcripts/111-age-store-rotation.txt)); the old key no longer reads it (113). The plaintext passes through the rotating process (115, unsupported: no rewrap). Blocking an old key means moving its file, which leaves nothing the store can report (116, unsupported: no reversible floor). | **Primitive.** `rotate` replaces the data key: the value's ciphertext changes ([159](../../../experiments/e5-provider-capabilities/evidence/transcripts/159-sops-rotation.txt)), and the new values no longer open under the file's old recipient stanzas ([160](../../../experiments/e5-provider-capabilities/evidence/transcripts/160-sops-rotation.txt), an AES-GCM authentication failure). `updatekeys` to a new compiler key keeps the data key: the ciphertext is unchanged ([165](../../../experiments/e5-provider-capabilities/evidence/transcripts/165-sops-rotation.txt)), and the old stanzas with the removed compiler key still open the current value ([166](../../../experiments/e5-provider-capabilities/evidence/transcripts/166-sops-rotation.txt)). The removed key is refused on the new file ([168](../../../experiments/e5-provider-capabilities/evidence/transcripts/168-sops-rotation.txt)). No reversible floor (170, unsupported). |
| **Metadata-only checks** | **Native.** The metadata identity reads KV version metadata (`created_time`, `deletion_time`, `destroyed`, `current_version`, `oldest_version`, `max_versions`; [064](../../../experiments/e5-provider-capabilities/evidence/transcripts/064-openbao-metadata-only.txt)), lists paths (065), and reads Transit key state (067). It cannot decrypt (068). The compiler cannot read metadata (069), and the executor cannot read key state (070): metadata and data are separate grants. | **Convention.** The metadata file proves presence, recipient and that the ciphertext matches its recorded digest ([117](../../../experiments/e5-provider-capabilities/evidence/transcripts/117-age-store-metadata-only.txt)). A damaged ciphertext is caught ([125](../../../experiments/e5-provider-capabilities/evidence/transcripts/125-age-store-metadata-only.txt)). A generation whose only key was deleted still passes every metadata check ([122](../../../experiments/e5-provider-capabilities/evidence/transcripts/122-age-store-metadata-only.txt)) and cannot be read ([123](../../../experiments/e5-provider-capabilities/evidence/transcripts/123-age-store-metadata-only.txt)). The metadata is written by the writer, not by the store. | **Partial.** Without any key the file shows `lastmodified`, the recipient count, that a MAC is present, and the sops version ([155](../../../experiments/e5-provider-capabilities/evidence/transcripts/155-sops-metadata-only.txt)); decryption fails (157). There is no version history to report, and the MAC is verifiable only with a key. |
| **Backup / restore** | **Native.** A Raft snapshot (079), a generation created after it (080), a forced restore (081): the older generation reads (082) and the newer one is gone ([083](../../../experiments/e5-provider-capabilities/evidence/transcripts/083-openbao-backup-restore.txt), "No value found"). Transit ciphertext from before the snapshot, including rewrapped ciphertext, still decrypts ([084](../../../experiments/e5-provider-capabilities/evidence/transcripts/084-openbao-backup-restore.txt), [085](../../../experiments/e5-provider-capabilities/evidence/transcripts/085-openbao-backup-restore.txt)). | **Files.** A tar of the store (130), a generation after it (131), a restore (132): the older generation reads (133), the newer has no metadata ([134](../../../experiments/e5-provider-capabilities/evidence/transcripts/134-age-store-backup-restore.txt)), and the artifact still opens (135). The keys are inside the snapshot (§6.3). | **Files.** A tar (173), a change read back (174-175), a restore (176): the value reads as before the change ([177](../../../experiments/e5-provider-capabilities/evidence/transcripts/177-sops-backup-restore.txt)), and the artifact opens (178). |
| **Migration** | *Into OpenBao:* see the next two columns. *Out:* one KV version becomes an age generation ([185](../../../experiments/e5-provider-capabilities/evidence/transcripts/185-migration-openbao-to-age.txt), 186). Of 11 versions at a mutable path, 10 move and the pruned one cannot ([187](../../../experiments/e5-provider-capabilities/evidence/transcripts/187-migration-openbao-to-age.txt)). Transit ciphertext cannot move at all: the key is not exportable (189, unsupported). | *To OpenBao:* the migrating process needs the compiler's age key, the publisher's token, and the administrator's token for the custom metadata, at once ([179](../../../experiments/e5-provider-capabilities/evidence/transcripts/179-migration-age-to-openbao.txt)). The source's creation time is lost unless carried as custom metadata ([181](../../../experiments/e5-provider-capabilities/evidence/transcripts/181-migration-age-to-openbao.txt)). Repeating the migration is refused, because the publisher may only create ([182](../../../experiments/e5-provider-capabilities/evidence/transcripts/182-migration-age-to-openbao.txt)). An artifact moves to Transit only through a process holding the executor's age key and the compiler's token (183-184). | *Import:* a SOPS file becomes a new KV generation ([190](../../../experiments/e5-provider-capabilities/evidence/transcripts/190-migration-sops-import.txt), 191). *Export:* a KV generation becomes a SOPS file, which the recovery identity can read ([192](../../../experiments/e5-provider-capabilities/evidence/transcripts/192-migration-sops-export.txt), [193](../../../experiments/e5-provider-capabilities/evidence/transcripts/193-migration-sops-export.txt)). |

### 4.1 Permissions

The permission half of acceptance criterion 1. ✓ allowed, ✗ refused, blank not exercised. Every
mark is an observed cell.

**OpenBao**, by policy:

| Operation | publisher | compiler | executor | metadata | admin |
|---|---|---|---|---|---|
| create a generation (`cas=0`, new path) | ✓ 001 | | | | |
| replace a generation | ✗ 002 (403) | | | | ✗ 003 with `cas=0`; ✓ 087 adds a version without CAS |
| read a secret value | | ✓ 004 | ✗ 005 | ✗ 006 | ✓ 021 |
| read KV metadata | | ✗ 069 | | ✓ 064 | ✓ 019 |
| read Transit key state | | | ✗ 070 | ✓ 067 | |
| encrypt an artifact | | ✓ 038 | ✗ 043 | ✗ 042 | |
| decrypt an artifact | ✗ 041 | ✗ 040 | ✓ 039 | ✗ 068 | |
| rewrap | | ✗ 054 | | | ✓ 055 |
| rotate a key, set its decryption floor | | | | | ✓ 050, 057 |
| sign | | ✗ 046 | | | ✓ 045 |
| export a Transit key | | | | | ✗ 048 (key not exportable) |
| snapshot, restore | | | | | ✓ 079, 081 (root token, through the fixture) |

Unseal needs no token, only the key share (074).

The admin column is the root token. No narrower policy was written or tested for rewrap, rotation,
the decryption floor, signing or snapshots, so this run shows who is refused them, not the least
privilege that allows them.

**Local stores**, by key possession. The operating system does not separate these identities here
(094, 140).

| Operation | needs | age store | SOPS |
|---|---|---|---|
| create a secret | the public recipient and write access to the directory | ✓ publisher 095 | ✓ no key 141 |
| replace a secret | write access to the directory | ✓ delete and recreate 102-103 | ✓ compiler 146 |
| read a secret | the recipient's key | compiler ✓ 097, executor ✗ 098 | compiler ✓ 143, recovery ✓ 144, executor ✗ 145 |
| open an artifact | the executor's key | executor ✓ 107, compiler ✗ 108 | executor ✓ 152, compiler ✗ 153 |
| read metadata | read access to the files | ✓ 100, 117 | ✓ 155 |
| rotate, change recipients | a key that decrypts | ✓ 111 | ✓ 158, 164 |

The SOPS replace ran under the compiler key; `sops set` was not tried without a key.

## 5. Failures hit while building it

Each of these was a defect in the instrument, found by reading a transcript rather than a verdict.
Each was fixed and the whole capture re-run on a fresh fixture. None is a finding about a candidate
except where the entry says so.

### 5.1 A sealed OpenBao answers `bao status` with exit 2

The first OpenBao run recorded the "is it sealed?" cell as a failure. `bao status` exits 2 when the
instance is sealed, and that is the answer, not an error. The helper now accepts 0 or 2.

### 5.2 A passphrase typed after the prompt went away

The pseudo-terminal helper for the passphrase-protected identity typed the passphrase more times
than age asked for it. The extra copy hit a closed pipe, and `pipefail` failed the cell. It now
types exactly one copy per prompt. The same investigation, in development runs that were not kept as
evidence, showed that `age -o` creates its output
under the caller's umask (`0664` with umask `002`): a decrypted file is group-readable unless the
caller narrows the umask first. The helper narrows it to `077`.

### 5.3 A create-only helper reported "created" after a refused write

In both local stores, a helper wrote under `noclobber`, and then printed its success line even when
the write was refused, because the refusal did not stop the function. Every such helper now returns
on the refusal. The age store's key creation and rotation steps had the same defect.

### 5.4 The SOPS race could not fail

The first race cell ran two writers of the same key and recorded which value the file held. Either
answer passed, so the cell showed nothing, and a procedural cell had to write a known value back for
the cells after it. The race now runs on its own file: two writers each add a different key, ten
times over, and the cell passes only if some attempt ends with both writers told they succeeded and
the file readable with exactly one of the two keys. A file holding neither key could be one sops
cannot read at all, so it is counted apart rather than as a lost update. Updates were lost in 10 of
10 attempts, and no attempt left a file holding neither key (149).

### 5.5 sops looked for the operator's own SSH keys

The first complete capture's refusals listed where sops had looked for an identity, including
`$HOME/.ssh/id_ed25519` and `$HOME/.ssh/id_rsa`. That capture was discarded; `run/test-lib`'s control
check reproduces the lookup. sops 3.13.3 looks for the caller's SSH keys as age
identities by default. The "no key" cells were therefore not isolated from the operator's keys, and
the transcripts named the operator's home directory. They still failed, because no key there was a
recipient.

Every sops call now goes through `e5_sops`, which points `HOME` and `XDG_CONFIG_HOME` at an empty
directory and clears every `SOPS_AGE_*` key variable. `run/test-lib` checks this against raw sops
as a control: raw sops must name `$HOME/.ssh`, and `e5_sops` must not. `run/collect-evidence` now also
refuses any path in the operator's home.

This is also a custody finding (§6.3): identities a caller never passed to sops are candidates for
decryption. A decryption through an SSH key that is a recipient was not exercised.

### 5.6 SOPS rotation was labelled `versioned`

The SOPS `rotate` and `updatekeys` cells carried `kind=versioned`. SOPS keeps no versions, so both
are primitives, and the label misstated exactly what acceptance criterion 2 asks about. While
relabelling them, two metadata cells were added that show whether each operation replaced the data
key (159, 165). They are read from the value's ciphertext alone, and each states its expectation
before it runs.

A changed ciphertext alone could also be the same key under a fresh nonce, so review asked for
proof by decryption. Two cells graft the recipient stanzas of a copy taken before the operation onto
the file after it: after `rotate` the graft no longer opens the values (160), after `updatekeys` it
still does (166).

### 5.7 Local-store generations were labelled `versioned`

The age store's generation cells carried `kind=versioned`. Its generations are file names that only
the writer's convention keeps immutable; the store enforces nothing. They are now primitives, and
`versioned` is reserved for behaviour the provider itself enforces. The same review found that the
OpenBao create-only cells did not say whose restriction it was: the new cells 086-090 show that it
is the publisher's policy, and that the administrator can add a version to a generation path.

### 5.8 A migration refusal failed for another reason

The cell showing that a repeated migration cannot replace a generation migrated generation `g2`,
which is not encrypted to the rotated compiler key. age failed first, the write received empty
input, and the 403 was real but not what the cell claimed to show. The cell now migrates `g1`
again, and its transcript holds only the publisher's 403 (182). Every other expected-failure
transcript was checked for the same defect: each fails for the reason its cell names.

### 5.9 Verbatim transcripts fail `git diff --check`

bao's table headers (`======= Metadata =======`), its trailing blank lines and sops's
space-padded blank lines trip the whitespace and conflict-marker checks that CI runs over the whole
tree. Tidying them would make the evidence differ from what the tools printed. `.gitattributes`
exempts `evidence/transcripts/*.txt` alone.

## 6. What this decides

### 6.1 Criterion 1: the capability and permission matrix

Section 4 is the matrix: 3 candidates by the 9 §7.3 elements plus permissions, with every entry
tied to observed cells and every absence named. 194 cells, 0 mismatches, one capture from
d1d2efb. The permission tables mark the operation/identity pairs that were not run as blank; not
every operation was tried under every identity.

### 6.2 Criterion 2: primitive or versioned provider behaviour

- **OpenBao** supplies complete versioned behaviour where the design needs it. A `cas=0` create
  cannot replace a generation, and the publisher's policy allows nothing else. The path does not
  enforce it on its own: the `gen/` paths are not `cas_required`, and the administrator added a
  version to one (087). A generation is immutable because of who holds which policy, which OpenBao
  enforces, not because of its path. Version history has a known limit that `max_versions=0` does
  not lift, which confirms §7.5's warning. Key versions have a reversible floor. All of it is
  visible to an identity that cannot read values.
- **The local age store** has no versioned behaviour of its own; its cells are primitives. Its
  generations are file names that a well-behaved writer never reuses, and its metadata is what that
  writer leaves. A writer that deletes and recreates a file
  replaces a generation undetectably (102-104), and a writer that skips the metadata leaves the
  store with nothing to report. The primitive underneath, age encryption to a recipient, works and
  gives the compiler/executor split by recipient choice.
- **SOPS** is an encryption primitive over a file. It has no versions, no compare-and-set, no
  locking and no store of its own: concurrent writers both succeed and one update is lost (149). It is not a secret provider on its own.

The metadata evidence that decides each case is in the matrix's metadata row. The distinction that
matters for §7.6 is between metadata the *provider* maintains (OpenBao's version and key state) and
metadata the *writer* maintains (the age store's JSON files). Only the first survives a writer that
is wrong.

For [retention and metadata](https://github.com/ginsys/bronzeward/issues/9), two OpenBao
observations need provider-aware reading:

- With `min_decryption_version=2`, the Transit key listing shows only version 2 ([059](../../../experiments/e5-provider-capabilities/evidence/transcripts/059-openbao-rotation.txt)).
  The listing alone does not say whether version 1 is blocked or gone. `min_available_version`
  (0 here: nothing trimmed) read against `min_decryption_version` does. Trim itself was not
  exercised.
- A pruned KV version is simply absent. `oldest_version` moves past it (019), and its entry is gone
  from the version map
  ([`openbao-metadata.jsonl`](../../../experiments/e5-provider-capabilities/evidence/openbao-metadata.jsonl),
  `mutable/registry` lists versions 2-11). `current_version` still makes the gap inferable, but the
  pruned version's creation time is gone.

### 6.3 Criterion 3: custody, unlock and migration trade-offs

**Custody.**

- OpenBao keeps Transit keys inside itself and refuses to export them. That protects them, and it
  also means artifacts can never leave OpenBao as ciphertext (§6.3, migration).
- The local stores keep keys as files. Their protection is file modes and process separation, which
  this single-uid run could not test.
- In both local layouts built here, the keys live in the same tree as the ciphertext, so a store
  snapshot carries both. A backup of either store is as sensitive as its keys. A real local
  provider must keep keys outside the backed-up tree, or accept that.
- sops, by default, looks for the caller's SSH keys as identities (§5.5).

**Unlock.**

- OpenBao's unlock is the unseal key share, held by an operator. Everything is unavailable until it
  is supplied, and nothing else is needed after.
- The local stores' unlock is the identity file itself. An unattended restart needs an unencrypted
  identity on disk. A passphrase-protected identity cannot be used unattended with the pinned age,
  and feeding it through a terminal moves the passphrase into the feeding process.
- Neither local candidate has a sealed state: a stolen disk with an unencrypted identity is an
  unsealed store.

**Rotation.**

- OpenBao rotates without plaintext leaving it (`rewrap`), and can block an old key version
  reversibly.
- The age store must decrypt to rotate. Rotation also rewrites a generation's ciphertext in place,
  so a dependency record that pins a ciphertext digest breaks on rotation, while the generation's
  identity (name and number) and its value do not change.
- SOPS `updatekeys` removes a recipient without changing the data key (165). A removed holder who
  kept an older copy of the file can still read the current value: the old copy's stanzas open it
  (166). Revocation in SOPS needs `rotate`, which replaces the data key (159-160).

**Migration.** Keys differ between candidates, so every migration decrypts and re-encrypts. The
migrating process holds both sides at once:

- the compiler's source key, the publisher's token, and the administrator's token for the carried
  metadata, for a secret (179)
- the executor's key and the compiler's token for an artifact (183)

That process sees every plaintext it moves.

- Generation identity survives, because the target path or file name carries it.
- Creation time does not survive, unless it is carried as metadata (181).
- Pruned versions cannot migrate at all (187).
- Transit ciphertext can only move by an executor decrypting it (189).

Migrating away from OpenBao therefore means re-encrypting every retained artifact or regenerating
it: the §7.5 distinction between applying stored ciphertext and regeneration.

**Unsupported, each recorded as an `unsupported` cell:**

- signing in both local candidates (109, 154)
- rewrap without plaintext in the age store (115). SOPS `updatekeys` re-wraps the data key for new
  recipients without re-encrypting the values, but it decrypts the data key to do so, and the old
  data key stays valid (166).
- a reversible decryption floor in both local candidates (116, 170)
- retention limits in the age store (105)
- version history, and compare-and-set or locking, in SOPS (148, 150)
- an operating-system permission boundary in both local candidates as run here (094, 140)
- moving Transit ciphertext out of OpenBao (189)

**Signing.** The PoC needs none. The only signing material the design names is for enrolment
([§5.1 core components](../Talos_Configuration_and_Machine_Management_Design.md#51-core-components),
"enrolment signing material"), and first-milestone acceptance
"excludes new-machine enrolment" (§18.2). Signing was exercised on OpenBao only to record that the
capability exists and is a separate grant.

## 7. Limits

- **One OpenBao topology.** Single node, integrated Raft, one unseal share, real init and unseal.
  Nothing here speaks to high availability, auto-unseal or seal wrapping.
- **The crash-to-sealed window was not observed.** The fixture's `inject start openbao` unseals
  automatically, so the crash cells (076-078) show recovery, not the sealed state an unattended
  restart would leave. The sealed state was observed through an explicit `bao operator seal`
  (071-075).
- **CLI only.** `age` v1.3.2 and `sops` 3.13.3 were driven as pinned command-line tools. A library
  integration may behave differently and is not evidenced.
- **One uid.** Every local-store identity ran as the same user. The permission split between local
  identities is by key possession only. Whether operating-system users and modes can enforce it
  was not tested.
- **The local store is this experiment's own.** Its layout is the smallest that answers §7.3, not a
  proposed design. Its weaknesses in convention-only properties are partly weaknesses of the layout.
  Its inability to enforce anything against a writer with directory access is not.
- **Not exercised here, owned elsewhere.** KV soft deletion, destruction and metadata deletion;
  Transit trim and key deletion; permanent key loss beyond one deleted local identity; classifying
  dependencies as retained, blocked, lost or unknown
  ([retention and metadata](https://github.com/ginsys/bronzeward/issues/9)). Restoring application
  and provider backups of differing ages
  ([key loss and restoration](https://github.com/ginsys/bronzeward/issues/10)).
- **Digests in the cell table.** `cells.tsv`'s command column carries the SHA-256 of each expected
  synthetic value, because the digest is an argument of the check. The values are random per run
  and never committed, so the digests reveal nothing. The column is not free of value-derived
  data.
- **One capture.** The SOPS race lost an update in 10 of 10 attempts here. That shows the race
  exists, not how often it is lost.
- **No administrator least privilege.** Rewrap, rotation, the decryption floor, signing and
  snapshots ran under the root token (§4.1).
- **Migration pairs.** Each local candidate was migrated to and from OpenBao, and SOPS imported
  into and exported from it. Migration directly between the age store and SOPS was not run.

## 8. Recommendation

This does not select a provider. It narrows what
[deployment profile selection](https://github.com/ginsys/bronzeward/issues/13) has to choose
between.

1. **OpenBao KV v2 with Transit meets every evidenced part of the required provider contract.** It
   does so natively: create-only generations under the publisher's policy, the compiler/executor
   split, metadata readable without values, a reversible decryption floor, and rotation without
   plaintext leaving it. The publisher, compiler, executor and metadata identities ran under
   least-privilege policies; the administrator's operations ran as root, so a narrower
   administrator policy is still to be written and tested. Nothing here argues against keeping it
   as the primary profile, and no negative finding blocks retention and metadata or key loss and
   restoration.
2. **On this evidence, §7.1's condition for the local age-backed store is not met** ("if the
   investigation proves the required properties"). Every property it showed is the writer's
   convention, and nothing stops a writer with directory access from replacing a generation or
   leaving stale metadata. Meeting it would need three things first: Bronzeward itself as the only
   writer; keys stored outside the backed-up tree; and a multi-user run showing that
   operating-system separation enforces the compiler/executor/publisher split.
3. **On this evidence, SOPS/age fits the import/export role §7.3 names, not the provider role.** It
   has no versions and no compare-and-set, and it loses concurrent writes silently. As an export
   format, revoking a recipient takes `sops rotate`, not `updatekeys`, and it must run with its
   default key locations disabled, or it looks for the operator's SSH keys.
4. **Migration between providers is a profile decision.** So is which identity may perform it. It
   always passes plaintext through one process holding both sides' credentials, cannot carry
   pruned versions, and cannot move Transit ciphertext.

## 9. Hand-off

**To [retention and metadata](https://github.com/ginsys/bronzeward/issues/9):**

- OpenBao exposes per-version `created_time`, `deletion_time` and `destroyed`, plus the path's
  `current_version`, `oldest_version` and `max_versions`. It exposes Transit `latest_version`,
  `min_decryption_version`, `min_available_version`, `exportable` and `deletion_allowed`. All of
  these are readable by the fixture's metadata-only policy.
- The Transit key listing hides versions below the decryption floor, so blocked and trimmed
  versions must be told apart by `min_available_version` (§6.2). Trim was not exercised, so how a
  trimmed version appears is unobserved.
- The `gen/` paths' create-only property rests on the publisher's policy; the administrator can add
  versions (087). A classifier that trusts "one version per generation path" must check it.
- The local age store exposes only what its writer recorded. A lost key is invisible to its
  metadata (122-123).

**To [key loss and restoration](https://github.com/ginsys/bronzeward/issues/10):**

- An OpenBao restore removes generations created after the snapshot (083) and keeps Transit key
  versions, including those rewrapped to (084-085). An application backup newer than the provider
  snapshot therefore references generations that the restored provider does not have.
- In both local layouts, the store snapshot also contains the private keys.

**To [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13):** §8 above.
