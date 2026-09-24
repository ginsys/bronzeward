# E5 key-loss and restoration experiment

**This is Phase-0 evidence code for [issue 10](https://github.com/ginsys/bronzeward/issues/10). It is
not the v1 implementation and it will not be maintained past the research report it produces.**

## What it is for

[Design §7.5](../../docs/design/Talos_Configuration_and_Machine_Management_Design.md#75-rotation-and-retention-contract)
and [§14](../../docs/design/Talos_Configuration_and_Machine_Management_Design.md#14-reliability-high-availability-and-disaster-recovery)
keep two recovery paths apart: applying a release's retained encrypted artifact, and regenerating
the configuration from its source versions. These scripts put the three backup families the
[investigation fixtures](../../fixtures/README.md) snapshot independently into losses and restored
combinations of different ages, and try both paths each time:

- **the management database** (PostgreSQL, `db-snapshot`/`db-restore`) holds the release records:
  source path and version, key name, the stored ciphertext and the artifact's digest;
- **the provider** (OpenBao Raft, `bao-snapshot`/`bao-restore`) holds the KV source versions, the
  Transit keys and the tokens it has issued;
- **the local credential store** (`.state/data`, `store-snapshot`/`store-restore`) holds the
  compiler's and the executor's tokens.

`run/scenario` builds three generations. The first two are snapshotted in all three families; the
third never is. Then each case sets one loss or one restored combination, and `run/release`
judges every release it names on two separate rows. `apply` decrypts the stored artifact with the
executor's credential and compares it to the recorded digest. `regen` reads the source version with
the compiler's credential, renders it, compares the digest and encrypts it without storing anything.
A third row, `check`, is the recovery classification (`applicable`, `regenerable`, `blocked`,
`absent`). It stores no release, dispatches nothing and shows that the release table did not
change. It is not read-only at the provider: its regeneration step sends a real Transit encrypt,
which Transit turns into a key creation for an identity holding `create`. The scenario compares
the provider's Transit key state before and after every check and records a change as the class
`provider-changed`.

Every observation is one row of `verdicts.tsv`: the command, the identity, the expected outcome
written down before it runs, the observed outcome and the state bundle that is its ground truth. A
row whose observation differs from the expectation is recorded as a mismatch and the run continues.

## Running it

A capture needs a fresh fixture. `KL_OUT` must name an empty absolute path on disk, outside this
checkout: each state gets its own evidence bundle.

```sh
fixtures/bin/up
KL_OUT=<somewhere on disk> experiments/e5-key-loss-restoration/run/all
KL_OUT=<the same directory> experiments/e5-key-loss-restoration/run/collect-evidence
fixtures/bin/down
```

`run/all` runs `run/scenario`, takes a final bundle and checks the leak scan of every bundle: the
fixture's positive control must be found in each, and nothing else. It refuses a `KL_OUT` that
already holds a capture, and a fixture that already holds the release table or the credential
store. `run/collect-evidence` needs the capture's own fixture still up: it rebuilds the refusal
patterns from the fixture's credentials, compares them with the fixture's pattern file, and compares
that file with the digest `run/all` recorded, so a fixture recreated since the capture is refused. It
copies the text of the capture into `evidence/` and leaves the
bundles' database dumps, snapshots and expanded store archives behind. Those archives hold the
management tokens, and so do the copies of the bundles under `KL_OUT`: delete `KL_OUT` once the
evidence is collected.

No secret value is ever an argument. Values go to the CLIs over stdin, a rendered artifact exists
only in a temporary file for the one call that needs it, and every decryption is reduced to a
digest verdict before it reaches a transcript. Tokens are passed through the environment and
never printed; only their accessors are.

## What it deliberately does not do

It does not classify retention from metadata; that is
[issue 9](https://github.com/ginsys/bronzeward/issues/9). It runs one provider (OpenBao) and one
database (PostgreSQL), as the fixtures do, and selects neither. It sets no retention window and no
restoration policy: those belong to [issue 15](https://github.com/ginsys/bronzeward/issues/15) and
[issue 19](https://github.com/ginsys/bronzeward/issues/19). The provider restores are Raft
snapshots of the same cluster, which keep its unseal key; restoring onto a new cluster is not
exercised.
