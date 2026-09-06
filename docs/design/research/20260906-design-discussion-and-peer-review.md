# Bronzeward: design discussion, peer review and follow-up

Date: 6 September 2026

Status: Historical design discussion and review record; not an implementation specification, accepted ADR or replacement for the authoritative design.

Reviewed repository HEAD: `2cadaaee8b007d8c6a896669d033b81c409cfaa2`

## 1. Purpose and review baseline

This document records the repository assessment, the item-by-item discussion with the project owner, and the subsequent peer-review exchanges. It distinguishes owner-confirmed directions from reviewer recommendations, assistant responses and unresolved investigations.

Sections 2–13 preserve the initial discussion report. Sections 14–23 record the later review, follow-up and reconciliation in order, including corrections to earlier wording. An earlier statement is not silently promoted into a current guarantee because it appears in this historical record. In particular, the peer review qualified the original statements about approval dispatch, secret retention, validation, renderer compatibility and duplicate operations.

The [main architecture concept](../Talos_Configuration_and_Machine_Management_Design.md) remains separate. The earlier [renderer research](20260826-talhelper-internals-and-topf-successor.md) is another historical input, not an implementation dependency.

At the reviewed HEAD, the repository contained an architecture concept, an earlier design review, two renderer research notes, a naming rationale and four diagrams. It had no application implementation or runnable test suite. The main architecture document was revision 0.4. All five Markdown documents and four diagrams were reviewed. Relative file/image links resolved. At that point, the checkout was clean and one commit ahead of the locally recorded `origin/main`; remote state was not refreshed.

The assessment and initial decision discussion were read-only, and the initial report was prepared as a separate artifact. After receiving peer feedback, the owner requested that it be moved into this research directory and extended to preserve the discussion's historical value. At that archival stage, the main architecture concept had not yet been reconciled. Section 22 records the subsequent authorized revision. No decision recorded here has been implemented or validated by a new prototype.

The reviewer was not identified in the supplied messages. Review summaries and successive reviewer replies were supplied; the underlying full review file, experiment definitions and estimates were not available. Quoted report line numbers refer to the version exchanged before archival, not necessarily this expanded document's current lines. Repository line references refer to the reviewed HEAD above.

Primary repository references, with line anchors at the reviewed HEAD:

| Reference | Relevant content |
|---|---|
| `docs/design/Talos_Configuration_and_Machine_Management_Design.md:48` | Product purpose and positioning |
| Same document, `:131` | Existing decisions, preferences and open points |
| Same document, `:243` | Native configuration, fragments, composition and renderer |
| Same document, `:315` | PostgreSQL/OpenBao responsibilities and publication |
| Same document, `:624` | API, approvals, reconciliation and operations |
| Same document, `:731` | Trust boundaries and secret access |
| Same document, `:783` | Reliability and recovery |
| Same document, `:874` | Version 1 scope and implementation phases |
| `docs/design/research/20260826-talhelper-internals-and-topf-successor.md:299` | Renderer conclusions revisited in this discussion |

## 2. Overall assessment

Bronzeward has a credible purpose and a coherent architectural direction. It is ready for focused technical investigations and a configuration-control prototype. Its operation and security contracts need to become explicit before broad lifecycle implementation.

The strongest existing choices are native Talos configuration, immutable releases separated from activation, direct Talos access, explicit infrastructure boundaries, and conservative handling of out-of-band changes.

The assessment identified six main concerns:

1. Compiled configurations are encrypted, but handling of secret-bearing source YAML was unspecified.
2. Approval requirements did not distinguish application enforcement from an independent security boundary.
3. Durable journals and retries did not define recovery from an uncertain remote outcome.
4. Independently restored application and secret data needed a compatible recovery protocol.
5. Renderer research overstated the requirement for multiple binaries and historical compatibility.
6. The initial implementation scope was broader than necessary to prove the core configuration workflow.

The discussion resolved the architectural direction for all six. It also expanded the requirements for small installations and clarified that operating database servers is outside the product.

## 3. Decision register

| Subject | Confirmed direction | Still open |
|---|---|---|
| Secret identification | Automate the Talos-generated secret bundle; help with other reliably identifiable secrets; operator retains responsibility | Exact schema/redaction signals and detection coverage |
| Secret custody | Identified secrets belong in the configured secret provider, outside ordinary plaintext source history | Lightweight provider implementation |
| Secret references | Structural substitution of complete values, including explicitly typed embedded YAML/JSON | Final syntax, metadata and supported encodings |
| OpenBao | Primary supported vault, not mandatory for every installation | Local alternative investigation |
| Database choice | PostgreSQL must not be mandatory for small setups; investigate SQLite | Libraries, SQLite operating limits and migration design |
| MySQL/MariaDB | Consider only if support does not introduce the extra cost the owner wants to avoid | Actual implementation, testing and maintenance cost |
| Database operation | External database services are user-operated | Connection/deployment documentation details |
| Approval | Application-enforced; privileged controller explicitly trusted | Exact resource schema and approval policies |
| Interrupted operations | Recover automatically from evidence; preserve uncertainty and require operator resolution where necessary | Per-operation state machines and retry rules |
| Restored application state | Require explicit recovery mode before mutations resume | Recovery-mode implementation and validation inventory |
| Renderer compatibility | Mirror native `talosctl` support for supported fleet versions and upgrade transitions | Supported-version matrix and packaging evidence |
| First milestone | Configuration control on an existing cluster | Detailed implementation specification after review/investigation |

## 4. Secret identification and custody

### Context and clarification

The initial concern was not exclusive to imports. Secret-bearing configuration can arrive through manual authoring, import of an existing configuration, or adoption of an out-of-band change.

The owner clarified the expected model:

- Talos-generated secret bundles, commonly handled separately as `secrets.yaml`, are an obvious default that Bronzeward can automate.
- Other secrets can appear in configuration for enabled software or embedded content. Examples raised were a Hetzner API token associated with management of the Kubernetes API IP, secrets in inline Cilium-related manifests, and other embedded YAML.
- Secrets must move into the secret store, but the operator is responsible for identifying material that Bronzeward cannot reliably recognize.
- Bronzeward should help when identification is straightforward, potentially using upstream Talos schema or redaction information. Exhaustive automatic detection is not required.

These examples motivate reference handling. They do not by themselves authorize adding Kubernetes workload deployment or GitOps management to the product; that remains outside the existing scope.

