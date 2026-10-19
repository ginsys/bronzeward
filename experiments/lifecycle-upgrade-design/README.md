# Upgrade source-path research fixture

This fixture for [#166](https://github.com/ginsys/bronzeward/issues/166) inspects
pinned public source files. It runs no upstream code and contacts no cluster.
It is not an upgrade implementation or a lifecycle behavior test.

## Reproduce

Requires Python 3.10 or later with its standard library. From this directory:

```sh
python3 source-check.py --fetch /tmp/bronzeward-upgrade-sources
python3 source-check.py /tmp/bronzeward-upgrade-sources
```

The first invocation downloads missing files from the exact Git commits in
`sources.json`. Both invocations verify every SHA-256 before inspecting the
source. Keep that cache outside the repository. Existing files are never
overwritten or refreshed automatically; a changed file fails the check.

The checks inspect installation/drain/reboot order, the prerelease API gate,
Kubernetes dry-run pre-pull and inventory paths, SSA field ownership, dependency
pins and the configuration guide's activation limits. The reversed synthetic
phase-order control must be rejected. These are string checks over reviewed,
hash-pinned files; they do not establish runtime behavior or exercise the
proposed recovery model.

To reproduce the hash-failure control, copy the cache to a disposable directory,
replace its `talos-upgrade` file with `synthetic corrupted source`, then run
`source-check.py` against the copy. It must exit 1 with
`FAIL talos-upgrade pinned hash`. Do not corrupt the source cache used by the
passing run.

## Recorded result

Python 3.13.5, 19 October 2026:

- Fresh fetch: exit 0; 11 download hashes and the 21 checks passed.
- Offline rerun: exit 0; 21 checks passed, retained in
  [`evidence/source-check.txt`](evidence/source-check.txt).
- Changed-source control: exit 1 as expected, before call-path checks; retained
  in [`evidence/hash-control.txt`](evidence/hash-control.txt).
- Retained logs contain only check names and outcomes. A scan found no private
  key block, provider-token field, client-key field or temporary control path.
  This fixture handles public sources and synthetic text only; it does not
  test the application's secret boundary.

The associated research note owns the interpretation and untested alternatives.
VM/hardware qualification, real upgrade/drain/reboot behavior, credential policy
and service-health implementations remain separate work.
