# Conformance cases

The shared conformance cases of the [client contracts §12](../docs/spec/client-contracts.md#12-shared-conformance-cases),
one record per case in `cases/<ID>.yaml`, in §12's record format: `id`, `setup`, `action`,
`exchange`, `fault` and `expect`, with each `exchange` item a mapping of `request` and `headers`
so that the file parses. The table in §12 stays the authority for each case's wording; a record
restates it for the check that runs it and adds one member:

- `runs`: per client (`web`, `cli`), the check that runs the case, the browsers or platforms it
  runs on and, when the check covers only part of the case, the part it covers.

A client change adds the records of the cases it runs, and extends a record when it runs another
part of the case. A case without a record has not run.

The table lists the web client's records. The records of the cases that `bronzewardctl` runs are in
the `*-cases.yaml` files and `cases.yaml` in
[`internal/clitest/testdata`](../internal/clitest/testdata), in §12's record format: `cases.yaml`
for the read and sign-in cases, and one file per command family for the others, such as
`write-cases.yaml`, `recovery-cases.yaml` and `recovery-scope-cases.yaml`. Each test runs exactly the
cases recorded in its file. `recovery-scope-cases.yaml` holds AMB-05, AMB-07 and AMB-12 with
`recovery scope-account` as the dropped write.

A web case that needs a real release or plan asks for a seeded deployment with
`POST /disposable?seed` on the browser harness's control server, and reads the identifiers from
`GET /seeded`. `mise run browser-auth` compiles `TestE2ESeed` of `internal/api` for this. The test
writes, with the helpers the API tests use, the target release and one plan in each state a plan
page shows (proposed, approved, committed, expired, cancelled, revoked), and `GET /seeded` names
them with the committed plan's machine and the other plans' machine. The committed plan has an
operation with its first attempt. The helpers also write supporting records that `GET /seeded`
does not name, such as the release the plans are applied from. Both routes belong to the harness process, and the server
has none for tests. The seed is written after the deployment's controller has started, an order
that `TestE2ESeedSurvivesARunningController` proves leaves the records as they are; the harness
header in `web/e2e/auth.conf.ts` has the details.

| Record | Runs |
| --- | --- |
| [AUTH-01](cases/AUTH-01.yaml) | web, sign-in and the server read: `mise run browser-auth` |
| [AUTH-02](cases/AUTH-02.yaml) | web: `mise run browser-auth` |
| [AUTH-03](cases/AUTH-03.yaml) | web: `mise run browser-auth` |
| [AUTH-05](cases/AUTH-05.yaml) | web: `mise run browser-auth` |
| [AMB-01](cases/AMB-01.yaml) | web, as a keyed repeat: `mise run browser-auth` |
| [AMB-02](cases/AMB-02.yaml) | web, the in-progress wait and the replay: `mise run browser-auth`; the bound and a published draft's `409`: `mise run web` |
| [AMB-03](cases/AMB-03.yaml) | web: `mise run browser-auth` |
| [AMB-05](cases/AMB-05.yaml) | web: `mise run browser-auth` |
| [AMB-06](cases/AMB-06.yaml) | web: `mise run browser-auth` |
| [AMB-07](cases/AMB-07.yaml) | web: `mise run browser-auth` |
| [AMB-08](cases/AMB-08.yaml) | web, an injected 503: `mise run browser-auth` |
| [AMB-09](cases/AMB-09.yaml) | web: `mise run browser-auth` |
| [AMB-12](cases/AMB-12.yaml) | web, the first attempt never committed: `mise run browser-auth` |
| [AUTH-07](cases/AUTH-07.yaml) | web, the change shown and a pending write set aside: `mise run browser-auth` |
| [AUTH-08](cases/AUTH-08.yaml) | web, the client's half: `mise run browser-auth` |
| [AUTH-10](cases/AUTH-10.yaml) | web: `mise run browser-auth` |
| [AUTH-11](cases/AUTH-11.yaml) | web: `mise run browser-auth`; the tab protocol in `mise run web` |
| [COMP-01](cases/COMP-01.yaml) | web: `mise run browser-auth` |
| [COMP-02](cases/COMP-02.yaml) | web: `mise run browser-auth` |
| [ETAG-01](cases/ETAG-01.yaml) | web, a fragment entry: `mise run browser-auth` |
| [ETAG-02](cases/ETAG-02.yaml) | web, an import's draft: `mise run browser-auth` |
| [PAGE-01](cases/PAGE-01.yaml) | web, the machine list: `mise run browser-auth` |
| [PAGE-02](cases/PAGE-02.yaml) | web, the machine list: `mise run browser-auth` |
| [PROT-01](cases/PROT-01.yaml) | web, the browser journey: `mise run browser-auth` with `BW_SPECS=protected` |
| [PROT-03](cases/PROT-03.yaml) | web, the three refusals: `mise run browser-auth` with `BW_SPECS=protected` |
| [PROT-04](cases/PROT-04.yaml) | web: `mise run browser-auth` with `BW_SPECS=protected` |
| [PROT-05](cases/PROT-05.yaml) | web: `mise run browser-auth` with `BW_SPECS=protected` |
| [PROT-06](cases/PROT-06.yaml) | web, cases 1 to 4, case 4 on the interrupt build's draft-commit stall (ginsys/bronzeward#366): `mise run browser-auth` with `BW_SPECS=protected` |
| [REC-01](cases/REC-01.yaml) | web, the banner, the process's refusal and a write's scope refusal: `mise run browser-auth` |
| [REC-02](cases/REC-02.yaml) | web, both refusals of the entry: `mise run browser-auth` |
| [REC-03](cases/REC-03.yaml) | web, plan creation on a real recovery start and an approval's scope refusal: `mise run browser-auth` |
| [REC-04](cases/REC-04.yaml) | web, (a) the dropped `201` and (b) the server's lost COMMIT reply, on the interrupt build (ginsys/bronzeward#366): `mise run browser-auth` |
| [TLS-02](cases/TLS-02.yaml) | web: `mise run browser` |
| [TLS-03](cases/TLS-03.yaml) | web, the read case: `mise run browser`; the write case: `mise run browser-auth` |

The web accessibility passes of journeys §13 are not §12 cases; they are recorded per browser in
[accessibility-web.md](accessibility-web.md).

## Browsers

Every web record above runs in the browsers its `browsers` list names, by `mise run browser` or
`mise run browser-auth`, in three workflows (decision B6 of ginsys/bronzeward#255):

| Workflow | Browsers | Required |
| --- | --- | --- |
| `ci.yml` | Chrome and Edge as the runner has them, Firefox stable and Firefox ESR | yes |
| `previous-majors.yml` | the previous major of Chrome, Edge and Firefox, pinned below | no: daily on main, on request, and on a pull request that changes its files |
| `safari.yml` | Safari | no: on main, daily and on request |

`previous-majors.yml` is outside the required checks (ginsys/bronzeward#893): three more
`browser-auth` legs would add about 45 runner-minutes to each of the three runs a change gets and put
three vendors' download servers in the merge path. The builds are pinned in
[`web/e2e/previous-majors.json`](../web/e2e/previous-majors.json), each from the vendor's own source:

| Browser | Build | Source |
| --- | --- | --- |
| Chrome | 156.0.8078.80 | Chrome for Testing, downloaded by WebdriverIO |
| Edge | 156.0.4314.58 | Microsoft's package repository, the `.deb` checked against its pinned sha256; the driver at the same version |
| Firefox | 159.0.1 | Mozilla's release archive, downloaded by WebdriverIO |

The run on 19 November 2026 ([run 44437080904](https://github.com/ginsys/bronzeward/actions/runs/44437080904)),
passed on all three: `mise run browser` (both specs in each browser, 6 of 6 spec files) and `mise run browser-auth` (14 of
14 spec files in each browser), with the artifact scan's planted control finding each planted value in
every job. The log shows the Edge and its driver at 156.0.4314.58. Every previous major was obtainable,
so none is recorded as untested. The jobs took 1 min 30 s for `browser` and 14 min 40 s to 15 min 34 s for each
`browser-auth` leg.

A failed run on main opens one issue, "Previous-major browsers run failed on main" (marker
`previous-majors-main-failure`, labels `type/implementation` and `area/ci`), and a later failure comments on it. The
pins are moved by hand until ginsys/bronzeward#907 adds a tool: a pin more than one major behind still
passes against an older browser, and a green run does not say so. Not run: the previous release of
Firefox ESR, and Safari's previous major.
