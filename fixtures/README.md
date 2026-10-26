# Investigation fixtures

A disposable environment for the feasibility experiments (design §18.1): a two-node Talos cluster,
PostgreSQL, OpenBao, and pinned `age`/`sops` CLIs, all created from nothing and removed completely.
Rationale, alternatives and recorded results are in the
[fixture investigation report](../docs/design/research/20260919-investigation-fixtures.md).

Nothing here selects the PoC database, provider or deployment profile. Every candidate is present
so the experiments can compare them.

## Prerequisites

Linux on x86-64, Docker Engine with the compose v2 plugin, and `bash`, `curl`, `jq`, `git`, `tar`,
`find`, `awk`, `cmp` and GNU coreutils (`sha256sum`, `timeout`, `install`, `stat`, `mktemp`), plus Go at the
version `mise.toml` pins (`up` refuses another; run it under `mise exec --`): `up` builds the server image from this checkout's commit, exported with `git archive`. Each command checks for what
it needs before doing anything. The user must be able to run `docker` without `sudo`. About 4 GiB
of free memory and 1.5 GiB of image downloads on first run. The host ports `fixtures/versions.env`
publishes on 127.0.0.1 (25432, 28200, 28080-28082) must be free, and stay below the kernel's ephemeral
range (`/proc/sys/net/ipv4/ip_local_port_range`): scenarios restart these services, and a port inside
that range can be taken by any outbound connection while its service is stopped. No other tool is taken from the host: `talosctl`, `sops`
and `age` are downloaded into `fixtures/.cache/` and refused on a checksum mismatch; since the cache is
writable, each is checked against the manifest's digest again before use (`talosctl` before its first
run in any command, bounded calls included, all four by `evidence` before it runs any and by `up` once
they are installed), so a binary replaced or damaged since the download is refused, not run as the
pinned one.

## Commands

