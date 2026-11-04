# Scanner cross-line experiment

**This is evidence code for [issue 598](https://github.com/ginsys/bronzeward/issues/598). It is
not the implementation of the web scanner and it will not be maintained past the research note it
supports:
[what state the web reference scanner must carry across lines](../../docs/design/research/20261103-scanner-cross-line-state.md).**

## What it is for

The web editor lists the `!bwref` references of a draft with a scanner (`web/src/position.ts`).
The scanner hides each comment before it reads tags, and it finds comments line by line. YAML
decides some things across lines: a quoted scalar can span lines, a `#` after a plain scalar is
text, not a comment, and a block scalar header changes how the next lines read. Each such case
was fixed alone (issues 560, 578, 592, 593 and 594). This experiment asks which state the scanner
must carry from line to line to list the same names as go-yaml, the parser of the server.

It answers a feasibility question and selects nothing for production.

The same files back a second note, on [issue 603](https://github.com/ginsys/bronzeward/issues/603):
[how the scanner reads a tag spelled through a handle](../../docs/design/research/20261104-tag-handle-resolution.md).
`tag-handles.mjs`, `tag-causes.mjs`, `run/tag-causes`, `serverprobe/` and `sets/tag-handle-cases.json`
belong to that note.

## What is here

- `main.go`, `main_test.go` — the oracle. It reads a set and writes, for each document, the names
  go-yaml v3.0.5 reads as `!bwref` references. It applies the grammar of the server's name check
  and marks a document the server refuses for its `!bwref` text. `main_test.go` checks it against
  the curated cases.
- `candidates/` — the scanners under test. `c0-main.mjs` is the scanner on main. `c1-state.ts`
  is C1: the same file with a stateful masker in place of the line-by-line comment check. The
  other files are the variants that the note compares with C1. `c0q-quote-carry.ts` is not a
  candidate: it is the plain quote carry of issue 592, kept as a failure case.
- `sets/` — the input sets (below) and `MANIFEST`, which lists the count and sha256 of each set.
- `cmp.mjs` — runs a candidate over a set and compares it with the oracle. `yaml-errors.mjs`
  lists what the `yaml` package refuses that go-yaml accepts. `bench.mjs` measures the cost of
  one scan of a large draft. `difference-lists.mjs` writes the full list of differing documents of
  each candidate that loses no name. `quote-carry-subset.mjs` lists the documents that the plain quote carry gets wrong
  and the result of every candidate on them.
- `run/all` — the whole experiment. `run/gen-sets` — writes the generated sets and checks them
  against `MANIFEST`. `run/web-tests` — runs the tests of `web/src/position.test.ts` and
  `position-extra.test.ts` against a candidate. `run/collect-evidence` — runs everything and writes
  `evidence/`.
- `position-extra.test.ts` — three tests for the implementation, beyond `web/src/position.test.ts`.
- `serverprobe/` — a second Go module that asks the server about a document. It calls
  `ingest.Stored` of `internal/ingest` with every name go-yaml reads declared, and it prints
  whether the server accepts the document. It is its own module on purpose. It builds with the
  yaml version of the server, and the oracle keeps its own pin. `mise run go` builds it, which
  compiles the root module's dependencies.
- `tag-handles.mjs` — the case table of the tag handle note: for each case of
  `sets/tag-handle-cases.json`, the go-yaml result, the server verdict and the result of the
  scanner on main and at an earlier commit. `tag-causes.mjs` and `run/tag-causes` group the
  differences of set S8 by the spelling of the tag and by the server verdict.
- `evidence/` — the collected run: `table.md` (every candidate over every set), `differences/`
  (the first 20 documents each candidate gets wrong, per set: a summary), `lists/` (every
  differing document of a candidate that loses no name, by index in the set, one file per
  candidate and set), `quote-carry-subset.txt`
  (the failure cases of issue 592), `yaml-errors.txt`, `bench.txt` and `web-tests.txt` (the web
  tests against main, C1 and C1c).

## The sets

The generators are code, not data. A set is rebuilt from its generator, and `MANIFEST` pins its
count and sha256.

| Set | Where it comes from | Documents |
|---|---|---|
| S0 indicators | The sweep of the fix for issue 560, copied unchanged: each indicator and node property right before a quote, in block, flow-sequence and flow-map context, with a reference on the next line | 8316 |
| S1 explicit key | The explicit-key sweep of issue 578: `?` keys that hold a quoted scalar with a `#`, in block and flow context, on one line and on several | 19960 |
| S2 context | The context sweep of issue 578: lines before a probe line, such as block scalars that hold a flow opener, then the probe at several indentations | 3000 |
| S3 review | Documents written by hand during reviews and probes, including quoted text that holds a tag | 70 |
| S4 families | The families sweep of issue 578: block scalar headers (indentation and chomping indicators, comments, some with a digit) and the lines around them | 20624 |
| S5 cases | The curated cases: one file, with the result go-yaml gives for each (`want: null` means go-yaml must refuse the document). It holds the documents of issues 560, 578, 592, 593 and 594, of the hosted review of pull request 599 and of the block scalar header (issue 598) | 39 |
| S6 random | Held out. Random documents from a pool of line fragments, with a fixed seed (mulberry32, seed 598). No candidate was tuned on it | 99983 |
| S7 colon flow | The colon-flow sweep from a review of issue 578: a flow collection opened right after a value colon, then a quote with a `#` and a reference | 21672 |
| S8 state edges | Added for the hosted review of pull request 602: blank and comment lines around block scalar headers, properties on empty and explicit keys, a colon on the next line, a comment right after a token, a byte order mark, what follows a document marker or an indicator (space, tab, carriage return, U+00A0 and other Unicode spaces), tag-like text in comments and quotes before a real tag, flow collections as block keys, every spelling of the tag (through a handle, verbatim) at every place a property stands, `%TAG` and `%YAML` directives, the line ends of those families as a CRLF, a lone carriage return, U+0085, U+2028 and U+2029, then every three lines of a pool of state-changing lines. Deterministic, no seed. Not run by `run/all` | 227762 |

## The gate

For every document go-yaml accepts, a candidate must list every name go-yaml reads as a `!bwref`
reference (`lost = 0`). It must also add no loss and no extra name to a document that the scanner on
main gets right (`new lost = 0`, `new extra = 0`). Extra names are otherwise allowed only where the
server refuses the document anyway. The table counts:

- `valid` — documents go-yaml accepts. A document go-yaml refuses reads no reference on the server.
- `lost` — valid documents where the candidate lacks a name go-yaml lists. `lost, server-fit` is
  the same count on documents the server does not refuse for their `!bwref` text.
- `bad-name docs` — valid documents where go-yaml reads a tagged value that is no name. No
  scanner lists these, so they are not counted as lost.
- `extra` — documents where the candidate lists a name go-yaml does not. `extra, server-refused`
  counts those the server refuses anyway.
- `new lost vs main`, `new extra vs main` — valid documents where the candidate loses or adds a
  name and the scanner on main does not.
- `refused-listed` — documents go-yaml refuses where the candidate lists a name. Not an error.
- `ms per 10k` — time to scan 10000 documents of the set.

## Running it

You need node 24 or later, go, and `web/node_modules` (`mise run web:install`). Give `run/all` an
empty work directory outside the checkout. A run writes a few hundred MiB there.

```sh
experiments/scanner-cross-line/run/all <work dir>
experiments/scanner-cross-line/run/collect-evidence <work dir>
```

`run/all` prints one line per candidate and set and writes `<work dir>/table.md`. Name candidates
(for example `c1-state`) to run fewer. `go test ./...` in this directory runs the oracle's test.

## Limits

- The oracle is go-yaml v3.0.5, the version the server pins. Another version can read a corner
  case differently.
- The sets reach only the shapes their generators write. S6 is the check against a shape nobody
  thought of, and it is random, not exhaustive.
- Timing is one machine and one run. Read it as an order of magnitude.