### Confirmed decision

Automatically handle the Talos-generated secret bundle. Provide assistance for other reliably identifiable secrets. Treat the operator as the final authority responsible for marking additional secrets.

Ordinary configuration sources retain references rather than identified secret values. Complete rendered machine configurations remain encrypted artifacts.

This replaces the earlier recommendation to encrypt all raw source YAML. Blanket encryption of all source was not selected. It also avoids a requirement to classify every arbitrary embedded format automatically.

### Required consequences

- Marked secrets must not also persist in plaintext fragment revisions, parsed representations, ordinary diffs or logs.
- Import and drift-adoption workflows need a secret-identification/extraction step before ordinary revision persistence.
- Unmarked values are ordinary configuration. The system does not guarantee confidentiality for a secret the operator failed to identify and assistance failed to recognize.
- Detection assistance must not claim completeness where upstream information is absent or insufficient.

Whether upstream schemas expose usable sensitivity metadata for every relevant field has not been established. That is an investigation item, not a confirmed capability.

## 5. Secret references without general YAML text templating

### Alternatives discussed

| Alternative | Benefit | Drawback |
|---|---|---|
| Text template expressions with helpers | Familiar, visible at the point of use, flexible inside arbitrary content | Makes operators responsible for quoting, indentation and encoding |
| YAML tags/reference objects | Structural replacement and serializer-controlled encoding | Embedded content is still opaque without explicit parsing |
| Separate document/field bindings | Reference metadata stays outside native configuration | Harder to inspect together; paths can become fragile in lists and embedded content |

The owner accepted explicit references but rejected recreating Helm-style template problems. The proposal was therefore narrowed from text rendering to structural value substitution.

### Confirmed behavior

1. Parse configuration as YAML.
2. Recognize an explicitly marked secret reference occupying a complete value.
3. Resolve the authorized logical secret name.
4. Replace the value structurally.
5. Serialize the resulting native configuration and validate it using upstream tooling.

Illustrative syntax only:

```yaml
password: '{{ secret "registry-password" }}'
```

The quoted expression is an ordinary YAML scalar until Bronzeward recognizes it. This is not a commitment to Go templates, a final grammar, or a general expression evaluator.

The intended constraints are:

- No loops, conditions, includes, arbitrary functions or recursive evaluation.
- A secret containing reference-like text remains literal secret content.
- Ordinary fields do not require manual quoting or indentation helpers; the serializer handles those details.
- Reference interpretation must be explicitly enabled/identified so existing literal text is not accidentally evaluated. The declaration mechanism remains open.
- Logical secret names are provider-independent and scoped to the compilation's authorized bindings.
- Missing bindings, unavailable versions or failed reads stop publication; they do not silently produce empty values or unresolved placeholders.
- Published releases retain the exact resolved secret-version references and artifacts. Updating a secret does not mutate an existing release.

### Embedded content

For explicitly identified embedded YAML or JSON, apply structural substitution within that parsed content, then serialize it back into the containing field.

For arbitrary text or scripts, initially allow referencing the complete content from the secret store. Unrestricted substring interpolation was not selected.

An embedded Kubernetes Secret could use `stringData` with a structural secret reference. No decision was made about the full set of binary, base64 or other typed conversions. Earlier text-template `quote`, `nindent` and `b64enc` examples are not the accepted interface.

The mechanism for declaring embedded content types, supported nesting depth, scalar types, literal escaping and interaction with fragment merging/provenance remains to be specified. These are peer-review targets, not reasons to introduce a general templating language.

## 6. Secret providers and encryption investigation

### Confirmed direction

OpenBao remains the main supported vault. Home users should have a simpler alternative that does not require operating that additional service. Secret references must remain independent of provider selection.

No lightweight encryption implementation was selected. The owner requested an investigation plan.

The existing OpenBao design performs multiple jobs: versioned secret storage, compiled-artifact encryption through Transit, credential storage, and potentially signing-related work. Replacing only secret lookup would leave other dependencies unresolved.

### Candidates discussed, not selected

| Candidate | Proposed use | Questions to establish |
|---|---|---|
| Local encrypted storage using age | Store encrypted secret revisions and artifacts using Bronzeward's persistence, with externally supplied decryption identity | Key custody, startup unlock, rotation, recovery and local trust boundary |
| SOPS with age | Read operator-maintained encrypted files; potentially support import/export | Automated writes/generation, immutable version identity, concurrency, retention and artifact encryption |

The working recommendation was to investigate an age-backed local provider, with SOPS/age initially considered for import/export. This remains a recommendation for investigation, not an accepted backend selection.

age supplies encryption tooling and a Go library; SOPS supports encrypted structured files and age recipients. Neither supplies Bronzeward's authorization, release or operation semantics by itself. [age project](https://github.com/FiloSottile/age), [SOPS documentation](https://getsops.io/docs/).

### Investigation tasks and evidence

1. Inventory all required secret and cryptographic operations, including Talos bundle generation, operator-supplied values, artifact encryption/decryption, version references and enrolment signing needs.
2. Compare OpenBao and lightweight candidates against that inventory without requiring a small installation to reproduce every OpenBao facility.
3. Define key provisioning, startup unlock and recovery procedures. Establish what must be backed up separately from application data.
4. Prototype generation, publication, restart, rotation, retained-artifact retrieval and restoration onto another host.
5. Verify that marked secrets do not appear in ordinary plaintext database records or logs. Account for derived representations and temporary persistence as part of the test.
6. Evaluate movement from local storage to OpenBao while preserving logical references, release identities and access to retained artifacts.

Expected deliverable: a recommended implementation, its limitations, supported combinations and concrete recovery procedures. No prototype has yet run.

## 7. Database portability and operational boundary

### Confirmed direction

PostgreSQL should remain a supported server option, but must not be a mandatory dependency for small setups. Investigate SQLite for small, single-instance installations. Consider MySQL/MariaDB if the selected persistence approach permits them without the additional cost the owner wants to avoid.

All supported databases must provide the behavior Bronzeward actually requires. Database selection must not weaken release atomicity, conflict detection or operation safety.

No ORM, query generator, driver or schema abstraction was selected. Driver availability is not evidence that migrations, locking behavior, tests and long-term support are free. The investigation must report that cost before promising MySQL/MariaDB support.

