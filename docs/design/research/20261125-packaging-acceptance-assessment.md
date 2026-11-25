# Packaging, documentation and clean-install assessment of ginsys/bronzeward#262

| | |
| --- | --- |
| Date | 25 November 2026 |
| Work item | [Clean-install verification and acceptance, ginsys/bronzeward#405](https://github.com/ginsys/bronzeward/issues/405), the last open sub-issue of [ginsys/bronzeward#262](https://github.com/ginsys/bronzeward/issues/262) |
| Contracts | The acceptance criteria, executable checks and recorded limits of ginsys/bronzeward#262; [client contracts](../../spec/client-contracts.md) §5-§7, §9.1, §10.1-§10.2, §12 |
| Assessor | The worker on changeset C of ginsys/bronzeward#405, an agent session. Not an independent review, and not the owner's acceptance |
| Decision enabled | Whether ginsys/bronzeward#262 can be closed, and which rows stay with the owner |

## 1. What was read, and how to read the states

Every state below was read from `origin/main` at `ff53ecf1` on 25 November 2026. Except the `darwin` row,
the job results come from the jobs of CI run 45704290548 at that head, read at 22:41 CET (the run was
still going: the `go` job and the four `browser-auth` jobs had not finished, every job this assessment
cites had). The `darwin` row comes from the log of job 139539603782 in CI run 45708951483, the run of the
pull request that added the job, read at 23:00 CET; the next push of that pull request cancelled that run
as a whole, and the job had already finished green. A file, job or test named in an evidence cell was
opened for this assessment; a statement with no such cell is not claimed.

The states: **met** (the criterion is shown by a check that ran); **met with a limit** (shown, and a
limit is named); **not met**; **written, not yet run** (the check exists and has never run). Nothing is
rounded up. The owner's acceptance of each row, and the closing of the issue, are not this document's.

Three statements the issue body relies on are no longer true, and the assessment uses the current ones:

| The issue body says | At the head read |
| --- | --- |
| No release automation exists; the workflows are `ci`, `pages`, `pr-review`, `tracker-audit` | `.github/workflows/` also holds `release.yml`, `release-dry-run.yml`, `release-sbom.yml`, `keychain.yml`, `safari.yml`, `previous-majors.yml`, `previous-major-pins.yml` and `changelog-fragment.yml` |
| The server serves API revision 3 | `internal/api/server.go:13` sets `apiRevision = 18` |
| `CI` already runs a `macos-latest` job | `ci.yml` held none before this change; `keychain.yml` and `safari.yml` hold the macOS jobs, and this change adds `packaged-darwin` |

The issue's own "Recorded limits" are three: no live-server workflow on `darwin`, the screen-reader
coverage accepted for [ginsys/bronzeward#255](https://github.com/ginsys/bronzeward/issues/255), and no
release before [ginsys/bronzeward#16](https://github.com/ginsys/bronzeward/issues/16). Two more limits
that belong to this work are not in the issue, and §5 records them: signing and attestation read from
workflow text, and the locked-keychain behaviour of
[ginsys/bronzeward#661](https://github.com/ginsys/bronzeward/issues/661).

## 2. The three acceptance rows

The four other sub-issues are closed and their files are on main (`gh issue view`, 25 November):
[ginsys/bronzeward#401](https://github.com/ginsys/bronzeward/issues/401) on 28 October,
[ginsys/bronzeward#403](https://github.com/ginsys/bronzeward/issues/403) on 28 October,
[ginsys/bronzeward#402](https://github.com/ginsys/bronzeward/issues/402) on 22 November and
[ginsys/bronzeward#404](https://github.com/ginsys/bronzeward/issues/404) on 24 November.
[ginsys/bronzeward#405](https://github.com/ginsys/bronzeward/issues/405) is open.

### Row 1: reproducible build and package checks, and the deployment documentation

State: **met with a limit.**

| Part of the row | Evidence |
| --- | --- |
| Builds for the supported targets | `.goreleaser.yaml`: `bronzewardctl` for linux and darwin on amd64 and arm64, the server for linux on both, `-trimpath`, `CGO_ENABLED=0`, times from the commit. `ci.yml` job `package` builds them twice on two runners (`package (a)`, `package (b)`: success in run 45704290548) |
| Two builds agree | `scripts/release/compare-sums.sh` fails on any `SHA256SUMS` line that differs unless `packaging/reproducibility-exceptions.txt` lists it; the file lists none. Job `package-check`: success |
| Versions and compatibility | `scripts/release/check-build.sh`: each binary holds the version and the revision, is built for its archive's target, and the linux/amd64 binaries report the version. `packaged-cli` runs COMP-01 and COMP-02 on the packaged binaries (`internal/clitest/cli_test.go:671,682`) and `packaged-web` serves the packaged bundle; both success |
| TLS, identity provider, endpoint configuration, upgrade, rollback documented | `docs/operator/deployment/hosting.md` (TLS, routing, the web files), `identity-provider.md` (Zitadel), `upgrade.md` ("Upgrade the server", "Roll back"), `docs/operator/clients/contexts.md` ("TLS trust"). Headings read; the pages' sentences were not re-read for this assessment |
| No credentials embedded | `mise run docs` runs `scripts/verify-docs.py` and its planted-sentinel test `scripts/test/verify-docs-scan-test.py`; job `docs`: success |

Limit: no release has ever run, so the signature and attestation steps are read from workflow text and
no artifact of a real release shows them (§5).

### Row 2: quick starts, reference, help, completion, screenshots, workflow examples, troubleshooting

State: **met with a limit.**

| Part of the row | Evidence |
| --- | --- |
| Quick starts, reference, help, completion | `docs/operator/clients/quick-start-cli.md`, `quick-start-web.md`, `cli-reference.md`, closed [ginsys/bronzeward#402](https://github.com/ginsys/bronzeward/issues/402); `scripts/test/completion-shells-test.sh` |
| Workflow examples for import, sources, releases, approval, drift, recovery | Journey pages `protected-import.md`, `connect-a-cluster.md`, `inventory.md`, `editing.md`, `publication.md`, `plan-and-approval.md`, `operation-tracking.md`, `drift.md`, `recovery-after-restore.md`, `sign-in-and-renewal.md` |
| Screenshots, troubleshooting | Six images in `docs/operator/clients/images/`; `client-troubleshooting.md` |
| Clients apart from server administration | `docs/operator/README.md` names three parts: operator clients, deployment, and the server, token and migration pages |
| Web steps have browser cases | `web/e2e/doc-steps.test.ts`: each numbered web step of eight pages needs a case tagged `[step <list>.<n>]`, and each tag needs a step; run by the `web` job (success) |

Limits: the step check reads the case sources, not a run, so a case that a browser skips at run time
passes it (the file's header says so), and a tag says a case exercises a step, not that it asserts every
sentence of it. Screen-reader coverage is the limit the owner accepted for
[ginsys/bronzeward#255](https://github.com/ginsys/bronzeward/issues/255).

### Row 3: clean install and the documented workflows on the agreed matrix

State: **not met.** One part is open, and it is not a rounding: the documented workflows are not run from
the pages (last row of the table). The `darwin` part, which was open when this document was first
written, has since run once (second row).

The agreed matrix is `linux/amd64` (CI, disposable fixture) and `darwin/arm64` (the macOS runner, Keychain).

| Part of the row | State | Evidence |
| --- | --- | --- |
| Clean install on `linux/amd64` | met with a limit | Job `doc-blocks` (success): `scripts/release/run-doc-blocks.sh` builds at `v0.0.0-dev`, installs the archives in a clean container (curl, tar, CA certificates only) and runs the marked blocks against a disposable server, failing when a marked block did not run or another one did. Limit: the build there is not the `package` job's archive (its version carries the commit) |
| Clean install and Keychain on `darwin/arm64` | met with limits | Job `packaged-darwin` and `scripts/release/run-packaged-darwin.sh`, added by this change. First run: CI run 45708951483, job 139539603782, on a `macos-26-arm64` runner, 22:00:44 to 22:01:37 UTC on 25 November, at the first head of the pull request that added it (that run was later cancelled as a whole by the next push; this job had finished green). Its log shows the archive `OK` against `SHA256SUMS`, then "login stored the sign-in in the Keychain, logout read it back, revoked it and removed it", then the archive "is a Mach-O 64-bit executable arm64, and prints" the build's version "with the server gone". Limits: one run, on a pull request head, not on main; install, `version` and the Keychain only, with no live-server workflow (no Docker, the server is Linux only); a keychain made unlocked for the run; the archive is unpacked from a file the job downloaded. A run cancelled for want of a macOS runner would read "not run" |
| Inaccessible and incompatible servers | met with limits | `packaged-cli` (success): TLS-01, TLS-02, COMP-01, COMP-02 and `TestPackagedVersion` on the packaged linux/amd64 binaries, with a wrong version planted to fail. `packaged-web` (success): TLS-02 and TLS-03's read case and the footer case in Chrome and Firefox on the Caddy-served bundle. Limits, from `CONTRIBUTING.md`: the web footer's server version, COMP and TLS-03's write case are not run on the bundle |
| Version reporting | met with the same limits | CLI prints its own and the server's version on the packaged binaries; the web footer shows the bundle's version and `Server unknown` when the server cannot be read (`web/e2e/packaged.e2e.ts:15`) |
| Reproducible artifacts, no local-only scripts | met | Rows above run in CI jobs from repository scripts; no check here needs a developer machine |
| Synthetic credentials | met | The scan of row 1; the fixtures and the stub identity provider use `BWSYNTH-` values |
| **Documented workflows run from the pages** | **not met** | Of 46 unindented `bash` or `sh` fences in the 14 client pages that hold any, 7 are marked `{.bw-run}`, on four pages (`install.md` 4, `contexts.md` 1, `inventory.md` 1, `sign-in-and-renewal.md` 1). No marked block exists on the other ten pages, so no check runs their commands from the page. Their behaviour is covered by cases in `internal/clitest` and `web/e2e` against a source-built fixture, and the web steps by the tag check of row 2. Count: lines opening a fence with `bash` or `sh`, at the margin, per page with `rg -c`; a fence indented in a list item is not counted, and the 7 equals the runner's own list in `scripts/release/run-doc-blocks.sh` (`default_pages`, four pages) |

Whether this last part is a gap in the criterion or an acceptable reading of "documented workflows" is
the owner's to say; this assessment does not decide it.

## 3. The issue's six executable checks

| Check in the issue | State | Where |
| --- | --- | --- |
| Packaging: two builds equal, binaries report the tag and commit, rendered Caddy configuration serves exactly `web/headers.json` | met | `package`, `package-check`, `hosting` (all success) |
| Clean install `linux/amd64` in a container with only curl, tar and CA certificates | met with a limit | `doc-blocks`; the signature steps are not run (§5) |
| Workflow pages: each CLI page's marked blocks run; each web page has a browser case per numbered step | met for what is marked; not met for pages with no marked block | §2 row 3 |
| `darwin/arm64`: install, `version` with the server unreachable, Keychain store and read, the inaccessible-server case | met with limits | `packaged-darwin`, run once (§2 row 3). The inaccessible-server case is the `version` check with the server gone; no other message on `darwin` is checked |
| Negative cases on the packaged artifacts | met with limits | `packaged-cli`, `packaged-web`, §2 row 3 |
| Scan, with planted sentinels | met | `mise run docs`, job `docs` |

## 4. What the macOS job does, so that its limits can be read

`run-packaged-darwin.sh` takes the `darwin/arm64` archive from `package-a`, checks it against its
`SHA256SUMS` line, unpacks and installs it as the install page does, and checks it is a Mach-O arm64
executable. It makes a keychain of its own the default one (the method of
`internal/client/store_darwin_keychain_test.go`) and restores the runner's lists. A loopback test double
(`scripts/release/stub-idp.py`, not Zitadel) stands in for the identity provider: `login --device` keeps
the sign-in in the Keychain, `security` reads the entry back in go-keyring's format, and `logout` in a
new process can send the stub the stored refresh token only by reading it from the Keychain; afterwards
`security` finds no entry. With the stub stopped, `version` must fail, print the client's version and
`Server unknown`, and a wrong expected version must fail the same check.

Before the first macOS run the script was rehearsed on Linux against the `package-a` artifact of the main
run above: the `linux_amd64` archive passed the install and version part, the planted wrong version
failed it, and the stub completed `login --device` and `logout` with the real client using a token file
as the store. The Keychain half, the temporary keychain and the Mach-O check were not run anywhere before
CI; they ran for the first time in the pull request's CI (§2 row 3).

## 5. Limits and owner rows

| Item | Kind | State |
| --- | --- | --- |
| No live-server workflow on `darwin` | recorded in the issue | Holds. The macOS job proves install, `version` and the Keychain only |
| Screen-reader coverage | recorded in the issue | Holds, as accepted in [ginsys/bronzeward#255](https://github.com/ginsys/bronzeward/issues/255) |
| No release before the licence | recorded in the issue | Holds. [ginsys/bronzeward#16](https://github.com/ginsys/bronzeward/issues/16) is open and carries the owner's deferral; there is no `LICENSE` file (only `LICENSE-STATUS.md`), and `scripts/release/stop.sh` fails a real run without it |
| Signing and attestation read from workflow text | not in the issue | `docs/operator/clients/install.md` says so itself ("no real signature or attestation shows the signer's name"). No release has run, so `cosign verify-blob` and `gh attestation verify` as an operator would run them are unobserved |
| First real release run | owner row | The `release` environment, `RELEASE_APP_ID`, `RELEASE_APP_PRIVATE_KEY` and the App `ginsys-release-bot` are the owner's to create; only a real run shows whether environment secrets reach the called workflow (issue comment of 24 November, 08:32 UTC; `CONTRIBUTING.md`) |
| Locked-keychain behaviour on macOS | not in the issue | [ginsys/bronzeward#661](https://github.com/ginsys/bronzeward/issues/661), open, carries the owner's deferral. Not exercised: the job uses an unlocked keychain made for the run |
| macOS capacity | platform | GitHub's macOS arm64 pool sometimes has no capacity ([ginsys/bronzeward#612](https://github.com/ginsys/bronzeward/issues/612)). The job is outside `checks` and starts only when the change touches the client, its build, its own scripts or `ci.yml`; a cancelled run is "not run" |
| Gatekeeper, quarantine, notarisation | not covered | The archive is unpacked from a file the job downloaded, not from a browser download |
| `darwin/amd64` | not covered | Built and checked statically by `check-build.sh`; never run |
| Participant sessions and automated validation | other issues | [ginsys/bronzeward#263](https://github.com/ginsys/bronzeward/issues/263), [ginsys/bronzeward#369](https://github.com/ginsys/bronzeward/issues/369) and [ginsys/bronzeward#368](https://github.com/ginsys/bronzeward/issues/368) are open and wait on this issue |
| Closing ginsys/bronzeward#262 | owner or lead | After the owner has assessed the rows above |

## 6. Spec statements still marked Proposed

`docs/spec` still holds **Proposed** markers. Counted as lines that contain `**Proposed` (a line count,
not an occurrence count; `rg -c`): 46 in `client-contracts.md` and 15 in `operator-experience.md`, 61 in all.
The control is that the pattern finds the sentence at `client-contracts.md:43` that defines the marker,
and the per-section count (`awk` over the headings) adds up to 46 and 15 again. Other spec files hold the
word in lower case only, in other senses (for example a plan's `proposed` state in `acceptance-plan.md`),
and are not counted.

Fifteen of the 61 sit in sections that ginsys/bronzeward#262 cites: 9 in the client contracts (§6, §7,
§9.1, §10.1, §10.2, §12) and 6 in the operator journeys (§4, §5, §8, §13). Three of the nine describe what
the packaging work delivered, so they read stale. Only the lines in this table were read, and only as far
as the text printed beside them:

| Line | Section | What it says | Reading |
| --- | --- | --- | --- |
| `client-contracts.md:1691` | §9.1 identity-provider requirements | "**Proposed** deployment requirements, which the packaging ticket documents" | Documented in `docs/operator/deployment/identity-provider.md` ([ginsys/bronzeward#403](https://github.com/ginsys/bronzeward/issues/403)); the marker looks stale |
| `client-contracts.md:2255` | §10.1 hosting and origin | "**Proposed**, under decision D1 (a): static files served from the same origin" | `scripts/release/render-caddy.py` and the `hosting` job deliver it; looks stale |
| `client-contracts.md:2263` | §10.2 script and content policy | "The two header lists below stay **Proposed**: they bind the static host" | The rendered Caddy configuration sends them and `hosting` compares them; looks stale |
| `client-contracts.md:394,397` | §6 TLS trust | clauses and the "required check of item 8" | Not read item by item; item 8 is stated as a test criterion |
| `client-contracts.md:638,661` | §7 compatibility | item 2's rule over every enumeration, not traced | Open by its own words |
| `client-contracts.md:2749,2833` | §12 shared cases | AMB-14, PROT-07 and others without a case record | Open by its own words |
| `operator-experience.md:352,403` | §4 command tree | "**Proposed.** Noun-verb, as `kubectl`, `talosctl` and `gh` users expect", and "**Proposed** conventions" | The command table exists (`cmd/bronzewardctl/commandtable.go`); not read further, so whether these markers are stale is unknown |
| `operator-experience.md:484,1636,1861` | §5.1, §5.9, §8 | Single clauses ("**Proposed for #253:**", "**Proposed:** no client", "**Proposed:** a client keeps a key per write") | Not read beyond the opening words; unclassified |
| `operator-experience.md:2059` | §13 usability acceptance | "**Proposed** acceptance tasks ... by participants" | The participant sessions are [ginsys/bronzeward#263](https://github.com/ginsys/bronzeward/issues/263) and [ginsys/bronzeward#369](https://github.com/ginsys/bronzeward/issues/369), open; this marker is expected to stay |

The other 46 lines are in sections that ginsys/bronzeward#262 does not cite (client contracts §8, §9.2-§9.7,
§11, and journey sections other than §4, §5, §8, §13); they are not classified here. Whether the three
stale markers are fixed is a documentation change for whoever next touches those sections; this change
leaves them, to stay within its scope. Journey 5.8 holds no marker line; journey 5.9 holds one (1636),
whose clause was not read.

## 7. What this assessment does not cover

- The sentences of the deployment pages were not re-read; only their existence and headings were.
- The remaining main CI jobs (`go`, `browser-auth`) had not finished when read.
- The result cited is the macOS job's first run, at the first head of the pull request. The job starts
  again at each later head of that pull request (the pull request as a whole touches the paths that start
  it) and at the push to main; those runs are not read here. Nothing was run on a Mac by hand.
- No participant has used the pages (ginsys/bronzeward#263).

## 8. Recommendation

Do not close ginsys/bronzeward#262 on this assessment alone. Row 3 is not met: ten of the fourteen client
pages that hold commands have none of them run from the page. That is a decision for the owner: accept
the reading, or ask for marked blocks on more pages. The `darwin` part of the row has run once and passed
(§2 row 3); rows 1 and 2 are met with limits; the owner rows of §5 are open.
