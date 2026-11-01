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
two files, in §12's record format:
[`internal/clitest/testdata/cases.yaml`](../internal/clitest/testdata/cases.yaml) for the read and
sign-in cases, and
[`internal/clitest/testdata/write-cases.yaml`](../internal/clitest/testdata/write-cases.yaml) for
the write cases. Its tests run exactly the cases recorded there.

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
| [PAGE-01](cases/PAGE-01.yaml) | web, the machine list: `mise run browser-auth` |
| [PAGE-02](cases/PAGE-02.yaml) | web, the machine list: `mise run browser-auth` |
| [REC-01](cases/REC-01.yaml) | web, the banner and the process's refusal: `mise run browser-auth` |
| [TLS-02](cases/TLS-02.yaml) | web: `mise run browser` |
| [TLS-03](cases/TLS-03.yaml) | web, the read case: `mise run browser`; the write case: `mise run browser-auth` |

The web accessibility passes of journeys §13 are not §12 cases; they are recorded per browser in
[accessibility-web.md](accessibility-web.md).
