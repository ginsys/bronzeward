# Fresh node evidence before publication

Work item: ginsys/bronzeward#247; prerequisite of ginsys/bronzeward#241.
Owner decision: 2026-10-19, fresh read-only executor observations of every
covered machine before compilation; offline/unreadable covered nodes refuse
publication. This is implementation approval, not the PoC assessment in #31.

## Contract and implementation

The snapshot supplies the target contract and covered machines. After checking
the pinned renderer can compile that contract, the executor obtains a fresh
`publication` observation for each machine, ordered by machine ID. Its start
binds the publish operation, controller, owner generation, epoch and endpoint.
Both start and completion require the running owner's unexpired lease, checked
after the operation lock. The compiler's provider interfaces are unchanged.

A known identity or minor mismatch refuses 422; unread identity/version/access
refuses 503. T3 rechecks evidence under the existing machine locks and refuses
changed endpoints (including changed away/back), a different evidence binding,
or a higher-basis unread/contradicting observation. A lower-basis read completing
later does not supersede the chosen evidence. Matching higher-basis reads pass.
Each release machine retains its observation ID; evidence IDs are excluded
from the content digest so commit-unknown retains the original release.

Authoritative contracts: design §7.4/§12.6, compilation §10.2,
persistence-api §6.1.1/§6.2 and execution-recovery §4.1. The initial schema is
edited in place under the pre-release rule. The additive API fields are
`publicationObservation` and nullable `publicationGeneration`.

## Database and provider verification

The required `mise run go-db` gate passed at
`e04d6272006fdfaa57e192d8531dbb5b0a47e8af`: 21 packages with tests and two
without test files, exit 0. The API package took 523.906 s, migrations 128.539 s
and monitor checks 114.864 s. The environment was Go 1.27.1 linux/amd64,
PostgreSQL 17.11 and OpenBao 2.6.1
(`ba7ad8861d0578cd4da4f7b9e5a6756d30484f8f`).
The later `247e22c2adff4bf21c6d3a7427587b86a2693035` changes documentation
only; the subsequent corrections at `aede267c50f7c10978e42aa1eb4a8c43c96feaee`
and `44e8cf8c8f0285e70768e07291c2cd6f4a0ee9e6` change fixtures and
documentation. Through `14262bd5283c78e863f88cf7ebe3df37a124b1e6`, no Go source
differs from e04d627. The later commit-unknown clarification adds a focused test,
described below, without changing production Go code. This nonverbose task does not
provide a complete skip inventory. The live S2 run separately records the
selected Go checks and requires that none of those checks skip.

The accounted `mise run verify` gate also passed at e04d627, exit 0, unchanged
checkout and settled process group. Its raw log SHA-256 is
`584319a3d3b9b1cc5f126eda9d8e56784b0421a3319bb063a34e083ac2cf9fc3`.
The final PR gate is recorded separately at its final candidate.

Use the repository's development database/provider setup and `mise run go-db`
to reproduce the full task. Set `TMPDIR` and `GOTMPDIR` to disposable storage.
The publication-specific checks use isolated PostgreSQL databases and explicit
read-only stand-ins:

| Check | Boundary exercised |
| --- | --- |
| `TestPublicationRunningMinor` | Matching minor publishes; different minor refuses 422; missing/malformed reports refuse 503. |
| `TestPublicationIdentityAndAccess`, `TestPublicationOffline` | Wrong node/cluster, unread identity/version and all five provider failure causes refuse before compiler value reads; failed evidence remains safe. |
| `TestPublicationEvidenceAtCommit` | Missing/misbound evidence, old generation, changed endpoint including away/back, and higher-basis unread/contradicting evidence refuse T3; matching newer evidence passes. |
| `TestPublicationReadFenced`, `TestPublicationLeaseAfterLockWait` | Superseded reads cannot start or complete; a lease expiring during the operation-lock wait cannot start a read. |
| `TestPublicationMixedCoverage` | Two matching machines publish with both evidence links; a mismatching later machine prevents publication. |
| `TestPublicationOlderReadCompletingLate` | Lower-basis late completion cannot supersede chosen evidence; release and observation APIs retain the binding. |
| `TestPublicationWorkerRefusal` | The actual worker persists the failed operation and observation, with no release or Desired change. |
| `TestPublicationEndpointRace` | T3 waits for the endpoint writer, then rejects the changed endpoint. |
| `TestReleasePublicationEvidence` | Schema rejects missing, unknown and another publication's evidence; matching evidence commits. |