### Product responsibility

Bronzeward owns its application schema, schema migrations, connection configuration, compatibility checks and correct use of transactions/concurrency mechanisms.

For external database services, users own provisioning, replication, HA, failover and database backup operation. Bronzeward will not operate a highly available PostgreSQL installation for them. CloudNativePG may appear as an external deployment example, not a required product component.

SQLite requires application management of the embedded database file/schema and documented safe backup/restore procedures. Multi-instance SQLite HA is not a requirement introduced by this discussion.

The application still defines recovery dependencies and verifies its behavior after restoration. This is distinct from operating a database backup or failover service.

### Investigation tasks

1. Define the persistence behavior needed for immutable revisions, atomic publication, draft conflicts, operation ownership, journals and restart recovery.
2. Identify PostgreSQL-specific assumptions in the existing concept, including JSONB, and distinguish essential behavior from implementation conveniences.
3. Evaluate suitable Go persistence approaches against PostgreSQL and SQLite. Assess MySQL/MariaDB cost alongside them.
4. Prototype publication and operation claiming under concurrent requests, interrupted transactions and restarts.
5. Verify application migrations and behavior after externally performed backup/restore.
6. Assess migration from SQLite to a server database without changing application identities or losing release history.

The first milestone discussion proposed testing SQLite and PostgreSQL against the same application contract. Precise backend release commitments and deployment combinations depend on the investigation results.

## 8. Approval enforcement

### Confirmed decision

Approval is enforced by Bronzeward's server and operation engine. The privileged controller is explicitly inside the trusted boundary.

This protects against unauthorized API requests and execution inconsistent with an approved plan. It does not promise that a compromised privileged controller cannot bypass its own checks.

An independent execution-authorization service was considered and not selected for the initial product. Such a service would hold machine credentials and validate approved requests independently, adding a separate privileged component and recovery dependency.

OpenBao key and secret policies do not automatically enforce approval records held in Bronzeward's database. Transit provides cryptographic operations and key access controls, rather than application-level rollout approval. [OpenBao Transit documentation](https://openbao.org/docs/secrets/transit/).

### Intended approval contract

Approve an immutable operation/rollout plan identifying:

- The release and exact per-machine artifacts, including pinned secret versions.
- Target machines and relevant assignment revisions.
- Operation type and parameters, including permitted apply/reboot behavior.
- Relevant starting-state assumptions.
- Rollout limits and approval validity/revocation conditions.

Before dispatch, the executor verifies authorization and those preconditions. Relevant changes require replanning; changes to the authorized action require renewed approval. Routine observation updates should not invalidate a plan merely because a timestamp changed.

Revocation prevents future dispatch; it cannot undo a command already accepted by Talos. Separate operation-specific rules govern cancellation and recovery.

The exact schema and freshness rules remain to be specified. Whether authors may approve their own changes, and whether any actions require multiple people, remain policy questions. Two-person approval was not made a requirement for a home installation.

Independent emergency Talos access remains supported. Bronzeward approval does not govern external actions, which can appear as drift and invalidate pending plans.

## 9. Recovery from interrupted operations

### Confirmed decision

Recover automatically when observations establish a safe next step. Where uncertainty cannot be resolved automatically, preserve that uncertainty and require operator resolution.

Pausing every interrupted mutation for manual intervention was considered and rejected in favor of evidence-based recovery. Blind replay was not accepted.

Example: Talos accepts a reset, but Bronzeward crashes before recording the response. The journal cannot alone prove whether the command arrived. On restart, Bronzeward must observe the machine and relevant cluster state before choosing another action.

| Step classification | Intended behavior |
|---|---|
| Completion proven | Record completion and continue |
| Retry established as safe | Retry within the operation's authorization and deadline |
| Outcome unresolved | Preserve assignment/state, stop dependent mutations and continue observation or request specific operator resolution |

Required design properties:

- Record durable intent before dispatch, including target, approved parameters and expected outcome.
- Prevent competing execution and account for old workers or remote commands that may still be running after ownership changes.
- Recheck identity and assignment before subsequent mutations.
- Give cancellation operation-specific semantics; stopping future steps does not imply undoing accepted actions.
- Do not treat timeout as proof of failure, completion or permission to replay.
- Offer specific recovery actions rather than a generic way to mark an uncertain operation successful.

The policy does not promise exactly-once delivery or execution of every remote action. Database idempotency and journals do not by themselves provide that guarantee.

Verification should interrupt operations before dispatch, after remote acceptance and before local completion recording, and exercise restarts, duplicate requests and competing workers. Per-operation postconditions and retry rules remain implementation work.

