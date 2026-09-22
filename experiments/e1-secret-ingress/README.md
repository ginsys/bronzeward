# E1 secret-ingress experiment

**This is Phase-0 evidence code for [issue 2](https://github.com/ginsys/bronzeward/issues/2). It is
not the v1 implementation and it will not be maintained past the research report it produces.**

The [design](../../docs/design/Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts)
permits it in as many words: "Narrow prototypes are allowed to obtain that evidence."

## What it is for

Design §7.1 requires that known or operator-marked secrets be extracted **before any ordinary
plaintext persistence**, across import, drift adoption, draft updates, original YAML text, parsed
indexes, request logs, error reports and staging — and forbids retaining an observed configuration
in a plaintext draft to redact later, because backups and history would already hold it.

This program exists to make that claim testable against the
[investigation fixtures](../../fixtures/README.md), and to produce the evidence a research report
can cite. It answers a feasibility question. It does not establish how v1 will be built.

## Why its own module

`experiments/e1-secret-ingress` is a separate Go module on purpose. When an implementation module
appears at the repository root, its `./...` will not reach this code, so disposable evidence code
cannot be swept into the implementation by accident. `mise run go` names each experiment module
explicitly for the same reason.

## What it deliberately does not do

No upstream Talos composition, merge or typed validation. No reference resolution. No release,
plan, approval or dispatch. No SQLite, no age/SOPS, no rotation or retention. No migration
framework, no concurrency, no server.

It selects nothing: not the marked-secret syntax, not a secret or encryption provider, not a
database driver, not a deployment profile. Those decisions belong to their own work items and are
made from evidence, not from what this prototype happened to import.
