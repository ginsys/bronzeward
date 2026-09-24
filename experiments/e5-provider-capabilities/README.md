# E5 provider-capability experiment

**This is Phase-0 evidence code for [issue 8](https://github.com/ginsys/bronzeward/issues/8). It is
not the v1 implementation and it will not be maintained past the research report it produces.**

## What it is for

[Design §7.3](../../docs/design/Talos_Configuration_and_Machine_Management_Design.md#73-secret-and-encryption-provider-candidates)
asks for an inventory before any provider is selected: secret creation, read and versioning,
artifact encryption, any signing needs, key custody, startup unlock, rotation, metadata-only checks,
backup/restore and migration. These scripts exercise that inventory, plus the permissions each
operation needs, against three candidates:

- **OpenBao** KV v2 and Transit, as the [investigation fixtures](../../fixtures/README.md) run it
- **a local age-backed store**, which the experiment builds itself under `fixtures/.state/data`
- **SOPS/age**, as an alternative store and as an import/export mechanism

Every operation is one *cell*: one command, run under one identity, with its expected outcome
written down before it runs. `run/lib.sh` records each cell as a row of `cells.tsv` with the
observed outcome, derived from the exit status and the output alone, and keeps the whole output as a
transcript. A cell whose observation differs from the expectation is recorded as a mismatch and the
run continues; mismatches are findings for the report, not failures to retry past.

Each row also carries a `kind`, the distinction acceptance criterion 2 asks for:

| kind | meaning |
|---|---|
| `primitive` | an encryption or storage primitive, with no version identity of its own |
| `versioned` | complete versioned secret or provider behaviour |
| `metadata` | an observation made from metadata alone, without the value |
| `unsupported` | the candidate has no such capability; named, never left blank |
| `-` | a procedural step (a seal, a crash, a snapshot) that the cells after it depend on; not a capability claim |

## Why shell, not Go

The issue's limits say the pinned `age` and `sops` are command-line versions and that a library
integration is not evidenced by these runs. Driving the pinned CLIs is therefore exactly the claim
being measured, and a program linking the libraries would measure something else.

## Running it

A capture needs a fresh fixture, and `E5_OUT` must name an empty absolute path on disk, outside this
checkout:

```sh
fixtures/bin/up
E5_OUT=<somewhere with ~100 MiB> experiments/e5-provider-capabilities/run/test-lib
E5_OUT=<the same directory>      experiments/e5-provider-capabilities/run/all
E5_OUT=<the same directory>      experiments/e5-provider-capabilities/run/collect-evidence
fixtures/bin/down
```

`run/test-lib` checks the cell recorder and `e5_sops`; it needs the `sops` and `age` binaries that
`fixtures/bin/up` caches, not a running fixture. `run/all` refuses an `E5_OUT` that already holds a
capture, and a fixture that still holds the local stores of an earlier one. `run/collect-evidence`
needs the fixture still up, because it rebuilds its refusal patterns from this run's credentials.

`E5_OUT` has no default, for the same reasons as E1's: an evidence bundle carries a PostgreSQL data
directory, a default under `/tmp` would put it in RAM on most systems, and one inside the checkout
would be removed by `fixtures/bin/down`. Deleting it afterwards is part of finishing the run.

No secret value is ever an argument. Values go to the CLIs over stdin, as the fixtures' own canary
does, because argv is readable by any local user.

## What it deliberately does not do

It does not classify retained, blocked, lost or unknown dependencies
([issue 9](https://github.com/ginsys/bronzeward/issues/9)), and it does not restore backups of
different ages in combination ([issue 10](https://github.com/ginsys/bronzeward/issues/10)). It shows
that each capability exists, what permission it needs, and one snapshot/restore round trip per
candidate.

It selects nothing: not a provider, not a physical layout, not a deployment profile. That decision
belongs to [issue 13](https://github.com/ginsys/bronzeward/issues/13).
