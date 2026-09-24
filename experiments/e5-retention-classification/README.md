# E5 retention-classification experiment

**This is Phase-0 evidence code for [issue 9](https://github.com/ginsys/bronzeward/issues/9). It is
not the v1 implementation and it will not be maintained past the research report it produces.**

## What it is for

[Design §7.6](../../docs/design/Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks)
classifies every secret or key version a release depends on as retained, blocked, lost or unknown,
from metadata alone. These scripts put each candidate's dependencies into those states and judge a
classifier against the provider's own view of each state:

- **OpenBao** KV v2 and Transit, as the [investigation fixtures](../../fixtures/README.md) run it.
  The classifier asks over HTTP on the published port with the fixtures' metadata-only token and
  nothing else, as a client outside the container would.
- **the local age store** laid out by the [provider-capability run](../e5-provider-capabilities/README.md),
  and **SOPS files**, whose classifier reads only the metadata kept beside the ciphertext.

The rules themselves are pure functions in `run/decide.sh`, checked on synthetic provider answers by
`run/test-decide` before any capture relies on them. `run/classify` asks the provider and applies
them. Every observation is one row of `verdicts.tsv`: the command, the identity, the expected
class written down before it runs, the observed class and reason, and the state whose evidence
bundle is the ground truth for it. A row whose observation differs from the expectation is recorded
as a mismatch and the run continues.

The rules follow the design's two constraints. **Lost** needs positive evidence of an irreversible
removal in the answer itself: a destroyed version, a version below the oldest kept, a Transit
version below the trimmed floor, a ciphertext or MAC that differs from the one referenced. Anything
the classifier cannot see (a denied read, an absent path, a failed or missing answer, a sealed or
unreachable provider) is **unknown**. A monitor records how long a dependency has been unknown and
raises an alert after an interval, and never changes the class.

Rows that check authority, not retention, use the other identities: a compiler that may read only
another scope, an executor that may only decrypt, and the metadata-only token trying to read a value.
They show that a retained verdict grants nothing.

## Running it

A capture needs a fresh fixture, and `RC_OUT` must name an empty absolute path on disk, outside this
checkout (each state gets its own evidence bundle, about 100 MiB in all):

```sh
experiments/e5-retention-classification/run/test-decide
fixtures/bin/up
RC_OUT=<somewhere on disk> experiments/e5-retention-classification/run/all
RC_OUT=<the same directory> experiments/e5-retention-classification/run/collect-evidence
fixtures/bin/down
```

`run/test-decide` needs nothing but `jq`. `run/all` runs it again, then `run/openbao` and
`run/local`, then takes a final bundle and checks its leak scan: the fixture's positive control must
be found and nothing else. It refuses an `RC_OUT` that already holds a capture, and a fixture that
still holds the local stores of an earlier one. `run/collect-evidence` needs the fixture still up,
because it rebuilds its refusal patterns from this run's credentials; it copies the text of the
capture into `evidence/` and leaves the bundles' database dumps and snapshots behind.

No secret value is ever an argument. Values go to the CLIs over stdin, and every read of a value is
reduced to a digest verdict before it reaches a transcript.

## What it deliberately does not do

It does not measure key loss and restoration, or backups of different ages in combination
([issue 10](https://github.com/ginsys/bronzeward/issues/10)): the rows where a key file is deleted
show only that the retention class cannot see it. It does not set the alert interval, which the
design leaves open; `RC_ALERT_AFTER` is this experiment's parameter. It selects no provider and no
policy; those belong to [issue 13](https://github.com/ginsys/bronzeward/issues/13) and
[issue 15](https://github.com/ginsys/bronzeward/issues/15).