Existing publication worker tests retain commit-unknown/idempotency checks and
interruption takeover; the release after takeover names generation 2 evidence.
Existing execution tests now compare their own observations with the count
before execution, since publication legitimately adds two timeline entries.
The migrations' execution fixtures likewise express revisions relative to the
publication entries. These adaptations preserve the execution assertions.

## Commit-unknown clarification

The second hosted advisory review identified ambiguous retry prose. T3 commits
the release, Desired, published draft, operation success and terminal event in
one transaction. A lost reply after successful COMMIT leaves that succeeded
operation in the database, so the claim predicate cannot select it for takeover.
The client reads the persisted result even if a covered node is now offline.
An explicit T3 retry uses the retained built unit and returns its existing
matching release before evidence checks, without another remote read.

If T3 rolled back, there is no release. Its unfinished operation may be claimed
after lease lapse; that new pass must obtain fresh observations and correctly
refuses an offline node. PA §6.1.1 and §13.3 now distinguish these paths.
No earlier release lookup or worker behavior change is required.

At clean `7d06094caee750fe537483521ce52cf78319be8b`,
`TestPublishWorkerCommittedReplyLost` performed a real COMMIT, then returned a
synthetic connection-loss error to the worker. It verified durable operation,
release, draft, Desired and original evidence, then made the node reader
unavailable and proved neither the original nor another worker could claim it;
no second observation, start, access read or release appeared. Six existing
retry, rollback, claim, takeover and offline checks also passed: seven top-level
tests, nine PASS records including subtests, zero failures or skips, exit 0.
This simulates loss of the commit reply, not a real database network partition.
The sanitized evidence retains the exact command, revision and log hash.

## Negative controls and development failures

The first new tests ran on the pre-implementation source at a74cee5 with test
edits still uncommitted. The matching-minor control passed, while different,
missing and malformed versions and absent access did not refuse. That failure
is a development reproduction, not evidence for the final revision.

Four temporary source overlays subsequently removed one production guard each.
They were rerun at clean committed revision
`aede267c50f7c10978e42aa1eb4a8c43c96feaee`; all four exited 1 for the expected
assertions, while their matching controls passed. No selected test skipped:

| Overlay | Expected and observed exit 1 |
| --- | --- |
| Minor comparison | Matching-minor control passes; different-minor test detects publication. |
| Higher-basis comparison | Matching-newer control passes; different and unreadable newer evidence incorrectly commits. |
| Endpoint comparison | Matching control passes; changed and changed-away/back endpoints incorrectly commit. |
| Lease check before lock wait | Matching-minor control passes; the expired owner incorrectly starts a read. Completion still fences it. |

Each overlay maps only `internal/api/publication_observation.go` to a temporary
copy through `go test -overlay`. It leaves the checkout unchanged. Run the named
checks with `BW_REQUIRE_PG=1` and the development `BW_TEST_PG_DSN`, through
`mise exec -- go test -v -count=1`, selecting `TestPublicationRunningMinor`,
`TestPublicationEvidenceAtCommit` or `TestPublicationLeaseAfterLockWait` as
appropriate. Removing the overlay must pass. The retained overlay source and
logs allow comparison of the exact disabled predicate.

Earlier development runs failed on fixture assumptions about empty timelines,
relative revision numbers and missing executor setup, plus three new-test
mistakes: a mismatched default Kubernetes version, an error-code query using
the wrong JSON field, and a schema matching control that consumed the unique
operation before subsequent rejection cases. The retained intermediate logs
are not assigned to a clean committed revision. The final database/provider
gate above supersedes them as acceptance evidence, without erasing them.

## S2 assertion review repair

The first hosted advisory review found a stale revision label in the sanitized
header and a weakness in S2's `released_once` assertion. The header now names
`aede267` as the chain that stopped at S3 and `44e8cf8` as the passing repair
chain. The assertion now requires nonempty release-machine coverage and a
positively matching observation for every machine; a missing row or null field
cannot disappear from a count of violations.

The [reproducible harness](../../../scripts/test/s2-publication-evidence-test.sh)
extracts the actual function, without executing the scenario's top-level code.
Each query uses fresh nullable PostgreSQL temporary tables inside a rolled-back
transaction with `search_path = pg_temp`. Deliberately missing/null rows test
the assertion independently of production schema constraints; several are
already forbidden by those constraints. This is a fixture assertion repair,
not evidence that the publication implementation accepted those states.

