# Extraction before persistence: secret ingress on import and drift adoption

| | |
|---|---|
| **Date** | 22 September 2026 |
| **Work item** | [Experiment E1 - secret ingress and redaction](https://github.com/ginsys/bronzeward/issues/2) |
| **Design reference** | [§7.1 Responsibility split and secret ingress](../Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress), [§6.9 Secret references and validation stages](../Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages), [§9.1 Adopt an existing configured cluster](../Talos_Configuration_and_Machine_Management_Design.md#91-adopt-an-existing-configured-cluster), [§12.4 Drift policy](../Talos_Configuration_and_Machine_Management_Design.md#124-drift-policy), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts) |
| **Artifacts** | [`experiments/e1-secret-ingress/`](../../../experiments/e1-secret-ingress/README.md), evidence under [`experiments/e1-secret-ingress/evidence/`](../../../experiments/e1-secret-ingress/evidence/) |
| **Decision enabled** | A design that extracts known and marked secrets before any ordinary plaintext persistence is feasible for import and drift adoption, and the two staging alternatives can be told apart on ownership and recovery. No provider, database driver, marking syntax or deployment profile is selected here. |

## 1. Question

Design §7.1 requires that "known or operator-marked secrets must be extracted before any ordinary
plaintext persistence", across ordinary drafts, parsed indexes, the original YAML text, request
logs, error reports, temporary files and staging. The same section forbids the obvious shortcut:
"Retaining the observed effective config in a plaintext draft and redacting it later is forbidden.
Database backups and historical records would already contain it."

Two questions follow, and only the second is about bytes.

1. Can ingestion be built so that extraction provably happens first, on success, on rejection, and
   when the process is killed at any persistence boundary?
2. Is the prohibition on redacting later a real constraint, or a precaution? If a plaintext draft
   can be cleaned up afterwards with nothing left behind, §7.1 is costing the design something for
   no reason.

A third question the issue asks separately: can schema-based detection be relied on, and with what
limits (§6.9).

## 2. Alternatives compared

§7.1 names two ways to hold a change between extraction and the sanitized draft, and leaves them
undifferentiated. Both were implemented behind one interface, over one shared claim table, so the
comparison is like-for-like rather than a comparison of two people's code.

| | protected transient | explicitly encrypted staging |
|---|---|---|
| What is held, and where | nothing outside the process: the claim row records that a run is in review, the change itself never leaves memory | the change as ciphertext, in a database row |
| Ownership | the process: run identifier, process identifier and a start token that a recycled identifier cannot forge | a named principal plus a claim token, so ownership survives the process |
| Expiry | a lease with a heartbeat | an expiry timestamp, enforced at read rather than only by a sweeper |
| Recovery owner after interruption | nobody, and that is the finding | a different principal, who decrypts and resumes from the recorded checkpoint |
| Provider dependence at recovery | none | total |
| What reaches the write-ahead log and the backups | the claim row only | the claim row and the ciphertext |

The second row of the last line is deliberate rather than an oversight: encrypted staging is a
database row, not a file, precisely because a row is what reaches the write-ahead log and the
backups, which is the property under test. Section 4 measures what each leaves behind.

A third approach — retaining the observed configuration as a plaintext draft and redacting it
afterwards — is what §7.1 forbids. It is implemented here as a control, not as a candidate.

## 3. What was built

A Go ingestion prototype in [`experiments/e1-secret-ingress/`](../../../experiments/e1-secret-ingress/README.md),
in its own nested module so that a future root module's `./...` structurally excludes it, and a
bash harness that drives it against the shared investigation fixtures. It is Phase-0 evidence code
and says so on its first line and in every package's doc comment.

The claim rests on four independent layers, and they are a conjunction, not a menu. The first is
the one that actually proves ordering; the rest are there to catch the case where it is wrong.

**A structural argument.** Exactly one type may hold plaintext, in an unexported field, and every
persistence function takes a sanitized type. Two places construct it: extraction, and staging's
resume path, which rebuilds one from bytes that were already sanitized when they were staged and
checks their digest first. **That restriction is enforced by a test, not by the compiler.** The
constructor is exported, because Go cannot make a function visible to one sibling package and no
other; a lexical call-site test fails if any other non-test file calls it, and it was proven to
fail on a planted caller. The zero value, which no call-site test can see, is rejected by every
persistence function.

An earlier draft of this report said persisting plaintext was a compile error. It was not, and
5.15 records how that claim got in. The deliberate controls are written in a separate package
against the raw database handle, because building a sanitized value around plaintext would fail
that test — so the forbidden design cannot pass through the ordinary path without breaking the
build's test suite, which is a weaker and accurate statement. A v1 that wants the compiler to hold
it would define the type in the package that constructs it (section 8).

**Calibrated negative controls, one per surface family.** The fixture's own positive control proves
only that its state directory was walked. Each surface this experiment claims to observe — the run
root, the database heap, the write-ahead log, the snapshots, the PostgreSQL statement log — has a
control that must produce a hit there, and a control that comes back clean means that surface is
not evidence and the report says so instead of drawing a conclusion from it.

**An ordering journal with causal content.** Each record carries a sequence number, the boundary
reached, wall and monotonic clocks, and for every write the SHA-256 of the exact bytes about to be
written together with the reference tokens that replaced each secret. A separate subcommand, run by
a process that is not the one that wrote the journal, asserts that every secret's extraction
precedes the first write of any payload that could contain it.

**Three observers that are not the prototype.** PostgreSQL's server-side `clock_timestamp()` and
write-ahead-log position; OpenBao's per-version `created_time`, read out of band through a
metadata-only identity; and the fixture's injection log, written by a third process. Both
containers share the host clock, which is stated here rather than assumed away.

### Boundaries and how each is stopped at

Two stop modes, because they answer different questions. A hold pauses the process at a boundary so
the disk can be captured while ingestion is provably mid-flight; a crash sends `SIGKILL` to the
process itself, so no deferred cleanup, no buffered write and no rollback handler runs. A design
that only avoids leaving plaintext behind because its cleanup path ran has not met §7.1, and only
the second mode can tell the two apart.

### Pinned versions

Recorded in every bundle's `versions.txt`, from the running containers rather than from the
manifest, so a bundle states what actually produced it.

| | |
|---|---|
| Captured | 2026-09-22, 21:48Z to 22:25Z |
| Fixture manifest commit | `4e3a83af5cbe35c4889ecfac92f90bbd9d2b6e77`, which is also the commit every bundle records as the one the running code was built from. The fixes committed after it (5.18) change no output on any path the matrix runs, and why for each is recorded there; the evidence was not re-captured for them |
| Host | Linux 7.1.3+deb13-amd64 x86\_64 |
| Docker / Compose | 26.1.5+dfsg1 / 2.27.1 |
| Talos | v1.13.6, both nodes; `talosctl` client v1.13.6 |
| Kubernetes | 1.36.2 (requested) |
| PostgreSQL | 17.11, `postgres:17-alpine@sha256:f02121de…` |
| OpenBao | v2.6.1, `ghcr.io/openbao/openbao:2.6.1@sha256:5b2486ab…` |
| Go | 1.27.1, the toolchain pinned in `mise.toml` |
| Prototype dependencies | `gopkg.in/yaml.v3`, `github.com/lib/pq` |

`lib/pq` was chosen over `pgx` for one reason: it has no transitive dependencies, so `go.sum` stays
auditable in full. That selects no driver for v1.

### Synthetic secrets

Nothing secret is committed and no real credential is used anywhere. The fixture chooses values
beginning with `BWSYNTH-`; the rest — OpenBao's root token and unseal key, Talos's cluster secrets
bundle — are collected after creation. All of them become fixed-string scan patterns, which is what
makes an absence claim here checkable at all.

No secret is ever a command-line argument. `argv` is world-readable through `/proc` and is captured
by the evidence bundles these runs produce, so every credential reaches the prototype through the
environment.

## 4. Expected and observed

Two tiers. The screen crosses every combination cheaply and its only authority is finding
candidates; the bundles are where the disk is actually read. Every number below comes from a file
under [`evidence/`](../../../experiments/e1-secret-ingress/evidence/), and every hit was attributed
by opening the file named rather than by its count — see 5.9 for why a count cannot be a finding
here.

### 4.1 How to read a leak scan

`fixtures/bin/evidence` records only paths with hits, so a directory that scanned clean and one the
scan never walked produce identical output: nothing. The fixture plants one positive control in its
own state directory, which proves that directory was read and nothing else, and every run of this
prototype plants a second in its run root.

**An honest bundle therefore holds exactly two lines.** Fewer means a control is missing and the
bundle proves nothing about the surface it was supposed to cover; more means something was found,
and which it was is settled by opening the file.

### 4.2 The honest path

| Bundle | Flow | Lines | Ordering |
|---|---|---|---|
| `honest-import-transient` | import, transient staging | 2 | pass |
| `honest-import-encrypted` | import, encrypted staging | 2 | pass |
| `honest-import-flushed` | import after a forced checkpoint | 2 | pass |
| `honest-adopt-transient` | drift adoption (§12.4), same input — see below | 2 | pass |
| `held-in-review-transient` | held at in-review, claim outstanding | 2 | pass |
| `held-in-review-encrypted` | held at in-review, claim outstanding | 2 | pass |
| `crashed-in-review-transient` | `SIGKILL` at in-review | 2 | pass |
| `crashed-in-review-encrypted` | `SIGKILL` at in-review | 2 | pass |
| `crashed-in-db-txn` | `SIGKILL` inside the database transaction | 2 | pass |
| `crashed-in-db-txn-dead` | the same disk, PostgreSQL still dead | 2 | — |
| `crashed-in-db-txn-restarted` | the same run after PostgreSQL restarted | 2 | — |
| `recover-transient` | second principal, transient staging | 2 | pass |
| `recover-encrypted-netsplit` | second principal, provider unreachable | 2 | pass |
| `recover-encrypted` | second principal, recovery completes with every reference row (5.16) | 2 | pass |

Ten secrets were extracted per run, before any persistence. The flush bundle exists so that a clean
data directory cannot be dismissed as "not written out yet": the checkpoint was forced first and the
result is the same.

**Import and drift adoption ran on the same input and the same code.** Every run here ingests the
effective machine configuration the fixture reads back off the control plane, which is what §12.4's
drift adoption sees; the fixture keeps no copy of the generator's pre-apply output. The prototype
also serves both flows from one function, which differ only in the label recorded. So the adopt row
is a second honest run under the drift-adoption label, not independent coverage of a second flow,
and an import of a freshly generated configuration is not exercised at all (section 7).

`crashed-in-db-txn-dead` carries a non-empty `unavailable.txt`: with PostgreSQL killed there is no
logical dump, and the bundle says so rather than presenting a partial read as a clean scan of the
database. It is captured anyway because the data directory as the crash left it can only be read
while the server is down.

**§9.1 — import must leave machine state unchanged.** The first and last bundles of the matrix
bracket everything in between, and their `talos-config-sha256.txt` values are identical.

### 4.3 The calibrated controls

Each per-surface control must produce a hit at its own file. Asserting on the line count alone would
let a control pass on another control's residue without ever reaching the surface it is named after
(5.8), so each row below names where the hit had to be.

| Control | Lines | Hit required at | Result |
|---|---|---|---|
| `control-leak-temp-file` | 3 | `/tmp/effective-config.yaml` under the run root | present |
| `control-leak-staging` | 3 | `/staging/pending.yaml` under the run root | present |
| `control-leak-app-log` | 3 | `/e1.log` under the run root | present |
| `control-leak-error-report` | 3 | `/error-report.txt` under the run root | present |
| `control-leak-db-log` | 3 | `logs/bw-fixture-postgres.log` | present |
| `control-leak-transformed` | 2 | nothing — the instrument must miss it | missed, as intended |

The statement-log control had to be changed to produce its hit at all. The fixture leaves
`log_statement` at `none`, so the first version of the control wrote a statement nobody logged and
came back clean — which in a bundle is indistinguishable from a surface that cannot be observed. It
now enables statement logging on its own connection first. This is the re-planning trigger firing
exactly as intended: a control that comes back clean means that surface is not evidence, and the
design changes before a conclusion is drawn, not after.

`control-leak-transformed` writes a base64 encoding of the canary and produces no hit. That is the
point: the scan finds exact copies, and this is what its blindness looks like, demonstrated rather
than restated as a caveat.

### 4.4 The forbidden design, measured

This is the result §7.1's second sentence asks for. All three runs write plaintext to the database
before extracting anything; they differ only in what they do next.

| Control | Occurrences in `pg_wal/` | Occurrences in the dump | Ordering |
|---|---|---|---|
| `control-persist-first` | 24 | 18 | **fail** |
| `control-persist-first-redact-after` | 8 | **0** | **fail** |
| `control-persist-first-rollback` | 16 | **0** | **fail** |

The middle row is the whole answer to question 2. Redact-after does exactly what its advocate would
claim: the plaintext it committed a moment earlier is gone from the live rows, so a `pg_dump` taken
afterwards is clean. It is still in the write-ahead log, eight times, in the same bundle. Rollback
gives the same shape for a transaction that was never committed at all — sixteen occurrences in the
write-ahead log for rows that no query will ever return.

So the prohibition is not a precaution. "Redact it later" produces a database that looks clean by
every means an application has and still holds the plaintext where backups and replicas read.

**The exact counts are not the result; the split is.** This matrix was captured four times on
fresh fixtures while the review fixes landed, and the write-ahead-log counts moved by one between
captures — persist-first 24, 23, 24, 24; rollback 16, 15, 16, 16 — because what else the log holds,
and so where a record straddles a page, depends on everything before it. The dump counts and the
redact-after pair never moved: nothing in the dump, eight in the log, all four times.

The single line each of these bundles shows at `logs/bw-fixture-postgres.log` is **not** theirs: it
is the statement `control-leak-db-log` logged at 22:18:06Z, in a log file that is appended to for
the whole matrix. Attributed by opening it, which is the rule this report follows throughout.

`honest-import-final` is an honest run that scans with six lines. Nothing in it wrote plaintext;
the database it ran against still holds what the controls put there — 18 occurrences in the dump,
30 in the write-ahead log, and 18 in the heap file of `parsed_index` (relation file
`base/16384/16400`, named from `pg_class` while the fixture was still up). The heap line is the
residue reaching the table's own data file once a checkpoint flushed it, the one place in this
matrix where the controls' plaintext is seen outside the log and the dump. That bundle is asserted
as residue rather than as clean, because a clean scan there would mean the controls' plaintext had
gone away by itself — see 5.8.

### 4.5 Ordering, checked by a process that did not write the journal

`e1 verify-order` passes every honest, held, crashed and recovery run, and fails all three
`--persist-first` controls, naming the record:

> seq 7: wrote machine\_draft (plaintext, before extraction) at sequence 7, before the first
> extraction at sequence 10; §7.1 forbids persisting the observed configuration and extracting
> afterwards

The two `verify-order` bundles write no journal of their own and are recorded as such rather than as
passing. This verifier could not fail on the design it exists to detect until late in the work; 5.11
records why, because the shape of that defect matters more than the fix.

### 4.6 Tier A — the screen

72 rows: two flows × two staging modes × two outcomes × nine crash points, each run without
capturing a bundle and screened over its run root and a fresh logical dump. No row needed a bundle.

The screen was then re-run with a different mark source — path binding replaced by a suffix rule
over `key`, `token` and `secret` — and the two tables are identical apart from run identifiers. The
ordering results do not depend on a marking syntax, which matters because §6.9 has not settled one
and this experiment does not settle it either.

A screen row that passes says the run root and the live tables held no synthetic secret material at
that moment. It says nothing about the heap, the write-ahead log or the backups, which is where
4.4's entire result lives. Both tables are committed as
[`screen.tsv`](../../../experiments/e1-secret-ingress/evidence/screen.tsv) and
[`screen-mark-suffix_key_token_secret.tsv`](../../../experiments/e1-secret-ingress/evidence/screen-mark-suffix_key_token_secret.tsv).

### 4.7 Schema detection (§6.9)

The denominator was established without the detector. The fixture's own key/secret/token rule,
applied to the secrets bundle Talos generated, decides which values are secret; the prototype only
says where in the configuration each of those values ended up. A ground truth the detector helped
choose would be the detector grading its own work.

| | |
|---|---|
| Scalar paths in the configuration | 57 |
| Ground-truth secret paths | 9 |
| Schema rules | 11 |
| **Recall** | **9/9** |
| **Precision** | **9/10** |
| Spurious | `doc[0].cluster.id` |

The one false positive is the shape that makes precision worth reporting separately: a cluster
identifier sits among the secrets and looks like one to a rule keyed on names. Over-extraction is
not a leak, but it is a cost, and an F-score would have hidden which of the two numbers moved.

**The miss is demonstrated, not caveated.** A secret planted at `machine.files[].content` — where
Talos writes an operator-supplied file to the node, and where no rule in the table looks — is not
found: recall over that configuration is 9/10 and the missed path is named in the report. The same
path handed back as an operator mark is extracted: the same document yields 10 secrets unmarked and
11 marked. That is what §6.9's division of responsibility looks like when it is measured.

**Talos ships no field-level sensitivity metadata a detector could use.** Read from the running
control plane: of 167 resource definitions, 35 carry `sensitivity: sensitive`, and the whole machine
configuration is one of them (`machineconfigs.config.talos.dev`, alongside
`osrootsecrets`, `etcdsecrets` and `kubernetessecrets`). The granularity is the resource, not the
field, and `talosctl` exposes no machine-configuration schema export. So the answer to §6.9:339's
question is negative, and that negative is what makes §6.9's refusal to promise completeness
load-bearing rather than cautious.

## 5. Failures hit while building it

Four of these were found only by pointing the prototype at the fixture's own Talos configuration
rather than at the fragments its tests were written from. The section is ordered that way
deliberately, because one shape recurs and it is the failure mode this whole experiment is exposed
to: **a check that could not have failed**.

Six of the fourteen below are that shape — 5.2, 5.5, 5.11, 5.12, 5.14 and the statement-log control
in 4.3. A silently truncated document, a screen counting runs that never ran, a verifier quantified
over an empty list, a dump that was never taken, a lint reading a file from outside the tree it was
checking, a control writing to a surface that was switched off. Every one of them reported success.
None is visible in the evidence it produced, because the evidence it produced was "nothing found",
which is also what a correct run produces.

5.15 adds a seventh, one level up: this report's own claim that the ordering was enforced by the
compiler, citing a test that did not exist. 5.16 adds an eighth: a recovery that dropped every
reference and passed because nothing read the rows. 5.17 adds a ninth: a redaction test that used
the one kind of field where redaction works, with a leak detector that could only see one spelling
of a leak. All three were found by an external review rather than by the habit that found the first
six, which is the argument for having both.

An experiment whose instrument can report clean without looking proves nothing at all, and the
count above is the honest reason for every positive control described in section 4.

### 5.1 `%#v` bypasses `String()`

A type that redacts itself through `String()` is still printed in full by `fmt.Sprintf("%#v", …)`,
which uses `GoStringer` and falls back to the struct's fields. `%+v` and `%v` go through `String()`;
`%#v` does not. The redaction type therefore implements `String`, `GoString`, `Format`,
`MarshalJSON`, `MarshalText` and `LogValue`, and a test asserts every one of them, together with
`%w` wrapping and `log/slog`, because a single unguarded verb in an error path is enough to put a
secret in a log line that §7.1 counts as persistence.

That was not the whole of it, and 5.17 records the rest: none of those methods runs for a value
held in an unexported field, and the plaintext then has to be unreachable by reflection rather
than merely overridden.

### 5.2 Silently reading only the first YAML document

`yaml.Unmarshal` into a `yaml.Node` decodes the first document of a stream and discards the rest,
returning no error. The fixture's `controlplane.yaml` is a multi-document file — Talos emits the
machine configuration and several sibling documents in one stream — so every path after the first
document was outside anything a mark or a detection rule could name. The run scanned clean because
it never looked.

This is the worst shape a defect in this experiment can take, and it is invisible in every piece of
evidence collected: the leak scan records hits, and there were none. The fix is structural rather
than a bug fix. Every path now carries a `doc[n]` prefix, including in a single-document file, so a
future change cannot quietly go back to indexing one document without every mark, rule and test
failing at once.

### 5.3 Refusing a document for an ambiguous key rejected every real configuration

A key containing a dot makes two locations share one path: `a.b: x` and `a: {b: x}` are both
`a.b`. The first implementation refused such a document outright. That rejected every real Talos
machine configuration, because `machine.nodeLabels` carries Kubernetes label keys and dots in label
and annotation keys are ordinary.

Refusing was the wrong response to a hazard that only exists where someone tries to address the
path. The subtree under such a key is now excluded from the index and reported instead, so the
coverage gap is visible beside the clean result rather than converted into a hard failure that
would have confined the experiment to fixtures it wrote itself.

### 5.4 A NULL array overrides a column default rather than falling back to it

A `nil` Go slice reaches PostgreSQL as `NULL`, and a supplied `NULL` overrides `DEFAULT '{}'`
rather than being replaced by it, so a `NOT NULL` column rejected every journal record that listed
no secret digest — which is most of them. The empty array is now passed explicitly. The behaviour
is correct SQL and reads as a bug only if one expects a default to be a fallback.

### 5.5 The screen counted 32 runs that never ran as passing

Tier A's screen scans each run's root for synthetic secret material and treats one hit — the
reachability control — as clean. A run that failed before reaching the database leaves exactly that
one hit, so 32 rows that exited 1 on the way in were recorded as passing. The screen now states the
outcome each row must have, from its flow, staging mode and crash point, and a row that exits any
other way is a failure regardless of what its scan found.

This is the same trap the two-line invariant exists for one level down, arriving by a different
route: an instrument that cannot distinguish "clean" from "never looked" will eventually be asked
to, and will answer wrongly.

### 5.6 Every capture of the first matrix exited silently

`set -e` with `pipefail` aborts a script when a pipeline inside a command substitution fails, with
no message. The bundle harness counted existing bundles with `find "$STATE/evidence" … | sort`
before taking a new one, and `fixtures/bin/evidence` creates that directory on its first run — so
on a fresh fixture the count failed, and every capture exited between the run and the bundle
without a word. The matrix reported 23 failures and printed nothing about any of them.

`shellcheck` does not catch this shape. The directory is now created before it is counted, and the
pipeline cannot abort the script.

### 5.7 The review checkpoint fired after the claim had been released

`--crash-at=in-review` is what produces the interrupted run the recovery comparison needs. The
checkpoint was reached in the calling function, after staging had already resumed and released the
claim, so the kill left nothing held and every recovery attempt failed with "run … is released, not
held" — a correct message about a state the control was supposed to prevent. The checkpoint now
fires between the hold and the resume, inside the window it names.

Nothing about the failure looked like a harness defect: the runs died at the right boundary and the
recoveries failed with a specific, plausible error. What gave it away was that all three recovery
variants failed identically, including the one that was supposed to succeed.

### 5.8 One database serves the whole matrix, and plaintext does not leave it

The deliberate `--persist-first` controls commit real plaintext. Every later run in the same
fixture inherits it: it stays readable in the heap, in the write-ahead log, in `pg_dump` output and
in every snapshot taken afterwards. An honest run captured after a control therefore cannot scan
clean, and the first full matrix produced exactly that — a clean run whose bundle showed nine
plaintext hits in `parsed_index` and one in `machine_draft`, none of them written by that run.

This is not a harness limitation to work around. It is §7.1's own sentence about backups and
historical records, measured, and it is reported as a result in section 4 rather than engineered
away. The matrix now takes every capture that must scan clean before the first control, and the run
that closes the §9.1 machine-state bracket at the end is asserted as residue: the run is honest,
the database is not, and a clean scan there would mean the controls' plaintext had gone away on its
own.

### 5.9 `BWSYNTH-` is itself a scan pattern, so a hit count is never a finding

The fixture emits the literal string `BWSYNTH-` as one of its scan patterns, and the canary, the
PostgreSQL password and the fixture's own non-secret run identities all carry that prefix. A leaked
connection string in a log is therefore indistinguishable *by count* from a leaked canary. Every
hit in this report is attributed by opening the file that holds it; no claim here rests on a
number alone.

### 5.10 Run identifiers collide within one fixture

The staging and draft tables key on the run identifier, so re-running the screen inside one fixture
made the second invocation fail on a primary-key conflict — a harness defect that presents exactly
like a flow defect. Each invocation now stamps its run identifiers with the instant it started. The
fixture is disposable; identifiers inside one fixture's lifetime are not.

### 5.11 The ordering verifier could not fail on the design it exists to detect

`e1 verify-order` passed all three `--persist-first` controls. Its rule was quantified over the
secrets a write record *lists*, and the forbidden design lists none: it writes the observed
document before anything has been extracted, so there is no digest to name yet and "every secret
this write lists was extracted first" was satisfied without examination.

The journals recorded the violation in plain sight the whole time — writes at sequence 7,
extractions from sequence 10 — and the verifier had nothing to say about it.

The unit tests did not catch it, and the reason is worth more than the fix. Both positive controls
construct a write record with a digest list, because the test author wrote down what the forbidden
design *ought* to record rather than what the prototype actually does. The synthetic fixture was
more honest than the program it stood in for, so the check passed against a shape that never occurs.

`Verify` now asserts the phase order directly: in a journal that records an extraction, no write may
precede the first one, whatever that write lists. A recovery is the one legitimate write with no
extraction of its own, and its record names the run it resumed, so that case is carved out
explicitly rather than falling through. Three tests were added, one of them the write that names
nothing.

This is one of the six checks in this experiment that could not fail, listed at the top of this
section. Each was found the same way: by asking what would have to be true for the check to report a
failure, and then noticing that nothing could make it.

### 5.12 The screen's database column was never read

The fixture refuses to act on a container it has not verified, and no harness script called the
verification. So every `pg_dump` the screen took failed, the failure was redirected away, and an
empty file was counted as a dump holding no secrets: 72 rows reported a clean database that was
never opened.

The verification now runs in the harness's own fixture check, and a dump that cannot be taken is
reported as `dump-unavailable` rather than as zero hits — the distinction the bundles already make
with `unavailable.txt`, which the screen had not been given.

### 5.13 A hand-written path into a multi-document file

The planted-miss ground truth named `doc[1].machine.files[0].content`, counted by hand. The
fixture's `controlplane.yaml` is already a multi-document stream (5.2), so the planted document is
`doc[2]`, and the prototype refused the mismatch with the right message: a mark that matches nothing
extracts nothing, and the run would look clean. The harness now asks where the planted value landed
instead of asserting it.

The refusal is the interesting half. That check exists because a mark naming a path the document
does not have is indistinguishable, in every other piece of evidence, from a mark that found nothing
to extract.

### 5.14 The lint passed locally by reading a file outside the tree under test

`run/lib.sh` sources the fixtures library through a `# shellcheck source=../../../fixtures/lib.sh`
directive, with no `source-path`, so shellcheck resolved it against its own working directory rather
than against the script. The work was done in a linked worktree, where three levels above the
checkout root is the main checkout — which has its own `fixtures/lib.sh`. The path resolved, the
lint passed, and it was reading a file from outside the tree it was checking.

CI has no such parent directory. The same command, the same pinned shellcheck version, the same
working directory relative to the checkout: fail.

The directive now says `source-path=SCRIPTDIR`, which resolves against the script and gives the same
answer everywhere. The defect class is the one this section keeps returning to: the check ran, it
reported success, and what it actually examined was not what anyone intended.

### 5.15 What the advisory review found, and the claim it retracted

An advisory review of the pull request raised 17 findings. Each was checked against the source
before anything was changed; 15 were real as stated, one was real with its premise reversed, and
one changed what this report may say. The review ran with degraded coverage — one chunk of the
diff went unreviewed and nine chunks' findings were never assessed — so its silence elsewhere is
not evidence of anything.

**The retraction.** This report said the sanitized type could be constructed only by extraction,
"so persisting plaintext is a compile error". The constructor is exported — Go cannot scope a
function to one sibling package — and staging's resume path calls it outside extraction. Its own
doc comment had said a package test asserted the call sites; there was a test for the plaintext
accessor and none for the constructor. So the report's central structural claim rested on a test
that did not exist.

It exists now, and was proven to fail on a planted caller. The claim in sections 3 and 6 is
restated at the strength the code supports. The refactor that would make it a compile-time property
was not done, because making it after the evidence was captured would have left the evidence
describing code that no longer existed; section 8 recommends it for v1. The matrix and every table
in section 4 were then re-captured against the corrected code.

This is the same failure as the six above, one level up: a check that could not fail, except that
the check here was a sentence in this report citing a guarantee the code never had.

**The reversed finding.** The review said drift adoption was never exercised against the effective
configuration because both flows were handed the same file. The file is the configuration read
back off the node, so adoption was exercised against exactly that. What was wrong was the harness
comment claiming import took the generator's output. The honest statement — both flows, one input,
one function — is now in 4.2 and section 7.

**The rest**, fixed and tested. Most are guards on paths the matrix does not reach, recorded
because each would have failed silently if it were ever reached:

- the ordering journal could be reopened and restart its sequence, lose a sequence number to a
  failed write, and return clean for an empty record set;
- staging judged expiry on the caller's clock while extending it on the server's — and set the
  initial expiry on the caller's too, which the review did not name — and let two concurrent
  resumes both take one encrypted claim;
- the redact-after control did not check that each parsed-index update matched a row, so it could
  report success without redacting; the measured result in 4.4 shows it did redact, but the
  control could not have said so if it had not;
- the statement-log control's quoting was correct only under a server setting it did not set;
- a KV key containing `#` or `?` would have reached a different secret;
- DSN redaction missed the URL form;
- the committed evidence was deleted before its replacement was known to exist;
- a failed fault injection was logged and its dependent capture taken anyway, so a bundle filed as
  "PostgreSQL dead" could have been taken with it running.

One finding led somewhere the review did not. Fixing the round trip of empty YAML documents showed
that yaml.v3 decodes an empty document as a null scalar rather than as empty content, so the
prototype refused any stream with an empty document in it as "a bare scalar" and its
empty-document handling had never run. No fixture configuration contains one, so no result here
depended on it.

### 5.16 The recovery that passed without its references

A second advisory review, of the corrected code, raised 13 findings: 10 real as stated, 1 real
with a mechanism that does not hold for the pinned driver, and 2 already fixed by the first round.
Coverage was degraded again — one chunk unreviewed, eight unassessed.

One of them changes a result. Encrypted staging encrypted the sanitized document and nothing else,
and a resume rebuilt the change with no references. Persistence writes the `secret_reference`
table from those references, so every draft a second principal recovered was persisted without a
single reference row — the mapping from each `bw:ref:` token back to the secret it replaced was
gone. `recover-encrypted` still exited ok and scanned clean, because nothing in the matrix read
those rows. The first capture of section 4 therefore reported "recovery completes" for a recovery
that had silently dropped part of what it recovered.

Staging now encrypts the whole change — document and references — as one envelope, a resume
refuses a payload that is not one, and the matrix asserts that the recovered draft carries one
reference row per secret the crashed run extracted. The evidence in section 4 is from after that
change.

It is the same shape as everything else in this section: a run that succeeded, a scan that found
nothing, and no check that would have failed.

The rest were guards on paths the matrix does not reach: the recovery carve-out missing from one of
the verifier's two rules; `Unsafe` returning the plaintext's backing array; a decrypt response with
no plaintext accepted as an empty document; a heartbeat able to revive a lapsed lease; the DSN
scanner not following lib/pq's escaping; extraction order depending on the caller having sorted;
`-h` exiting 1; and the evidence collector writing one file without its path rewrite and deleting
the old evidence before the new was in place.

One question it raised is left open rather than decided here: **who may send a heartbeat**. The
lease can no longer be revived once lapsed, but any caller may still extend a live one. Under
transient staging the owner is the process; under encrypted staging the claim can pass to a second
principal, so "only the holder" needs a definition of holder across a recovery. That belongs with
the staging decision in the milestone-02 specification.

### 5.17 The redaction that held only for exported fields

A third advisory review raised 7 findings, all real. One undoes part of 5.1.

fmt calls a value's `Format`, `String` or `GoString` only when reflection is allowed to take it as
an interface, and it is not allowed to for an unexported field. A secret held in one — the natural
way to carry it inside a private struct — was printed by fmt walking its fields instead, and the
plaintext came out as a list of decimal bytes. The test that was meant to cover containers used
an exported field, which is the one case where the overrides do run.

Two further layers of the same mistake turned up while fixing it. Moving the bytes behind a pointer
was not enough, because fmt's bad-verb path — `%s`, `%q` or `%d` applied to a pointer —
dereferences it. The plaintext is now captured by a closure, which fmt prints only as an address
under every verb and which reflection cannot look inside. And the test helper itself searched for
the plaintext only as text, so the decimal-byte leak had been caught only because the same output
also lacked the redaction; it now looks for the decimal, hex and spaced-hex spellings too.

No run leaked this way — the prototype never holds a secret in an unexported field that is then
formatted — which is why no scan could have found it. It is recorded because it is exactly the
class of defect the redaction type exists to rule out, and because the leak scan would not have
matched the decimal spelling if it had happened.

The other six were: a test comparing against a path no document can produce, so one of its cases
asserted nothing; a 10ms timing bound a loaded runner could exceed; the leak control accepting a
value shorter than the scan can match; the staging envelope's silent rewrite of invalid UTF-8; a
malformed URL-form DSN escaping redaction; and an evidence swap that a cross-device move could leave
half-done.

### 5.18 The fourth review, and the fixes made after the evidence

A fourth advisory review raised 4 findings, all real, and the rounds had been shrinking — 17, 13,
7, 4. These were fixed after the last capture of section 4, and the evidence was not captured a
fifth time, because none of them changes the output of any path the matrix runs. The reason for
each is stated rather than assumed:

- **The extraction guard missed values the encoder rewrites.** It searched the sanitized text for
  each extracted value, so a multi-line secret — re-encoded as an indented block — or one written
  back with escapes would have passed with the plaintext still in the document. The document is now
  parsed back and every scalar compared as a value, with a test proven to fail without it. No
  recorded run can have depended on the gap: had it let a value through, that run's sanitized draft
  would hold the plaintext, and every dump scan in section 4 shows the drafts do not.
- **A zero-valued leak surface counted as a control.** `Surface` is a string whose zero value is
  `""`, not `"none"`, so an options literal that omitted it was scored as a leak control. The
  prototype's flags always set it, with `none` as the default, so no run was affected.
- **Transit key names reached the request path unchecked**, as KV keys had before 5.15. They are
  now held to the same rules, plus no `/`; the fixture's key name produces a byte-identical path,
  which the test asserts.
- **The staging envelope checked UTF-8 only in the document**, not in the references it carries.
  Every reference a run produces is valid UTF-8, so no staged payload changes.

## 6. What this decides

**§7.1's ordering requirement is implementable, and the evidence for that is structural.** Every
persistence function takes a sanitized type that only extraction and staging's resume path may
construct. In this prototype that is enforced by a call-site test rather than by the compiler (3,
5.15), so the accurate statement is that the forbidden design cannot pass through the ordinary path
without failing the test suite, and the deliberate controls had to be written in a separate
package against the raw database handle to exist at all. That is still a stronger statement than
any scan result in section 4: a scan says one run left nothing behind, while the type and its test
say no run through that path can write an unextracted document. Making it a compile-time property
is a known, cheap step for v1 (section 8, item 2).

**§7.1's prohibition on redacting later is a real constraint, not a precaution.** 4.4 measures it.
A plaintext draft that is redacted immediately afterwards leaves a database that is clean to every
query an application can make, and eight occurrences of the plaintext in the write-ahead log of the
same bundle. A rolled-back transaction leaves sixteen, for rows that never existed. The sentence in
§7.1 about backups and historical records is describing exactly this and should not be softened.

**The two staging alternatives differ on one axis that matters, and it is not secrecy.** Both keep
plaintext out of ordinary persistence. They differ on who may take over after an interruption:

- *Protected transient* has no recovery owner. The interrupted run's change is gone with the
  process, and the owner's only correct move is to declare the run dead and re-ingest. The prototype
  refuses the recovery rather than improvising one, and that refusal is the finding, not a gap.
- *Explicitly encrypted staging* has a recovery owner: a different principal decrypts the staged
  change and resumes from the recorded checkpoint. The cost is a total dependency on the provider at
  recovery time, measured here by making the provider unreachable — the recovery fails, and the
  claim stays held.

The paired result to carry forward: after a successful recovery the staging row is deleted, and the
deleted *ciphertext* remains in the write-ahead log and in every earlier snapshot, which is
harmless. The identical delete under redact-after leaves *plaintext*. Same code path, same
instrument, opposite consequence.

**Schema detection can be relied on for identified fields and for nothing beyond them.** Recall is
9/9 on this configuration and 9/10 once a secret is placed where no rule looks; the operator mark
covers exactly that gap, and Talos publishes no field-level sensitivity metadata that would let a
detector close it (4.7). §6.9's refusal to promise completeness for unmarked values is therefore
the correct design position, and this experiment supplies the evidence for it rather than assuming
it.

**What this does not decide.** No provider is selected: OpenBao is what the shared fixture already
runs, and the comparison belongs to its own investigation. No database driver, no marking syntax, no
deployment profile, no licence position. The `docs/spec/compilation.md` contract that will record
the staging decision is reserved to milestone 02 and is not written here.

## 7. Limits

**The prototype is both subject and instrument.** The three external observers reduce that, and do
not remove it. The structural argument is what carries the claim; the scans are what catch the case
where the structure is wrong.

**Ordering is a property of the program, not of bytes at an instant.** A clean scan at one moment
matches exact copies of known values in the places the scan was pointed at. It is necessary
evidence here, not sufficient, and section 4's controls exist to show which surfaces the scan can
speak about at all.

**The leak scan finds exact copies.** A transformed secret — re-encoded, split, hashed — is not
found. This is demonstrated rather than caveated: a control that writes a base64 encoding of the
canary produces no hit at all. Talos key material is matched in the base64 form the secrets bundle
and machine configuration carry; a decoded PEM copy of the same key would not match.

**Surfaces this experiment cannot see.** OpenBao's storage volume is not observable and its
snapshots are encrypted by OpenBao itself: the evidence shows that a secret reached the provider,
not what else is in there. TLS is disabled between the prototype and OpenBao in this fixture, so
Transit plaintext crosses loopback in the clear and no packet capture exists here — a real
unmeasured surface, and a genuine comparison point against a local encryptor. Swap, core dumps and
process memory are unmeasured; core dumps are disabled and nothing is locked into memory.

**Unaddressable configuration keys.** A key containing a dot or a bracket makes its subtree
unreachable by this prototype's path syntax, so a secret under `machine.nodeLabels` or an
annotation key cannot be marked, detected or substituted here. This is a limit of the prototype's
addressing, not of the design; it is reported by every run rather than left implicit, and a v1
implementation needs an escaping scheme.

**One input for both flows.** Import and drift adoption both ingest the configuration read back
off the node, through one function (4.2). What a generated configuration looks like before Talos
applies and normalises it is not exercised, so any difference in where its secrets sit is
unmeasured here.

**Ownership and expiry were checked without contention.** Every run is one process against one
claim. The guarded transitions and server-side expiry added after review (5.15) close the race and
clock-skew holes by construction, and each is reached on the matrix's ordinary path, but no run
drives two principals at one claim concurrently or skews a caller's clock. Who may extend a live
lease is not checked at all, and is left to the specification (5.16).

**The environment bounds the schema-detection denominator.** A Docker-provisioned Talos node
produces no disk-encryption, installer or disk configuration — precisely the secret-bearing areas
absent here. Recall is over the fields this environment produces, and §6.9 forbids the completeness
claim outright in any case: what the design can promise is reliability on identified fields and
explicit operator responsibility for the rest.

**No reboot, upgrade, reset, installer or disk behaviour exists here**, and there is no remote
transport. The database data-directory copy in each bundle is what was on disk at that instant, for
the leak scan; it is not a consistent backup and is not presented as one.

**This does not prove a v1 implementation will be correct.** It shows that a design which extracts
first is feasible, for this prototype, these flows, these secrets and these boundaries.

## 8. Recommendation

1. **Keep §7.1's ordering requirement as written, and keep the sentence forbidding a plaintext draft
   that is redacted later.** 4.4 measures the shortcut it forbids: the plaintext survives in the
   write-ahead log after the redaction and after a rollback. No weakening of that sentence is
   supported by anything here.

2. **Carry the structural construction into the v1 contract, not just the behaviour — and make
   the compiler hold it.** The property that made this experiment come out the way it did is that
   persistence functions accept only a type extraction constructs. This prototype enforces the
   "only" with a call-site test, which is weaker than it first claimed (5.15). v1 should define the
   sanitized type in the package that constructs it, with an unexported constructor, so that no
   other package can build one at all; the resume path then has to go through that package too,
   which is the correct place to re-check a resumed document anyway. A v1 specification that states
   the ordering requirement without requiring that shape is asking every future change to re-derive
   it. The milestone-02 compilation contract is where this belongs.

3. **Adopt explicitly encrypted staging when a change must survive the process, and keep protected
   transient as the default for changes that need not.** The difference is a recovery owner, not
   secrecy, so the choice follows from whether an interrupted review must be resumable by a second
   principal or may be re-ingested. Encrypted staging's price is a hard provider dependency at
   recovery, which is measured here rather than assumed. **This is a recommendation, not the
   decision**: §7.1 leaves the two undifferentiated and the decision belongs to the specification
   work in milestone 02.

4. **Specify the marking mechanism as load-bearing, not as a fallback.** 4.7 shows schema detection
   missing a secret that the mark catches, and no upstream metadata that would let detection close
   the gap. Whatever syntax is chosen, the contract should say that unmarked values carry no
   completeness promise — which is what §6.9 already says, now with evidence.

5. **Two prototype limits need a v1 answer before they become inherited defects.** Paths containing
   a dot or a bracket are unreachable by this prototype's addressing, so a secret under
   `machine.nodeLabels` or an annotation key cannot be marked at all; v1 needs an escaping scheme.
   And every document in a multi-document stream must be addressable, which this prototype had to be
   corrected to do (5.2).

6. **Treat the six vacuous checks in section 5 as a standing hazard for the evidence work, not as
   incidents.** A control that comes back clean because its surface was switched off, a screen that
   counts runs that never ran, a dump that was never taken, a verifier quantified over an empty
   list, a document read only as far as its first `---`, a lint reading a file from outside the tree
   under test: each looked exactly like a passing result. The habit that found all six is asking
   what would have to be true for the check to fail, and confirming something could make it. Any
   subsequent experiment producing absence evidence should carry a positive control per surface and
   assert it, which is what the reachability control and the two-line invariant do here. The three
   the habit missed — an overclaimed guarantee, a recovery that dropped its references and a
   redaction that held only for exported fields (5.15–5.17) — were found by an independent review,
   so the evidence work needs one of those too.
