# Architecture

How the Bronzeward code is laid out, as it is on `main`. For what it must do and why, read the design and the specification, linked below.

## 1. What this is

Bronzeward is a server and a command-line client that keep the configuration of Talos Linux machines under change control: import it, edit it, publish it, approve it, apply it, and watch for drift. The [main design](docs/design/Talos_Configuration_and_Machine_Management_Design.md) owns the architecture and the product constraints; this file describes how the code is laid out to meet them.

## 2. Map

"Must not" is stated only where an import fact or a test shows it (section 4).

| Path | Owns | Must not |
| --- | --- | --- |
| `cmd/bronzeward` | The `serve` command, and `migrate`, `token`, `orphans` and `version` | |
| `cmd/bronzewardctl` | The operator's command line, over the HTTP API | Link a server package, except `internal/talos/discover` |
| `api/` | `openapi.yaml`, the HTTP contract | |
| `web/` | The browser console (Vite). `web/src` holds page components (`.tsx`) and logic (`.ts`). Reads of the Bronzeward API go through `request.ts`, writes through `write.ts`; sign-in (`auth.ts`, `userinfo.ts`) calls the identity provider directly | |
| `fixtures/`, `conformance/`, `packaging/` | A disposable test environment, the shared client test cases, packaging inputs | |
| `internal/api` | The HTTP API: sign-in checks, roles per route, idempotency, transactions, and the background loops (import runner, publish worker, executor, adoption, drift) | Be imported by any package but `cmd/bronzeward` |
| `internal/server` | Builds the HTTP handler | |
| `internal/auth` | Sign-in tokens for people (OIDC) and for services; stores only token digests | |
| `internal/config` | Settings held outside the database | |
| `internal/database` | Opening PostgreSQL | Import another internal package; be imported by any package but `cmd/bronzeward` and `internal/dbtest` |
| `internal/migrate` | The schema: one embedded migration | Import more than `id` |
| `internal/staging` | A staging claim: one import's hold on a draft. It is fenced: each write names the owner and its generation (a number raised at each takeover), and when a newer server instance takes over, the older one's writes are refused | Import another internal package |
| `internal/ingest` | The pure part of an import: parse, find secrets, replace them by references | Import `api`, `staging` or `database` |
| `internal/compile` | Builds one machine's configuration from resolved inputs | Import `api`, `staging` or `database` |
| `internal/provider` | The OpenBao client: one role type per identity | Import more than `classify` and `id` |
| `internal/classify` | The one procedure that judges a dependency | Import another internal package |
| `internal/monitor`, `internal/orphans` | The dependency monitor's passes; a report of provider values nothing refers to | |
| `internal/talos` | Reading a node's configuration; the one apply call (`apply.go`), made through the Talos machinery (Sidero's Go client library, `github.com/siderolabs/talos/pkg/machinery`) | Let any other file call the machinery's mutating methods |
| `internal/talos/discover` | Reading a cluster's identity from a talosconfig | Write to a node |
| `internal/talos/signer` | Turning a first key (a document that holds the cluster's Talos CA key, or lets the server fetch it) into that key | Import beyond its allowlist |
| `internal/client`, `internal/client/apitypes` | The command line's request core; types generated from `openapi.yaml` | Link a server package |
| `internal/id`, `internal/machinename`, `internal/clusterid`, `internal/textdiff` | Identifier format; the machine-name grammar; the standard spelling of a cluster ID; the plan's text diff | Import another internal package |
| `internal/seam`, `internal/baotest`, `internal/dbtest`, `internal/clitest`, `internal/talos/talostest` | Test support: an interruption point, a real OpenBao, a database per test, the command line against a server (tests only), a stand-in node | |

## 3. How data moves

A file named without a directory is in `internal/api`.

**Connect a cluster and import its machines.**

1. `bronzewardctl cluster onboard` reads the cluster's identity from the operator's talosconfig with `internal/talos/discover`. (The console uses `POST /discoveries`.)
2. It records the cluster and each machine (`POST /clusters`, `POST /machines`).
3. It sends the cluster's first key (`firstkeys.go`): a control-plane machine configuration, a secrets bundle or, by default, an admin talosconfig, from which `internal/talos/signer` reads a control plane's configuration. The signer extracts the CA key and tries it at a node, and `internal/provider` stores it.
4. It opens a draft and starts one import per machine (`ingestions.go`): the server takes a staging claim and hands the job to the runner after the commit.
5. The runner (`runner.go`) reads the node, `internal/ingest` replaces secrets by references, `internal/provider` stores each value, and one transaction records the machine's import base (the imported configuration the draft starts from) in the draft and ends the import. `internal/ingest` also removes a `HostnameConfig` whose hostname equals the machine's name, which the compiler fills again, and the draft transaction refuses `409 stale-input` if the machine was renamed while the import ran.

**Edit and publish a release.**

1. Fragments, profiles and assignments are changed in a draft (`drafts.go`, `fragments.go`).
2. `POST /drafts/{id}/publications` queues a publish operation.
3. The publish worker (`worker.go`) claims it under the server's owner generation.
4. `buildRelease` (`pipeline_build.go`) compiles each machine with `internal/compile` and encrypts the artifacts. It also refuses a release in which two machines would get one Kubernetes node name (`hostname_clash.go`), and `publishCommit` refuses a machine that would take the name another machine's last applied release still records (`hostname_swap.go`).
5. `publishCommit` (`publish.go`) writes the release and moves each machine's desired configuration in one transaction; a worker that lost its claim writes nothing.

**A plan to an applied configuration.**

1. `POST /plans` records a plan for one machine, with a diff from `internal/textdiff`.
2. `POST /plans/{id}/approvals` approves it.
3. The executor loop (`executor.go`) dispatches each approved plan (`dispatch.go` checks again and decrypts the artifact).
4. `sender.go` is the only file that dials a client able to apply; it calls `apply.go` in `internal/talos`.
5. The server observes the node again (`observe.go`) and classifies the outcome (`outcome.go`); the drift loop (`drifter.go`) keeps observing.

## 4. Rules the code keeps

| Rule | Enforced by |
| --- | --- |
| The command line links no server package, except `internal/talos/discover` | `TestClientClosureExcludesServerPackages`, `internal/client/closure_test.go` |
| Only one call in `internal/talos/apply.go` can apply a configuration; the machinery's client, machine API and COSI state packages are imported only under `internal/talos`; the apply client is dialled only from `internal/api/sender.go` | `TestMachineryCallsAllowlisted`, `TestMachineryImportedOnlyHere`, `TestDialApplierCallers`, `internal/talos/guard_test.go` |
| Cluster discovery only reads | `TestDiscoverReadsOnly`, `internal/talos/discover/discover_test.go` |
| The signer's imports are an allowlist | `TestImportsAreAllowlisted`, `internal/talos/signer/guard_test.go` |
| An immutable table refuses update, delete and truncate | trigger `refuse_mutation` in `internal/migrate/migrations/0001_schema.sql`; `TestImmutableTablesRefuse`, `internal/migrate/install_test.go` |
| A machine's and a cluster's identity cannot change | trigger `identity` in the same schema; `TestPlanImmutableTables`, `internal/migrate/plan_test.go` |
| A plan moves only along the allowed states, and an approval counts only in its epoch (a fresh identifier a restore creates; approvals made under an earlier one no longer count) | trigger `refuse_plan_state_transition` in the same schema; `TestPlanStateTransitions`, `internal/migrate/plan_test.go` |
| A secret value is never rendered, marshaled or quoted: the compiled configuration holds it as plaintext but prints a placeholder and refuses to marshal, and a provider error never quotes it | `TestMaterializedNeverRenders`, `internal/compile/compile_test.go`; `TestRedactedTextGuards`, `internal/compile/render_test.go`; `TestErrorsNeverQuoteThePlaintext`, `internal/provider/transport_test.go` |
| Only a token's digest is stored | `TestIssueStoresOnlyTheDigest`, `internal/auth/tokens_test.go` |
| Each route's roles and human-only flag match the specification and the OpenAPI description | `TestRouteTable`, `internal/api/router_test.go`; `TestDescriptionMatchesRouteTable`, `internal/api/openapi_test.go` |
| An additive change to the API raises `apiRevision` by one and adds its row to the revision table in `docs/spec/persistence-api.md` | `TestAPIRevisionIsSpecified`, `internal/api/server_test.go`, fails when the table and the constant differ. That a change bumps it at all: by review only (`internal/clitest/core_test.go` expects the revision in the `version` output, so a bump without that update fails) |
| An owner's statement on a staging claim is refused unless owner, generation, epoch, state and lease all hold | `TestOwnerFenceMatrix`, `internal/staging/claim_test.go` |
| No package may import a package its row in section 2 forbids | by review only |

## 5. Boundaries

| What crosses | Defined in |
| --- | --- |
| The HTTP API, for the command line and the console. It carries a revision, `apiRevision` in `internal/api/server.go`, and the command line refuses a server below `MinRevision` in `internal/client/client.go` | `api/openapi.yaml` |
| The database. Bronzeward is unreleased: until a first release the schema is this one migration, edited in place, and a database made by an earlier version of it is refused at startup and recreated | `internal/migrate/migrations/0001_schema.sql` |
| The secret provider (OpenBao) | `internal/provider` |
| The identity provider (OIDC) | `internal/auth` |
| The Talos API | `internal/talos` |

## 6. Where to start reading

1. `cmd/bronzeward/main.go`: the server's commands.
2. `internal/api/api.go`: what the API does for every request.
3. `internal/api/publish.go`: one write path, from the worker to the transaction.
4. `internal/migrate/migrations/0001_schema.sql`: the tables and the rules the database keeps.
5. `internal/talos/apply.go`: the one place a machine is changed.

A pull request that changes what the map, the flows, the rules or the boundaries say updates this file in the same pull request (see [CONTRIBUTING.md](CONTRIBUTING.md#change-and-review-workflow)). Nothing checks this file against the code except that review.
