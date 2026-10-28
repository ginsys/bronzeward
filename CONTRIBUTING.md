# Contributing

Work from a GitHub issue with an explicit objective, scope, exclusions, deliverables, acceptance criteria and verification method. Accept responsibility before assigning yourself. Refine incomplete work before implementation; an issue's existence is not implementation readiness.

## Authority

| Information | Authority |
| --- | --- |
| Work scope, acceptance criteria, ownership and completion | GitHub issue |
| Blocking relationships | Native GitHub dependencies |
| Delivery grouping and completion criteria | GitHub milestone |
| Architecture and product constraints | [Current main design](https://github.com/ginsys/bronzeward/blob/main/docs/design/Talos_Configuration_and_Machine_Management_Design.md) |
| Detailed PoC contracts | [Specification index and reserved contract locations](docs/spec/README.md), produced during [milestone 02](https://github.com/ginsys/bronzeward/milestone/2) |
| Reproducible experimental evidence | Repository research documents and prototypes |
| Historical discussion | [Historical report](docs/design/research/20260906-design-discussion-and-peer-review.md) |

Do not maintain parallel status checklists in repository documents or chat. Issue acceptance criteria belong in the issue; native dependencies alone define blockers. Body links explain design references and outcomes rather than duplicating a dependency checklist. Permanent decisions must not exist only in chat. Accepted design changes update the relevant authoritative document in the same change as their implementation.

The working design is not an implementation specification or proof of passing experiments. Keep observed evidence, proposed behavior and unsupported behavior distinct. The first PoC selects one database/provider profile after investigation. Do not assume every investigated option is supported.

## Delivery terminology

| Design terminology | GitHub delivery grouping |
| --- | --- |
| Phase 0: evidence before implementation contracts (§18.1) | [01 - Feasibility evidence](https://github.com/ginsys/bronzeward/milestone/1) |
| “milestone-1 specification” (§18.1) | [02 - PoC specification](https://github.com/ginsys/bronzeward/milestone/2) |
| Phase 1 / “first milestone”: configuration control (§18.2) | [03 - Working configuration-control PoC](https://github.com/ginsys/bronzeward/milestone/3) |
| Operator clients for the Phase 1 scope (§11.3); not a design phase | [04 - Operator experience: CLI and web UI](https://github.com/ginsys/bronzeward/milestone/4) |

The [design's configuration-control scope](docs/design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster) owns acceptance and exclusions. GitHub milestone numbers do not renumber the design phases.

## Triage and labels

Use the work-item form. Select investigation, decision, specification, implementation or validation; during triage apply exactly one corresponding `type/investigation`, `type/decision`, `type/specification`, `type/implementation` or `type/validation` label. A form dropdown does not dynamically apply a label.

During triage, require investigations to describe alternatives and the decision their evidence enables. That field is optional at submission so other work types can omit it; incomplete investigations need refinement before work starts. Outcome links are optional and describe enabled decisions or artifacts, never a second blocker list. Form-generated headings may use `###` while seeded bodies use `##`; section names and meaning are the contract, not heading depth.

The label set is managed from [ginsys/.github](https://github.com/ginsys/.github) (`standards/labels.json`); its daily audit reports drift and a manual apply deletes labels it does not know. Issue open/closed state is authoritative for completion. Use `status/in-progress` when accepted work is underway and `status/needs-review` when it awaits review; these two labels are mutually exclusive. Use `status/needs-refinement` while implementation detail or acceptance verification remains incomplete. It may coexist with either workflow label. Add one or more `area/` labels during triage for the design component or repository concern the work touches; a new `area/` value is created live to unblock triage and back-filled into the label registry in the same unit of work.

Refine or subdivide incomplete implementation and acceptance issues with concrete contracts and checks before removing refinement labels. Preserve native dependencies and acceptance coverage when subdividing. Stable `bronzeward-key` markers identify seeded issues and must not be changed or duplicated. Preserve human edits.

The [licence decision](https://github.com/ginsys/bronzeward/issues/16) is independent owner work in milestone 02 and does not block the PoC specification review. Completing a milestone still requires its own issue completion criteria; this independence does not mark the licence work done.

## Evidence and closure

Investigations compare alternatives and identify the decision their evidence enables. Record exact tool versions, synthetic inputs, reproduction commands, expected/observed outcomes, failure cases, limitations and recommendations in landed research documents and minimal prototypes. A negative finding can close an investigation; resulting decisions and remediation remain separately tracked. A review cannot declare a required safety property feasible without supporting evidence.

Implementation follows finalized contracts and includes meaningful verification of applicable success, rejection, interruption and recovery behavior. Update code and related documentation together. Closure requires every specified acceptance criterion, the required evidence and landed artifacts, assessed by the owner or designated reviewer. An open draft PR or a local passing check alone is not closure evidence.

The [PoC compatibility investigation](https://github.com/ginsys/bronzeward/issues/5) defers Upgrade/LifecycleClient execution testing; full design experiment E3 is not claimed complete. The [specification review](https://github.com/ginsys/bronzeward/issues/20) disposed of it: the transition tests are deferred to the machine-lifecycle phase as a named acceptance item ([design §18.3](docs/design/Talos_Configuration_and_Machine_Management_Design.md#183-phase-2---machine-lifecycle)), and the [review report](docs/design/research/20260927-poc-specification-review.md#8-e3-disposition) records the disposition. PoC compatibility evidence does not complete E3.

## Change and review workflow

1. Prepare a scoped change on a feature branch; preserve unrelated work and keep the default branch unchanged.
2. Run the [documentation checks](#documentation-checks) and any checks required by the issue, then review the actual diff against the current design/specification. Record exact checks and limitations.
3. Open a draft PR stating the problem, scope, linked issues, validation evidence and outstanding limitations. Include related documentation in the same PR.
4. Have the owner or designated reviewer assess correctness, scope, safety, evidence and documentation. Record findings and resolve them or explicitly record an accepted disposition.
5. Complete required evidence before seeking readiness or merge authorization. The owner may merge their own PR after review. Agents require explicit authorization to change PR readiness, merge or change branch protection.

Every PR runs the `CI` workflow (documentation checks, the [documentation site](#documentation-site) build, `actionlint`, the Go build, vet, format and test checks, `go-db` (the root module's tests against PostgreSQL and OpenBao service containers), conventional-commit subjects, action pins, and the packaging check: the release archives built twice by `mise run package` and compared, each build's binaries checked for this commit and version, and the web tarball served through its rendered Caddy configuration; aggregated as the `checks` context), a Codex review and an advisory Claude review (`PR Review`). Review threads must be resolved before merge. Merges are rebase-only through the merge queue: after review, the owner runs `gh pr merge --auto` and the queue lands the PR once `checks` and `PR Review` report on the merged result. Do not claim CI success without an actual run result. Review and merge are separate actions.

A release is made only by running `Release / Create` on `main` with the tag (`v0.N.0-alpha.M`, `-beta.M` or `-rc.M`) after a PR has added that tag's `CHANGELOG.md` section; never push a tag by hand. Its `dry_run`, the default, checks the tag, the changelog and the licence, waits for `CI` to pass on the commit and builds and checks the archives through `Release / Publish`, then keeps them as a workflow artifact and changes nothing. A real run tags the commit, and Publish signs `SHA256SUMS`, attests each archive and publishes a pre-release. Both workflows refuse a real run while the repository has no `LICENSE` ([ginsys/bronzeward#16](https://github.com/ginsys/bronzeward/issues/16)).

## Documentation checks

With Python 3.9 or newer and PyYAML 6 installed in your Python environment, run from the repository root:

```sh
python3 scripts/verify-docs.py
git diff --check
git diff --cached --check
```

With [mise](https://mise.jdx.dev/) installed and `origin/main` fetched, `mise run verify` runs everything the `CI` workflow runs except `go-db` and `idp`, which need running services, and the packaging jobs (with `BW_VERSION=v0.0.0-dev` exported for both, `mise run package`, then `mise run package:check` on a committed checkout; then `mise run hosting`, which needs Docker): the documentation check, the documentation site build, `actionlint` and `shellcheck`, the commit-lint fixture tests, the whole-tree whitespace check, the conventional-commit check on this branch's commits, the [Go checks](#go-code) and the action-pin check (which clones go-kure/.github into the gitignored `upstream/`). `go-db` needs a database and an OpenBao: run `mise run dev-db` and `mise run dev-bao`, then `mise run go-db`.

The verifier reads repository files as UTF-8 and reports file locations relative to the repository root. It checks root guidance and Markdown under `docs/spec/` and `docs/operator/`: inline relative links and anchors, local equivalents of this repository's `blob/main` document links, balanced fenced blocks and trailing whitespace. It also checks issue-form YAML, field names/types/requiredness, disabled blank issues and the CLAUDE delegation.

It also runs the example and credential scan ([ginsys/bronzeward#262](https://github.com/ginsys/bronzeward/issues/262) P12). Every text file under `docs/`, the Caddy configuration rendered from `web/headers.json` and the `packaging/*.example.*` files must hold no PEM private-key block, JWT-shaped string, `bwt_` automation token, `client_secret` value or home-directory path (`/home/<name>/`, `/Users/<name>/`, a Windows `Users` directory in any case, its backslashes escaped or not); a match may run over lines. Write a workstation path as `/home/<user>/`. Host names are checked only in what an operator copies: code under `docs/operator/` (fenced blocks at any indentation, those in a list item or a blockquote included; inline code, also over the lines of one paragraph; and every line indented four or more spaces that does not continue a list item, which is treated as indented code even where Markdown would not), the rendered Caddy configuration and the example files. There, a URL's whole host, a mail domain, a dotted `name:port` or bracketed IPv6 `[address]:port` and an IPv4 address must be `example.invalid`, `example.com` or `localhost` (or a subdomain of one) or `127.0.0.1`. The exceptions are URLs starting `https://github.com/ginsys/bronzeward/` and the fixture's synthetic values, which are listed in `SYNTHETIC_HOSTS` and matched by exact value, and a host written as a placeholder (`$NAME`, `${NAME}`, `{$NAME}`). A single-label `name:port` such as `db:5432` is not checked. Prose links are references, not deployment values, and are not host-checked. Images are not scanned, so take screenshots only on the fixture, after the page's text passes. The scan guards against an accidental leak, not against text written to evade it. It runs before the link checks, and a refusal names the pattern and the line, never the matched text; a link check does not quote a destination that matches a credential or home-path pattern. `scripts/test/verify-docs-scan-test.py` (run by `mise run docs`) plants one sentinel per pattern and code form in a copy of the tree and checks that each one fails without being quoted.

Link checks cover inline links without titles or spaces in their destinations. Titled and reference-style links are skipped and need manual review. Trailing whitespace is rejected, including Markdown's two-space hard line breaks; use a paragraph break instead. The verifier does not fetch external links, verify live tracker state, fully lint Markdown or validate design semantics. Review changed external references and the actual diff separately; for a committed PR, also run `git diff --check <base-commit>...HEAD` using its actual base commit.

## Documentation site

The documentation is published at <https://ginsys.github.io/bronzeward/>, rebuilt from `main` by the `Pages` workflow on every push. It is a [MkDocs Material](https://squidfunk.github.io/mkdocs-material/) site over `docs/`, configured in `mkdocs.yml` with the toolchain pinned in `requirements-site.txt`. With mise and Python 3.10 or newer (the tasks install the toolchain into the gitignored `.venv-site/`):

```sh
mise run site        # strict build into site/, as CI runs it
mise run site:serve  # live preview at http://127.0.0.1:8000
```

Write documentation for GitHub, as before; the site needs no edits of its own:

- Navigation follows the directory tree, so a new page under `docs/` appears without configuration. Page titles come from each file's first heading.
- `docs-map.yaml` also publishes Markdown from outside `docs/` (this file, the README as the home page, every experiment README and the fixtures README). A new experiment README is picked up by its glob.
- `scripts/site/hooks.py` points links at the published copy: a link to a file under `docs/` or to a mounted file, relative or through `blob/main`, becomes a site link, and any other repository file (code, evidence, a directory) becomes its GitHub URL on `main`. Links in code blocks and inline code are left as written. Anchors keep GitHub's form, including the `-1`, `-2` suffixes of repeated headings, and `mermaid` fences render as diagrams. `scripts/test/site-hooks-test.py` covers the rewriting and runs before each build.
- The build runs with `--strict`: a link to a missing page or anchor fails it, and the CI `site` job reports that on the PR.

## Go code

The implementation is the root Go module (`cmd/`, `internal/`); the Go version comes from `mise.toml`. Its `go.mod` ignores `experiments/`, whose modules are Phase-0 evidence and are checked separately, and the generated `site/`, which `mise run verify` rebuilds while the Go checks run. From the repository root:

```sh
mise run go                                        # the generated-types check, then format, go.mod tidiness, build, vet and test checks (per module, as mise.toml lists them); database tests skip
mise run generate                                  # regenerate internal/client/apitypes from api/openapi.yaml with the pinned oapi-codegen; commit the result with the description change
mise run dev-db                                    # a disposable development PostgreSQL on 127.0.0.1:55433
mise run dev-bao                                   # a disposable dev-mode OpenBao on http://127.0.0.1:58201, root token bw-dev-root-token
mise run dev-down                                  # stop dev-db, dev-bao and dev-idp, which removes them and their data (shared by every worktree on the host)
mise run go-db                                     # the root module's tests against PostgreSQL and OpenBao (BW_TEST_PG_DSN and BW_TEST_BAO_ADDR/BW_TEST_BAO_TOKEN, else dev-db's and dev-bao's); a missing service fails
mise run dev-idp && mise run go-idp                # the sign-in self-test against a Zitadel test provider and dev-db's database (fixtures/README.md); mise run dev-idp-down removes the provider
mise run build                                     # bin/bronzeward and the fixture issuer bin/oidc-fixture (bin/ is gitignored)
mise run dev                                       # dev-db, dev-bao and build, then migrate, the fixture issuer and serve, as the lines below do by hand, until Ctrl-C
go run ./cmd/bronzeward migrate -config examples/bronzeward.yaml   # apply the embedded migrations and record the installation; run with the server stopped
go run ./cmd/bronzeward serve -config examples/bronzeward.yaml     # the server on 127.0.0.1:8080; try curl -i http://127.0.0.1:8080/livez
go run ./cmd/bronzeward token issue -config examples/bronzeward.yaml -name ci -roles author,publisher -responsible h-all -operator h-all   # prints the token once
go run ./cmd/bronzeward token list -config examples/bronzeward.yaml      # rotate and revoke: see go run ./cmd/bronzeward token
go run ./fixtures/oidc keygen -out <file>                                # the fixture issuer's key: a synthetic secret, never committed
go run ./fixtures/oidc serve -key <file> -issuer http://127.0.0.1:5556  # the issuer examples/bronzeward.yaml names
go run ./fixtures/oidc mint -key <file> -issuer http://127.0.0.1:5556 -human h-author [-defect expired]
curl -s -H "Authorization: Bearer $(go run ./fixtures/oidc mint -key <file> -issuer http://127.0.0.1:5556 -human h-viewer)" http://127.0.0.1:8080/api/v1/acts
```

Publication reads every covered node through the executor before compilation and
retains the observation on each release machine. An offline node or unreadable
identity/version blocks publication, including first adoption; a prior observation
cannot substitute. The compiler receives no Talos credentials. See
[persistence and API §6.1.1](docs/spec/persistence-api.md#611-publication-observations)
for commit checks, interruption and evidence limits.

`examples/bronzeward.yaml` is the development configuration, matching `mise run dev-db`. A deployment writes its own. `mise run dev` keeps the issuer's synthetic key in the gitignored `.dev/oidc.key` and prints a `mint` line that uses it. The server refuses to start unless the database holds exactly its migrations (run `migrate` first). Until the first release the schema is one migration, `internal/migrate/migrations/0001_schema.sql`, edited in place: a schema change edits that file, never adds one, and a database migrated by an earlier commit is refused and recreated (`mise run dev-down`, then `mise run dev-db`) ([persistence-api.md §11](docs/spec/persistence-api.md#11-migrations) rule 6). It listens on plain http: clients reach the API over HTTPS terminated in front of the server, and the hop behind that is the deployment's choice ([persistence-api.md §9.1](docs/spec/persistence-api.md#91-conventions)). Every request but `GET /livez` authenticates. `GET /api/v1/acts`, `POST /api/v1/identity-revocations`, inventory (`POST` and `GET` on `/api/v1/clusters` and `/api/v1/machines`, with their items, a machine's Talos endpoint replacement, `POST /api/v1/machines/{id}/talos-endpoints`, and its recorded observations, `GET /api/v1/machines/{id}/observations`, each with its start and what it read or why not, never a configuration or its digest), draft creation and reading (`POST` and `GET` on `/api/v1/drafts`, with its items), draft editing under the draft's ETag (`PUT` and `DELETE` on a draft's `fragments/{name}`, `profiles/{name}` and `assignments/{machine}`, and `POST /api/v1/drafts/{id}/discard`), the source reads (`/api/v1/fragments`, `/profiles` and `/assignments`, with their items, revisions and `-revisions/{id}`), publication (`POST /api/v1/drafts/{id}/publications`, `202` with a `publish` operation), release reads (`GET` on `/api/v1/releases`, its items and `/api/v1/releases/{id}/machines/{m}/review`), plan creation and reading (`POST` and `GET` on `/api/v1/plans`, with its items; a duration the request leaves out is `execution.planDefaults`), dependency reads (`GET` on `/api/v1/dependencies`, optionally `?class=`, its items with their `/releases` and `/alerts`, and `/api/v1/dependency-alerts`), ingestion (`POST /api/v1/ingestions` with `source: document` or `source: machine` and no declared references, optionally with `review: true` under encrypted staging, which pauses the run once its envelope is stored, `GET /api/v1/ingestions/{id}`, the operator review of a paused ingestion, `GET /api/v1/ingestions/{id}/review` and `POST /api/v1/ingestions/{id}/marks`, which re-extracts the staged document with the marks and pauses again, `POST /api/v1/ingestions/{id}/continuations`, which runs the draft transaction from the staged envelope, `POST /api/v1/ingestions/{id}/abandonments` and `POST /api/v1/ingestions/{id}/takeovers`, which resumes an encrypted claim whose lease lapsed from its staged envelope) and `GET /api/v1/operations/{id}` are served. An ingestion start and a publication need `provider` and `ingestion` configured, and answer `503` otherwise; every server with both runs a publish worker, which claims queued and lapsed `publish` jobs under the `ingestion` timers, compiles and encrypts under the compiler identity, and commits the release ([persistence-api.md §5.1](docs/spec/persistence-api.md#51-fences-and-claims)); a `source: machine` ingestion reads the cluster's Talos access from the provider and dials the machine's recorded endpoint; the server also reads that access under the executor identity (`provider.executorTokenFile`) for an observation, which dials a plan's route or the machine's current endpoint through the same read-only Talos client and records its start before any read ([execution-recovery.md §4.1](docs/spec/execution-recovery.md#41-timeline-content)); every server with both and an executor token runs an adoption loop, which, after each approval and every `ingestion.heartbeat`, takes an `evidence` observation for each approved adopt plan whose machine's scope is open and records its adoption (T6) relying on it, retries no sooner than `ingestion.lease` later when the node could not be read, and stops at a refusal, which is a refusal entry on the machine's timeline ([execution-recovery.md §6.3](docs/spec/execution-recovery.md#63-adopt)); such a server also runs a drift loop, which one `execution.driftInterval` after startup and every interval after that (default 5 minutes) takes a plan-less `drift` observation of each machine that has an `Applied` and an open scope, and whose recording transaction opens, updates or closes the machine's drift record ([execution-recovery.md §6.1](docs/spec/execution-recovery.md#61-detection)); every server sweeps due staging claims once at startup and then every `ingestion.sweep`, or every minute without an `ingestion` block, since another instance on the same database may have left claims behind. Every server with `provider` configured also runs the dependency monitor ([dependency-monitor.md](docs/spec/dependency-monitor.md)): under the metadata identity only (`provider.metadataTokenFile`, which reads metadata and no value), it classifies every provider object version a release references in passes, one dependency after another with each provider request bounded at 10 seconds, each pass starting 60 seconds after the previous one started or at once if that one took longer (§6.1), so a pass over many unreachable dependencies takes longer than a minute; the pass that first observes `lost`, `blocked`, a regression to `unknown` or a scheduled deletion alerts on it, and while a dependency stays `unknown` it alerts again on the first pass at least 15 minutes after it became `unknown` or was last alerted so (§6.2), so long passes delay every alert; and the read routes serve a class as `stale` once it is more than three intervals old (§6.3). These timings are the monitor's defaults and take no configuration. Each alert is a `dependency_alert` row and one JSON line on standard error with `"event":"dependency-alert"` and the row's fields, written at least once by whichever instance logs next (§7.1). Silence is not health: every instance runs a watchdog once per interval, which raises `monitor-stalled` when it finds three intervals without a pass's progress, `GET /api/v1/dependencies` carries the last completed pass's time, and a server that is down raises nothing, so its liveness is the deployment's to watch (§6.3). A `retained` class never admits a dispatch: publication classifies each pinned version afresh and execution checks access at use (§8). Every other route of the API contract is routed and role-checked, and answers `501 not-implemented` until the issue that owns it lands. `internal/auth` and `internal/api` are exercised by `mise run go-db`, as are `internal/provider`'s policy tests: they run the committed fixture policies (`fixtures/openbao/policies`) against dev-mode OpenBao through child tokens, not the fixture's own server or tokens.

## Running S0 locally

Acceptance-plan scenario S0 runs against the [fixtures](fixtures/README.md), which need Docker, not CI's runner. Each mode starts from a fresh fixture:

```sh
mise run dev-db && mise run dev-bao           # go-db's database and OpenBao, for S0 step 1
fixtures/bin/up && fixtures/scenarios/s0 run  && fixtures/bin/down   # steps 1, 2, 3, 5 and the schema negative controls
fixtures/bin/up && fixtures/scenarios/s0 walk && fixtures/bin/down   # step 4's denied walk and the identity revocation
```

Step 1's suites run in a fresh clone of the commit the server image was built from, made with no git configuration but the clone's own and run with only the environment variables they need (locations, locale, Go, `BW_TEST_*`, proxies), with git's default attributes files, hooks, templates and mise's global and system configuration off, so nothing in the working tree or your git or mise configuration, and no other variable of yours, reaches what they test; it needs network access for `upstream-sync` and the site's Python packages. Each run writes its checks to `fixtures/.state/evidence/s0-<mode>-<utc>/s0.tsv`, the commands they ran with their exit statuses to `commands.tsv`, with the logs they read, and scans that directory for synthetic secrets. `down` deletes `.state`: copy the directory out first to keep it.
