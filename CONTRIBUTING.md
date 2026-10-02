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

Every PR runs the `CI` workflow (documentation checks, the [documentation site](#documentation-site) build, `actionlint`, the Go build, vet, format and test checks, `go-db` (the root module's tests against PostgreSQL and OpenBao service containers), conventional-commit subjects, action pins; aggregated as the `checks` context), a Codex review and an advisory Claude review (`PR Review`). Review threads must be resolved before merge. Merges are rebase-only through the merge queue: after review, the owner runs `gh pr merge --auto` and the queue lands the PR once `checks` and `PR Review` report on the merged result. Do not claim CI success without an actual run result. Review and merge are separate actions.

## Documentation checks

With Python 3.9 or newer and PyYAML 6 installed in your Python environment, run from the repository root:

```sh
python3 scripts/verify-docs.py
git diff --check
git diff --cached --check
```

With [mise](https://mise.jdx.dev/) installed and `origin/main` fetched, `mise run verify` runs everything the `CI` workflow runs except `go-db`: the documentation check, the documentation site build, `actionlint` and `shellcheck`, the commit-lint fixture tests, the whole-tree whitespace check, the conventional-commit check on this branch's commits, the [Go checks](#go-code) and the action-pin check (which clones go-kure/.github into the gitignored `upstream/`). `go-db` needs a database and an OpenBao: run `mise run dev-db` and `mise run dev-bao`, then `mise run go-db`.

The verifier reads repository files as UTF-8 and reports file locations relative to the repository root. It checks root guidance and Markdown under `docs/spec/`: inline relative links and anchors, local equivalents of this repository's `blob/main` document links, balanced fenced blocks and trailing whitespace. It also checks issue-form YAML, field names/types/requiredness, disabled blank issues and the CLAUDE delegation.

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

The implementation is the root Go module (`cmd/`, `internal/`); the Go version comes from `mise.toml`. Its `go.mod` ignores `experiments/`, whose modules are Phase-0 evidence and are checked separately. From the repository root:

```sh
mise run go                                        # format, go.mod tidiness, build, vet and test checks (per module, as mise.toml lists them); database tests skip
mise run dev-db                                    # a disposable development PostgreSQL on 127.0.0.1:55433
mise run dev-bao                                   # a disposable dev-mode OpenBao on http://127.0.0.1:58201, root token bw-dev-root-token
mise run dev-down                                  # stop both, which removes them and their data (shared by every worktree on the host)
mise run go-db                                     # the root module's tests against PostgreSQL and OpenBao (BW_TEST_PG_DSN and BW_TEST_BAO_ADDR/BW_TEST_BAO_TOKEN, else dev-db's and dev-bao's); a missing service fails
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

`examples/bronzeward.yaml` is the development configuration, matching `mise run dev-db`. A deployment writes its own. `mise run dev` keeps the issuer's synthetic key in the gitignored `.dev/oidc.key` and prints a `mint` line that uses it. The server refuses to start unless the database holds exactly its migrations (run `migrate` first). It listens on plain http: clients reach the API over HTTPS terminated in front of the server, and the hop behind that is the deployment's choice ([persistence-api.md §9.1](docs/spec/persistence-api.md#91-conventions)). Every request but `GET /livez` authenticates. `GET /api/v1/acts`, `POST /api/v1/identity-revocations`, inventory (`POST` and `GET` on `/api/v1/clusters` and `/api/v1/machines`, with their items, and a machine's Talos endpoint replacement, `POST /api/v1/machines/{id}/talos-endpoints`), draft creation and reading (`POST` and `GET` on `/api/v1/drafts`, with its items), ingestion (`POST /api/v1/ingestions` with `source: document` and no declared references, `GET /api/v1/ingestions/{id}` and `POST /api/v1/ingestions/{id}/abandonments`) and `GET /api/v1/operations/{id}` are served. An ingestion start needs `provider` and `ingestion` configured, and answers `503` otherwise; every server sweeps due staging claims once at startup and then every `ingestion.sweep`, or every minute without an `ingestion` block, since another instance on the same database may have left claims behind. Every other route of the API contract, `/marks` and `/takeovers` included, is routed and role-checked, and answers `501 not-implemented` until the issue that owns it lands. `internal/auth` and `internal/api` are exercised by `mise run go-db`, as are `internal/provider`'s policy tests: they run the committed fixture policies (`fixtures/openbao/policies`) against dev-mode OpenBao through child tokens, not the fixture's own server or tokens.

## Running S0 locally

Acceptance-plan scenario S0 runs against the [fixtures](fixtures/README.md), which need Docker, not CI's runner. Each mode starts from a fresh fixture:

```sh
mise run dev-db && mise run dev-bao           # go-db's database and OpenBao, for S0 step 1
fixtures/bin/up && fixtures/scenarios/s0 run  && fixtures/bin/down   # steps 1, 2, 3, 5 and the schema negative controls
fixtures/bin/up && fixtures/scenarios/s0 walk && fixtures/bin/down   # step 4's denied walk and the identity revocation
```

Step 1's suites run in a fresh clone of the commit the server image was built from, made with no git configuration but the clone's own and run with only the environment variables they need (locations, locale, Go, `BW_TEST_*`, proxies), with git's default attributes files, hooks, templates and mise's global and system configuration off, so nothing in the working tree or your git or mise configuration, and no other variable of yours, reaches what they test; it needs network access for `upstream-sync` and the site's Python packages. Each run writes its checks to `fixtures/.state/evidence/s0-<mode>-<utc>/s0.tsv`, the commands they ran with their exit statuses to `commands.tsv`, with the logs they read, and scans that directory for synthetic secrets. `down` deletes `.state`: copy the directory out first to keep it.
