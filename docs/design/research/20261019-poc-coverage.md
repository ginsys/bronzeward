# PoC refusal and observation-access evidence

Evidence supplement for [ginsys/bronzeward#241](https://github.com/ginsys/bronzeward/issues/241),
19 October 2026. The [acceptance record](20261018-poc-acceptance-evidence.md)
remains the campaign record. This supplement expands R5 and R45; it does not
replace the independent timeline review, criterion verdicts or owner assessment.

## Evidence identity and reproduction

Source audit began at `ab327884527fee3b3ed32f0e2dfc8ffde5bf0ce3`.
Fresh checks ran at `c9398d45c1b690e8e11ce541dbb76af0ec690308`, on landed
publication repair `bc59346428ea267addfca37881c14eb4ecfda108`. The checkout
remained clean throughout execution. The [sanitized results](evidence/20261019-poc-coverage.txt)
retain the commands, package outcomes, focused names, controls, explicit skips
and original log hashes. This note and the acceptance-record update follow
those checks. Later review findings and follow-up checks are recorded separately below;
they do not change the identity or outcome of those original runs.

`mise run go-db` exited 0: all 21 packages with tests passed, and two had no
test files. The focused command exited 0 with 17 top-level passes (63 PASS
lines including subcases) and six explicit environment skips. In particular,
all 25 observation-purpose/access-failure combinations passed. Each of the two
removed-cluster-predicate controls exited 1 as expected: its foreign reference
was accepted with HTTP 200, which the committed refusal check detects. The
unmodified code passed both foreign-refusal and same-cluster paths.

The existing `TestAdopterRetryOutlivesClosedScope` can skip when its passes
outlast a lease. Its separate verbose run passed (exit 0, 0.83 seconds); the
nonverbose full task does not establish whether that individual run skipped.

The machinery/sensitivity gates require their dedicated C10 runner; the four
live Talos checks require a configured Talos target. They explicitly skipped
here. Their separately retained scenario/C10 results remain at their original
revisions in the acceptance record; this run is no new live Talos evidence.

The five retained result logs passed an exact screen for four synthetic
canaries and private-key headers. A positive control detected all five inserted
patterns. This is a stdout/stderr screen, not a scan of database/WAL, memory,
other encodings or unknown secrets. The named tests separately assert their
persisted records, timeline and logs, including the intentional error-text
control. The retained integrated runs carry their own broader surface scans.

Earlier scratch-overlay attempts remain retained, including the initial
untrusted-mise failure (`bw241-draft-tests.log`, exit 1) and the subsequent
passing scratch checks. They are not substituted for the committed checks.
The production publication defect and its failed diagnostic attempts belong to
the separate [repair evidence](20261019-publication-cluster-scope.md).

The local verification environment reports Go 1.27.1 on linux/amd64,
PostgreSQL 17.11 and OpenBao 2.6.1
(`ba7ad8861d0578cd4da4f7b9e5a6756d30484f8f`), read back on 19 October 2026.

The mappings below identify assertions, not just matching test names. Tests in
`internal/api` use isolated PostgreSQL databases; synthetic Talos/provider
responses are distinguished from the retained live scenarios. Compiler tests
use the pinned Talos machinery on synthetic inputs. The fixture scenarios use
the one selected container profile; they establish no VM/hardware or production
claim. A refused publication's “nothing” means no release or Desired change:
its operation, act, events and immutable publication observation starts/results
can persist (PA §6.1.1/§14). An interrupted read can leave a start without a
completed observation; retained failed evidence is not a published release.

## Review findings and remaining coverage

The local range review at `0724177ae21553c1d0c7abfaa60f40a85f1c1760`
found two gaps. C13-17's node-minor requirement applies at publication; the
commitment-stage refusal cited below does not establish it. An independently
repeated isolated database/stand-in-provider probe published a v1.13 release
for a synthetic observation reporting v1.12.4, while the v1.13.6 control also
published. The expected-failure run exited 1; retained log SHA256
`fb270eb50ed89f75e2b90adbbfd094af785681ca93f813c1af7172425d9ded96`.
The behavioral repair is [#247](https://github.com/ginsys/bronzeward/issues/247),
a native prerequisite of #241. At that review, version-evidence acquisition
still needed refinement: initial source-machine ingestion records no running
version, drift reads require an Applied baseline, and the compiler cannot
read Talos credentials. **R5 was incomplete at that revision.** Dispatch's
existing check remains useful but cannot substitute for the publication
refusal. The later owner decision and repair evidence are recorded separately
below; they do not change the failed probe's result.

PA14-13 also lacked a synchronous draft-update HTTP failure check. The added
`TestFragmentPutDatabaseFailure` checks a rolled-back T1 against a successful
commit, including the safe 503, draft/ETag, unreleased claim, retained synthetic
provider generation, absence of committed source/act/replay rows and secret
absence. At `02d012c222d51714a9ce08b7087f8c9d1fab35fb`, both cases passed
(exit 0). A temporary production overlay changing only that failure's HTTP
status from 503 to 500 failed the refusal case (exit 1), while its successful
commit control still passed. The checkout stayed clean during both runs.
The result file retains commands, log hashes and the clean follow-up log
screen with positive control. The older asynchronous and live kill results
remain at their original revisions. This closes PA14-13's missing response
coverage, not C13-17's publication defect.

### Publication repair evidence recorded separately

The [#247 repair report at `032ecd4`](https://github.com/ginsys/bronzeward/blob/032ecd4e15d9916f9269509fe7705684bbb43cf6/docs/design/research/20261019-publication-node-evidence.md)
records the 19 October owner decision: fresh read-only executor observations
of every covered machine, including first adoption, before compiler pinned-value
reads. Its [sanitized evidence at that revision](https://github.com/ginsys/bronzeward/blob/032ecd4e15d9916f9269509fe7705684bbb43cf6/docs/design/research/evidence/20261019-publication-node-evidence.txt)
separates the following runs and their limits. [PR248](https://github.com/ginsys/bronzeward/pull/248)
landed at `b3a479c0c0a4170d95d33e71b0ce799cd51a5eb7` on
2026-10-19 at 19:14:04 UTC. The landed tree equals the verified candidate;
branch and merge-queue CI passed, and #247 was assessed and closed. The
original source identities below remain the identities of the executed checks:

| Evidence | Exact source and outcome |
| --- | --- |
| Database/provider gate | [`e04d627`](https://github.com/ginsys/bronzeward/commit/e04d6272006fdfaa57e192d8531dbb5b0a47e8af): `mise run go-db` exited 0, 21 tested packages and two without tests; nonverbose output does not provide a complete skip inventory. |
| Guard-removal controls | [`aede267`](https://github.com/ginsys/bronzeward/commit/aede267c50f7c10978e42aa1eb4a8c43c96feaee): four overlays failed the expected assertions while matching controls passed, with no selected test skipped. |
| Full S2 | `aede267`, `steps-20261019T155019Z`: 373 PASS, 0 FAIL, 0 SKIP, including the required early node-access refusal under partition, seal and pause, offline-node refusal and seven publication interruption points. The chain later failed eight S3 assertions; this is a passing S2 within a retained failed chain. |
| Repaired integrated chain | [`44e8cf8`](https://github.com/ginsys/bronzeward/commit/44e8cf8c8f0285e70768e07291c2cd6f4a0ee9e6), `steps-20261019T163018Z`: every requested step exited 0, with 48 exact-revision, empty-diff, complete bundles and positive-control-only scans. Pass-mode skips remain explicit; this was not a full S1/S2/S5 negative-matrix rerun or an S8 human review. |
| S2 assertion controls | [`6fe0af3`](https://github.com/ginsys/bronzeward/commit/6fe0af3afb0eca83844489d15d7069a43dfc8d17): the extracted old assertion from `0476c19` produced 13 PASS / 15 FAIL (expected exit 1); the corrected assertion passed all 28 controls (exit 0), both with zero SQL errors. Nullable temporary tables test missing/null/wrong evidence and nonempty coverage; they do not establish production reachability. No live chain was rerun for this assertion-only repair. |
| Successful COMMIT with lost reply | [`7d06094`](https://github.com/ginsys/bronzeward/commit/7d06094caee750fe537483521ce52cf78319be8b): seven top-level tests, nine PASS records including subtests, zero FAIL/SKIP, exit 0. `TestPublishWorkerCommittedReplyLost` performs real COMMIT then returns synthetic `08006`; operation/release/draft/Desired and original evidence remain, neither worker can reclaim, and an unavailable node causes no new observation/read/release. Six existing retry, rollback, claim, lapsed-owner and offline checks passed alongside it. This is not a real database network partition. |

Under a provider-wide outage the required access read fails before any compiler
pinned-value read. The failed operation names `publication-evidence`, safe cause
`unreadable-identity-or-version`, the machine and its observation; it has no
`dependency` object. Full S2 checks the linked publication generation and absent
access-version/node values, with `provider-unavailable` unread causes. Later
compiler dependency failures remain separately checked by `TestReadPinnedRefuses`
and the other compiler tests below. Neither refusal path creates a release.

Known identity/minor mismatches refuse 422; missing or unreadable identity,
version or access refuses 503. Different/malformed node versions are stand-ins;
the live profile remains Talos 1.13.6. The original 25-case access matrix and six
environment skips above remain five-purpose evidence. The fresh six-purpose
run below supplies the added publication checks. #241's closeout assessment
remains separate from #31's human acceptance.

### Fresh coverage after the landed repair

At `1cc6ddc4960615e42db646e21e8428960d95606f`, the focused command passed all 22 top-level tests
(100 PASS records, no failures or skips), including all 30 named failed-access
combinations across the six purposes. The access-version test additionally
asserts all 12 latest/stale combinations in its loop; these are not 12 named
subtests. Publication records the running operation, generation and owner,
including absent access/node values and safe causes after failed reads. The
stale control distinguishes access version 1 from the normal publication
version 52; it does not assert publication success or refusal.

The full `GOFLAGS=-v mise run go-db` passed all 21 tested packages; two have
no tests. Its complete verbose log records 1026 top-level passes,
2135 PASS records and 6 explicit skips: `TestMachineryGate`, `TestSensitivityGate`, `TestLiveRead`, `TestLiveApply`, `TestLiveUnreachable`, `TestLiveRoleProbe`.
The combined run lasted 546 seconds (2026-10-19T19:17:12Z to 2026-10-19T19:26:18Z), with a
clean unchanged checkout. The sanitized results retain both exact commands,
versions, names, skip inventory and log hashes. Both complete logs passed the
four-canary/private-key-header screen; its positive control detected all five
patterns. This remains a stdout/stderr screen with the limits stated above.

This run repeats the R5/R45 checks and the inherited publication repair on the
combined tree. Earlier failed probes, guard-removal controls and live scenarios
retain their original revisions. No live fixture rerun or human S8 review is
claimed. The retained clause mappings and these checks supply R5/R45 evidence;
publication, hosted review and issue acceptance still govern #241's closure.

## R5: compilation §13

Rows follow the contract's order. Each alternative in a condition is explicit.

| Row and condition | Executable evidence and assertion |
| --- | --- |
| C13-01 Parse failure; mark addresses no node | `ingest.TestRefusalSweep`, `TestExtractRefusalCreatesNothing`; `api.TestRefusalAbandons`; S1 refusal rows: rule/path, abandoned claim, no payload/generation or draft change. |
| C13-02 Guard hit | Same refusal sweep and S1 guard cases; S2 rejected fragment update. Guard rule/path is returned, claim abandoned, draft/ETag unchanged; canary scans cover the response and durable surfaces. |
| C13-03 Provider write fails part-way | `ingest.TestCommitFailureAndOnce`, `api.TestProviderUnavailableAbandons`; S1 provider/interruption rows: earlier generations remain unused, no draft commit, claim abandoned and payload cleared. |
| C13-04 Crash before payload | S1 kills before and after generation creation; `api.TestTakeoverNothingToDecrypt` and staging takeover tests: transient lapse abandons; encrypted takeover without ciphertext abandons, retaining only claim/orphans. |
| C13-05 Crash after payload before T1 | S1 encrypted/transient interruption cases; `api.TestTakeoverResumesToT1`: encrypted takeover resumes once at the new generation, transient claim abandons, no premature draft commit. |
| C13-06 Crash inside T1 | `api.TestCrashInsideT1LeavesDraft` and S1 draft-transaction kill: draft/revisions roll back, unreleased claim retains ciphertext. |
| C13-07 Paused mark refuses before provider write | `api.TestMarkRefusedReturnsPaused`, `TestMarkFirstGenerationFailureReturnsPaused`, `TestMarkRefusalNamesNoPath`: earlier envelope and paused state retained; operation records refusal without secret-bearing path. |
| C13-08 Mark refuses after provider write | `api.TestMarkPartWayFailureAbandons` and S1 review failure: abandoned claim, no payload, created generations orphaned, draft unchanged. |
| C13-09 Killed mark; killed continuation | `api.TestMarkInterruptedTakeoverPausesWithoutMarks`, `TestContinuationKilledBeforeT1TakeoverCommits`; S1 kills each re-entered mark stage and continuation: pending review pauses again; continued envelope reaches T1 once. |
| C13-10 Stale heartbeat/commit/release; paused state | `staging.TestOwnerFenceMatrix`, `TestPausedOwnerTransitionsRefused`, `api.TestT1RefusedAfterGenBump`, `TestAbandonRefusedAfterGenBump`: each owner/generation/epoch/state term refuses the stale write, row unchanged, matching-owner controls admitted. |
| C13-11 Absolute expiry; abandonment; recovery entry, including paused | S1 expiry/abandonment/review rows; `api.TestRecoveryEntryAbandonsPausedClaim`, `TestRecoveryEntryEndsJobsAndClaims`; staging expiry/sweep cases: payload cleared, claim abandoned, later import required. |
| C13-12 Undeclared/unused name; local tag; key/sequence tag; reserved text; bad path | `ingest.TestValidateAuthoring`, `TestRefusalSweep`; API fragment refusal: each rule/path refused without a committed fragment. Sweep checks all formatting verbs for synthetic values. |
| C13-13 Dependency not retained; unreadable | `compile.TestCheckPinnedRefuses`, `TestReadPinned`; `api.TestReadPinnedRefuses`, `TestBuildReleaseDependencyUnknown` retain isolated compiler classification/read refusals. Full S2 at `aede267` retains soft-deleted/destroyed pinned-value refusals and separate monitor classifications/alerts; its provider-wide outage cases now prove the earlier publication node-access 503, not a compiler pinned-value read. No release in either path; see the revision-bound repair evidence above. |
| C13-14 Kind mismatch; base64 on non-string | `ingest.TestResolveRefusals`, `TestValidateAuthoring`; `api.TestBuildReleaseRefuses`: kind/encoding refusal, no artifact/release, safe error. |
| C13-15 Secret reference belongs to another cluster | New `api.TestFragmentPutRefusesOtherClusterReference`: references actually minted through both foreign import-base and fragment routes; local draft rejects each, same-cluster control succeeds. Removing either cluster predicate makes its corresponding case fail. Publication diagnostics found a missing check, separately tracked in [#244](https://github.com/ginsys/bronzeward/issues/244): the inconsistent stored source reached provider reads and a successful worker release. `TestReadPinnedClusterScope` and `TestPublishWorkerRefusesForeignGeneration` cover its repair. The repair landed through [PR245](https://github.com/ginsys/bronzeward/pull/245) at `bc59346428ea267addfca37881c14eb4ecfda108`; its [evidence note](20261019-publication-cluster-scope.md) retains the failed probe and green checks. Supported authoring prevents constructing this source; direct database corruption is not asserted remotely reachable. |
| C13-16 Ordinary fragment overrides/deletes an import-base reference | `compile.TestCompileRefusesABaseOverride`, `TestCompileRefusesAnAliasOverride`, `TestCompileBaseOverrideNamesOneFragment`; S2/S5: refusal names fragment/path, no release; authorized composition is the control. |
| C13-17 Contract newer than renderer/not node minor; Kubernetes outside window | `compile.TestCheckContract`, `TestKubernetesVersionRefuses` and `api.TestBuildReleaseRefuses` retain renderer/Kubernetes checks. #247 adds `TestPublicationRunningMinor`: same-minor publication succeeds, different minor refuses 422, missing/malformed version refuses 503; the minor-guard overlay detects the missing comparison. `TestPublicationMixedCoverage` refuses a mismatching covered machine; `TestPublicationWorkerRefusal` retains the failed operation/observation with no release or Desired change. The repair runs are bound above; the original failed probe and dispatch-only limitation remain historical evidence. |
| C13-18 Native composition/validation rejects | `compile.TestComposeRejectsTypeMismatch`, `TestComposeRejectsAnUnloadableBase`, `TestCompileReturnsTheRealVerdict`, `TestCompileMessages`; `api.TestBuildReleaseRefuses` preserves rule/path and withholds unsafe native text. |
| C13-19 Trace/flip counterpart missing; shape differs; passes disagree | New `compile.TestCompileRefusesAnUntraceableFragment` rejects real-valid input whose trace cannot be built, on either side of a boolean input; successful controls retain both inputs and attribute the flip. `TestFidelityFailsOnAnInjectedChange` rejects changed shape, untraced leaf and flipped shape; `TestCompileTraceInvalidIsFidelity` rejects real-valid/trace-invalid disagreement. Full source lists are constructed before each flip; no test claims an externally supplied flip input API exists. |
| C13-20 Reserved output; unattributed exact copy | `compile.TestComposeRefusesReservedTextInOutput`, `TestCompileRefusesAnExactCopy`, `TestCompileCopyChecksEveryLeaf`, `TestCompileRefusesACanonicalCopy`, mapping/key-copy cases and no-secret assertions: paths only, no artifact; attributed copies are separately controlled. |
| C13-21 Encryption failure; stale persistence input | `compile.TestEncryptArtifactsRefuses`, `api.TestBuildReleaseEncryptionRefused`, `TestBuildReleaseDependencyUnknown`; `TestPublishCommitChangedHeadMoved`, `TestPublishCommitUnchangedHeadMoved`, `TestPublishCommitAppliedImportBaseChanged`: no release, failed operation with expected classification. |

## R5: persistence/API §13 worked examples

| Example | Executable evidence and persisted outcome |
| --- | --- |
| PA13.1 Concurrent edits | `api.TestFragmentPutPreconditions` refuses a stale ETag before ingestion; `TestFragmentPutMovedBeforeT1` refuses movement during ingestion and leaves abandoned-claim orphans. `TestPublishCommitChangedHeadMoved`, `TestPublishCommitUnchangedHeadMoved` and its concurrent case refuse the moved head; S2's unlocked `FOR SHARE` control demonstrates the stale commit. |
| PA13.2 Provider ahead of database | S1 T1 interruption and `TestCrashInsideT1LeavesDraft` retain the unreleased claim/provider generations without draft entries. Transient sweep and encrypted takeover have separate outcomes. The orphan report lists without deleting; S1/new import creates new names. |
| PA13.3 Database ahead of client/provider | `TestPublishWorkerCommittedReplyLost` at `7d06094` proves successful T3 already committed operation success and original evidence: no reclaim or fresh read after the reply is lost, even with the node now unavailable. `TestPublishCommitRetry` reuses its built unit and returns the matching release before evidence checks; `TestPublishCommitExistingRelease` is a synthetic second-operation transaction control, not ownership takeover. Request transaction tests separately distinguish committed/still-committing/not-committed. S7 older-OpenBao restore alerts on missing retained generations and refuses regeneration while the surviving artifact/key permits the separate apply claim. |
| PA13.4 Repeated requests | `TestReplay`, `TestKeyReusedForAnotherRequest`, `TestSameKeyInFlight` with unlocked control, `TestFragmentPutLiveClaimForKey`, `TestPublicationRequestStates`, `TestApprovalRefusals`: one effect, stored replay, live entry-claim conflict, wrong-key fingerprint refusal, active operation reuse, published-draft conflict and exactly one approval. |
| PA13.5 Migration | `migrate.TestSecondRunWaitsThenSkips`, `TestNoLockControl`, `TestKilledMigrationLeavesNothing`, schema/checksum checks: second runner waits; killed transaction leaves no migration; incompatible binary refuses startup. |
| PA13.6 Older restore | S7 nominal restores backups older than ownership/approval/attempt/revocation changes, enters a fresh epoch, refuses stale tokens, records unaccounted scopes, preserves denied subjects and reissues automation. S7 no-entry is the explicit residual control; one released scope resumes while other scopes stay gated. |

## R5: persistence/API §14

The row numbers below follow the 40 contract rows at the audited revision.
Shared transaction tests assert act/idempotency counts; publication refusal
helpers assert no release, the failed operation and terminal event. Publication
observation starts/results and their machine timeline entries can also persist,
including all-null node/access results from a failed provider read. Those
assertions do not turn all failures into “nothing persisted.”

| Row and alternatives | Executable evidence and assertion |
| --- | --- |
| PA14-01 Missing/malformed/expired/over-long/wrong signature | `api.TestUnauthenticated`, `auth.TestEveryDefectUnauthenticated`, `TestParseTokenLength`, `TestAutomationDefects`: 401, no principal/act/request record, no epoch header. |
| PA14-02 Revoked/denied subject; earlier-epoch token | `TestRevokedCredentialIs403`; `auth.TestRevokedAndDeniedHumans`, `TestAutomationRevokedOrDeniedIdentity`, `TestAutomationExpiredRevokedEarlierEpoch`; `api.TestServiceTokenEpochRecheckedAtRouting`, `TestServiceTokenEpochRecheckedInTransaction`: 403 or 401 as specified; no transaction effect. |
| PA14-03 Missing role; automation on human-only route | `TestForbidden` and S0 role matrix: 403, required roles/human-only explanation, no principal for refused admission. |
| PA14-04 Missing key/If-Match | `TestNotImplemented` exercises the shared key/header gate; `TestFragmentPutPreconditions`, `TestDraftUpdatePreconditions`: 428 before claim/provider/record creation. |
| PA14-05 Key reused for different request | `TestKeyReusedForAnotherRequest`, `TestKeyedFingerprint`: 422, only original act/record remains. |
| PA14-06 Same key in flight, including draft claim | `TestSameKeyInFlight`, `TestSameKeyInFlightNoKeyLockControl`, `TestSameKeyAfterRollbackExecutes`, `TestFragmentPutLiveClaimForKey`: wait/replay or execute after rollback; live entry claim conflicts; one effect. |
| PA14-07 Entry key from another recovery start | `TestRecoveryEntryRefusals`: 409 names stored request/epoch, no callback or epoch change; fresh key enters. |
| PA14-08 Earlier-epoch record | `TestEarlierEpochRecord`, `TestReplayWaitsForEpochChange`: 409 names old record/epoch, never replayed success. |
| PA14-09 Digest-key version unavailable; new-key active ingestion; new-key refused entry | `TestKeyedFingerprint` distinguishes denied old version (422) from provider unavailable (503), preserving one record. New `TestNewKeyAfterDigestKeyLoss` actually loses that version, then proves a new key conflicts with the active ingestion or ingests refused draft input afresh, leaving a second abandoned claim. `TestFragmentPutRefusalReplayed` covers replay while the original key remains usable; C13-03/08 cover orphan generations after partial writes. |
| PA14-10 ETag mismatch | Fragment/draft precondition tests: 412, no claim or generation, unchanged draft. |
| PA14-11 Update/discard during queued/running publication | `TestDraftUpdatePreconditions`, expanded `TestDraftDiscard` name the operation and preserve revision/state in both states; discard also asserts no discard act. Failed-operation controls permit editing or discarding. |
| PA14-12 Compilation refuses draft input | Fragment refusal/replay and ingestion refusal cases: 422 paths/rule, abandoned claim, persisted refusal record/act, no fragment; provider orphans only when a write occurred. |
| PA14-13 Database failure inside T1 | `TestCrashInsideT1LeavesDraft` and S1 interruption retain their rollback/claim evidence. New `TestFragmentPutDatabaseFailure` adds the synchronous 503, safe body, unchanged draft, held claim and provider orphan, with a successful-commit control. Both passed at `02d012c`; changing only the failure status to 500 made the refusal check fail while the success control still passed. |
| PA14-14 Changed/unchanged head; changed import base; newly covered machine; missing/misbound/superseded publication evidence | `TestPublishCommitChangedHeadMoved`, `TestPublishCommitUnchangedHeadMoved` and concurrent case, `TestPublishCommitAppliedImportBaseChanged`, `TestPublishCommitMachineCovered` and concurrent case retain input-movement checks. #247's `TestPublicationEvidenceAtCommit` and `TestPublicationEndpointRace` add binding, endpoint and higher-basis evidence refusals against matching-newer controls: failed stale-input, no release. |
| PA14-15 Introduced name now exists | `TestPublishCommitIntroducedNameExists` and concurrent case: expected-absent mismatch fails stale-input, no release. |
| PA14-16 Assignment change on held scope | `TestPublishCommitScopeHeld` and concurrent case: failed scope-busy, no release; S4 held-scope control. |
| PA14-17 Blocked/lost/changed dependency identity, concurrent creation-time seed | `TestReadPinnedRefuses`, `TestBuildReleaseEncryptionRefused`, `TestPublishCommitStatusOtherIdentity`, `TestPublishCommitStatusOtherIdentityConcurrently`, `TestPublishFirstNamedConcurrentlyRefused`, `TestPublishAfterMonitorWaitedRefused`; S2 step 6: failed validation-failed names dependency, no release. |
| PA14-18 Missing/unreadable publication identity/version/access; unknown value/encryption/read metadata; missing Date; sealed/unreachable provider | #247's `TestPublicationIdentityAndAccess` checks the early evidence refusal before compiler reads. Full S2 at `aede267` proves partition/seal/pause fail 503 `dependency-unavailable`, naming machine/observation/rule/cause rather than a dependency; the linked failed observation has no access version or node values. `TestReadPinnedRefuses`, `TestBuildReleaseDependencyUnknown` and `compile.TestEncryptArtifactsRefuses` retain the separate later compiler dependency failures and response controls. Neither path releases anything; immutable observations already recorded remain. |
| PA14-19 Known publication identity/minor mismatch; compiler rejection; mixed Kubernetes versions | #247's `TestPublicationIdentityAndAccess`, `TestPublicationRunningMinor` and `TestPublicationWorkerRefusal` retain the machine/observation-bound 422 with matching controls. `TestBuildReleaseRefuses`, `TestPublishWorkerRefused` retain compiler rule/path errors. The failed operation/event and any publication evidence remain; no release. |
| PA14-20 Stale pin/selection; changed assignment without import base; no covered machine | `TestSnapshotRecheck`, `TestSnapshotCoverage`, `TestPublishCommitNoImportBase`: validation-failed, no release; `TestSnapshotComposition` is the complete-snapshot control. |
| PA14-21 Commit-unknown | `TestPublishWorkerCommittedReplyLost` at `7d06094` retains the already-succeeded operation and original release/evidence with no reclaim or node reread. `TestPublishCommitRetry` returns the matching natural key using the retained built unit before evidence checks; `TestPublishCommitExistingRelease` supplies a separate synthetic transaction-level control. Request commit-unknown cases cover the in-flight outcome separately; no result assumes rollback. |
| PA14-22 Deadlock exhaustion; rejected COMMIT; statement constraint | `TestPublishCommitDeadlocked`, `TestPublishCommitRejected`, `TestPublishCommitConstraintViolated`: failed 503 or 500, no release. `TestPublishCommitRejected`'s `08006` control deliberately rolls back first, leaving no release and a running operation for retry; it is distinct from successful COMMIT/reply loss in PA14-21. A newly claimed unfinished pass obtains fresh observations and may refuse an offline node. |
| PA14-23 Declaration/text-digest mismatch | `TestPublishCommitDeclarations` covers reference, source, version, digest, occurrence and provenance inconsistencies: failed internal-error, named clause logged, no release. `TestPublishCommitDeclarationsAgree` is the successful control. |
| PA14-24 Superseded worker; lapsed lease | `TestPublishCommitFenced`, `TestPublishRunSuperseded`, `TestPublishRunLapsed`, `TestPublishRunStopped`: no stale commit or failure event; rightful takeover publishes once. #247 adds `TestPublicationReadFenced` for supersession before/during access reads (no completed stale evidence) and `TestPublicationLeaseAfterLockWait` for lease expiry during the operation-lock wait (no observation start). |
| PA14-25 New request for published draft | `TestPublicationRequestStates`: 409 names the release, no second operation. |
| PA14-26 Second approval in epoch | `TestApprovalRefusals`: 409, original approval and plan state unchanged. |
| PA14-27 Apply plan not Desired | `TestPlanCreationRefusals`: 409 names Desired, no plan/counter movement. |
| PA14-28 Apply without Applied; adopt with Applied/no drift; moved assignment | `TestPlanCreationRefusals`: each 409, no plan/counter movement; valid control succeeds. Drifted adoption has separate checks. |
| PA14-29 Release does not cover machine | Same table: 422, no plan/counter movement. |
| PA14-30 Duration/attempt range; transport maximum/verification bound | Same table and `TestPlanCreationDurations`: malformed/out-of-range cases 400, no plan/counter movement, valid boundary values admitted. |
| PA14-31 Approval/identity revocation races commitment | `TestCommitmentRevocationsWait`, S4 DS row 003 and identity-lock controls: serialized before or after commitment; later attempts recheck authority. |
| PA14-32 Plan/approval/adoption/unfreeze before accounting | `TestPlanCreationRefusals`, `TestApprovalRefusals`, `TestAdoptionRefusals`, `TestFreezeRefusals` plus S7: 409 recovery-mode-active, no admitted mutation. |
| PA14-33 Commitment/attempt/adoption before scope release | `TestCommitmentScopeGateControl`, `TestAttemptRefusals`, `TestAdoptionScopeHeld`, S7: gate refuses with refusal entry; released-scope control proceeds. |
| PA14-34 Publication changes unreleased assignment | `TestPublishCommitRecoveryMode`, S7: failed recovery-mode-active, operation/event retained, no release. |
| PA14-35 Recovery-start request before entry | `TestRecoveryStartServesOnlyTheEntry`: reads, writes, unmatched/unclean routes refused before principal creation; entry and liveness exceptions preserved. |
| PA14-36 Second entry in same start, new key | `TestRecoveryEntry`: 409 names first epoch, one callback and one epoch transition. |
| PA14-37 Any deadlock retries exhausted | `TestDeadlockRetriesExhausted`: 503, no effect/act/request record. |
| PA14-38 Failed/killed migration | `migrate.TestKilledMigrationLeavesNothing` and failure cases: failed migration absent, earlier migrations retained; startup refuses missing schema. |
| PA14-39 Schema/checksum mismatch | Migration check matrix and `TestServeRefusesAnUnmigratedDatabase`: startup refused, no schema modification. |
| PA14-40 Restore without entry | S7 no-entry: old tokens can pass until entry. This is the documented detection limit, not a positive safety guarantee. |

## R45: observation and mutation boundaries

| Required clause | Assertion and control |
| --- | --- |
| Observation client read allowlist | `talos.TestMachineryCallsAllowlisted` checks the actual reader/applier call sets, including typed calls. Injected reader `ApplyConfiguration` and COSI `Destroy` calls must be detected. Exported-surface/import/caller guards constrain alternate call paths. |
| Plan-less drift observation | `api.TestObserveDrift` records drift-purpose observation with null plan/operation, the access path/version, identity/configuration evidence and no node metadata leak. It does not create mutation authority. |
| Access version and failed read | At `c9398d4`, `TestObservationRecordsTheAccessItRead` checks the original five purposes and distinguishes the stale-credential control; `TestObservationPurposeAccessFailure` crosses those purposes with absent, denied, malformed, unavailable and unclassified-status errors (25 cases): recorded purpose/plan/operation and cause, no access or node values, no dial, no provider error text in records/timeline/logs. The fresh run at `1cc6ddc` covers all six purposes with 30 named failure combinations and 12 latest/stale loop assertions, including publication's running owner, operation and generation. The latest/stale control records the access actually read; it does not assert publication success or refusal. `TestObserveAccessReadFails` independently uses a stand-in node for drift failures. `TestObserveNodeReadFails` covers malformed/refused credentials, a silent endpoint and individual read failures; `TestObserveErrorTextControl` detects the deliberate client-error-text leak. These node-side checks use drift observations, while R44 retains dispatch-specific refusals. |
| Mutating client refused after revocation | `TestExecutorRevokedControl`: revoke after commitment and before attempt; shipped path records zero attempts/sends, enters unresolved/cancelled as specified. Skipping comparison 1 admits the control's exact no-reboot artifact send to its Talos stand-in (ER choice §10.32). `TestExecutorRevocationHeld` checks the lock wait. |

## Acceptance-boundary audit

The source cross-check accounts for all 50 AP rows. The named test functions were inspected by assertion, not inferred from
a matching name. Fresh full-suite results and the focused R45 checks above
supplement the retained integrated runs.

| Indexed required-check list | Acceptance-record rows | Audit outcome |
| --- | --- | --- |
| C §15 | R1–R5 and R50 | Interruption/scans, claims, review, SR/SP and fidelity remain bound to their recorded tests and fixture runs. R5's clauses are mapped here; #244 is repaired, and #247 now supplies separately revision-bound publication-minor repair evidence landed through PR248; the fresh combined-tree checks above complete the executable mapping; #241 closure still requires publication, review and acceptance. |
| ER §9.2 | R6–R20 | Existing execution, accounting, identity, drift and restoration evidence remains in the record. R11 and R17 retain the future bound-health cases in #188; the late-read and late-landing residuals remain limits. |
| PA §16 | R5, R15, R21–R28 and R35–R50 | Refusal traceability is expanded here; R42's platform checks and R45's observation boundary receive explicit mappings. All other rows retain their named tests, controls and scenario references. |
| DM §10.1 | R29–R34 | All 15 items map to these rows: live provider states/publication/restore variants remain distinct from synthetic classifications, identity changes, ordering, watchdog and log controls. No additional provider topology is claimed. |

Design §18.2 and AP §7 map adoption/extraction to S1/S3 handover, native
authoring/publication to S2, exact approval to S0/S3, direct no-reboot worker
dispatch and separate state to S4, drift handling to S5, interruption to S6 and
explicit restoration to S7. S8 carries timeline usability. The acceptance
record preserves the separate original run revisions and all limitations.

AP §7.1's 50 rows remain individually owned by that record. R5 is expanded
above, R45 separates observation permission from execution authority, and R11's
health-window cases remain explicitly open in #188 until a future operation
class binds health. Adding a health check to today's PoC is not a coverage fix.
No later lifecycle or remote-transport feature is necessary to close a PoC row.

Two details in the indexed contracts need explicit mapping beyond the table's
short labels. PA §16's platform input and release-mode clauses sit with R42:
`api.TestInventoryRefusals` rejects missing/null/unknown/wrong-type platforms
without inventory writes; `TestInventoryPlatform` records and serves each
supported value. `migrate.TestMachinePlatform` has dropped-constraint controls,
and `TestReleaseConstraints` requires the named `release_machine_platform`
refusal for a mismatched mode while its matching-mode control commits.
These tests already exist; the audit adds their mapping, not platform support.

ER §9.2's observation ordering includes a future bound-health branch. R17's
`TestSettle` cases exercise digest, identity and assignment contradictions and
confirmed failures; the source explicitly names the PoC's unbound-health limit.
The health branch is retained in #188: contradictory higher-basis health reads
in both directions, and agreeing higher-basis reads that confirm failure after
the deadline. R17 therefore means the PoC predicates are evidenced, not that a
bound-health reader or its ordering cases have run.

At this report's 19 October checkpoint:

Milestone 03 requires the integrated evidence and owner assessment, not merely
closed implementation issues or passing CI. #31 remains open for the independent
S8 review by the owner-named person, six criterion verdicts and the dated owner
assessment. Milestone 02 stays
open while the excluded licence decision #16 remains unfinished. Later upgrade,
maintenance/reset and transport work is separately tracked without implementation.

**20 October update:** the [dated delegated assessment](20261020-poc-timeline-assessment.md#3-criterion-verdicts-and-independent-review)
records the independent agent reading and corrects the owner-named-person
characterization above. The [current criterion verdicts](20261018-poc-acceptance-evidence.md#7-acceptance-criteria-of-ginsysbronzeward31)
retain the assessment's limits; original run SKIPs are unchanged and no human
trial is claimed. Issue closure still requires the assessment to land.

## Reproduction

Use the repository's disposable development PostgreSQL/OpenBao setup and run
`mise run go-db`. Set `TMPDIR` and `GOTMPDIR` to suitable disposable storage.
For the focused run, use the environment, four package paths and anchored union
of the 23 test names in the historical section of the sanitized result file.
The later `1cc6ddc` section separately retains the exact 22-name command over
API, compiler and Talos packages, followed by `GOFLAGS=-v mise run go-db`;
use those commands for the six-purpose extension and verbose skip inventory. Do not set `GOFLAGS=-json`:
the method inventory's nested `go list -m -f` cannot combine those options.

For either authoring control, make a temporary copy of
`internal/api/fragments.go`, replace only `m.cluster = $1` (import-base) or
`f.cluster = $1` (fragment) in `carriedReference` with `$1::text IS NOT NULL`,
and map the original absolute path to that copy in a Go overlay JSON object's
`Replace` dictionary. The cast preserves the SQL parameter binding, so failure
comes from accepting the foreign reference, not an untyped parameter. Run:

```sh
BW_REQUIRE_PG=1 \
  BW_TEST_PG_DSN='postgres://bronzeward@127.0.0.1:55433/bronzeward?sslmode=disable' \
  mise exec -- go test -v -count=1 -overlay="$coverage_overlay" \
  -run '^TestFragmentPutRefusesOtherClusterReference$/fragment$' ./internal/api
```

`coverage_overlay` names the temporary JSON file; select `/import-base$` for
that origin. Each control must exit 1 because it gets HTTP 200. Removing the
overlay must exit 0. Keep the checkout unchanged. The compiler control proves
both real inputs remain and both attributions exist when tracing succeeds;
the refusal input is valid for real composition but cannot be traced. It does
not invent a public API accepting externally supplied trace/flip fragments.