Talos apply modes also need distinct completion rules: temporary `try` changes require confirmation, and `staged` changes do not immediately alter the current configuration. These were raised as concrete correctness concerns, not implemented workflows. [Talos configuration editing documentation](https://docs.siderolabs.com/talos/v1.13/configure-your-talos-cluster/system-configuration/editing-machine-configuration).

## 10. Explicit recovery mode after restoration

### Confirmed decision

Require explicit recovery mode after restoration of Bronzeward's database or secret state, before mutations resume.

The user performs database/vault restoration through external tooling. Bronzeward validates whether the restored application state is usable and safe to act on.

A newer database backup can reference secret versions or encryption keys missing from an older secret-store backup. A restored older database can also describe operations as pending even though machines already completed them. Successful database startup is insufficient evidence of application recovery.

Backups need compatible dependencies, not necessarily identical timestamps. Validation should distinguish what is needed for each activity:

| Activity | Required dependencies/evidence |
|---|---|
| Observe a machine | Usable credentials and verified identity |
| Apply a retained release | Decryptable artifact, valid authorization and compatible current machine state |
| Compile a new release | Available source revisions and required exact secret versions |
| Resume an operation | Observations establishing completed steps and safe continuation |

Intended recovery flow:

1. Enter recovery mode after external restoration; mutations are paused.
2. Check application records, relevant secret/key references and artifact decryptability.
3. Refresh reachable machine identities, running state and relevant assignment evidence.
4. Classify interrupted operations using the agreed recovery policy.
5. Report ready, blocked and unresolved scopes.
6. Resume management for the appropriate scope only after explicit operator release from recovery mode.

Releasing recovery mode is not blanket approval for discovered changes. Normal drift and plan-approval rules still apply.

A missing dependency for one historical release need not prevent unrelated observation. The dependency inventory and retention policies should explain which secret/key versions are required for retained releases and the operator's selected recovery window.

Bronzeward cannot assume it can detect every external restoration automatically. Explicit recovery-mode entry belongs in the restore procedure.

This is recovery of the management application, distinct from managed-cluster etcd snapshot/restore workflows.

## 11. Renderer compatibility and historical artifacts

### Discussion outcome

The owner challenged the value of retaining Talos version X support after a cluster had moved to Y. The accepted requirement was narrowed accordingly:

> Mirror what `talosctl` natively supports for supported fleet versions and upgrade transitions; do not maintain a broader compatibility layer.

Relevant mixed-version cases are different clusters running different supported versions and machines at different supported stages of an upgrade. An old cluster history alone does not require keeping its renderer executable available indefinitely.

Upstream machinery supports generation for older version contracts. Therefore one build can cover multiple contracts within a verified upstream support range. Multiple binaries are not automatically required by a multi-version fleet. [Pinned upstream contract source](https://raw.githubusercontent.com/siderolabs/talos/v1.13.6/pkg/machinery/config/contract.go).

### Confirmed direction

- Pin and record the renderer build for provenance.
- Use retained exact artifacts when executing an eligible published release; do not silently regenerate them with a newer renderer.
- Retain historical artifacts/provenance according to retention policy without promising indefinite support for their Talos versions.
- Verify the combinations required by upstream-supported fleet versions and upgrade transitions.
- Introduce multiple renderer binaries only if a concrete requirement and compatibility evidence justify them.

The earlier research conclusion that multiple builds are necessarily required must be qualified when the documents are revised. Indefinite exact historical regeneration was not selected as a product requirement.

Keep the observed running version, configuration contract, desired installer image and renderer implementation conceptually separate. Precise renderer/client packaging, support-window publication and the meaning of native support for each operation remain investigation/specification details. No downgrade or rollback support beyond upstream behavior was added.

## 12. First milestone and next work

### Confirmed milestone

The owner selected **configuration control on an existing cluster** as the first usable milestone. Worker enrolment and reset/reuse follow afterward.

Proposed contents of this selected milestone:

- Explicit adoption of a disposable existing cluster with known configuration/secret material, establishing a baseline without mutating machines.
- Native fragment/profile revisions, reuse and structural secret references.
- Validated immutable publication, encrypted artifacts, redacted diffs and source provenance.
- Approval of an exact plan and direct application of a demonstrably non-disruptive worker change.
- Verification of resulting state.
- Drift detection with explicit freeze, adopt and revert.
- Safe recovery from an interrupted apply and explicit recovery mode after restoration.
- A minimal authenticated API sufficient to exercise the workflow; a polished UI is not the first proof.

Acceptance criteria proposed during the discussion:

1. Editing or publishing does not mutate machines.
2. Execution uses the exact approved artifact and parameters.
3. Stale or revoked approval prevents dispatch.
4. Marked secrets are absent from ordinary persisted sources, responses and logs.
5. Interrupted operations continue safely or remain explicitly unresolved.
6. Restored state cannot trigger mutations before recovery validation and operator release.
7. Out-of-band configuration changes are reported rather than silently overwritten.

These are proposed criteria elaborating the agreed milestone, not a completed detailed specification or passing test results.

This milestone does not establish bootstrap, reset, OS/Kubernetes upgrade, remote transport or etcd-recovery safety. Scheduled managed-cluster etcd snapshots remain in the broader version 1 scope; external database-server backup management does not become part of it.

### Remaining work and open questions

1. Peer-review this report before updating the authoritative architecture.
2. Resolve any resulting contradictions or missing decisions.
3. Consolidate accepted outcomes into the design and investigation plan.
4. Perform database-portability and lightweight-secret/encryption investigations.
5. Specify the first milestone's interfaces, state transitions and acceptance tests, then implement it incrementally.

Important open details include the reference grammar, embedded-format declarations, scalar/binary encoding, scoped secret authorization, schema-assisted detection, provider key custody, database/library selection, operation ownership, per-operation recovery evidence, approval policy, restoration validation and the exact upstream compatibility matrix.

The project's precise open-source license was not selected in this discussion. The initial review also noted the need for an entry-point README and the already acknowledged corrections to figures 2–4. These were not promoted into additional architecture decisions.

## 13. Technical sources used during the discussion

These support specific mechanisms, not claims that the proposed application has been implemented or tested:

- [Talos v1.13 configuration editing](https://docs.siderolabs.com/talos/v1.13/configure-your-talos-cluster/system-configuration/editing-machine-configuration): apply-mode semantics.
- [Talos v1.13 configuration patching](https://docs.siderolabs.com/talos/v1.13/configure-your-talos-cluster/system-configuration/patching): upstream merge semantics relevant to future reference/provenance design.
- [Talos v1.13.6 version-contract source](https://raw.githubusercontent.com/siderolabs/talos/v1.13.6/pkg/machinery/config/contract.go): backward configuration-generation support.
- [OpenBao Transit](https://openbao.org/docs/secrets/transit/): cryptographic services, caller context and named-key permissions.
- [OpenBao KV v2](https://openbao.org/docs/secrets/kv/kv-v2/): versioned secret storage.
- [age](https://github.com/FiloSottile/age): encryption format, tooling and Go library.
- [SOPS](https://getsops.io/docs/): encrypted structured files and age support.

The broader Omni comparison and ecosystem research were not comprehensively revalidated in this discussion. Their historical claims should not be presented as a fresh competitive or licensing assessment.

## 14. First peer-review feedback supplied by the owner

The following is the review summary as supplied, rather than an independently verified statement of findings. The reviewer later corrected some of its wording in section 16. The two finding identifiers are C1 (plaintext exposure during drift adoption) and C2 (retention of release-pinned secrets and encryption keys).

> Verdict
>
> Direction is sound; the report is unusually disciplined about labelling status. Not ready for a full
> specification — ready for a milestone-1 specification gated on six experiments. Three blockers, none
> architectural:
>
> 1. Two "confirmed" decisions rest on undefined terms (renderer "native support" §11; reference
>    "declaration mechanism" §5, explicitly left open).
> 2. One confirmed decision contradicts an existing repo workflow, unrecorded.
> 3. Two upstream defaults silently void the immutability guarantee the release model rests on.
>
> The two critical findings
>
> C1 — drift adoption leaks cluster PKI. Report §4 requires extraction before persistence; repo
> Talos_Configuration_and_Machine_Management_Design.md:717 still adopts the observed effective config into a
> mutable draft, stored as plaintext TEXT + JSONB (:353). That config carries the cluster CA key and join
> token. It then lives in every base backup and WAL segment; redacting later doesn't help (heap tuples
> survive until VACUUM, the backup is already off-box). Voids the differentiator that carried the build gate
> (:1214). Fix: make extraction a precondition of the write, not a pipeline step.
>
> C2 — release-pinned secret versions die by default. OpenBao KV v2 keeps 10 versions by default and prunes
> the oldest on write; Transit trim deletes key versions permanently. A weekly-rotated secret pinned by
> rel_42 is gone in ten weeks — and if the Transit key version goes, the retained artifact becomes
> undecryptable and the release unexecutable. Repo :357 already applies the right reasoning to secret
> generations; it just needs extending to every referenced secret, plus a consistency check that alarms
> before the reference breaks.
>
> Rest of the review
>
> 6 Required-before-implementation (renderer rule is one line, not multi-binary — verified against
> contract.go @ v1.13.6 and the two-latest-minors support policy; approval dispatch race; try is a 60 s
> auto-revert timer, not a confirmation; fencing tokens not locks; YAML tag over marked string; drafts
> aren't upstream-validatable). 6 investigation questions, 3 optional. Plus: 7 unresolved owner decisions
> separated from settled ones and from routine implementation choices, the report's 6 overclaims, a
> repo-vs-report discrepancy table, and 6 experiments with estimates — the secret-leak sweep first, since
> it's the only one whose failure is unrecoverable.

The counts and unseen lists above are attributed to the reviewer. They were not adopted as a verified implementation gate from this summary alone.

## 15. Assistant's response to the first peer review

The assistant accepted the underlying implementation risks but challenged several status descriptions, broad conclusions and proposed remedies. This section records that response; it is not an additional owner approval.

### C1: extraction before plaintext persistence

The requirement was accepted: extraction must precede ordinary plaintext persistence, including draft storage, parsed JSON, logging and temporary staging. An interactive import that needs staging must protect its unresolved content.

However, section 4 already required extraction before ordinary revision persistence. The repository still described the older workflow because the owner had postponed authoritative document updates until peer review. This was a reconciliation requirement, not an implemented leak. The response proposed covering import and drift adoption, failed requests and interrupted processing in the acceptance test.

### C2: retention across all secret and encryption dependencies

The substantive gap was accepted. OpenBao KV v2 defaults to ten versions per key; writing beyond the configured limit permanently deletes older versions. A mount-level `max_versions` value of zero retains the default of ten, rather than unlimited history. [OpenBao KV v2 API](https://openbao.org/docs/api/secret/kv/kv-v2/).

The response distinguished three mechanisms:

- The ten-week example assumes weekly writes to the same key. Separate immutable generation objects do not necessarily encounter that limit.
- Transit trimming is an explicit destructive operation, not automatic pruning on rotation. Raising minimum decryption settings can also deny access to old ciphertext without destroying the key material. [OpenBao Transit API](https://openbao.org/docs/api/secret/transit/).
- Losing a source secret version prevents regeneration that needs it. It does not necessarily prevent applying a retained, decryptable complete artifact. Losing the artifact's encryption key is a different failure.

The proposed requirement was reference-aware retention for every secret and encryption dependency. An alarm cannot guarantee protection before an external administrator deletes a dependency. Retention policy, restricted destructive permissions and checks before Bronzeward-controlled cleanup must therefore be distinguished from guarantees about user-operated OpenBao.

The reviewer later clarified that several of these distinctions already appeared in the unavailable full review body and had been lost only in the summary.

### Other findings

| Finding | Assistant assessment at this stage |
|---|---|
| Undefined renderer support | A valid specification gap; upstream release support and generation compatibility are different axes. The owner's architectural direction remains settled. |
| Approval dispatch race | Valid. Define a precise boundary after which revocation cannot prevent an already-authorized dispatch; the initial report's wording promised too much. |
| `try` confirmation | Specify the follow-up configuration-update workflow. Talos documents a default one-minute reversion unless another configuration update is applied; do not imply a separate confirmation RPC. |
| Fencing rather than locks | The stale-executor risk is real, but a database token is not enough unless the relevant execution boundary enforces it. Require evidence of the safety property before choosing the mechanism. |
| YAML tag rather than marked string | An open syntax choice, not a proven blocker. Compare literal handling, embedded content and parser behavior. |
| Draft validation | Distinguish early draft syntax/binding checks from full Talos validation after composition and reference resolution. Do not imply that nothing about a draft can be validated. |

The response also challenged the description of secret exposure as the only unrecoverable failure: loss of the only usable encryption key can be unrecoverable too. The six experiments could not be accepted as a gate without seeing their definitions. Undefined grammar or declaration details were treated as work needed for a specification, not grounds to erase previously agreed architectural direction.

The owner then requested archival of the report, feedback, response and further discussion in this research directory, explicitly as a historical design discussion and review.

## 16. Reviewer's follow-up supplied by the owner

The owner forwarded the assistant's response to the reviewer and supplied the reply below. Claims about API fencing, certificate revocation and duplicate-operation safety are preserved as reviewer statements; qualifications in section 17 must be read alongside them.

> Five corrections accepted, three points held, and one of your objections upgrades a finding rather than settling it.
>
> Accepted
>
> 1. C1 status — my wording violated your brief. You told me to treat discrepancies as proposed changes, not mistakes. My finding body
> says the report "correctly identifies" the extraction requirement, but my verdict said "unrecorded" and "the report does not flag
> it." That is wrong: report :89 states it. The accurate statement is narrower — the requirement is recorded; what is missing is the
> enumeration of which repository passages it invalidates (:717 is named nowhere in the report, unlike :275 for the renderer).
> Severity: the risk class stays critical, the current status is "requirement recorded, reconciliation pending." No leak exists because
> nothing is implemented.
>
> Your acceptance test (import and drift adoption, including failed requests and interrupted processing) is stronger than my experiment
> 1, which only covered the success path. Interrupted processing is the case that produces staged plaintext with no cleanup owner.
>
> 2. Renderer — I conflated two axes. Release-support policy and generation compatibility are independent, and I derived one rule from
> both. The generation axis alone gives the rule: pinned machinery minor ≥ highest target contract in the fleet, with backwards
> generation reaching further than the support window (contract v1.0 per contract.go @ v1.13.6). The support policy says what ought to
> be running, not what can be rendered — a fleet may legitimately run an out-of-support version and render fine. My conclusion
> survives; the derivation was sloppy and the corrected version is wider, not narrower.
>
> 3. Fencing — you are right, and it makes the finding worse, not smaller. My remedy had the stale worker enforcing its own staleness,
> which is circular: the stale process's lease view is the thing that is stale. Worse, the enforceable options are thinner than I
> implied — Talos apid has no fencing or generation concept, and no client-certificate revocation path that a manager could rely on, so
> nothing at the remote end can reject a stale executor. That means the safety property cannot be "no stale dispatch occurs." It has
> to be stated per operation class: idempotent ApplyConfig of the same artifact is harmless under duplication; ResetMachine,
> BootstrapCluster and WipeDisks are not. So: define the property per operation, then pick a mechanism — which is what you asked for,
> and I skipped it.
>
> 4. "Only unrecoverable failure" — too strong. Loss of the only usable encryption key is equally unrecoverable. My ordering of the
> experiments does not survive that; both branches rank together.
>
> 5. Summary compression. The C2 distinction you restate — losing a source version blocks regeneration, losing the key blocks
> application — is in the review body verbatim; my chat summary collapsed them into one sentence and lost it. Same for the six
> experiments: definitions and estimates are in §6 of the file, not the summary. Reasonable to reject a gate you could not see.
>
> Held
>
> C2 substance, sharpened by your own objection. You are right that separate immutable generation objects avoid the ten-version limit —
> and repository :357 already mandates exactly that, for the Talos bundle. My scenario used an operator-supplied registry password,
> and that is the case that matters: the structural-reference mechanism in report §5 names a logical secret and pins a version, which
> is the mutable-path-with-version-history pattern :357 deliberately rejected for bundles. So the finding is not "KV prunes things"; it
> is that §5 introduces a second secret class to which :357's discipline was never extended. Your framing — reference-aware retention
> across every secret and encryption dependency — is the correct requirement, and it lands in the same place.
>
> On enforcement: you are right that an alarm cannot beat an external administrator. Since OpenBao is user-operated (§7), the deny on
> delete/destroy/trim and metadata writes is a documented deployment requirement, not something Bronzeward can enforce. Worth saying so
> explicitly, or the retention contract reads as a product guarantee it cannot make.
>
> Two smaller notes: my text says max_versions: 0 means 10, so the zero correction is aimed at something I did not claim; and I listed
> trim as an explicit operation, not automatic pruning — though placing it beside the KV default in the summary invited that reading.
> Your min_decryption_version refinement is a genuine addition: it breaks decryption immediately, non-destructively, and is reversible
> until trimmed.
>
> The alternatives-table defect, independent of syntax. I accept that tag-versus-string is an open design choice and I overreached in
> recommending one. But the defect I found is not about which form wins: report §5's table assigns "embedded content is still opaque
> without explicit parsing" to the tag row alone, and it applies equally to all three rows. As written the table cannot discriminate,
> so it will not support whichever comparison you run. That stands whatever syntax is chosen.
>
> Three form-independent properties stay required. Resolution ordering (once, on the merged document — otherwise the release's
> pinned-version set does not match its artifact whenever a higher layer overrides a referencing field), the encoding modifier as a
> closed enum, and diff redaction by path-recorded-at-resolution rather than value matching. None depends on the grammar.
>
> Validation staging. Your framing is better than mine — name the stages and their guarantees. My dummy-substitution remedy is one
> implementation of the late stage; the requirement is the naming.

## 17. Assistant's response to the reviewer follow-up

The response accepted the convergence on the underlying risks and corrected its own assumptions about the unavailable full review. It retained specific qualifications rather than treating the new reply as a completed specification or a fresh owner decision.

### Accepted corrections and refinements

- **Plaintext exposure:** the agreed status is “requirement recorded, reconciliation pending.” The missing work is enumerating the affected repository passages and testing failure/interruption paths as well as success. There is no implemented leak in a documentation-only project.
- **Retention:** the rule must cover operator-supplied secrets as well as Talos bundles, encrypted artifacts and recovery dependencies. The first response's notes about `max_versions: 0`, explicit trimming and source-versus-artifact loss should not be described as corrections to a full review the assistant had not seen. The summary was insufficient to establish what the full review already contained.
- **External operation:** Bronzeward should document required secret-store permissions and retention configuration, check compatibility where possible, and refuse its own unsafe cleanup. It cannot promise to prevent destruction by an independent privileged administrator. Exact policies need to distinguish runtime, rotation and cleanup identities; a broad denial of every metadata write is not a fully specified policy.
- **Reference alternatives:** the original table does not fairly express the embedded-content requirement. Text templating can insert bytes into an opaque string, but safe context-aware substitution still needs encoding information. Structural approaches need an explicit format or a whole-content reference. This shared requirement must be compared separately from tag/string/binding syntax.
- **Validation:** name the stages and the guarantees of each. YAML/reference validation can precede resolution; complete upstream validation applies to materialized configuration. Dummy substitution is a candidate technique, not an approved requirement or proof of validity with the real values.
- **Experiment priority:** both plaintext exposure and loss of the only usable encryption key deserve early testing. The full experiment list and estimates remain unavailable in this record.

A logical secret name with a pinned revision does not itself require a mutable OpenBao path: it could map to an immutable physical object. The confirmed gap is the absence of a retention contract for all referenced secret classes, rather than a settled choice of unsafe physical layout. Extending immutable object storage and requiring sufficient retention for externally managed references are alternatives to evaluate, not decisions made by this exchange.

### Qualification: duplicate ApplyConfig is not automatically harmless

Safety cannot be inferred from the RPC name or artifact identity alone. A stale worker can apply artifact A after another worker has already applied B, undoing newer desired state. Even repetition of A depends on mode: reboot mode has a reboot effect, and `try` has temporary-state/timer semantics. These consequences follow from the documented modes; they were not reproduced in a new test during this discussion. [Talos apply modes](https://docs.siderolabs.com/talos/v1.13/configure-your-talos-cluster/system-configuration/editing-machine-configuration).

The specification needs to classify **operation, mode, target/assignment and observed state**, including interleavings. It must not assume administrative credentials or database leases provide a Talos-enforced fence. The reviewer asserted the absence of native fencing and usable certificate revocation; this record does not claim a comprehensive API audit proving those negatives.

Where an older executor could still act and no enforcement boundary can exclude it, automatic conflicting mutations must not proceed on the strength of a new lease alone. The implementation must establish a safe ownership transition or remain unresolved. This does not require selecting an independent authorization service or distributed execution architecture at this stage.

### Qualification: the renderer version inequality is a prerequisite, not a complete matrix

Separating generation compatibility from upstream release support is correct. A matching major/minor-capable machinery build must be new enough to understand the required contract and documents. However, a minor-version comparison alone does not prove patch/pre-release feature compatibility, validation behavior or client RPC support.

The `v1.13.6` source includes older contract constants through `v1.0` and states backward generation support. Their presence alone is not evidence that every configuration or operation for those releases has been tested by Bronzeward. [Pinned contract source](https://raw.githubusercontent.com/siderolabs/talos/v1.13.6/pkg/machinery/config/contract.go).

The owner's direction remains to mirror native `talosctl` behavior, without inventing extended compatibility or requiring historical renderer binaries. Document the tested operation/contract combinations rather than using release-support policy as a parser capability limit.

### Secret resolution, encoding and redaction

The assistant supports the following design direction for investigation, subject to the existing requirement to preserve upstream composition semantics:

1. Compose references symbolically where possible; resolve only references that survive into the effective configuration.
2. Resolve each selected secret version consistently within the compilation and record the effective dependency set used by the artifact.
3. Keep the source/provenance graph separate: it can legitimately mention an overridden fragment or reference that does not contribute to the final artifact.
4. If encoding modifiers are needed, define a closed set rather than expose arbitrary template functions. The initial set is still open.
5. Record sensitive locations at resolution and preserve that metadata through embedded serialization and final output processing. Use it for diff redaction; value matching alone is insufficient.

Resolving a now-overridden reference early does not inevitably make an artifact incorrect: a recorded superset of inputs can still be consistent provenance. It does introduce unnecessary access/dependencies and makes the effective dependency set less precise. The requirement should express the effective-set invariant rather than claim that every early-resolution implementation necessarily produces a mismatch.

The composition spike must determine whether reference nodes can pass through the selected upstream patch machinery and typed fields without changing merge behavior. “Resolve after merge” must not become a reason to implement a second generic YAML merge engine. Derived source-path mappings alone are also insufficient if later transformations move or expand sensitive content; the redaction metadata must follow the actual output locations.

For embedded values, known Talos PKI and marked secrets, the specification should name draft checks, materialization, upstream validation and safe rendering of errors/diffs separately. Recording sensitive paths after resolution does not itself prevent an earlier error or staging path from leaking a value.

## 18. Historical status and reconciliation map

The owner requested that this material be preserved as a design discussion and review, rather than treated as a completed specification. The archive retains the initial position, peer criticism, corrections and remaining differences. Original owner-confirmed directions in section 3 remain distinct from later reviewer/assistant proposals.

The full review file was not supplied, including its seven owner questions, six experiment definitions/estimates, complete finding bodies and overclaim list. This document does not reconstruct or adopt those unseen contents. No new owner approval of the section 17 implementation details is implied by archiving the conversation.

The passages below identify reconciliation work in the main architecture concept at the reviewed HEAD. Listing them does not modify that document:

| Repository passage | Discussion/review consequence |
|---|---|
| `Talos_Configuration_and_Machine_Management_Design.md:353` — raw TEXT and parsed JSONB | Specify that known/marked secrets must be extracted before ordinary plaintext persistence; protect unresolved staging and failure paths. |
| Same document, `:357` — immutable Talos secret generations | Extend the dependency/retention discipline to all referenced secret classes and distinguish logical names from physical provider layout. |
| Same document, `:381` — garbage collection | Check dependencies and selected retention/recovery windows before application-controlled cleanup; distinguish external administrator actions. |
| Same document, `:717` — adoption of observed configuration into a draft | Make extraction/protected staging a precondition of the ordinary draft write, not a later cleanup step. |
| Same document, `:275` — renderer/contract pinning | Qualify the multi-binary implication and separate native generation compatibility, release support and retained artifact provenance. |
| Same document, `:695` — plans and apply modes | Define draft/materialized validation stages, exact apply-mode completion and the approved-dispatch boundary. |
| Same document, `:725` — durable operations and locks | Specify ownership transitions and per-operation/mode recovery, including stale A-after-B application and unresolved old executors. |
| Same document, `:753` — decrypt only published artifact contexts | State application-enforced approval and the trusted controller boundary rather than imply OpenBao independently knows approval state. |
| Same document, `:797` — consistent recovery set | Incorporate explicit recovery mode, scope-specific dependencies and user-operated restoration. |
| Same document, `:878` — PostgreSQL baseline | Reflect the SQLite investigation and external database-operation boundary without promising untested backend support. |
| Same document, `:960` — first implementation phase | Align with the selected configuration-control milestone and investigations, rather than silently treating broad lifecycle scope as the first delivery. |

Remaining work is to reconcile the main design deliberately, specify the unresolved contracts and perform the agreed investigations. Neither archival nor reviewer agreement establishes passing experiments, implemented safety guarantees, a supported backend matrix or production readiness.


## 19. Reviewer round 3: withdrawals and reconciliation additions

The owner supplied a further response reporting six withdrawn claims, seven carried corrections and four open design choices. These counts describe the reviewer's own accounting; the full finding inventory was not supplied. The substantive points were:

- **Duplicate ApplyConfig:** the reviewer withdrew universal duplicate safety. A stale executor can apply A after B, and reboot/try modes can repeat side effects. Classification must consider operation, mode, assignment revision and intervening state.
- **Logical references:** naming does not imply mutable-path storage. The missing requirement is retention across all secret classes. The reviewer added that a provider must let a least-privilege checker assess version retention without reading the secret.
- **Resolution timing:** downgraded from a required ordering to an open design choice. Early resolution with a recorded provenance superset may be legitimate. The reference experiment must include a non-string target: upstream typed unmarshal can reject a placeholder before validation. Reimplementing composition with a custom merge engine remains excluded.
- **Renderer compatibility:** the contract-version inequality is necessary, not sufficient. The existing renderer research's legacy Upgrade/LifecycleClient transition at v1.13 shows why the experiment must cover operation/RPC combinations as well as generated output.
- **Reconciliation map:** the earlier eleven entries were accepted, with two omissions: the original main document at `:1028` makes the full reuse loop the acceptance criterion; `:1312` mandates multi-party approval for RotateSecrets inside a coordination column.
- **Reference hygiene:** if full finding bodies later become available, restate the relevant substance in repository-accessible prose. Do not cite an inaccessible reviewer-local plan file as evidence.

## 20. Assistant response to round 3

The assistant accepted these dispositions and narrowed the provider criterion: metadata-only retention evidence must not imply readability, decryption success or authorization under the eventual compiler/executor identity. Failed or unavailable checks must remain unknown rather than be treated as missing versions.

The two reconciliation additions were accepted with these treatments:

| Original passage at reviewed HEAD | Treatment |
|---|---|
| `Talos_Configuration_and_Machine_Management_Design.md:1028` | Preserve the available → node → available → node loop, but label it the later lifecycle milestone. The first milestone remains configuration control on an existing cluster. |
| Same document, `:1312` | Separate operation coordination from authorization policy. Keep PKI workflow coordination while leaving self-approval/multi-party rules open. |

These extend the section 18 map to thirteen passages. The main document remained unchanged during this exchange; the owner had asked to finish discussion with the reviewer first.

## 21. Final reviewer response and convergence

The reviewer's final response accepted the preceding distinctions and proposed a four-state metadata-only dependency check:

| Proposed state | Reviewer's intended distinction |
|---|---|
| retained | Present and usable by an authorized identity. |
| blocked | Present but unreadable for a reversible reason, such as KV soft deletion or a Transit decryption-version floor. |
| lost | Permanently unavailable, for example KV destruction/pruning or Transit trim. |
| unknown | The check could not be performed. |

The reviewer required that unknown never be reported as lost or count as a pass, and that unknown beyond a defined interval be independently alertable. This prevents an unreachable provider producing silent monitoring that appears healthy. The check establishes retention only; actual readability, authorization and decryption belong to the identity compiling or executing **at the point of use**. A green monitoring result does not authorize dispatch.

The reviewer argued that the criterion is provider-independent: OpenBao exposes metadata separately from secret data, while a proposed local encrypted store can expose file/row presence without decrypting ciphertext. The reviewer accepted both additional reconciliation treatments in section 20 and reported nothing else outstanding.

The assistant accepted convergence with two precision changes when carrying the contract into the main design:

- Define **retained** as metadata-established presence with no known retention block. “Usable by an authorized identity” would otherwise suggest a decryption or permission guarantee that the metadata-only check expressly does not establish. Local file presence alone cannot establish ciphertext integrity or private-key availability.
- Require evidence of irreversible removal before reporting **lost**. An absent listing, denied request or unreachable provider can be insufficient evidence. “Lost” describes the current provider; independent backup restoration is a separate possibility. Examples of irreversible loss are not exhaustive: whole-key destruction also matters, not only Transit trim.

Provider-specific interpretation remains necessary even though the capability criterion is common. Scheduled deletion timestamps, archived key versions and metadata permissions must be understood before assigning a state. The timeout/alert interval remains a specification choice, not an invented default.

Primary sources consulted for these qualifications were the [OpenBao KV v2 API](https://openbao.org/docs/api/secret/kv/kv-v2/) and [Transit API](https://openbao.org/docs/api/secret/transit/). They establish provider behavior; they do not prove a Bronzeward implementation or the suitability of the proposed local provider.

Review convergence settles the direction and reconciliation wording. It does not select reference syntax, physical secret layout, SQL abstraction, encryption/key-custody design, approval policy, a stale-executor mechanism or a supported compatibility matrix. It also does not adopt the unseen full review's experiment estimates or owner-question list.

## 22. Authorized documentation reconciliation

After convergence, the assistant proceeded with the owner's previously stated sequence:

1. Extend this historical record with the remaining exchanges and final qualifications, preserving the initial report and earlier responses as historical snapshots.
2. Reconcile the [main design](../Talos_Configuration_and_Machine_Management_Design.md) as revision 0.5, dated 6 September 2026.
3. Replace the old 0.2-to-0.3 review with the [0.4-to-0.5 transition review](../Design_Review_v0.4_to_v0.5.md), covering the actual baseline and new design.

The main revision carries pre-persistence extraction into ingestion and drift adoption, extends retention to all secret and encryption dependencies, separates metadata checks from use-time authorization, narrows renderer compatibility, specifies evidence-based operation recovery and explicit post-restoration recovery mode, and distinguishes the configuration-control milestone from later lifecycle delivery. It also records the SQLite/local encryption investigations and the external database/vault operations boundary.

The six planned experiments in the revised design are a new, visible synthesis of the discussed requirements, not a reconstruction of the reviewer's unavailable full experiment definitions or estimates. Neither tests nor prototypes were executed as part of this documentation reconciliation. The revised design remains a working concept whose unresolved mechanisms require evidence before an implementation specification can claim their guarantees.

## 23. Final review of the v0.5 reconciliation

The owner supplied the reviewer's final assessment of the revised documents. The reviewer found the reconciliation accurate and thorough after checking C1, C2, R1–R6, the six investigation questions and both additional reconciliation-map passages. In particular, extraction before persistence was carried into initial adoption (§9.1), as well as the storage boundary (§7.1) and drift adoption (§12.4). The reviewer found no overclaims or silently closed design choices.

The reviewer also accepted the reconciliation coverage table, the separately dated source additions and the Mermaid diagrams, including figure 3's explicit reservation about reference-resolution ordering. A suspected missing G3 reference was withdrawn after locating its definition; no reference change was needed.

The sole remaining suggestion was cosmetic: the main design's §13.1 trust-boundary table called the execution role “Reconciler”, while §13.2 called it “Executor”. The assistant standardized that role label to “Executor”. The reviewer considered the documents ready to commit even without this terminology correction.

This verdict approves the documentation reconciliation, not an implementation or experimental result. The open choices and planned evidence remain as recorded in revision 0.5.