At clean `6fe0af3afb0eca83844489d15d7069a43dfc8d17`, PostgreSQL 17.11,
the same harness checked the old function from published `0476c19` and the
corrected function. The old function missed 15 of 28 controls (expected exit 1);
the corrected function passed all 28 (exit 0), with zero SQL errors in both.
The controls cover one/two-machine success, missing/null/badly bound evidence,
wrong minor, mixed valid/invalid machines, empty coverage and the original
release/draft assertions. The checkout remained unchanged. Syntax, ShellCheck
and whitespace checks passed. No full live chain was rerun for this assertion
change; the live results above and below keep their original revisions.

With the disposable development database available, choose an empty artifact
directory outside the checkout and set `TMPDIR`/`GOTMPDIR` as above:

```sh
bash scripts/test/s2-publication-evidence-test.sh fixtures/scenarios/s2 "$artifact_dir/after"
git show 0476c196fa06f27c5320de50b27efb929b114ba9:fixtures/scenarios/s2 > "$artifact_dir/before-s2"
bash scripts/test/s2-publication-evidence-test.sh "$artifact_dir/before-s2" "$artifact_dir/before"
# The old-function control must exit 1; the corrected function must exit 0.
```

`BW_TEST_PG_DSN` defaults to the repository's disposable development PostgreSQL.
The retained development attempt that could not connect through the restricted
sandbox is a harness/environment failure, not an assertion result. The final
harness refuses a failed connection preflight and records SQL errors separately
from assertion outcomes. Both committed logs passed the same synthetic-value
and private-key-header screen, with all positive controls detected; this only
screens emitted output.

## Live fixture verification

The fresh chain completed at clean revision
`44e8cf8c8f0285e70768e07291c2cd6f4a0ee9e6`, with every step exiting 0 and
no tracked edits during execution. Retained record: `steps-20261019T163018Z`.

```sh
mise exec -- fixtures/scenarios/integrated "$record_dir" \
  up s1p s3h s2p s3r s4c s4x s4 s5p s7 s7n s7t s7o down
```

Set `TMPDIR` and `GOTMPDIR` to the same disposable directory on disk before
running. The profile uses Talos 1.13.6, requested Kubernetes 1.36.2,
PostgreSQL 17.11, OpenBao 2.6.1 (an older snapshot of that same version in S7), Docker
26.1.5, Compose 2.27.1, SOPS 3.13.3 and age 1.3.2. The retained manifest and
bundles pin the exact tool and image digests.

| Scenario | PASS | FAIL | SKIP |
| --- | ---: | ---: | ---: |
| `s1-pass-20261019T163225Z` | 36 | 0 | 2 |
| `s2-pass-20261019T163420Z` | 53 | 0 | 4 |
| `s3-handover-20261019T163336Z` | 29 | 0 | 0 |
| `s3-run-20261019T163614Z` | 98 | 0 | 0 |
| `s4-controls-20261019T163901Z` | 66 | 0 | 2 |
| `s4-restores-20261019T164201Z` | 141 | 0 | 1 |
| `s4-run-20261019T164706Z` | 33 | 0 | 0 |
| `s5-pass-20261019T164803Z` | 73 | 0 | 3 |
| `s7-no-entry-20261019T165828Z` | 49 | 0 | 0 |
| `s7-old-bao-20261019T170553Z` | 41 | 0 | 0 |
| `s7-run-20261019T165047Z` | 128 | 0 | 0 |
| `s7-transit-20261019T165941Z` | 46 | 0 | 0 |

All 48 bundles name this revision, have an empty fixtures diff,
complete capture and no leak-scan hit outside the positive-control file.
The built server image is `sha256:417c71280c270e335ae434c008c839441e71296ebdfd94d7300d5b633a2a19e9`.
Teardown confirmed no fixture containers, networks, volumes or state left.
The explicit pass-mode skips are retained in the sanitized evidence. They
do not rerun the full S1/S2/S5 negative matrices; earlier acceptance runs retain
their own revisions and coverage. No S8 human review ran in this chain.

Full S2 separately passed all 373 checks at clean `aede267` in the retained
run below, before that chain stopped at S3. It covers offline-node refusal,
all seven publication interruption points, generation-2 takeover with one
release and provider-wide outage refusal with linked safe observations.
Soft-deleted/destroyed pinned-value refusals, monitor classifications and
alerts passed too. Neither the Go implementation nor S2 changed between that
run and this chain; this chain reruns S2's pass paths, the repaired S3/S4
controls and the S7 integrated/variant sequence. S7's separate stale-instance
closing mode was not rerun at this revision; its earlier evidence remains
revision-bound.
S4's frozen-scope, schema/DS controls and transaction-level race remain
explicit skips naming their separate checks.

