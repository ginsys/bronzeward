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

| Record | Runs |
| --- | --- |
| [AUTH-01](cases/AUTH-01.yaml) | web, sign-in and the server read: `mise run browser-auth` |
| [AUTH-02](cases/AUTH-02.yaml) | web: `mise run browser-auth` |
| [AUTH-03](cases/AUTH-03.yaml) | web: `mise run browser-auth` |
| [AUTH-05](cases/AUTH-05.yaml) | web: `mise run browser-auth` |
| [AUTH-07](cases/AUTH-07.yaml) | web, the change shown with no write pending: `mise run browser-auth` |
| [AUTH-08](cases/AUTH-08.yaml) | web, the client's half: `mise run browser-auth` |
| [AUTH-10](cases/AUTH-10.yaml) | web: `mise run browser-auth` |
| [AUTH-11](cases/AUTH-11.yaml) | web: `mise run browser-auth`; the tab protocol in `mise run web` |
| [TLS-02](cases/TLS-02.yaml) | web: `mise run browser` |
| [TLS-03](cases/TLS-03.yaml) | web, the read case: `mise run browser` |