| Command | Does |
|---|---|
| `fixtures/bin/up` | Generates synthetic secrets, starts PostgreSQL and OpenBao, initializes and unseals OpenBao, creates the Talos cluster, then asks both services again with this run's credentials before it reports the fixture up. It also builds the server image (the `bronzeward` and `oidc` binaries, and `bronzeward-interrupt`, the same command built with the `fixtureinterrupt` tag; imported for this run and never pulled) with the Go version `mise.toml` pins, refusing a `GOEXPERIMENT` or `GOWORK` setting, and refuses an image whose `bronzeward` carries the interruption seam or whose `bronzeward-interrupt` lacks it. It starts the OIDC issuer and creates instances A, B and C without starting them, since S0 begins from an empty schema (see [Instance C](#instance-c-and-the-interruption-seam)). Takes about two minutes once images are cached. |
| `fixtures/bin/bw <command> [flags]` | Runs one `bronzeward` command (`migrate`, `token issue`, `token list`, ...) from the server image, on the Compose network, with instance A's configuration; `-config` is added. `serve` is refused: the instances run it. |
| `fixtures/bin/seed` | Issues the automation identity of the acceptance plan (§2): `ci`, roles `author` and `publisher`, `h-all` responsible. The token goes to `.state/automation-token` (0600) and joins the leak-scan patterns. Run it once, after `bw migrate`. A seed interrupted after the token was issued leaves it in `automation-token.partial`, which the leak-scan patterns include, so `evidence` refuses the run and `seed` refuses to issue another: run `down`, then `up`. |
| `fixtures/scenarios/s0 run\|walk` | Acceptance plan S0 end to end, each mode on its own fresh `up`. `run`: step 1's suites (`go-db` needs its database and OpenBao: `mise run dev-db` and `mise run dev-bao`, see CONTRIBUTING.md), run in a fresh clone of the image commit, step 2's concurrent migrations, step 3's authentication refusals (and that they wrote no principal row), step 5's absent routes (dispatch, token issue and revocation, role grants) and the schema negative controls. `walk`: step 4's 37-row denied walk (`walk.tsv`: identity, roles held, route, status, and the qualifying roles and human-only flag the refusal named; each of the 19 human-only rows must be refused as human-only, not only for a role automation lacks) and the identity revocation it ends with, checked in the database (revoked row, `recovery-admin` act) and again after `deniedSubjects` is emptied and A restarted. A server image built from a checkout that differed from its commit (`up-build`, which `evidence` copies into the bundle) is a `FAIL`: S0 needs a clean checkout. Both modes check, before the scenario and again after `evidence` has captured and scanned its bundle, that the working tree is still that commit with no path differing, since the scenario's own scripts and `evidence` run from it; `run` checks again after step 1. Step 1 does not run in the working tree: `run` clones the checkout into `.state/s0-clone` with git's global and system configuration off, no template and none of the caller's `GIT_*` settings, checks out the commit `up-build` records, and runs `mise run verify`, `mise run go-db` and the advisory-lock control there. The suites get only the caller's variables they need: `PATH`, `HOME`, `TMPDIR`, the XDG and mise data, cache and state directories, the locale, Go's settings, `BW_TEST_*`, and the proxy and CA settings. Every other variable is dropped (`GIT_*`, `MISE_*`, `BASH_ENV`, exported functions, ...), so none can select the repository, the configuration, the shell or the tasks they run. The values kept reach the suites through a pipe, never a command line. For every git the suites run too, git's global and system configuration, the default attributes and excludes files under `$HOME`, hooks and templates are off, so a repository a suite creates (`upstream-sync`'s) gets no hook. mise reads neither its global or system configuration nor a `mise.toml` above the clone. Other tools' own configuration files under `$HOME` (pip's, for one) are still read. `run` checks that `verify`'s and `go-db`'s tasks, with those they depend on, are defined in the clone and run there. The clone copies the objects and refs and the checkout's `origin/main`, which `commit-lint` compares against; no index, skip bit, untracked or ignored file, `.git/info` file, attribute or filter of the checkout's reaches it, and `upstream-sync` fetches go-kure/.github into it afresh. Each step log names the clone's HEAD, `step1-clone.log` records the clone, and the clone is removed after step 1 (`down` removes `.state` in any case). Without the clone the suites do not run, and step 1 is a `FAIL`. `run` also checks, in the clone, that `go env` has no `GOFLAGS`, `GOEXPERIMENT` or `GOWORK`, which could select no tests or change what they build, and that its Go version is the one `up-build` records, which `GOTOOLCHAIN` could switch. `BW_TEST_PG_DSN` reaches the advisory-lock control through the environment only, so a password in it is in no retained file. So is a bundle with `unavailable.txt`: a surface never captured was never scanned. Each request and its response, the token withheld, is kept as `http/<n>.txt`. Each check is a `PASS`, `FAIL` or `SKIP` line in `.state/evidence/s0-<mode>-<utc>/s0.tsv`, and each command a check, a suite, a migration or a start ran is a line of `commands.tsv` with its exit status, tokens written as `<token>`; the directory is then scanned by `evidence`, and a hit anywhere in the bundle other than its positive control is a `FAIL`. Every token the scenario mints or issues is searched for in the directory and the bundle too. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s1 run` | Acceptance plan S1, adoption of the existing cluster, on a fresh `up` (the Go-test rows need `mise run dev-db` and `mise run dev-bao`, as S0's step 1 does). The preconditions (S0's `migrate` and `seed`, A, B and C started, the pre-state bundle with each node's configuration digest and machine-configuration resource version); steps 1 to 4: the cluster and both machines recorded by the Talos cluster ID, node IDs and endpoints read from the nodes, the os:admin talosconfig written to the cluster's access path over stdin, an import draft, and each node ingested from the machine, the worker's planted file secret marked; the pass criteria the database and OpenBao answer (`!bwref` references, create-only generations listed under their claim by the metadata identity, each baseline's digest equal to the pre-state, the executor refused the baseline); the refusal set (a mark that addresses nothing, an unparseable document, a token also an unmarked label, automation on the human-only route), each refused before any provider write; every fixture-producible Talos access refusal but another node ID (secret absent, not a talosconfig, malformed, OpenBao sealed, the node silent behind a link split, another cluster ID) and a control import with every fault taken away; takeover and lease rows on instance C (a stalled run's claim kept live by its heartbeat, then taken over by B while OpenBao is partitioned (`netsplit openbao`), then by A, which commits once; C's late writes refused); and the interruption matrix (C killed at each of nine ingestion steps, transient and encrypted staging: the claim abandoned, by the sweep or by a takeover with nothing to decrypt, the operation failed `ingestion-abandoned`, the draft's ETag unchanged, every generation made before the kill still listed). Each success, refusal and interruption point, the last in each staging mode, gets its own bundle, which must be complete and scan clean; an interruption's is captured while C is still stopped, so its layer is what the kill left. Rows only a Go test can reach, and every test the acceptance plan's §7.1 names for ginsys/bronzeward#22 (the takeover, Talos access and inventory identity-key tests), run first, in a clone of the image commit, as S0's step 1 does; `TestLiveRoleProbe` among them reads the fixture's control plane, the one node that generates client configurations, with `bin/up`'s talosconfig, so they run before the apid logs are read and its requests stay outside step 5's interval. Step 5 takes the post-state and checks that neither node's digest nor resource version changed and that neither node's apid log records a Talos mutation request past the entries read before the run, with a dry-run apply as the log's positive control; a log that no longer begins with those entries, its buffer having dropped lines, fails the row. A mutation request is any method `fixtures/talos-api-methods.tsv` classifies as mutating, matched by its whole name; that file lists every method of the pinned Talos API services, which `TestTalosAPIMethods` checks against the services and methods the modules generate and the read-only guard. Two precondition rows check that the matcher counts a log line of each mutating method and of no read method, and that a failing extraction or count gives no count rather than 0. Results go to `.state/evidence/s1-run-<utc>/s1.tsv`; the directory and the final bundle are scanned, and every token minted or created is searched for, as in S0. The issuer's tokens live five minutes and the run is longer, so the author token is minted again after each capture. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s1 node-id` | S1's Talos access refusal for another node ID, on a fresh `up` of its own: after S1's preconditions and import draft, a machine is recorded with a node ID no node has at the control plane's endpoint (`201`), and its import is abandoned with `409 machine-identity-mismatch`, cause `node-id`, nothing kept, in a bundle of its own; as in `s1 run`, between a pre-state and a post-state bundle neither node's configuration digest or resource version changes and neither apid log records a mutation, each with its positive control. It stays out of the integrated run because that machine stays recorded, and its scope, which no restoration observation can make `ready`, would keep S7's recovery mode from ending. Results go to `.state/evidence/s1-node-id-<utc>/s1.tsv`; the final scan and token search are `s1 run`'s. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s2 run` | Acceptance plan S2's draft-update ingestion and publication matrices (its negative controls, compilation §15), after `s1 run` and `s3 handover` on the same `up` (S2's precondition: S1's cluster and worker, the worker adopted). A new draft; a fragment update with a marked label holding the canary, which succeeds (200, the ETag moved, the claim released, its one generation listed); two refused (an unparseable document, `422 parse`; the marked value also an unmarked label, `422 guard-value` naming the label), each leaving the claim abandoned, no generation and the ETag unchanged; and the update sent to C, which is killed after each of the eight draft-update steps (`claim` to `construct`; the baseline is for imports only): the sweep abandons the transient claim once its lease lapses, every generation made before the kill is still listed, the draft's ETag is unchanged and no idempotency record is kept. Then the publications, each of a new draft into which the worker is imported with its file content marked, each sent to C as `h-publisher` with A and B paused so that C's publish worker claims it: one succeeds (claimed at generation 1, one release, the draft published); one refuses after the worker is paused (`503 dependency-unavailable`, retained fresh observation, unchanged Desired), then resumes the worker before capture; one the compilation refuses (a cluster fragment overriding the worker's machine token, which the worker's assignment selects: the operation `failed` naming `base-override` and the worker, no release, the draft open); and one killed at each of the seven publication points (`publish-claim` to `publish-commit`): the operation stays `running` at generation 1 with no release; its bundle is captured, the state read again unchanged, then A and B resume, C is started again, and the job is claimed at generation 2, committed no earlier than the stopped run's lease deadline, and succeeds with one release. Then S2's steps 1 to 7 on the `Applied` state S3's handover left: the worker's `Applied` is the release its adoption recorded (a completed adopt operation, source `adoption`, baseline revision 1, its import base's configuration digest, the bytes read from the node), checked before the matrices and again after them, a pre-state bundle scanned clean whose worker configuration digest is that adopted one, a database and OpenBao snapshot; as `h-author` a draft importing nothing, a fragment whose one node label is a `!bwref` to the reference the `Applied` release records at the worker's file content, a profile listing it and the worker's assignment selecting the profile, each `200` with the ETag moved, while the automation identity's PUT under a replaced ETag gets `412` and writes nothing (no revision, act, idempotency record or claim); as `h-publisher` the publication on A (all workers running) succeeds with one release that becomes `Desired`, `Applied` unchanged and no operation but `publish` since the precondition; the review shows the label redacted as the reference, its provenance naming the fragment revision; the release names the fragment, profile and assignment revisions and exactly the pinned renderer (the node's contract minor, the image's machinery version and `go.sum` checksum, the fixture's Kubernetes version); its stored artifact is Transit ciphertext whose stored digests are its ciphertext's and, decrypted by the OpenBao administrator, its configuration's, its dependency records exact versions all `retained`, the label's reference with both its reproduction and its effective record and still its import base occurrence at the worker's mark, and its rows refuse `UPDATE` and `DELETE`; a post-state bundle shows both nodes' machine-configuration resource versions unchanged. Step 6: the monitors of A, B and C classify every dependency status of the release `retained`, each observed since it was published, and `GET /dependencies` serves them so, none stale, each with its identity, class, reason and times as recorded; then its negative controls on the label reference's KV version, each against the fixture's OpenBao through `inject`: soft-deleted, it is `blocked` (`soft-deleted`) with every other dependency `retained`, one `blocked` alert naming the release (after at most a `regression` of an undecidable deletion time, when a pass falls in the deletion's own second), served so by `GET /dependencies/{id}` and its alerts; destroyed, it is `lost` (`destroyed`) with one `lost` alert; OpenBao partitioned (`netsplit`), sealed and paused, every dependency observed since is `unknown` (`unreachable`, `unavailable`, `unreachable`) with one `regression` alert for each but the lost one, `GET /dependencies` serving each `stale` exactly when its recorded observation is over three intervals old (an unreachable request takes its whole 10 s, so a pass over more than about 18 outlasts three intervals), and once OpenBao is back the version is `lost` again with one `lost` alert. The alerts are checked exactly, every alert since the phase began; after each control a new unchanged draft pinning the version through the label is published as `h-publisher` and refused (`422 validation-failed` naming the version and its class while it is `blocked` or `lost`; `503 dependency-unavailable` naming the failed fresh publication observation during each outage: the executor cannot read Talos access, so it refuses before pinned dependency reads; the observation retains no access version or node values), the operation `failed` with no release. Step 7 restores both snapshots while A, B and C keep running (paused across both restores: a pool reconnecting between the database swap's session kill and its rename would refuse it, and a monitor pass between the restores would record the destroyed version or the restoring OpenBao into the restored database), and the automation identity repeats steps 1 to 3, its acts recorded under `author` then `publisher`. The negative controls no fixture request reaches (the `FOR SHARE` race, the compiler's refusal of a literal copy of a resolved value, the fidelity check's injected change, the provider classifications, replay and key reuse, a reply lost after `COMMIT`, a lapsed job's claim and its first owner fenced, the release tables' immutability) run as their Go tests in a clone of the image commit, as S1's do, each a row of its own (they need `mise run dev-db`). The provider-state controls (DM §10.1 items 2 and 3: a KV version soft-deleted, undeleted, destroyed and pruned past `max_versions`; its metadata deleted after and without `retained`; a Transit version below the decryption floor, the floor lowered, then trimmed; a Transit key deleted) run as `TestLiveProviderStates` against the fixture's own OpenBao and go-db's PostgreSQL (`mise run dev-db`), each subtest a row of its own. The machinery gate (compilation §10.1, S2's precondition) runs first, against the same fixture, and S2 stops there if any of its rows fails: `experiments/c10-machinery-gate/run/all` on this checkout, with `GOFLAGS`, `GOEXPERIMENT` and `GOWORK` unset and the image's Go version, held before and after to the image commit with no path differing or skipped and every tracked file's bytes, hashed unfiltered, the commit's, its output outside the checkout and removed (on interruption too; a failed removal fails the row) once its result tables (`c10/`, each present and not empty) and `run/all`'s own output (`c10-gate.log`) are kept, each only if it scans clean against the run's own leak-refusal patterns as `run/collect-evidence` does (the go test log is not kept), every SR and SP row as expected and every control held. Each success, refusal and kill point gets its own bundle, which must be complete and scan clean; a kill's is captured while C is still stopped and A and B still paused, so no takeover commits during the capture. A database dump taken after both matrices is scanned in the final bundle, with a control that it holds an interrupted claim's row, and every token minted is searched for, as in S1. Results go to `.state/evidence/s2-run-<utc>/s2.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s3 handover` | Acceptance plan S3's handover, after `s1 run` on the same `up` (S1's open import draft, both machines imported into it). As `h-publisher` the draft is published as one release covering both machines; then, for each machine, an adopt plan is created as `h-publisher` and approved as `h-approver`. Each plan commits to a completed adopt operation with one adoption record; each machine's `Desired` and `Applied` are that release, `Applied`'s source is `adoption`, its baseline revision 1 and its digest the import base's, which is the configuration digest the pre-state read from the node; the adoption and the `Applied` change are each on the machine's timeline once; no operation but `adopt` follows the publication; a second adopt plan for an adopted machine is refused `409`. A post-state bundle shows both nodes' configuration digests and machine-configuration resource versions the pre-state's, and neither node's apid log records a Talos mutation request past the entries read before the plans (control: a dry-run apply there is found past them, and the matcher counts each mutating method once and fails closed). The state it leaves is S2's precondition. Results go to `.state/evidence/s3-handover-<utc>/s3.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s3 handover-controls` | The controls that refuse or hold an adoption before it commits, on a fresh `up` and `s1 run` of their own (not after `handover`, which adopts both machines). The import draft is published; then, on the worker, a node label is changed out of band with no reboot, so its configuration digest is no longer the baseline's: its approved adopt plan is refused (comparison 4.4, one refusal entry, still recorded once three heartbeats later), the plan stays approved and the machine has no `Applied`, no adopt operation and no adoption record; its bundle is scanned clean and shows the worker's digest changed, the control plane's unchanged, and no Talos mutation of the system past the out-of-band change. Then, with A and B paused and C told to stall at the new `adoption` seam (after the plan's evidence observation is recorded, before the adoption), the control plane's adopt plan is created with `maxObservationAgeSeconds` of 10 and approved on C: the stalled observation is recorded, ages past what the plan binds, and the stall is released. The adoption is not recorded from it and nothing is refused (an observation that is no evidence is retried, not refused: C's log says so); after the lease it is recorded from a later observation, whose start entry is after the release, with no refusal entry. Each case's bundle is scanned for every fixture secret, a database dump holds an adoption record, and every token minted is searched for, as in S1. The post-commitment controls (an operation interrupted after commitment, a replay, recovery) are S4's. Results go to `.state/evidence/s3-handover-controls-<utc>/s3.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s3 run` | Acceptance plan S3's approval part, after `s1 run`, `s3 handover` and `s2 run` on the same `up`. Preconditions: the worker's `Desired` is S2's release and its `Applied` the handover's, the worker has no plan or operation but the handover's, and S2's release holds a revision the automation identity authored; then A's, B's and C's links to the worker are cut (`inject linksplit <A\|B\|C> worker`) around approvals, so the controller's evidence observation of any approved plan reads nothing and none commits. Step 1: after the controller's heartbeats (16 s) no `apply-config` plan or operation exists for the worker and the pre-state's digests are unchanged; the database and OpenBao are snapshotted (`s3-step4`). Steps 2 and 3: `h-publisher` creates the `apply-config` plan from the S2 release (`no-reboot`, the bound route, deadlines, maximum attempts, expiry, maximum observation age), `h-viewer` reads it with the diff withheld (the worker holds the import base's configuration), and `h-approver` approves it with no self-approval reason; the approval names the plan, approver, role and epoch in its response, its row, the timeline and `GET /approvals/{id}`; the database and OpenBao are snapshotted again (`s3-step3`). The controls that act before commitment, on that live state: automation, `h-recovery` and `h-viewer` approving (`403 forbidden`, naming the `approver` role, and for automation the humans-only route), a second approval by `h-approver` and by `h-all` (`409` naming the plan as `approved`), a plan from the superseded import release (`409` naming the S2 release), plans past their 8 s expiry, cancellations by the creator `h-publisher`, the creator automation identity, `h-approver` and `h-recovery` (the role recorded for each) and `h-publisher` cancelling the automation identity's plan (`403`), the approval revoked by `h-approver` and by `h-recovery`, and an unapproved plan with every dependency `retained` that 16 s of controller heartbeats dispatch nothing for, with no use-time check of it while step 3's approved plan gets a fresh one; a bundle is scanned after the approval and again after the controls. Step 4: both snapshots are restored with A, B and C paused, and three plans are prepared in release order, then approved after the worker links are cut again, each on its own release: the automation identity's plan approved by `h-all`, `h-all`'s own draft, fragment (listed in the profile the worker's assignment selects), publication and plan, and `h-author`'s unrelated draft published by `h-publisher` on a release that carries `h-all`'s fragment revision unchanged, approved by `h-all`. Each approval is marked self-approval with exactly the reasons PA §10.5 records for it, `owned-automation` among them since every release contains the automation identity's revision. For step 4, it verifies that the restore removed every apply-config plan and joins the worker links for publication observations. Each plan is created while its release is Desired and stays proposed; after all three are prepared, it cuts every link again before approving them in case order. After the approvals all three plans are rechecked `approved`, each by its own approval. The controls that need the commitment or attempt transaction are S4's: with the links cut, no operation follows. Neither node's digest or resource version changes and neither apid log records a mutation. The database is dumped (scanned in the bundle), then both `s3-step3` snapshots are restored with A, B and C paused, leaving step 3's plan `approved` as S4's starting state and none of the controls' or step 4's plans and releases. Results go to `.state/evidence/s3-run-<utc>/s3.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s4 run` | Acceptance plan S4's safe apply, after `s1 run`, `s3 handover`, `s2 run` and `s3 run` on the same `up`. Preconditions: step 3's plan is the only `apply-config` plan, the worker's, `approved`, `no-reboot`, with no operation and ten minutes or more before its expiry; its release is the worker's `Desired` and not its `Applied`; the three links to the worker are recorded cut; and the worker is served with `Desired`, `Applied` and `Observed` as three values. After a pre-state bundle and both apid log baselines, the links are joined again (`inject linkjoin <A\|B\|C> worker`) and the plan's operation must complete within 240 s. On the worker's timeline: the last use-time check before the commitment passed, with its access and artifact; the commitment names the operation and an evidence observation of the plan begun after that check; one attempt, number 1, after the commitment, on the plan's route and the commitment's observation, its response `accepted` after it; the attempt moving the operation to `sending`, then its state entries `sending` to `verifying` and `verifying` to `completed`, in that order before the `Applied` change; a `completion` observation begun after the response reads the release's configuration digest and is the one the result names; `Applied` is the release from source `operation`, with the artifact's digest and the baseline revision one more, and one `Applied` change entry. The worker's apid log records exactly one mutation, `ApplyConfiguration`; a post-state bundle shows the worker's digest the artifact's and its resource version moved, the control plane's both unchanged and its apid log without a mutation (control: a dry-run apply there); the worker is served with all three values the release; no live `apply-config` operation holds the worker's scope or the cluster's rollout slot. The operation, the timeline and the observation records are kept as served. A database dump is scanned in the final bundle, with a control that it holds the attempt's row, and every token minted is searched for, as in S1. The negative controls are `s4 controls` and `s4 restores`; the DS re-run is `s4 ds`. Results go to `.state/evidence/s4-run-<utc>/s4.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s4 controls` | Acceptance plan S4's negative controls, after `s3 run` and before `s4 run` on the same `up` (after `s4 run` the worker holds S2's configuration, which the restored plan's evidence would contradict). Same preconditions as `s4 run`, and step 3's plan at least 25 minutes from its expiry, then a pre-state bundle and both apid log baselines. A plan body naming the control plane's endpoint as the worker's route is refused at creation (`400 invalid-request`, no plan). Step 3's plan is cancelled, A and B are paused, C is restarted with its link still cut and then alone joined to the worker and told to stall once its evidence is recorded (`server/c/interrupt`, `dispatch-evidence stall`), and a plan with a 10 s maximum observation age is created and approved on C. With OpenBao sealed (`inject seal openbao`), then stopped (`inject kill openbao`), C's use-time checks of the plan fail with their access `provider-unavailable` and no operation exists; once started (`inject start openbao`, which unseals it) a check passes and the evidence is recorded. When that evidence is older than 10 s, C is told to stall after the commitment instead (`dispatch-committed stall`): no refusal entry, C's log says the evidence was older than the plan allows and retried after a lease, and the commitment names a later observation; its operation stays `committed` with no attempt. With A and B running again, a second approved plan for the worker gets no use-time check, commitment or refusal for 16 s, and a publication changing the worker's assignment fails `409 scope-busy` naming the worker and the holding operation. A frozen scope and the DS rows 011, 022 and DB row 018 are `SKIP` lines naming where they are checked. Neither node's digest or resource version changes and neither apid log records a mutation. The database is dumped (scanned, with a control that it holds the held operation's row); then, with every worker link cut again and A, B and C paused, both `s3-step3` snapshots are restored and C's stall is released: `s4 run`'s starting state. Results go to `.state/evidence/s4-controls-<utc>/s4.tsv`. Exits 1 on any `FAIL`. All publisher links are joined only while the first operation is verified to hold the scope, so the publication reaches its scope-busy check after fresh node reads. Every link is cut again before the approved-plan snapshot is restored. |
| `fixtures/scenarios/s4 restores` | Acceptance plan S4's commitment-level negative controls, after `s3 run` and before `s4 run` on the same `up` (before or after `s4 controls`). Same preconditions and start as `s4 controls`. A and B are paused; each case kills C with its link cut, restores both `s3-step4` snapshots (no `apply-config` plan, the worker's `Desired` the S2 release), tells C where to stall, starts it, and plans the S2 release as `h-publisher` on C before joining C's link. The assignment changed after the approval (draft, reassignment, publication), and a later release published before the approval: the commitment refused by comparison 2, one refusal entry, no operation. The plan cancelled, and `h-approver`'s identity revoked (`POST /identity-revocations`), once the operation is committed with no attempt: the attempt refused by comparison 1, then `committed` to `unresolved` and `unresolved` to `cancelled` on the timeline, no attempt. The identity revoked once the evidence is recorded: the commitment refused by comparison 1. With OpenBao paused (`inject pause openbao`) the plan approved, no operation for 16 s, and `h-approver`'s approval of a second plan under a token without `bw-approvers` (`oidc mint -without-group`) `403`; once unpaused the plan commits and its attempt is admitted, C stalled before sending (`dispatch-attempted stall`). DS row 003 is a `SKIP` line naming its Go check, run in `s4 ds`. Each case's bundle is captured, and must scan clean, before the next restore; a failed kill, restore, stall or start before a case stops the run. Neither node changes and neither apid log records a mutation; the database is dumped (scanned, with a control that it holds the last operation's row); C is killed with its link cut, started again on the `s3-step4` snapshots with its control file removed, and paused while both `s3-step3` snapshots are restored under it (a start puts a link split back only once the instance answers, and step 3's plan is approved), then A, B and C unpaused: `s4 run`'s starting state. Results go to `.state/evidence/s4-restores-<utc>/s4.tsv`. Exits 1 on any `FAIL`. In the assignment case, C stalls at `dispatch-evidence` while connected, allowing its separate publication worker to read both nodes before the commitment resumes. In the Desired case, publication runs while the earlier plan is still proposed, before its approval. |
| `fixtures/scenarios/s4 ds` | The dispatch-safety re-run S4's preconditions name, after `s4 run` on the same `up` (the Go-check rows need `mise run dev-db` and `mise run dev-bao`, as S0's step 1 does). Preconditions: S4's safe apply done (step 3's plan, the first `apply-config` plan, committed to an operation that completed), no `apply-config` operation holds the worker's scope or the cluster's rollout slot, the worker's `Desired` is its `Applied`, no link to the worker is recorded cut. A and B are paused and C dispatches alone. Each completing row first publishes a release changing the worker's configuration (a role fragment of its own node label, listed in the `workers` profile), then plans it as `h-publisher`; it passes when the operation completes, one `ApplyConfiguration` reaches the worker's apid log, and the row's bundle shows the worker's digest the artifact's and its resource version moved. 001: no fault. `h-approver` revokes the approval once the evidence is recorded (002, `dispatch-evidence stall`): the plan stored `revoked`, the commitment refused by comparison 1, no operation; once committed (004, `dispatch-committed stall`): the attempt refused by comparison 1, then `unresolved` and `cancelled`; once the attempt is recorded (005, `dispatch-attempted stall`): sent anyway, the revocation between the attempt and its response. 007: with C stalled after the commitment and A's link cut, `h-recovery` asks A to take the operation over (`POST /operations/{id}/takeovers`, generation 1); C's attempt is refused by comparison 7 and nothing is sent; A's link joined, A classifies a retry and completes with one attempt at generation 2. C killed once the evidence is recorded (018, `dispatch-evidence kill`): no operation, and C started again runs the plan; killed after the commitment (019, `dispatch-committed kill`): C started again takes the operation over (`controller-start`), retries and completes; each kill's bundle is captured and scanned while C is stopped. 012, 014, 015 (plans with a 30 s transport deadline, `dispatch-attempted stall`): the worker detached (`netsplit worker`), paused, or cut from C (`linksplit C worker`) while the attempt is sent, so no response accounts for it and the operation is `unresolved`; once healed, C's paced recovery observations read the worker, `h-recovery` takes the operation over on C and accounts for the attempt (`POST /operations/{id}/attempts/{attempt}/accountings`) with the first successful read after the transport deadline as its start and a recovery observation begun the settle floor (30 s) after the deadline, the takeover and that read as its end: `201`; nothing landed, so taker classifies a retry and completes with 2 attempts. 013, on 012's operation: the accounting refused `409` while C still holds the attempt, then after the takeover with an end begun inside the settle floor. 016 (`dispatch-sent stall`, `kill postgres`): C's log says the response is retried after a lease; PostgreSQL started, it is recorded and completes. 017 (`dispatch-responded stall`, `netsplit worker`): a completion observation reads no configuration and the operation stays `verifying`; joined, the next read completes it. 020 (`dispatch-sent kill`): the request landed with no response; C started again takes the operation over, `h-recovery` accounts for the attempt as above, and the observation completes it with 1 attempt and no response. 021 (`dispatch-observed kill`): C started again takes the verifying operation over and completes it. Rows 003 (the revocation waits for the commitment, then the executor's attempt is refused and cancelled, nothing sent), 006 (its control: without comparison 1 the executor sends the artifact to a Talos node after a revocation placed after the commitment), 008-011, 022 and 023 are their Go checks, run after the live rows in a clone of the image commit against go-db's PostgreSQL and OpenBao, and each fails if that run did; row 003 has no unlocked control (execution-recovery choice §10.31); row 022 (node validation) has no live plan, since compilation validates the artifact before publication: `TestLiveApply` sends the worker its own configuration and then one with an unknown machine type, refused as a rejection with the configuration unchanged, and `TestRecordResponse` moves a rejected operation to `rejected`. The control plane's apid log records no mutation; the database is dumped (scanned, with a control that it holds the last attempt's row). The table goes to `.state/evidence/s4-ds-<utc>/ds.tsv` (row, result, source, outcome), the checks to `s4.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s5 run` | Acceptance plan S5 steps 1 to 6, after `s4 run` on the same `up`, with or without `s4 ds` between them: step 4 takes S2's label fragment out of the `workers` profile, or, when it is the only fragment there (without `s4 ds`'s role fragments; an empty profile is refused `400`), points the worker's assignment back at S3's handover cluster fragment; the worker then selects no profile, so a later `s7` or `s8` release adds its role fragment to the worker's assignment instead. Preconditions: the worker's `Applied` is its `Desired` from source `operation`, no operation holds its scope, no drift record is open, it is not frozen, no link to it is recorded cut. Each instance's `driftInterval` is set to 5 s in `.state/server/<instance>/config.yaml` and A, B and C are killed and started; a drift observation of the worker opens no record. Step 1: the worker is patched with the pinned `talosctl` in no-reboot mode with a registry auth password, a new `BWSYNTH-` secret (a registries change applies immediately, a new file needs a reboot); its apid log records that one mutation. Step 2: within a minute one drift record opens, naming a drift observation, `Applied`'s digest and the patched one, its opening entry on the timeline; the scope is not frozen. The database and OpenBao are snapshotted as `s5-step3` (step 7's restore point). Step 3: `h-author` freezes the scope (`201`, the freeze entry under the author role); an approved revert plan on the frozen scope is passed over by the controller (30 s after its approval, which woke the controller, still approved, nothing observed for it, no operation) and cancelled. Step 4: `h-author` opens a draft. A drift adoption leaving S2's label unmarked is refused (`422` `guard-value` naming the label, the claim abandoned, no generation, the draft's ETag unchanged, the record open). `h-author` then ingests the worker as a drift adoption, marking S1's file content, S2's node label of it and the password (the ingestion stored and served as `drift-adoption`, one generation per reference of its revision, the marks' and the Talos schema secrets', its bundle scanned). After it, one at a time, a drift adoption sent to C, the interruption build, exits it at each of compilation §2.3's steps 0 to 8, and once its lease lapses the sweep abandons the claim and fails the operation, every generation created before the kill still listed, the ETag unchanged; C is started again after each; each outcome's bundle is scanned. `h-author` takes S2's label fragment out of the `workers` profile, the adopted import base holding the label as a reference (a fragment overriding one refuses publication); `h-publisher` publishes it, the release's import base holding the three marks as references to create-only generations of the drift adoption, each holding as a string the value the baseline holds at that path (the password's is step 1's), and its baseline decrypting, by the administrator, to the patched digest; `h-publisher` creates an adopt plan, which binds the record; `h-approver` approves; the adoption commits under the freeze: `Applied` the release from source `adoption`, its digest the import base's and the patched one, the baseline revision one more, the record closed `adopted`, still frozen. The worker's digest and resource version are the patched state's and its apid log records no mutation past the patch; the control plane's are the pre-state's, with none. Step 5: whether the release's artifact digest equals the baseline's is written to `step5-measurement.txt`, either way a `PASS`. Step 6: the worker is patched again with a registry mirror, nothing secret, its apid log recording that one mutation; within a minute a new record opens; `h-publisher` creates a revert plan, which binds it; `h-approver` approves it, the approval answered and on the timeline as approving an unseen overwrite; an unfreeze by `h-publisher` is refused `403`; `h-approver` unfreezes (`201`, the unfreeze entry under the approver role); the revert completes as S4's apply does, `Applied` the adopted release from source `operation` with its artifact's digest, the record closed `reverted`; the worker's digest is the artifact's, its resource version moved, its apid log recording one mutation past the second patch, the control plane's none. The database is dumped (scanned, with a control that it holds the adopt operation's row), the password is searched for with the run's tokens, and the interval is set back to 168 h with A, B and C restarted. The checks go to `.state/evidence/s5-run-<utc>/s5.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s5 roles` | Acceptance plan S5 step 7, after `s5 run`, outside the integrated run. A, B and C are paused, the `s5-step3` snapshots restored and the instances unpaused: step 2's record open, the scope not frozen. `h-publisher`, `h-approver`, `h-recovery` and the automation identity each freeze the scope (`201`, the freeze entry under the publisher, approver, recovery-admin and author role, the automation identity's first qualifying one), `h-approver` unfreezing it after each (`201`, under the approver role). The checks go to `.state/evidence/s5-roles-<utc>/s5.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s6 run` | Acceptance plan S6.1, after `s4 run` and `s4 ds` on the same `up` (S4's preconditions hold: no operation holds the worker's scope, `Desired` is `Applied`, no link recorded cut). S6's instance A is C, the one interruption build, and its B is B; the fixture's A is paused throughout and B is stopped at the start. Each point of ER §5.3 runs on a release of its own, as `s4 ds` does: C is killed at it (018 `dispatch-evidence`, 019 `dispatch-committed`, 020 `dispatch-sent` with a 30 s transport deadline, 021 `dispatch-observed`), the state the table names is checked and bundled, and B is started: 018's plan is committed after C's kill and runs on B; B takes 019's, 020's and 021's operations over at its start (`controller-start`, generation 2) and completes them, 020's only after `h-recovery` accounts for the landed attempt by decision past the settle floor, 021's on a new completion observation of B's. B is then stopped and C started for the next. The stale owner: C stalls after the commitment, B is started with its link to the worker cut and takes the operation over, C released is refused by comparison 7 and sends nothing, and B, its link joined, retries and completes. Steps 4 to 6: the worker detached (`netsplit worker`) while C's attempt is in flight, so the response is lost; B started takes the operation over, the worker is joined, a newer plan approved meanwhile waits (no use-time check, commitment or refusal names it) and its approval is revoked; `h-approver`'s accounting is refused `403`, `h-recovery`'s is accepted, and nothing having landed the retry completes with 2 attempts. Step 7: C killed after the commitment, B started with its link cut takes the operation over and cannot classify it; `h-recovery` resolves it `cancelled` (`POST /operations/{id}/resolutions`), the entry naming `h-recovery`, `recovery-admin`, the reason and the evidence relied on, and the scope is released. Every start of B whose link is recorded cut runs with the worker detached, so that no route reaches the worker before the split is put back (ginsys/bronzeward#178). After steps 4 to 6, the worker's timeline and observations, the send operation's events and the operation, each as `h-viewer` is served them (every page), are kept as `s6-send-timeline.json`, `s6-send-observations.json`, `s6-send-events.json` and `s6-send-operation.json`: S8 step 2 answers its questions for S6.1 from them (`S8_S6_KEPT`). The settle-floor refusal, the fence and scope-index controls and comparison 4 on a newer plan are `s4 ds` rows 013, 008, 011 and 010, read from that run's `ds.tsv`. The run ends with A, B and C running and no link cut. The table goes to `.state/evidence/s6-run-<utc>/s6-points.tsv` (row, result, C's kill time, operation, state, classifications, scope, outcome), the checks to `s6.tsv`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s7 run` | Acceptance plan S7's nominal run, its preconditions and steps 1-3 and 5-11, after `s5 run` or `s5 pass` on the same `up` (the integrated run runs `s5 pass`), or directly after `s4 run` (S6 runs outside the integrated run). S7's A is C, as S6's is; the fixture's A and B are stopped at the start and A stays stopped. Before *T*: C commits a worker plan and stalls before its attempt, a pre-entry plan is approved and waits on the held scope, and an import of the worker on C pauses for review. At *T*: `db-snapshot s7-t`. After *T*: B, its control file `server/b/interrupt` naming `dispatch-sent`, takes the operation over at its start, retries it and is held after its request is sent, so the request lands and the scope stays held; a second plan is approved, the pre-entry plan's approval revoked, `h-all` listed in B's `deniedSubjects` and then revoked, and `bao-snapshot s7-provider` taken. Step 1 kills B and C, checks both stopped, records the time and restores both backups; the restored state is checked against *T* (the epoch, the operation committed with no attempt, the pre-entry approval, the second plan and `h-all`'s revocation gone, the claim paused). Step 2 starts B-recovery: a read and a plan answer `409 recovery-mode-active`, `h-all` `403 identity-revoked` from `deniedSubjects` alone, and no executor runs. Step 3: `h-recovery` records the entry naming both backups by name and digest, each with the time it was taken; every scope is pre-restore unaccounted, the operation `unresolved` at generation 2 in the new epoch, the claim abandoned before its absolute expiry; through B-recovery a draft changing the worker's assignment is published and its job stays `queued`, and a second draft is opened. Step 5: the worker's digest is the held release's artifact. Step 6, through B-recovery: `h-all`'s revocation re-recorded, the seed automation token (`401` since the restore) reissued with `bin/bw token rotate` under `h-recovery`, the operation taken over again to generation 3 with the new epoch as its owner epoch, and an import into the second draft started under its kept ETag, its claim in the new epoch. Step 7: once each scope has a first read after the stop plus the maximum transport deadline and a recovery read a settle floor later, one decision accounts both (the control plane `blocked`, the worker `unresolved`); the operation is reclassified `cancelled` with no attempt; the control plane is paused; the worker is marked `ready`, its observation opening the landing's drift record, and the control plane `blocked` on its observation. Step 8 releases the worker. Step 9 first unpauses the control plane for publication observations, checking that its scope stays blocked and unreleased. B-recovery is killed and B started without the flag; its job worker runs step 3's publication, and a revert of the new `Desired` binding the drift record, approved in the new epoch as an unseen overwrite, runs as S4 and closes the record `reverted`. The control-plane scope is checked blocked and unreleased before and after publication and after the worker completes. Step 10 marks the control plane `ready` and releases it. Step 11 exits recovery mode; every scope is `normal`. The worker's and the control plane's served timelines (every page) and `GET /recovery` are kept as `timeline-w.json`, `timeline-cp.json` and `recovery.json`, and the epoch `h-viewer` is served before *T* as `epoch-before-t`, copied to `.state/s7-epoch-before-t` for S8. The run leaves recovery mode off and B running; the reissued automation token is the run's own, kept nowhere, so `.state/automation-token` holds the seed token the restore made unusable and a scenario that needs it after S7 needs a fresh `up`. The drafts, the job, the operation and the epoch go to `.state/evidence/s7-run-<utc>/s7-state.tsv`, the checks to `s7.tsv`. Step 4 and the controls about A are the closing run's. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s7 closing` | Acceptance plan S7's closing run, with a missed stale instance, after `s4 run` on a fresh `up`, outside the integrated run. As `s7 run`, except: step 1 pauses C instead of killing it (C's control file is removed, so C proceeds once unpaused). After step 3, a second entry under another key answers `409 conflict`. Step 4 unpauses C: within 60 s C logs that its executor and its publish worker find the epoch superseded; the held operation stays `unresolved` under B at generation 2 with no attempt from C, the worker is not mutated, the publish job stays `queued` and never claimed, no commitment entry names C, and the pre-entry plan waits; through C, a takeover and an ingestion start under the second draft's ETag answer `503 epoch-superseded`, and a plan on the pre-restore unaccounted worker and an approval of the pre-entry plan answer `409 recovery-mode-active` naming the machine and scope (C runs without the recovery flag, so the per-scope check answers). Step 7 adds the release of the `blocked` control plane (`409 conflict`), step 8 an exit with the control plane unreleased (`409 conflict`), and step 9 checks that the revert's commitment entry names another controller than C and that C has committed nothing since step 4. The ready-but-unreleased plan, the epoch term and the process-epoch comparison are `SKIP` lines naming their Go checks. Evidence goes to `.state/evidence/s7-closing-<utc>/`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s7 no-entry` | Acceptance plan S7's variant "the same restore started normally, with no entry", after `s7 run` on the same `up`. B is stopped and C started. C commits a worker plan and stalls before its attempt; at *T*: `db-snapshot s7-ne`. After *T*: B, its control file naming `dispatch-attempted`, takes the operation over at its start and records its attempt, stalled before it sends. C is paused, B killed, both control files removed, and the database alone restored (`db-restore s7-ne`); nothing starts with `-recovery-start` and no entry is recorded. Unpaused, C makes its attempt with the owner token B's takeover had replaced: the attempt is C's at generation 1 in the restored epoch, the operation completes with one `ApplyConfiguration` on the worker, and the installation stays in the restored epoch out of recovery mode. This is the residual PA §14's last row states, shown, not closed. Leaves C running. Evidence goes to `.state/evidence/s7-no-entry-<utc>/`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s7 transit` | Acceptance plan S7's variant deleting the Transit key the `Desired` releases name, after `s7 no-entry` on the same `up`. C, B-recovery and A are stopped and B started, out of recovery mode, both machines' `Desired` binding `bw-artifact`. At *T*: `bao-snapshot s7-tr` and `db-snapshot s7-tr`; B is killed, both restored, B-recovery started and the entry recorded. `bao-delete-key bw-artifact`: within two monitor intervals one `regression` alert of its status, `unknown`, `absent`, naming both `Desired` releases; both scopes are accounted `blocked` and each mark is `blocked` with only `artifact` missing. The key is then rotated as an operator would: a new key `bw-artifact-2`, the `bw-compiler` and `bw-executor` policies rewritten as their tracked files plus the same grant on it (the tracked files are unchanged), B's `provider.keys.artifact` set to it, B-recovery killed and B started without the flag. The worker's mark stays `blocked` on `artifact` until a new draft changing nothing is published: its release, compiled under `bw-artifact-2`, is both machines' `Desired`, each mark is `ready`, both scopes are released and recovery mode exited. Leaves B running under `bw-artifact-2` with the widened policies. Evidence goes to `.state/evidence/s7-transit-<utc>/`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s7 old-bao` | Acceptance plan S7's variant with the OpenBao snapshot older than the database (PA §13.3), after `s7 transit` on the same `up`. C is stopped and B started. `bao-snapshot s7-old` first; then an import of the worker, its file secret and S2's label holding it marked, into a draft that removes S2's label fragment from the workers profile if the worker selects it and the fragment is still listed. When S5's fallback left the worker selecting no workers profile, the unused profile is left unchanged. The draft is published, so its release pins KV generations the snapshot does not hold; that release is planned, approved and applied to the worker, so a later draft compiles the worker from it; then `db-snapshot s7-old` at *T*. B is killed, both backups restored, B-recovery started and the entry recorded. Within two monitor intervals each pinned generation is observed `unknown`, `absent` (OpenBao's `404`), with one `regression` alert each naming the release, still one after a later monitor pass has observed each again. B-recovery is then killed and B started without the flag, recovery mode still on: a new draft changing nothing is published and its operation fails `503 dependency-unavailable` naming one of those generations, with no release. Leaves recovery mode on and B running; run it last on an `up`. Evidence goes to `.state/evidence/s7-old-bao-<utc>/`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s8 run` | Acceptance plan S8 steps 1-3, after `s7 run` on the same `up`, on B (A and C stay stopped). Step 1: `h-viewer` reads the worker's timeline and observations (every page) and the events and resource of S4's safe apply, S5's revert (the first completed one approved as an unseen overwrite) and S7's held operation (from `s7-state.tsv`); the `Bronzeward-Epoch` served is the installation's and not the one `s7 run` kept before *T*. Step 2: `fixtures/s8-answers.jq` answers, for each operation and for S6.1's lost response (from the files a separate `s6 run` kept, named by `S8_S6_KEPT`, its `s6-run-<utc>` evidence directory as copied to a record), who approved, what each attempt carried and when, whether its sending is known, what was observed (each observation taken for it or its plan, or named by its entries, with what the read returned or which values it could not read and why), why the scope was held and by whom, who decided an accounting, and how it ended, from what was served only; the answers go to `answers.json` and each is checked, with the structure every answer rests on (entries in commit order, the events the operation's own timeline entries, the resource's attempts the timeline's with both deadlines and the time compared, no response text served, the commitment's approval served with its principal and role, every named observation served and each its timeline entry). Step 3, last, since `h-viewer` stays revoked: B commits a worker plan and stalls before its attempt; its event stream, opened with a token minted then, stays open past the token's `exp` and ends by itself within 15 s before it and 5 s after; resumed with `Last-Event-ID` and a fresh token it answers `200` (the control); `h-viewer` is then listed in B's `deniedSubjects`, B killed and started again, and `h-recovery` records `h-viewer`'s revocation (`201`, `deniedSubjectsListed` true); the same resumption answers `403 identity-revoked`. B takes the operation over at its start and completes it. Evidence goes to `.state/evidence/s8-run-<utc>/`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s8 controls` | Acceptance plan S8's negative controls, after `s1 run` on a fresh `up`, on A. `h-viewer` opening a draft, creating a plan and freezing the worker answer `403 forbidden`. In a clone of the image commit, against `go-db`'s PostgreSQL and OpenBao (`mise run dev-db`, `mise run dev-bao`): `TestApplyClasses` (a Talos error text quoting a configuration value, in every class and both shapes, kept out of the response recorded; the same error from a live node is `s4 ds` row 022's, not repeated here), `TestOperationEvents`, `TestMachineTimelineAndObserved` and the three `TestEventStream*TokenExpiry` checks, none skipped (`s8-go.log`). The scan's positive control: a copy of a served timeline with one leak-scan pattern appended is captured with a bundle, whose scan must report that one file; the copy is removed straight after. Evidence goes to `.state/evidence/s8-controls-<utc>/`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s8 revocation` | Acceptance plan S8 step 3's check of a revocation alone, after `s8 controls` on the same `up`, on A, with no `deniedSubjects` entry: on the latest operation with two events, a resumption after its first event with a token minted then answers `200` (the control); `h-recovery` records `h-viewer`'s revocation (`201`, `deniedSubjectsListed` false); the same resumption with the same unexpired token answers `403 identity-revoked`. Evidence goes to `.state/evidence/s8-revocation-<utc>/`. Exits 1 on any `FAIL`. |
| `fixtures/scenarios/s0\|s1\|s2\|s3\|s5 pass` | Each scenario's `run` (for S3, the approval part) restricted to its pass paths, for the integrated run (acceptance plan §2: negative controls and interruption matrices run separately, each from a fresh `up`). Each control left out writes a `SKIP` row naming it and saying the full mode runs it. S0 leaves out the schema controls and the restore; S1 the Go rows, the refusal set, the Talos access refusals, takeover, the interruption matrix, the review, expiry and orphan rows; S2 the second author on each ETag, the update and publication matrices on C, step 6's soft-delete, destroy and OpenBao controls and the Go rows; S3 the controls before commitment (wrong identities, a second approval, the superseded release, expiry, cancellations, revocations, the unapproved plan); S5 the frozen-scope revert plan, the non-approver unfreeze and the drift adoption's interruption matrix with its generation-count control, keeping step 4's refused drift adoption (S5's pass criteria: the new secret found nowhere outside OpenBao "on success, rejection and each interruption"). The final dump control names a row the pass mode wrote (S1 the worker's machine, S2 step 7's release). |
| `fixtures/scenarios/integrated <record dir> run\|controls\|s6\|<step>...` | Acceptance plan §2's integrated run, and the scenarios it runs separately, from a fresh `up`. `run`: `up`, `s0 pass`, `s1 pass` (which, after `s0`, does not seed again), `s3 handover`, `s2 pass`, `s3 pass`, `s4 run`, `s5 pass`, `s7 run`, `s8 run`, `down`; it needs `S8_S6_KEPT`, so the `s6` preset runs first. `controls`: `up`, `s0 run`, `s1 run`, `s3 handover`, `s2 run`, `s3 run`, `s4 controls`, `s4 restores`, `s4 run`, `s5 run`, `s5 roles`, `down`, the full modes with the controls `run` leaves out. `s6`: `up`, `s1 run`, `s3 handover`, `s2 run`, `s3 run`, `s4 run`, `s4 ds`, `s6 run`, `down`. Or a list of steps (`up down s0 s0p s0walk s1 s1p s1nid s3h s3hc s2 s2p s3r s3rp s4 s4c s4x s4ds s5 s5p s5roles s6 s7 s7c s7n s7t s7o s8 s8controls s8revocation`, a step ending in `p` being that scenario's `pass`; `s1nid` is `s1 node-id`, `s3hc` `s3 handover-controls`, `s4c` `s4 controls`, `s4x` `s4 restores`, `s7c` `s7 closing`, `s7n` `s7 no-entry`, `s7t` `s7 transit`, `s7o` `s7 old-bao`), each step at most once per list. The modes that need an `up` of their own run as lists: `up s1nid down`, `up s1 s3hc down`, `up s1 s3h s2 s3r s4 s7c down`, `up s1 s3h s2 s3r s4 s7 s7n s7t s7o down`, `up s0walk down`, `up s1 s8controls s8revocation down`. The record directory must lie outside the fixture, since `down` removes `.state`: each run writes `<preset>-<utc>/chain.log` (the checkout and its changed-path count, then each step's start, end and exit status), each step's output as `<step>.log`, and, after every step but `down`, a copy of `.state/evidence/` under `evidence/`. It stops at the first step that fails and leaves the fixture up for inspection. |
| `fixtures/bin/inject <action>` | Failure injection and backup/restore. Run it without arguments for the list. Targets include instances `A`, `B`, `C` and `B-recovery` (B with `-recovery-start`; `start` refuses either of B and B-recovery while the other runs) and the `issuer`; `linksplit`/`linkjoin <A\|B\|C> <controlplane\|worker>` cut one instance from one Talos node with a blackhole route in the instance (in a table of its own, looked up by a rule, since Docker will not join a network whose subnet a main-table route overlaps), recorded in `.state/linksplits` and put back by `start` (an instance with a split recorded is started off the Talos network and joins it at its fixed address only once its splits are back, so its process never reaches a cut node), and `seal`/`unseal openbao` seal OpenBao and unseal it. `netsplit` refuses an instance or the issuer: it would cut the instance from its database too. `.state/injections.log` gets two timestamped lines per action (the outcome handler is armed before the first is written, and a signal is held from before that line until the outcome is armed, so a `begin` is never left without its outcome; a signal that ends the command between two of its steps is recorded as `failed`, not as the `done` the shell's status would read as): `begin` when it starts, then `done` or `failed rc=N`, or `unknown rc=N` for a `bao-restore`, `bao-soft-delete`, `bao-destroy` or `bao-delete-key` whose request went out and got no answer (a timeout, a dropped connection or a signal), since OpenBao may have applied it all the same (a request OpenBao answered with success is `done` whatever ends the action after that, a `bao-restore` whose wait for an active node gave up included, since the mutation is made), and for a `kill`, `start`, `pause`, `unpause`, `netsplit` or `netjoin` whose Docker request failed and whose outcome the daemon cannot be asked for: when the CLI fails, whether by a refusal, a dropped connection or a signal, or a signal ends the command anywhere after the request returned and before the action completed, the container's state is read back (a few times over, since a kill is answered before the container has stopped), and the state asked for is `done`, another state `failed` (a `start` whose CLI failed stays `unknown` even with the container running, since the service was not seen to answer; a `start` the daemon answered whose wait for the service a signal ends is `unknown` too, while a wait that ran to its limit is `failed`); for a `netjoin` the state asked for is membership at the node's fixed address, or under the service's alias, not membership alone, since a container attached meanwhile at another address is a member the connect refuses. `start` is done only once the service or Talos node answers again: for PostgreSQL an authenticated query on the fixture's database, not `pg_isready`, which answers whatever the parameters, sent from inside the container's namespace to its address on the Compose network, since the image trusts loopback and a query there would prove nothing about the password. `bao-delete-key` reads the key's `deletion_allowed` setting first, then sends two requests; a deletion refused after the setting was applied puts it back to what was read (a key already deletable is left so) and fails, and is `unknown` when that cannot be done either, or when the deletion went out and got no answer, whatever the setting was. Every OpenBao request `inject` sends goes from inside the network namespace of the container verified under the name, by the pinned client image `CURL_IMAGE` (pulled by `up`; `inject` never pulls), to that container's own listener: on the host, the published port is an address that whatever holds it by then answers, and would be handed the root token; a container stopped or gone has no namespace to join, so the client is not run (Docker's status 125) and the mutation is `failed`, nothing having been sent; a name that answered to no container when the command checked is refused before anything is logged. A snapshot name is one path component, without a slash and without `.partial.` in it or `.partial` at its end (the file name's suffix would complete the marker); a KV path or Transit key name is slash-separated components of letters, digits, `_`, `-` and `.`, none being `.` or `..` (which would reach another API path than the one the log names), and is refused before anything is logged; no argument may hold a control character, since each is written into the log, which must itself be the regular file `inject` made, under that one name. A snapshot gets its final name only once it is complete; `db-restore` and `store-restore` replace the live data only after the snapshot was read in full. The rename that publishes a snapshot, or puts a restored store in place, is the commit, as is the one transaction that swaps the restored database in: an action interrupted after it is logged `done`, since the artifact it left is what a later restore acts on (after an interrupted `db-restore` the server is asked, for up to five seconds, whether the database under the live name is the restored copy, by the OID read from it before the swap, which a rename keeps, since a transaction still in flight at the first look can still commit a moment later; one that never shows the restored OID in that span is `unknown`, not `failed`, and `bronzeward_previous` is left until the next `db-restore` drops it whichever way it reads). Every request to a service is time-bounded, so an action against a hung service fails instead of hanging; PostgreSQL is given the same limit as its statement and lock timeouts for the session, since ending the client leaves the statement running inside the container, and one waiting on a lock would otherwise still commit after the log said the action failed (`pg_dump` and `pg_restore` lift both for their own session). |
| `fixtures/bin/evidence [path ...]` | Writes versions, container logs, a database dump, a copy of the PostgreSQL data directory as written to disk (read from the container whether the server runs or not), what each instance wrote to its writable container layer (`layers/`: the `docker diff` list and each entry added or changed, kept as an unextracted tar archive, never what is mounted; a layer that cannot be read, a path that is not absolute and canonical, or an instance with a tmpfs, leaves the bundle incomplete), OpenBao metadata and Talos config digests to `.state/evidence/<utc>/`, then scans them, and any extra paths given, for synthetic secret material. Prints the bundle path. A bundle is some tens of MiB, most of it that data directory. |
| `fixtures/bin/down [--purge] [--adopt]` | Removes every container, network and volume, then checks that nothing is left, by the labels and under each of the fixture's fixed names, and fails if something is, or if Docker could not be asked. The claim and then `.state/` are removed last and only after that check passed, so a `down` that could not finish can simply be run again. `--purge` also removes `.cache/`. It refuses a fixture it cannot show to be this checkout's own; `--adopt` overrides that. |
| `fixtures/bin/selftest [--keep]` | `up`, each injection once with an assertion, `evidence`, `down`. `--keep` runs the checks against a fixture that is already up. |

An experiment uses the fixture like this:

```sh
fixtures/bin/up
set -a; . fixtures/versions.env; . fixtures/.state/secrets.env; set +a
export TALOSCONFIG=$PWD/fixtures/.state/talosconfig
# ... run the prototype against 127.0.0.1:$POSTGRES_PORT, http://127.0.0.1:$OPENBAO_PORT,
#     Talos nodes $TALOS_CONTROLPLANE_IP and $TALOS_WORKER_IP, local stores in fixtures/.state/data ...
fixtures/bin/inject kill postgres
fixtures/bin/evidence /path/to/the/prototype/tmp
cp -r "$(ls -d fixtures/.state/evidence/* | tail -1)" /somewhere/outside/.state   # down deletes .state
fixtures/bin/down
```

## OIDC issuer

[`oidc/`](oidc/) is a disposable OIDC issuer for Bronzeward's authentication tests
(persistence-api.md §10.1). It serves a discovery document and a JWKS for one synthetic ES256
key, and it mints access tokens for synthetic humans: valid ones, or ones carrying a single named
defect. It is not an identity provider. It has no login, no clients and no refresh.

```sh
go run ./fixtures/oidc keygen -out fixtures/.state/oidc-key.json
go run ./fixtures/oidc serve -key fixtures/.state/oidc-key.json -issuer http://127.0.0.1:5556
go run ./fixtures/oidc mint -key fixtures/.state/oidc-key.json -issuer http://127.0.0.1:5556 -human h-author [-defect expired]
go run ./fixtures/oidc mint -key fixtures/.state/oidc-key.json -issuer http://127.0.0.1:5556 -human h-approver -without-group bw-approvers   # removed from a group
go run ./fixtures/oidc defects   # every defect mint can put in a token
```

- **Humans.** Each synthetic human's subject is its name, and its groups are the ones
  `examples/bronzeward.yaml` maps to roles. The list is `issuer.Humans` in
  [`oidc/issuer/issuer.go`](oidc/issuer/issuer.go).
- **Key file.** The key file holds the private key. It is a synthetic secret: keep it under the
  gitignored `.state/` and never commit it.
- **Started by `bin/up`.** `up` runs `serve` in the `issuer` container from the server image, with a
  key it generates in `.state/issuer/key.json`, as `http://issuer:5556`. Instances A and B reach
  it by that service name over plain http: it is the one host their configuration lists in
  `auth.oidc.plainHTTPHosts`. It is not published on the host; tokens are minted offline from the
  key, as `scenarios/s0` does with the image's `oidc mint`. The key's private part is a leak-scan
  pattern. The commands above remain for running it outside the fixture.
- **Defect cases stay here.** The verifier's own tests, every defect token and fixture scenarios
  S0-S8 run against this issuer. Only the sign-in self-test below uses a real provider, and it
  claims interoperability with that pinned release only.

## Test identity provider

[`dev-idp`](dev-idp) runs Zitadel at the `ZITADEL_IMAGE` pin as a real authorization server for
the clients' sign-in: authorization code with PKCE `S256`, refresh with rotation, the device
grant, revocation and end-session. It is a test fixture and selects no identity provider for
deployments. Everything in it is synthetic.

```sh
mise run dev-db          # the self-test's database
mise run dev-idp         # prints the issuer, project ID (the audience) and client IDs
mise run go-idp          # the self-test, internal/api/idp_test.go and internal/clitest/login_idp_test.go
mise run dev-idp-down    # its containers, network and .dev/idp/; mise run dev-down does this too
```

- **Shape.** Zitadel, its own PostgreSQL (`POSTGRES_IMAGE`, not dev-db's container, whose
  databases the tests create and drop) and the groups target share the Docker network
  `bw-dev-idp`. Only Zitadel is published, as `http://localhost:58280`, the issuer every caller
  must use: Zitadel serves an instance by its external name. The server needs no
  `plainHTTPHosts` entry for it (loopback).
- **Seed.** One project `bronzeward` with the five role keys of `examples/bronzeward.yaml`; the
  Native app `bronzewardctl` (redirect `http://127.0.0.1/callback` on any port, no secret, code,
  refresh and device grants); the User Agent app `bronzeward-web` (callback and post-logout URI
  under `http://localhost:58443`, or `BW_DEV_IDP_WEB_ORIGIN`); the humans of `issuer.Humans`
  with those roles as grants, and `h-none` with none. Access tokens live 5 minutes, refresh
  tokens 10 minutes idle and 1 hour absolute, a device code 1 minute (Zitadel's default is 5).
- **Groups.** Zitadel's roles claim is an object, which the server refuses as `groups`, and the
  pinned release has no groups scope. An Actions v2 `preaccesstoken` target, `oidc groups`
  ([`oidc/groups/`](oidc/groups/)), adds `groups`: the user's role keys in the project, `[]` for
  none. It verifies Zitadel's HMAC signature on every call and is created with
  `interruptOnError`, so a refused or unreachable target fails the token request.
- **Deny list.** Zitadel's default HTTP-client deny list refuses every loopback and private
  address, the target's included. dev-idp replaces it with `169.254.0.0/16`. That is a setting
  for this throwaway provider only, never a production default.
- **Headless sign-in.** The self-test plays the login UI: the machine user `bw-login`
  (`IAM_LOGIN_CLIENT`) creates a session through the Session API and finishes the authorization
  request with CreateCallback. No login container runs.
- **State.** `.dev/idp/` (gitignored) holds the provider's two machine-user tokens, the target's
  signing key, the target's static `oidc` binary, which its container runs read-only, and
  `idp.env`, the `BW_TEST_IDP_*` variables `go-idp` reads. The containers are
  kept until `dev-idp-down` removes them with their data, so `docker logs` still reads one that
  exited. While `dev-idp` waits for each to answer, a container that exits fails it at once with
  that container's log tail; one that exits during the few seconds of seeding calls fails the
  call instead. They are
  shared by every worktree on the host; `.dev/idp/` is per checkout, so start and stop the
  provider from the same one.
- **Self-test gate.** Without `BW_TEST_IDP_ISSUER` the tests skip; `go-idp` sets
  `BW_REQUIRE_IDP=1`, so there a missing provider fails them. CI's `idp` job runs the three
  tasks and then checks that `dev-down` left nothing; it is not a required check.
- **Live checks.** The provider behaviour these rest on, and its residuals, are recorded in
  [the Zitadel test-provider record](../docs/design/research/20261022-zitadel-test-idp.md).

## Test static host

[`webhost/`](webhost/) serves the built web package (`web/dist`) as the component in front of
the server would ([client contracts §10.1](../docs/spec/client-contracts.md#101-hosting-and-origin)):
every page with the §10.2 headers of `web/headers.json`, whose CSP placeholders it fills from
`-issuer` and `-provider-origins`; `/api/v1/` and `/livez` proxied to `-api`; every other path
answered with the application. It listens on `127.0.0.1:58443`, behind the test provider's
seeded web origin `http://localhost:58443`. `-redirect-api <origin>` makes it answer every API
request with a `307` to that origin instead, the fault of conformance cases TLS-02 and TLS-03.

`mise run browser` builds it, starts it in that mode with a counting target on
`http://127.0.0.1:58444`, runs `web/e2e/` in each browser of `BW_BROWSERS` (default
`chrome,firefox`; also `edge`, `firefox-esr`, `safari`) and scans `web/e2e/out/` for the run's
canary, a `BWSYNTH-` value only the target serves, and for any JWT. No server, database or
identity provider runs: the page signs in through a synthetic provider in the harness
(`web/e2e/provider.ts`) on `http://127.0.0.1:58445`.
Each chrome and edge session gets a profile directory under one directory per run, removed when
the run ends. Both browser tasks run WebdriverIO with a `TMPDIR` of their own, remove it after the
run, and fail when a browser profile was left in it (`scripts/ci/browser-tmp.sh`). When
`BW_BROWSERS` names `chrome` or `edge`, they refuse to start with a `TMPDIR` longer than 51 bytes:
Chrome binds a socket under the run's directory, and a Unix socket path holds at most 107 bytes.
`BW_WDIO_CACHE` chooses where WebdriverIO keeps
its browser and driver downloads; the tasks default it to the outer `TMPDIR`.

`mise run browser-auth` runs the AUTH cases (`web/e2e/auth/`) after `dev-db` and `dev-idp`. It
creates two databases on dev-db's PostgreSQL and drops them at the end, with two servers and
two hosts on loopback: one server trusts the test provider, behind a host on `58443`; the other
trusts the fixture issuer (`58447`), whose key set the synthetic provider serves on `58446`
under its own issuer, behind a host on `58448`. A control route on `58449` chooses the minted
token, defect included, the next sign-in receives. The servers listen on `58450` and `58451`.
The scan also looks for the provider's synthetic password and login token.

## Instance C and the interruption seam

Instance C runs `/bronzeward-interrupt`, the server built with the `fixtureinterrupt` tag, from the
same image and configuration shape as A and B. B runs it too, so that `s7` can hold B after its
request is sent (`dispatch-sent`, acceptance plan S7's held request); A and B-recovery never run it. The tag compiles in
[`internal/seam`](../internal/seam)'s `At(step)`, which ingestion calls at each of its steps
(`claim`, `read`, `parse`, `identify`, `substitute`, `guard`, `generation-first` after the first of
several generations, `generation`, `construct`, `baseline`, `staged`, and `draft` before the draft
transaction), a mark's run at those it re-enters (`parse` to `construct`, then `envelope` once its
new envelope is encrypted and before it is stored), and the publish worker at each of a publication's interruption points
(`publish-claim`, `publish-snapshot`, `publish-observations`, `publish-pins`, `publish-compile`, `publish-encrypt`,
`publish-commit`, the last inside the commit transaction before its `COMMIT`), and the adoption loop
once (`adoption`, after the plan's evidence observation is recorded and before the adoption is). Without the tag
`At` is empty, and `up` refuses an image whose `bronzeward` holds the seam.

C reads `server/c/interrupt` and B `server/b/interrupt` (each mounted as `/etc/bronzeward/interrupt`;
only `s7` writes B's) at every call: one line,
`<step> kill` or `<step> stall`. `kill` writes `fixture interrupt: kill at <step>` to stderr and
ends the process with status 137 at once, nothing deferred run, as an operator's `kill -9` would
(a signal would not do it: the kernel ignores a SIGKILL a container's PID 1 sends itself). `stall`
blocks that ingestion, publication or adoption, the heartbeat running on, until the file no longer names the step. A missing
file, or one naming another step or action, interrupts nothing. The scenarios write the
file by rename, so C never reads half a line, and remove it before they start C. `mise run verify` vets
every package and tests the seam and its callers with the tag.

## OpenBao keys and policies

`bin/up` enables KV v2 at `secret/` and Transit, and creates four Transit keys: `bw-artifact`,
which the compiler encrypts artifacts under and the executor decrypts, and ingestion's
`bw-baseline`, `bw-staging` and `bw-digest` (compilation §2.3, §3.1, §4.1). The role policies are
the files in [`openbao/policies`](openbao/policies):

- `bw-ingestion`: creates generations under `secret/data/gen/`, without read or update; encrypts
  under the baseline key; encrypts and decrypts under the staging key; computes an HMAC on the
  digest key's exact path; reads each cluster's Talos access credential under
  `secret/data/access/talos/`, without write, list or delete (persistence-api §3.3).
- `bw-compiler`: reads generations; encrypts under the artifact key.
- `bw-executor`: decrypts under the artifact key; reads each cluster's Talos access credential, as
  ingestion does.
- `bw-metadata`: the server's metadata identity (dependency-monitor §4, choice §11.9): reads KV
  metadata under `secret/metadata/` and Transit key state under `transit/keys/` by name, without
  list, value or Transit operation.
- `bw-metadata-only`: the classification experiment's and evidence observer's identity, which
  also lists and reads no value.
- `bw-orphan-report`: the orphan report's identity (persistence-api §6.4): lists and reads metadata
  under `secret/metadata/gen/`, nothing else.

`bin/up` writes them from those files, which `mise run go-db` also writes into a dev-mode OpenBao
for the policy tests (`internal/provider`), so the fixture and the tests hold the same policies.
Ingestion, the compiler, the metadata identity and the orphan report have tokens here, one of
each per instance, without OpenBao's default policy; executor tokens come with that component.

## Versions

[`versions.env`](versions.env) is the single manifest: image index digests, CLI release URLs with
their SHA-256, the Kubernetes version and the topology. An experiment report cites the commit of
that file; `bin/evidence` records it together with the versions the running services report. The
values are frozen. Bump one only in a commit that says why, because evidence recorded against the
old value no longer reproduces. The file is deliberately not a format Renovate reads.

## Synthetic secrets

No secret is committed. `bin/up` generates all of them into the gitignored `.state/`:

- `secrets.env`: a canary value, the PostgreSQL password, and the OpenBao root and metadata-only
  tokens. Values the fixture chooses itself start with `BWSYNTH-`. The commands read this file as
  data, never as shell code: only those four assignments are accepted, each once, all four must be
  there, the file must be the regular file `bin/up` wrote under that one name, and anything else
  in it stops every command. `bin/up` itself exports the values it generated and never reads the file back,
  and `bin/selftest` reads the file a child `up` wrote the same way as every other command. A
  value the caller's shell holds under one of those names is never used.
- `bao-init.json`: the single OpenBao unseal key, in base64 and in hex, and the root token.
- `server/a/openbao-ingestion.token`, `server/b/openbao-ingestion.token`,
  `server/c/openbao-ingestion.token`: each instance's OpenBao token, under `bw-ingestion` alone, beside its `config.yaml` and read by it as
  `/etc/bronzeward/openbao-ingestion.token` (see [OpenBao keys and policies](#openbao-keys-and-policies)).
  A missing one fails the scan-pattern list.
- `server/a/openbao-report.token`, `server/b/openbao-report.token`, `server/c/openbao-report.token`:
  each instance's orphan-report token, under `bw-orphan-report` alone, read as
  `/etc/bronzeward/openbao-report.token` by `bw orphans`. A missing one fails the scan-pattern list.
- `server/<instance>/openbao-compiler.token`, `server/<instance>/openbao-metadata.token` and
  `server/<instance>/openbao-executor.token` for instances a, b and c: each instance's compiler,
  metadata and executor tokens, under `bw-compiler`, `bw-metadata` and `bw-executor` alone, read as
  `/etc/bronzeward/openbao-compiler.token`, `/etc/bronzeward/openbao-metadata.token` and
  `/etc/bronzeward/openbao-executor.token`. A missing one fails the scan-pattern list.
- `talos-secrets.yaml`, `controlplane.yaml`, `talosconfig`, `kubeconfig`: the cluster's own
  generated secrets bundle and client configs.
- `scan-patterns.txt`: every one of the above as a fixed string, the client private keys in
  `talosconfig` and `kubeconfig` included. `bin/evidence` reports the files that contain any of
  them, with the number of occurrences only; a matched line is never printed. A name that holds a
  newline or another control character is reported in bash's quoted form, so that the report stays
  one entry per line.

The scan has a positive control, `.state/data/canary-control.txt`. If the scan does not find it
holding exactly what `bin/up` planted, or cannot read a path or file it was given, the command
fails, because an empty result would then prove nothing. Only that file and its copies inside
expanded store snapshots count as the control; any other file of the same name is reported like
every other hit, and a control whose content changed, or that is a symlink or has a second hard
link, is no control at all: the command then fails, since the planted canary is absent.
The bundle's `versions.txt` names the manifest commit; a capture that cannot read it from git
fails rather than write a bundle that cannot be tied to its pins, and the bundle's
`fixtures-diff.txt` holds the status of every changed or untracked file, the diff of the tracked
ones and the whole content of the untracked ones (text or not), so that the commit plus that file
says what ran; it is empty when `fixtures/` is the commit's, and the like record `bin/up` wrote at
creation is required, since one removed could otherwise not be told from one never needed. Both diffs are git's own (`--no-ext-diff`, so a diff helper from the
caller's configuration is not handed them) and carry binary content whole; the status lists
untracked files whatever `status.showUntrackedFiles` says. A symlink anywhere
under `fixtures/` outside `.state` and `.cache`, tracked or not, is refused, since the file would
carry the target's name and not the bytes that ran; so is an untracked file that only
`.git/info/exclude` or a global excludes file hides, since neither the status nor the content
would show it (the repository's own `.gitignore` files are the one exclusion the bundle stands
by); the refusal withholds a name that holds secret material, like every path the command prints. So is
what git records nothing of, an empty directory or a special file (a named pipe, a socket, a
device), since the commit plus a diff would not say it is there; a gitlink (a submodule, or a
repository staged as one) or a nested `.git` under `fixtures/`, since the diff would carry a
commit hash at most and not the bytes under it; a `filter`, `text`, `eol`,
`ident` or `working-tree-encoding` attribute that git's attributes give a file, tracked or
untracked, since git would then compare the attribute's output and not the bytes that run
(`core.autocrlf` is turned off for every listing and diff for the same reason, so a script
turned to CRLF is recorded as the bytes it has, and `core.ignoreCase` with it, so an untracked
file whose name differs from a tracked one's only by case is listed and not taken for the
tracked one, and git's stat cache is not trusted either — `core.trustctime`, `core.checkStat`,
`core.fsmonitor` and `core.untrackedCache` are set so that a file changed to bytes of the same
length with its mtime put back is compared by content, not taken for the commit's; `core.fileMode`
is forced on, so a command made non-executable is in the diff; `core.symlinks` is forced on, so a
regular file checked out where the commit has a symlink (as `core.symlinks=false` does) is in the
diff as the regular file it is; replacement refs are turned
off with `--no-replace-objects`, so the diff is taken against the commit the manifest names, not
against a replacement of it; and the caller's `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE` and the
other repository-selection variables are dropped, so every listing is this checkout's, which is
also checked, by the repository's top level); a tracked file git is told to skip
(assume-unchanged or skip-worktree), since a change there is in no status and no diff; and
an ignore file that is not the commit's, a modified `.gitignore` or an untracked one, since the
listings apply the working tree's rules and a file a new rule hides would be in none of them,
with the diff carrying the rule and not the file (the committed rules are anchored, so a
directory named `.state` or `.cache` anywhere else is untracked like any other). The diff is taken
against the commit recorded, not `HEAD` read a second time, and every listing and the diff are
then taken a second time and held to the first: the same commit, the same status and the same
diff byte for byte, or the capture is refused, since a checkout, a commit or a change to a file
between two of them would have the listings pass a tree the diff does not describe (a change
made and undone between the two takes of one listing is not seen). `bin/up` makes the same
checks before it records the commit the fixture is made from, so that record stands by what the
bundle stands by. Each archive expanded into the bundle has its SHA-256 recorded next to the
expansion, read from the archive's content so that its name, whatever it holds, is not in the
record (and `tar` runs with `--no-unquote`, so a backslash in a name is a backslash, not an
escape), and the control's copy inside one is the control only while the archive under that
name still has those bytes; a dump or archive that is a symlink or has a second name is refused,
not expanded, since the fixture never makes one and what the other name holds would be copied
into the bundle. Each path is scanned once, however many of the given paths contain
it and under whichever spelling (an extra path that is a symlink to a scanned directory, or lies
under one, is not walked again; its own name is still matched; an extra path that resolves to a
parent of a scanned directory, such as a symlink to the checkout or to `.state`, is walked by the
name it resolves to, so the control is met at its own name and not taken for a leak), and reported once, whether the
secret is in its content, in its name or in both. Names of files and directories,
and the target path a symlink stores, are matched as well as file contents; a name that holds a secret is withheld from the report, which
gives the inode instead. Symlinks are followed, so a linked file or directory is scanned through its link and
a link that cannot be followed fails the command; extra paths may be relative and may have any
name. `bin/evidence` is meant to run while parts of the fixture are down: a source it
cannot read, such as the live database after `inject kill postgres`, is named in
`unavailable.txt` in the bundle and everything else is still captured and scanned. Compressed
backups are expanded before scanning, without needing the database server. What an interrupted
action leaves is covered too: a dump or archive still under its temporary `.partial.` name is
expanded as far as it goes (a truncated one is named in `unavailable.txt`), a `store-restore`
staging directory under `.state` is scanned like the live store, and a database left under a
`db-restore` scratch name is dumped next to the live one, and a bundle a capture left before its
own leak scan ran (it stays marked incomplete, by a `.incomplete` file removed only after the
scan) is scanned as one more root by the next capture whose scan completes, since it holds a
dump, logs and a data-directory copy nothing scanned; that capture then marks it scanned
(`scanned-by.txt` in the earlier bundle names the one whose `leak-scan.txt` holds the hits), so
it is scanned once, not at every capture after. A capture holds a lock on its marker for as long
as it runs, and an earlier bundle is taken only when its marker can be locked: one that another
capture is still writing is left to its own scan and said so, not scanned half-written and
marked scanned. The OpenBao metadata is recorded from listings parsed as JSON; a listing that is
not is recorded as unknown, and the bundle stays marked incomplete. With the live store directory gone, as
a `store-restore` killed between its two renames leaves it, the capture goes on without it, names
it in `unavailable.txt`, and takes the control from the copy the staging directory holds. The
next `store-restore` refuses while such a leftover exists and names it as the only copy, to move
back by hand (a `store-restore` that finds a `data` directory made while it held the earlier
store aside leaves the same leftover, since the earlier store is neither moved back over that
directory nor removed under it); one left by a restore that completed, killed while removing its staging directory
(the live directory present, the staging one holding only `previous`), it names as the replaced
store, to remove. OpenBao snapshots are encrypted by
OpenBao and are scanned as they are. The pattern list in `scan-patterns.txt` is rebuilt from the
generated credentials at every capture and must equal the file, or the scan is void. Talos key material is matched in the base64 form
the secrets bundle carries, so a decoded PEM copy would not match.

Use only these values in experiments. Never point a prototype at a real cluster, vault or database
from here.

## Ownership and recovery

`.state/` belongs to whoever ran `bin/up`, and its lifetime is one run. Nothing in it is backed up
or recoverable by design: losing `bao-init.json` loses that OpenBao instance, which is the
intended way to study key loss. `bin/down` is the only cleanup and is safe to run at any time,
including after a failed or interrupted `up`; under an `up`, `inject` or `evidence` from this
checkout that is still running it refuses instead of removing what that command is creating or
changing. The lock is `.state/lock`, which `up` makes first thing in the directory and holds for
its run; `down` and `inject` hold it alone for theirs, `evidence` shared with other captures, and
every command that finds it held says so and stops rather than wait. It is released when the
holder ends, however it ends. Before it removes `.state`, or `.cache` under `--purge`, `down`
also reads the mount table and refuses while a filesystem is mounted at or below either, since
the removal would go into it; `inject` and `evidence` look the same way before removing a
staging directory or a partial copy of their own. `.state`, its `data`, `backups`, `evidence` and `talos`
directories, and `.cache` must be real directories: every command refuses to run while one of them
is a symlink, because secrets, or the CLIs, would be read or written outside the checkout, or the
removal of `.state` would take a link and leave the cluster's credentials at the far end. The files `bin/up` generates
(`lock`, `secrets.env`, `bao-init.json`, `talosconfig`, `kubeconfig`, `talos-secrets.yaml`,
`controlplane.yaml`, `scan-patterns.txt`, `injections.log`, the node-volume, node-container,
node-network, Compose-container, Compose-volume and Compose-network records, `up-manifest`, `up-fixtures-diff.txt`, `up-fixture-name`, `up-daemon`, `up-versions.env`, `up-compose.yaml` and the instances' `openbao-ingestion.token`, `openbao-compiler.token`, `openbao-metadata.token`, `openbao-executor.token` and `openbao-report.token`) must each be
the regular file it wrote, with no second name: a symlink or a hard link there stops `inject`,
`evidence` and `down` before anything is scanned or removed, since the secret would outlive
teardown under the other name. The same holds for a snapshot about to be replaced by one of the
same name, for a snapshot about to be restored from (a symlink or a second name there would install
what nothing here wrote), for every entry in `.state/backups` at teardown (dot-named ones included), for a `store-restore` staging
directory, and for the marker `down` leaves when the node volumes are not all known. `secrets.env` is written whole and renamed into place at each stage of
`up`, so an interruption leaves it complete or absent, never cut mid-line, and a second name made
for it between two stages stops `up` there. The first two records `up` writes, before anything
else is made, are the fixture's name and the ID of the Docker daemon it is made on: every name,
label and port the commands use is derived from `FIXTURE_NAME` in `versions.env`, and every check
is made against the daemon the shell reaches, so with `versions.env` edited, the checkout
switched, or `DOCKER_HOST` or the Docker context changed since `up`, `down` would look for
nothing, find nothing, remove `.state` and report success while the fixture ran on. Every command
refuses while `versions.env` names another fixture than the record; `inject`, `evidence` and
`down` refuse while the shell reaches another daemon than the record, or while `.state` holds no
daemon record at all. Within one command the daemon cannot change under it either: every command
pins the Docker context it started under (`DOCKER_CONTEXT`, unless the caller set it) for its
whole life, so a `docker context use` in another shell mid-way does not have `up` record the
daemon on one and create the services on another; a context re-pointed by `docker context update`
is caught by the daemon record before `inject`, `evidence` and `down` act, and not by `up` before
its records. The rest of `versions.env` (ports, node addresses, image pins, requested
versions) is bound the same way: every command reads the file once, as bytes, clears every value
the manifest must set before taking its values from that reading, and refuses a file that leaves
one unset, so a value exported by the caller's shell never stands in for one the file omits; the
file is parsed as plain `KEY=value` lines and never sourced, so a value written as a shell
expansion is refused rather than evaluated to something the recorded bytes do not say, and only
the manifest's own keys are assigned, each once, so a line setting `STATE`, `PATH` or a key a
second time is refused by name;
`up` records those bytes as `up-versions.env`, and every command refuses while
the bytes it read differ from the record, since an edit since `up` would send an `inject` to
another port or report a version never requested; restore the file, or the checkout it came from,
then run `down`. `compose.yaml` is recorded the same way, as `up-compose.yaml`, and Compose is run
from the two records, at `up` and at `down`, never from the checkout's files: the services are
created well after the records are written (the CLIs are fetched in between), and an edit in that
window would create them from bytes no record holds; both file records are compared to the files
again once `fixtures/` has been inventoried, so the inventory carries the bytes recorded; and
`lib.sh` and the command running are digested as they are on disk at load, compared again at the
inventory and at the end of `up`, so a script edited before launch and put back before the
inventory does not pass for the commit's. At
teardown the daemon record is the last entry removed from `.state`, the two records Compose is run
from go just before it and together, and an empty `.state`, which a removal cut short after the
daemon record leaves, is taken for none: a removal cut short at any point leaves a directory the
next plain `down` removes. The daemon is asked before the claim is made on it;
the four records are
written whole and renamed into place, the first thing under `.state`, and a failure or a signal
before they are in place, the exit handler armed before the claim is taken and run on every
exit but the one past the flag set once the fixture is up (a signal between two commands leaves
the shell's status at 0), takes the records
(when the daemon record is this run's), the directory and the claim with it (only while the claim
carries the nonce this `up` labelled its attempt with: one another `up` holds, or none, is not
this one's to remove), since nothing else
exists yet: so no `.state` without the record is one `up` left, and none is taken for a fixture
on whichever daemon the shell reaches now. While a Compose volume is being made and its label not
yet read back, the hint a failure prints names that volume to inspect by hand, not `--adopt`,
which would remove it by the label whoever made it. The commands also check
that every container answering to a fixture name carries the fixture's own labels (Compose
project and directory, or Talos cluster name), since the names are fixed and, once the real
container was removed by hand, anything can take the name while the claim stands, and that it is
the container `up` recorded under that name: the labels can be given to any container, so `up`
records the ID of each of the four under its name once it exists, and `inject` and `evidence`
act on a container under a fixture name only if its ID is the one recorded under that name (the
two Talos nodes' names swapped, each is still one recorded, and a pause of the worker would go
to the control plane); a container
under a fixture name that Docker cannot be asked about stops the command too, since only "no such
container" means absent. The node-volume record's content is held against Docker as well: `down` removes
a recorded name only if it is an anonymous volume's, and the volume, if it still exists, carries
no label. The Talos label is only a cluster name, which any container or network can be created
with, so `up` records the IDs of the two containers and the network it created once they exist
(the network as the one both recorded containers are attached to, which must carry the label,
not from a listing by the label, which a network made under it meanwhile would answer):
`inject netsplit` and `netjoin` touch the network under the fixture's name only if its ID is
recorded (a Talos node would otherwise be given its fixed address on whatever network took the
name once every node was off it), and
`down` refuses while a container or network carries the label without being recorded, or while
labelled ones exist and there is no record, as an `up` interrupted right after creating the
cluster leaves it. The Compose containers and network are held the same way: the project labels
Compose gives them are not enough on their own, since any container or network can be given them,
so compose.yaml also labels the two services with this run's own value (`BW_RUN_ID`, exported by
`bin/up`, never in a file) and `up` checks it along with the project labels before recording their
IDs after `compose up` (the network, again, as the one the recorded containers are attached to,
which needs no label of its own), `down`
holds every labelled container and the labelled network to the records, and
`inject` the network. Only `down --adopt` removes by label alone, and only
where no record exists: with a record, a labelled container, network or volume the record does not
name is someone's and no `down` removes it. The removals take exactly the IDs that check held,
never a fresh listing on the label, which would take in one given the label since the check
(`talosctl cluster destroy`, which selects by its own label, is not run; the cluster's state
directory it would remove goes with `.state`); the Compose containers and network the same way, not
through `compose down`, which looks the project up again by its labels when it removes. Every other command acts by the ID its check
held too: `inject` and `evidence` read, exec into, log and copy from the container verified under
the name, `inject` connects and disconnects the network under the ID recorded and sends its OpenBao
requests from inside that container's network namespace rather than to the published port, `up`
records each Compose container by the ID its labels were read from and asks its final readiness
of the recorded IDs (running and not paused, then an authenticated OpenBao request, the password
probe from inside the PostgreSQL container's namespace against its own address on the Compose
network, not by the service's name on it, and a bounded request to each Talos node), and the claim is
dropped by the ID its labels were read from, and only while its owner label still names the
checkout the command saw holding it at its start (one retaken by another checkout since is that
fixture's, and is left, with `down` ending on it). That is what the hint of an `up` that failed names
from the first Compose volume until the last record is published, since a plain `down` refuses
what it finds without its record; an `up` that found a Compose volume name taken (the label read
back is another run's), or whose `docker volume create` failed or whose read-back did (the volume
may exist all the same, and whose it is is not known), publishes the record of the volumes it
did make and names that volume to inspect, remove or rename by hand, then a plain `down`, not
`--adopt`. The named volumes `compose.yaml`
declares get fixed names too (`<fixture>_postgres-data`, `<fixture>_openbao-data`), and Compose
reuses a volume of that name it did not create: `up` refuses while one exists, and `down`, with
or without `--adopt`, refuses to remove the Compose side while one exists without the
project label, since it is someone's data. The label is only the project name, which any Compose
run of that name puts on the volumes it makes, and a look before `compose up` cannot rule out a
volume made in between, so `up` makes the two volumes itself, first of all, labelled with the
run, reads the label back (a volume that existed keeps its own labels, and `docker volume create`
on an existing name says nothing) and refuses when it is not this run's; `down` removes a
labelled volume only as the run recorded, `--adopt` or not, and without the record, as an `up`
interrupted right after making the volumes leaves it, only `down --adopt` removes it by the label. The Talos node
volumes are recorded from the two containers whose IDs were recorded, not from whatever carried
the label at the time. The checkout is
identified by its physical path, so the same checkout reached through a symlink is still its own.
Evidence worth keeping must be copied out of `.state/` before `down`.

## Known limits

- Talos runs as containers. Configuration RPCs and `apply-config --mode=no-reboot` work; reboot,
  upgrade, reset, disk and installer behavior do not exist here. This matches the scope of the PoC
  path and the deferred Upgrade/LifecycleClient tests, and nothing more.
- Reading a node's machine configuration needs the `os:admin` Talos role. Measured on this fixture
  (`internal/talos` `TestLiveRoleProbe`, Talos v1.13.6): talosconfigs for `os:reader` and
  `os:operator` read the version but are refused the `MachineConfig` resource (`PermissionDenied`).
  The only bound on what an ingestion read can do with that credential is `internal/talos`'s
  read-only interface, which its guard tests hold to an allowlist of machinery calls.
- Instances reach OpenBao over plain http on the Compose network (`plainHTTPHosts: [openbao]`):
  Transit plaintext and the ingestion tokens cross it unencrypted. Both are synthetic.
- Kubernetes images pulled inside the Talos nodes are pinned by version, not by digest.
- The PostgreSQL tag floats under a fixed digest. The exact server version is in each evidence
  bundle.
- SQLite has no service. Put database files under `.state/data`; the engine version is whatever
  the prototype links, so each experiment records it. `store-snapshot` copies files, which is only
  a valid SQLite backup while no process has the database open.
- The Talos subnet must not overlap the Kubernetes pod or service ranges. With an overlapping
  subnet Talos leaves the node address out of its API certificate and the cluster never bootstraps.
- The leak scan reads each file once, as it is at that moment. A writer still running while
  `bin/evidence` captures (a restore, a dump, a prototype) can put a secret into a file after the
  scan read it, or into a file the listing never saw. Quiesce the experiment's writers before
  capturing; the bundle's `captured` line in `versions.txt` says when the scan ran.
- Instances A and B reach PostgreSQL by its service name, `postgres`, on the Compose network, not
  by the container ID `up` recorded. Every other command names the fixture's containers by
  recorded ID; the server's DSN is resolved by Docker's DNS when it connects.
- Requests from the host to A and B go to `127.0.0.1` on their published ports, which answer for
  whatever holds the port by then, as for PostgreSQL and OpenBao. The tokens sent there are
  synthetic and scanned for. S2's `TestLiveProviderStates` rows send this run's synthetic OpenBao
  root token to OpenBao's published port the same way: a Go test on the host cannot send from
  inside the container's network namespace as `inject` does, and checking the port's holder first
  would only narrow the race.
- A link split is re-applied by `inject start` once the instance answers. Between the instance's
  start and that re-application (up to the minute `start` waits for `/livez`), the instance can
  reach the node.
- S0 step 3's automation token from an earlier recovery epoch is not driven end to end: no route
  enters recovery mode before ginsys/bronzeward#29. `scenarios/s0` records it as `SKIP`; the Go test
  `TestAutomationExpiredRevokedEarlierEpoch` in `internal/auth` covers the refusal.
- S1 counts the generations an interrupted or refused import left with the metadata identity's
  `bao kv list`: a refusal is checked to have created none, an interruption to have kept every one
  it made. After the interruptions, `bw orphans` runs under the orphan-report identity and must list
  each interrupted claim's generations as orphans of that abandoned claim, with every count
  unchanged after it; its output is kept as `orphan-report.txt`. It runs against instance A's
  configuration only, on cluster scope, and the Go tests in `cmd/bronzeward` and
  `internal/orphans` carry the report's other checks.
- S1's operator review rows (`scenarios/review.sh`; `scenarios/s1 review` runs them alone) read
  the review route's answer into the shell only, never into a file: its transcript records the
  status, and the answer's document is matched for the worker's synthetic file secret in memory.
  A mark failing after a provider write is produced by partitioning OpenBao while the mark's run
  stalls after its first generation. Recovery-mode entry with a paused claim is not run: no route
  enters recovery mode before ginsys/bronzeward#29.
- S1's interruptions are at the points `internal/seam` names. A kill between two of them, inside
  a database transaction, is covered by the Go test `TestCrashInsideT1LeavesDraft`, which S1 runs.
- Talos node volumes are anonymous. `bin/up` records their names in `.state/` once the cluster
  exists, and `bin/down` removes those and any still attached to fixture containers. If
  `talosctl cluster create` fails before a container exists, its empty volumes cannot be told apart
  from anyone else's and are left behind; `docker volume ls --filter dangling=true` shows them.
  The same holds for `down --adopt` run without the owning checkout's `.state/` after Talos
  containers were removed by hand. In both cases `down` says in its last line that it did not
  look for such volumes, instead of reporting that no volume is left; so does a `down` that found
  nothing labelled and no record, since a run with nothing to remove has not looked for them
  either. `bin/up` marks `.state/`
  before it creates the cluster and clears the mark once the records are published, so a `down`
  after an `up` interrupted in between says the same even when every container is gone; `down`
  rewrites the record whole and publishes it by rename, so an interrupted `down` leaves it
  readable. The converse limit: the
  record in `.state/` can be rewritten by anyone who can write there, and `down` can only hold
  each name to what Docker says of a Talos node volume. Any anonymous, unlabelled volume on the
  daemon, written into the record, is removed with the rest. The Talos containers, unlike the
  Compose ones (below), carry no label of `bin/up`'s own: `talosctl`'s Docker provisioner accepts
  none, so a replacement created under one of the two fixed names in the moment between
  `cluster create` returning and `bin/up`'s look after it, which the Compose containers are told
  apart from by a run label, is not caught there.
- A host that restarts the Docker daemon when a new bridge interface appears, for example through
  a NetworkManager dispatcher script, stops the containers that were already running each time a
  fixture network is created. `bin/up` detects this and fails. Fix the host hook so that it ignores
  `br-*`, `veth*` and `docker0`; the fixture does not work around it.
- One fixture per Docker daemon. Names, ports and subnet are pinned so that evidence reproduces,
  which means two checkouts on one daemon would share them. `bin/up` refuses to start while
  fixture containers, networks or volumes exist: a leftover PostgreSQL volume keeps the password
  of the run that created it. It then takes the claim: a container named `bw-fixture-claim` that
  is created, labelled with this checkout's path and never started (with a tmpfs in place of the
  data volume its image declares, so that no anonymous volume is made for it, which removed
  without `--volumes` would outlive every record). Docker refuses a second one
  of that name atomically, so of two `up` started together, from one checkout or two, only one
  proceeds. Docker can create the claim and still answer with an error (a connection lost after
  the request went through); an `up` refused that way looks at the claim, and one that carries
  the nonce this very `up` labelled its attempt with is reported as its own and removed by its
  exit handler, which is armed before the claim is asked for, so that a signal while the create
  returns leaves no claim either; a claim from this checkout under another nonce is a second `up`
  running here, and is reported as such and left. The claim's label is what the commands read the claim by, and a container
  that carries it under another name is nobody's they know: `down` names it, removes nothing,
  and leaves it to be removed or relabelled by hand; on its way out `down` removes the claim
  container only, never every carrier of the label. `bin/down` refuses when the claim or Compose names another directory as the origin,
  or when fixture resources exist and no claim from this checkout shows them to be its own; run
  `down` in the checkout that owns them, or `down --adopt` if that checkout is gone or the claim
  was removed by hand. A `.state/` alone does not count, because it can be left over from an
  earlier fixture, and `down` drops the claim only after everything else is verified gone. `bin/inject` and `bin/evidence` refuse unless the claim names this checkout: a `.state/`
  left over here does not make another checkout's fixture, which answers to the same names, fair
  game.
- The lock in `.state/lock` holds the commands of one checkout apart. `down --adopt` from another
  checkout reads no lock of this one and removes a fixture an `up` here may still be creating: it
  is for a checkout that is gone. Two instants are not covered: `up` makes the directory and the
  lock in two steps, so a `down` in between finds an empty directory and removes it, and that
  `up` then fails at the lock file with nothing made; and `down` removes the lock just before the
  directory, so a `down` started in between proceeds and one of the two reports the directory as
  not removed. The mount table is read once, before anything is removed; a filesystem mounted
  below `.state` while `down` runs is not caught.
- `bin/evidence` observes OpenBao and PostgreSQL out of band, from inside their containers. After
  `inject netsplit openbao` every client finds the provider unreachable while
  `openbao-metadata.jsonl` still shows its true metadata. The bundle records both sides:
  `openbao-client-view.txt` says whether the published port answered, and `versions.txt` shows the
  partition as `networks=[]` on that container's line. An experiment compares what its own client
  concluded with that ground truth. The metadata file reports `unknown` only when the provider
  cannot be asked at all: stopped, sealed, paused, or a listing that fails.
- `bin/evidence` copies the PostgreSQL data directory into each bundle, about 46 MiB each, so a
  run of many captures fills `.state/evidence` accordingly (two `selftest --keep` runs against
  one fixture left 1.7 GiB); `down` removes it with the rest. A copy taken
  while the server runs is not a consistent backup and is not meant as one: it is what was on
  disk at that moment, for the leak scan.
- CI lints these scripts but does not run them.