The first launch, `steps-20261019T151526Z`, omitted `mise exec` and stopped
at `up`, exit 1: ambient Go 1.27.2 differed from the pinned 1.27.1. It ran no
scenario and created no server image. The scoped `fixtures/bin/down` completed
before rerunning; its result and the failed chain remain retained. This was a
launcher error, not a passing fixture result.

The next run, `steps-20261019T151940Z`, at 247e22c ran from 15:19:40 to
15:49:04 UTC. Initial import and handover passed (36/0/2 and 29/0/0
PASS/FAIL/SKIP). S2 finished with 367/3/0 and the chain stopped. Its three
failures were the old expectation that a provider-wide outage produces a
pinned-dependency problem. Publication correctly refused earlier with 503 and
`publication-evidence`: the required executor read could not retrieve Talos
access. Network partition, seal and pause each produced that mismatch.

All seven publication interruption points and the live offline-worker refusal
passed in that failed run. Its selected Go checks also passed without skips.
All 33 bundles matched the revision, had empty fixture diffs and complete
sources, and contained only the leak scan's positive-control matches. The
scoped teardown then reported no fixture containers, networks, volumes or
state left. Later scenarios did not run. This is retained failed-run evidence,
not a successful integrated acceptance result.

The correction at aede267 changes the outage assertion to require that early
503 and its linked publication observation, correct generation and absent
access-version/node values. Soft-deleted/destroyed pinned-version refusals,
monitor classifications, alerts and the compiler's isolated unavailable-value
checks stay covered. AP S2 and the fixture README explain the order.

The third run, `steps-20261019T155019Z`, at aede267 ran from 15:50:19 to
16:22:19 UTC. S1 pass and handover passed again; full S2 passed all 373 checks
with zero failures or skips, including the repaired outage assertions. S3 pass
then failed eight assertions (66/8/1 PASS/FAIL/SKIP), and later scenarios did
not run. Its step-4 publications correctly refused the worker that S3 had
intentionally disconnected to hold approved plans before commitment. The
remaining failures followed from the missing releases, not an approval
implementation change. Both failed publications retained 503
`publication-evidence` problems naming their observations.

All 37 bundles matched aede267, had empty fixture diffs, complete captures and
positive-control-only scans. The scoped teardown completed before edits.
The repair prepares each release and its plan while connected and still
unapproved, then disconnects every instance before approving the three plans.
Their immutable releases retain the same self-approval reason checks. A
parallel source audit identified analogous fixture sequencing in S4 controls
and S7 recovery: those also need node reads available during publication while
preserving their held commitment or blocked-scope assertions. These fixture
adaptations change no product safety check.

A later advisory review found that AP S4 omitted the existing A/B pause
prerequisite for its commitment-level restore controls. The fixture already
pauses both instances before those cases and directs requests to C, the sole
running publisher/executor. The retained `s4-restores-20261019T164201Z/s4.tsv`
at `44e8cf8` passes that pause, both assignment/Desired publications and both
comparison-2 refusals. The written procedure now states this prerequisite;
the fixture is unchanged, and no additional live run is claimed.

## Evidence retention and scan limits

The owner retains raw logs, temporary guard controls and fixture bundles. Only
sanitized results and their SHA-256 references are published. The development
and committed test stdout/stderr logs were screened for the three named
synthetic test values and private-key headers, with zero hits. Every pattern
matched its positive control. This screen says nothing about surfaces the
command did not emit; database/timeline assertions and live fixture bundle
scans provide separate evidence.

The [sanitized results](evidence/20261019-publication-node-evidence.txt) retain
exact commands, statuses, skips and raw-log SHA-256 references, including all three
failed live attempts and the repaired chain. Raw bundles remain with the owner
until released; this summary does not replace their captured evidence.

## Limits

The selected live profile runs Talos 1.13.6. Different and malformed version
reports are explicit read-only stand-in tests, not an upgrade or a claim about
a second live Talos profile. No health predicate is required for publication;
configuration/resource reads remain diagnostic. No mechanism can exclude an
unrecorded remote change after a read; dispatch retains its own fresh checks.
Publication availability now depends on every covered node. There is no new
lifecycle/transport implementation, credential policy, deployment claim or
PoC owner verdict. The independent timeline review and owner assessment remain
open in ginsys/bronzeward#31.
