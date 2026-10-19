# Publication secret-reference cluster scope

Evidence for [ginsys/bronzeward#244](https://github.com/ginsys/bronzeward/issues/244),
19 October 2026. Found during the R5 audit for
[ginsys/bronzeward#241](https://github.com/ginsys/bronzeward/issues/241).

## Contract and finding

[Compilation §5.1](../../spec/compilation.md#51-syntax) and §13 require a
machine's compilation to refuse a secret reference belonging to another cluster.
At `ab327884527fee3b3ed32f0e2dfc8ffde5bf0ce3`, publication parsed the generation
path without comparing its cluster with the publication's cluster.

The supported authoring API already filters carried references by cluster.
The diagnostic intentionally supplies an inconsistent stored source; it does
not establish a remotely reachable cross-cluster read or assume a hostile
database writer is a supported actor. It tests the publication boundary's own
contract.

## Repair and checks

Publication now checks every generation's cluster while assembling the pinned
references, before any metadata classification, value read or artifact creation.
A mismatch uses the existing `422 validation-failed` mechanism and identifies
only the source revision and reference name. The worker records a failed
operation and terminal event, retains its publication act, leaves the draft open
and creates no release. No public endpoint, schema or credential policy changes.

`TestReadPinnedClusterScope` covers import-base and fragment sources, with a
valid source before the tested source and a valid reference before the foreign
reference inside that source. Each refusal requires zero provider calls and no
resolved values or dependency statuses. Same-cluster controls resolve all three
names across both sources with one deduplicated read. The worker test
`TestPublishWorkerRefusesForeignGeneration` seeds an inconsistent immutable
fragment, submits publication through the API, then checks the durable outcome,
zero provider calls and no synthetic value in the problem or logs.

## Recorded diagnostic

The tests first ran through Go's temporary source overlay on the main revision
above, with isolated test databases in the development PostgreSQL instance and
synthetic provider responses. The unchanged production source is also the
removed-guard control:

| Source | Expected and observed result |
| --- | --- |
| Original main plus the new tests | Exit 1: both foreign-reference cases performed two metadata and two value reads instead of refusing; both same-cluster controls passed. The worker succeeded, recorded `queued, claimed, succeeded` and made 23 provider calls. |
| Same tests plus the scope guard | Exit 0: all four source/scope cases and the actual worker refusal passed. Existing successful publication, retained/empty pinned inputs and ordinary publication-worker checks also passed. |

The final diagnostic test file's SHA-256 is
`41ee8f2a37e2694f84054308eaaa5bf538bed657529291590e2405669e097cf0`;
the guarded production file's is
`23a89c5b35f9c510f91fd1d617a9328e58c071e3777d86c6faaaaadbf40c1006`.
The retained red and green logs have SHA-256
`974e719d6ae88d1c11d897b79fb025515699029a56fe8e743a70ae4ed26677b0`
and `cbddb439bfb5aae86ed353ea8ac44b00f671240b95df99c7edd7e482e0903940`.
The earlier single-source diagnostic and its original source remain retained;
the results above include the stronger source-order check.

The diagnostic environment uses Go 1.27.1 on linux/amd64, PostgreSQL 17.11 and
OpenBao 2.6.1 (`ba7ad8861d0578cd4da4f7b9e5a6756d30484f8f`), with versions read
back on 19 October 2026. The two new tests use stand-in providers; OpenBao is
used by the broader database/provider task, not by those diagnostic cases.

## Committed-branch evidence

The following checks ran at
`032b5433a6ad39a8ff62fdcba411b9c1b964a89e`, with `TMPDIR` and `GOTMPDIR`
set to the retained campaign's disposable Go storage. The checkout was unchanged
during execution. The environment and provider versions were those recorded above.

| Check | Exit | Observed result | Retained log SHA-256 |
| --- | --- | --- | --- |
| `mise run go-db` | 0 | All 21 packages with tests passed; two packages had no test files. | `e6e57707669d303c6acf785c02dee3d102860dfe5e6506841817a74bf4ce1332` |
| Focused committed cases and explicit skip inventory | 0 | All four source/scope cases, the worker refusal and `TestTalosAPIMethods` passed; the latter classified 75 methods across ten services. Six environment-dependent tests explicitly skipped, as detailed below. | `fbcc61bd2d915464bd6f3cbe2053846504917fbc8ebeabb7d48b03d0e5e2f768` |
| Committed tests with the original production file overlaid | 1, expected | Both same-cluster controls passed. Both foreign-reference cases failed after two metadata and two value reads; the worker succeeded with 23 provider calls instead of refusing. | `ef5463860aab85949354ea3e4fdd4a57822cd9ee18613f797dc260f8f203662b` |

The focused command used `go test -v -count=1 -p=1 -parallel=1 -timeout=3m`
through `mise exec`, with the development database variables from the reproduction
below, against `./internal/api ./internal/compile ./internal/talos`. Its anchored
test selection comprised the two new tests, `TestTalosAPIMethods`,
`TestMachineryGate`, `TestSensitivityGate`, `TestLiveApply`, `TestLiveRead`,
`TestLiveRoleProbe` and `TestLiveUnreachable`. The machinery and sensitivity gates
require the dedicated `experiments/c10-machinery-gate/run/all` environment. The
four live tests skipped because `BW_TEST_TALOSCONFIG` and
`BW_TEST_TALOS_ENDPOINT` were explicitly unset. These checks made no live Talos
request and do not replace the separately retained fixture evidence.

An earlier attempt to collect the full task as JSON used `GOFLAGS=-json` and
exited 1. Its only failing test was `TestTalosAPIMethods`: its nested
`go list -m -f` inherited that flag and failed with
`go list -f cannot be used with -json`. The command failed independently with
the flag and succeeded without it; the normal repository task above then passed.
The failed run remains retained, SHA-256
`36ea1651c06eb0642464a05be6e34e97750f8ba098337823bcd0651040f23a71`.
It is not counted as a passing suite. The PR records final candidate verification
separately from these revision-bound database/provider results.

## Reproduce and limits

Use the repository's development database/provider setup and `mise run go-db`.
The two named tests above use isolated databases and stand-in providers; the
task also runs the repository's real-provider checks. To reproduce the failing
control from the committed repair checkout, retain its tests and overlay the
original production file. The temporary directory holds public source and the
overlay description; this does not edit the checkout:

```sh
scope_tmp=$(mktemp -d)
git show ab327884527fee3b3ed32f0e2dfc8ffde5bf0ce3:internal/api/pipeline_deps.go > "$scope_tmp/pipeline_deps.go"
python3 - "$scope_tmp" <<'PY'
import json
from pathlib import Path
import sys
scratch = Path(sys.argv[1])
source = Path.cwd() / "internal/api/pipeline_deps.go"
(scratch / "overlay.json").write_text(json.dumps({
    "Replace": {str(source): str(scratch / "pipeline_deps.go")}
}))
PY
BW_REQUIRE_PG=1 \
  BW_TEST_PG_DSN='postgres://bronzeward@127.0.0.1:55433/bronzeward?sslmode=disable' \
  mise exec -- go test -v -count=1 -p=1 -parallel=1 -timeout=3m -overlay="$scope_tmp/overlay.json" \
  -run '^(TestReadPinnedClusterScope|TestPublishWorkerRefusesForeignGeneration)$' ./internal/api
```

The final command must exit 1 with the foreign-reference and worker assertions
failing; the same-cluster controls must pass. Removing the `-overlay` argument
must exit 0. Keep `TMPDIR` and `GOTMPDIR` on suitable disposable storage for
both commands. Do not run these controls by modifying a running fixture checkout.

This is compilation-stage refusal evidence, not a multi-tenant isolation,
production-readiness or arbitrary-database-corruption claim. R5's complete
clause mapping remains with #241; the independent PoC owner assessment remains
with #31.
