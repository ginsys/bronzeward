# Talos v1.15.0-beta.1 first tests

**Evidence code for [ginsys/bronzeward#1212](https://github.com/ginsys/bronzeward/issues/1212). It is
not part of the implementation and is not maintained past the
[research report](../../docs/design/research/20261126-talos-115-beta-first-tests.md) it produces.**
No product module changes: the beta exists here only as patches.

Every command works from a clone that holds commit `cf597ab9`.

| Path | What it is |
|---|---|
| `patches/b-machinery-beta1.patch`, `c-machinery-1147.patch` | `go.mod` alone, zero context: the beta module, and v1.14.7 |
| `patches/2a-talostest-cosi.patch` | one call in the `internal/talos/talostest` test helper, adapted to the `cosi-project/runtime` the beta forces; one new in-memory state per namespace, as before |
| `patches/2b-contract-v114.patch` | measurement only: under a v1.15 machinery, v1.14 stays the supported contract, with the v1.14 Kubernetes window |
| `probe/*_probe_test.go.in` | the probes. The `.in` suffix keeps Go and lint away; they compile only after a run script copies one into an export's `internal/compile` |
| `run/build <a\|c\|b-raw\|2a\|2b> <dir>` | export the base commit into a new directory and apply that build's patches |
| `run/probe` | test 1: build A, C and B, run the compile probe, compare |
| `run/test2 <build> <build\|api\|rest>` | test 2: build or test one build; the tests need what the `go-db` job profile starts (PostgreSQL, OpenBao) |
| `run/live <talosctl dir> <new work dir>` | test 3: disposable Talos clusters in the host's Docker (read the header first: what it sends, writes and removes) |
| `run/new-fields.py <old module dir> <new module dir>` | the YAML keys one machinery module declares and another does not |
| `versions.env` | the live run's pins |

`go.sum` is not committed for the patched builds. `run/build` lets Go write it, requires checksum
database verification (`GOSUMDB=sum.golang.org`, nothing excluded, no `GOFLAGS` but `-v`), and
fails if that changes `go.mod`. A host that sets `GOPRIVATE` has to unset it for this, as
`run/live` does.

`run/probe` prints verdicts, digests, document kinds, key paths, YAML styles and the compiler's
redacted messages. It never prints configuration bytes or keys. All inputs are synthetic.
`run/live` leaves node configurations, which hold the destroyed cluster's secrets, in its work
directory: delete it after reading.
