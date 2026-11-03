# State the web reference scanner must carry across lines

3 November 2026. Outcome record for [#598](https://github.com/ginsys/bronzeward/issues/598). The
web scanner that lists the `!bwref` references of a draft reads comments line by line. YAML decides
some things across lines, and six separate fixes each patched one case. This record measures which
state the scanner must carry to list the same names as go-yaml, the parser of the server. It
changes no product code: the implementation follows in a separate pull request.

## Scope and decision enabled

`web/src/position.ts` hides each comment before it reads `!bwref` tags. It finds a comment with
`commentStart` and `startsScalar`, which look at one line (about 30 lines of code). A quoted scalar
that spans lines, a `?` or `-` before a quote, a block scalar header, a flow collection that stays
open and a leading byte order mark all change what a `#` means, and none of them is visible in one
line. The fixes for [#560](https://github.com/ginsys/bronzeward/issues/560),
[#578](https://github.com/ginsys/bronzeward/issues/578),
[#592](https://github.com/ginsys/bronzeward/issues/592),
[#593](https://github.com/ginsys/bronzeward/issues/593),
[#594](https://github.com/ginsys/bronzeward/issues/594) and the hosted review of
[#599](https://github.com/ginsys/bronzeward/pull/599) each moved the line one case further.

The scanner serves the editor only: it shows which references a draft uses and where. The server
decides what a draft declares (`internal/ingest/declare.go`). A name the scanner lists and the
server does not read is a wrong hint. A name the server reads and the scanner does not list is a
hidden reference, which is worse: the editor shows a draft as not using a name that it uses.

Decision enabled: the implementation replaces the line-by-line check with the state described
below. It fixes #560, #578, #592, #593 and #594 in one change, so the narrow fix for #578
(published) and the fix for #593 (in review) are superseded. Their tests, and the tests of #599,
stay as the regression floor of the implementation.

## Pinned versions and inputs

- Oracle: `go.yaml.in/yaml/v3` v3.0.5, the version in `go.mod`. The oracle in
  `experiments/scanner-cross-line/main.go` lists the `!bwref` values go-yaml reads and applies the
  grammar of the server's name check.
- `yaml` 2.9.1 (`web/package.json`), the package the web editor uses.
- Node 24, which strips TypeScript types, so a candidate is the web source with edits.
- Synthetic input only: documents with made-up keys and names such as `x/foo`.
- Eight sets, rebuilt from generators and pinned by count and sha256 in `sets/MANIFEST`. The
  [README](../../../experiments/scanner-cross-line/README.md) says where each set comes from.

## Reproduction

```sh
experiments/scanner-cross-line/run/all <work dir>
experiments/scanner-cross-line/run/collect-evidence <work dir>
```

`run/all` generates the sets, checks them against `MANIFEST`, runs the oracle, runs every candidate
over every set and writes the table. `run/collect-evidence` copies the table, the first differences
per candidate and set, the `yaml` package errors, the timings and the result of the web tests into
`experiments/scanner-cross-line/evidence/`. A run needs a few hundred MiB in the work directory,
which must be outside the checkout.

## The oracle and the gate

The first oracle listed every value go-yaml reads under the `!bwref` tag. That was too raw: the
server refuses a tagged value that is no name, so no scanner needs to list it, and the web scanner
already drops such values (`NAME` in `position.ts`). The oracle now lists names only and marks two
kinds of document the server refuses on its own rules (`internal/ingest/declare.go`): a tagged
value that is no name, and a scalar whose text contains `!bwref` (reserved text). In a document the
server refuses, a wrong hint is harmless, because the operator cannot save the draft. Even so, the
gate below counts such hints against a candidate whenever main did not show them.

The gate has three parts. A candidate must show, over every set:

1. `lost` = 0: no valid document (one go-yaml accepts) lacks a name go-yaml lists.
2. `new lost` = 0: no document that main gets right is lost.
3. `new extra` = 0: no document that main gets right gets a name go-yaml does not list.

The table also reports:

- `lost, server-fit`: `lost` on documents the server does not refuse for its `!bwref` text. It
  equals `lost` in every row.
- `extra` and `extra, server-refused`: documents where the candidate lists a name go-yaml does
  not, and the part of them in documents the server refuses anyway.
- `bad-name docs`: valid documents where go-yaml also reads a tagged value that is no name. This is
  the raw reading. It is 259 documents of S6 and none in any other set, the same for every candidate.
  **On the raw reading, no candidate has `lost` = 0 on S6.** The server refuses every one of the
  259 documents for the bad name, so no draft is affected, and the web scanner drops non-names on
  purpose.

S6 is held out: random documents of two to six lines, built from a pool of line fragments (quotes
with and without a closing quote, flow openers and closers, block scalar headers, node properties,
plain text, real and ghost references) by a seeded generator (mulberry32, seed 598). The fragments
and the scanner were written apart, and no candidate was tuned on S6. Most S6 documents are not
valid YAML; go-yaml's answer decides which count (3756 of 99983).

An independent cross-check ran on a copy of the candidates: nine sweeps of the fix for #593, a set
of 714 documents with byte order marks and 15 minimal probes (23878 valid documents). It found two
faults of the first C1, both fixed and both now in S5: a second byte order mark at the start of
the stream, and a `!bwref` in block scalar text after a quote and a `#` (162 documents that main
gets right).

## Candidates

| | Idea |
|---|---|
| C0 | The scanner on main: one line at a time. It holds none of the narrow fixes for #578 and #593 that are not yet merged. |
| C1 | A masker that reads the lines in order and keeps state (below). It replaces `commentStart` and `startsScalar`. |
| C1b | C1, and the content of a block scalar is also blanked, so a `!bwref` in block text is no tag. |
| C1c | C1, and a `!bwref` counts only where the masker reads it as a node property. |
| C2 | Find the comments with the CST parser of the `yaml` package. |
| C3 | Parse with `parseAllDocuments` and read the tags from the tree. On a parse error, fall back to C0. |
| C3b | C3 using the partial tree the package gives for a document it refuses. |
| C3+C1 | C3, with C1c as the fallback when the package refuses the document. |

State C1 carries from line to line (type `Carry` in the candidate):

| Variable | Meaning |
|---|---|
| `stack` | the columns of the open block entries: a key, or the indicator of a `- `, `? ` or `: ` entry |
| `flow` | the depth of the open flow collections (`[` and `{`) |
| `ns` | a node starts at the next token, so a `#` there is a comment |
| `open` | a scalar that goes on in the next line: an open `"` or `'`, or a plain scalar |
| `holder` | the column the next line must exceed to continue an open plain scalar |
| `block` | an open block scalar: its holder column, indentation indicator and content indent |

C1c adds a set of offsets, the places where the masker read a `!bwref` as a node property.

The rules that are not obvious:

- A document marker (`---`, `...`) resets all of it.
- In flow context `?` and `:` are indicators, and a `#` right after a flow indicator is a comment.
- A tag is read up to a blank (a verbatim tag, `!<...>`, up to its `>`). An anchor or an alias ends
  at a flow indicator in flow context.
- **Byte order mark.** go-yaml drops one at the start of the stream without counting a column, and
  its scanner skips a second one right after it as one column. So on line 0 only: drop one (the
  offsets move by one, so `BOM---` is still a marker) and read a second as a blank. A byte order
  mark anywhere else is text: go-yaml reads `---` then a line `BOM"p # q": !bwref x/foo` as a plain
  key and lists nothing.
- **Block scalar text.** The lines of a block scalar are text. C1 passes them through unmasked, C1b
  masks them whole, and C1c ignores a tag in them.

The masker does not check that a document is valid. It follows the node structure far enough to
tell where a scalar starts and where it ends. In `c1-state.ts` the `Carry` type, its helpers and
`scanLine` take about 240 lines, against about 30 for the two functions they replace.

## Observed

Valid documents per set: S0 2503, S1 19600, S2 1271, S3 65, S4 4050, S5 33, S6 3756, S7 8344.

**Lost** (the first part of the gate). Zero for C1, C1b and C1c on every set.

| | S0 | S1 | S2 | S3 | S4 | S5 | S6 | S7 |
|---|---|---|---|---|---|---|---|---|
| C0 | 0 | **5232** | **318** | **9** | **13** | **7** | 0 | **4172** |
| C1, C1b, C1c | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| C2 | **12** | **1488** | **53** | **1** | 0 | **4** | 0 | **1192** |
| C3 | 0 | **1488** | **63** | **1** | 0 | **4** | 0 | **1192** |
| C3b | 0 | **1488** | **53** | **1** | 0 | **4** | 0 | **1192** |
| C3+C1 | 0 | 0 | 0 | 0 | 0 | **2** | 0 | 0 |

**Extra** names, as *documents with an extra* (of which *new against main*). For C1, C1b and C1c,
every extra is in a document the server refuses. C0 is different: some of its extras are in
documents the server accepts, so they are false hints on a good draft (S2: 342 documents with an
extra, 180 of them refused; S5: 9, 1 refused). `new lost` is 0 for C1, C1b and C1c on every set.

| | S0 | S1 | S2 | S3 | S4 | S5 | S6 | S7 |
|---|---|---|---|---|---|---|---|---|
| C0 | 0 | 0 | 342 | 17 | 23 | 9 | 917 | 0 |
| C1 | 0 | 0 | 342 (**162**) | 10 (**2**) | 18 (**9**) | 3 (**2**) | 959 (**121**) | 0 |
| C1b | 0 | 0 | 0 | 10 (**2**) | 0 | 0 | 470 (**1**) | 0 |
| **C1c** | 0 | 0 | 0 | 0 | 0 | 0 | 164 (0) | 0 |
| C3+C1 | 0 | 0 | 0 | 0 | 0 | 0 | 3 (0) | 0 |

(`evidence/table.md` has every column and every candidate, including C2, C3 and C3b.)

- **C1c meets the whole gate:** `lost` = 0, `new lost` = 0 and `new extra` = 0 on all eight sets,
  including the held-out S6. C1 meets only the first two parts. C1b leaves 3 new extras. C0, C2
  and C3 do not meet the first.
- **Why plain C1 has new extras.** A `!bwref` in a block scalar's text after a quote and a `#`
  (`a: |` then a line `  b: ?"z # !bwref ghost`) is text, and the server refuses the document for
  it. Main hid the name only because it read that `#` as a comment. C1 passes block text
  through unmasked, so it shows the name. C1b masks block text and clears 291 of the 294 new
  extras. The 3 left (S3 2, S6 1) are a `!bwref` inside a quoted scalar in a flow collection, for
  example `a: [? "x # !bwref ghost , y"]`, where main again reads the `#` in the quote as a
  comment. C1c clears them because the name is not a node property.
- **The rest of C1c's extras** (S6, 164 documents, none new) come from the extent of a plain scalar
  that the masker does not record, such as `x/foo,` in block context or a continuation line that
  starts with `- `. All are in documents the server refuses. Recording that extent in the masker
  would close them; the gate does not need it.
- **C2 and C3 fail for a reason of principle.** The `yaml` package refuses many documents go-yaml
  accepts. In the sets it refuses 2832 of the 19600 valid S1 documents, 1192 of S7, 2060 of S4 and
  120 of S0 (`evidence/yaml-errors.txt` lists counts per error class, with the shortest examples).
  The classes: indentation of a flow collection in a block collection (2344 streams), a missing
  character (2237 streams; a mixed class that holds comments not separated by a blank, missing
  commas, missing closing delimiters and a missing quote), an implicit key spanning lines (1213), a
  block entry in flow context (582), and a tag followed by a flow indicator without a blank. A fallback
  to line-by-line reading therefore falls back exactly where the lines are hardest. C3+C1 would
  meet the gate on S0 to S4, S6 and S7, but it loses the 2 documents of S5 with two leading
  byte order marks (the package parses them and finds no tag, so no fallback happens), pays for the parse and still needs the whole masker.
- No candidate failed on any document: each returned a list for every input.

### Cost

`bench.txt`, one scan of a synthetic draft on one machine. Earlier runs of this work gave
3.2 to 6.4 ms for C0 and 4.6 to 5.3 ms for C1 at 9000 lines, so read the two as equal:

| Lines | C0 | C1 | C1c | C3 | C3+C1 |
|---|---|---|---|---|---|
| 900 | 0.48 ms | 0.52 ms | 0.41 ms | 12.9 ms | 12.1 ms |
| 9000 | 5.1 ms | 4.6 ms | 4.6 ms | 144 ms | 108 ms |

On 10000 small documents C0, C1 and C1c take 57 to 190 ms on the generated sets, C2 0.9 to 1.2 s,
C3 1.1 to 1.6 s and C3+C1 1.3 to 2.2 s. The hand-written sets S3 and S5 have longer documents and take 0.5 to
1.4 s for C0, C1 and C1c. The editor calls `references` on each change, so a 110 to 140 ms scan of
a large draft is a visible delay, and 5 ms is not. C1 costs about the same as C0.

## Alternatives

| # | Option | Result | Cost |
|---:|:---|:---|:---|
| 1 | C0, a patch per case | six patches so far, and `lost` is 5232 on S1 and 4172 on S7 | none, but the next case is a new patch |
| 2 | C1c, state carried in the scanner | meets the gate | about 210 more lines in `position.ts`; one test assertion changes (below) |
| 3a | Parse in the browser (C2, C3) | fails the gate; slow | the `yaml` package is not go-yaml; no fix in the scanner can change that |
| 3b | Ask the server to parse the text | exact by construction | on paper only, below |
| 4 | C3+C1 | loses 2 documents of S5; 25 times slower | needs the masker anyway |

Alternative 3b, from `api/openapi.yaml`: the server serves `declarations.references` for a stored
fragment (`FragmentDeclarations`), so a saved draft already has an exact list. The editor needs the
list for text the operator has not saved, on each change. That needs a new parse-only endpoint and a
round trip per change. It also sends unsaved text to the server, which `position.ts` today avoids
on purpose (nothing it reads is re-encoded, stored or sent). I did not try it.

## Recommendation and decision

Implement C1c. It meets the gate on every set, and a cross-check on independent sets (above)
found no regression against main. It costs the same as main.

**Decided by the lead, reversible by the owner: C1c, not C1b.** The options were C1c (no new lost
or extra on any set) and C1b (3 new extras, all in documents the server refuses). Both blank the
text of a block scalar, so both change the one assertion below: for the undeclared folded text,
C0 lists `x/hidden` and C1b and C1c list nothing. The reason for the change: the server refuses
the reserved text `!bwref` in any string
([compilation §5.5](../../spec/compilation.md#55-reserved-text)), so a `!bwref` inside undeclared
block text is never a reference there, and the assertion that pins the opposite records old
scanner behavior, not a requirement. C1b is the measured fallback if the owner prefers it and
accepts its 3 extras. Plain C1 is the only variant that keeps the assertion, and it adds
new extras (S2 162, S6 121).

What the implementation changes in `web/src`:

1. `commentStart` and `startsScalar` give way to the masker (`Carry`, its helpers and `scanLine`),
   with the byte order mark rule and the node-property set. The experiment switches
   (`setBlankBlock`, `setOnlyAtNodeStart`, `setCommentsFromCst`) and the CST code do not go in.
2. `spotOfReference` for a literal holder scans the block's own slice with the normal rules. The
   header line is blanked to spaces of the same length, so the lines keep their offsets and read
   as nodes. Reading the slice as it is would make the lines text. This keeps every assertion
   about declared embedded documents as it is.
3. One assertion changes, in "a declared embedded document's references come from its decoded
   text only", for the undeclared folded text `cfg: >-` then `  # note` then `  a: !bwref x/hidden`:

   | | Before | After |
   |---|---|---|
   | `references(folded)` | `["x/hidden"]` | `[]` |
   | comment | "Undeclared, the text is scanned as it is written, as before." | the text of a block scalar is text, so an undeclared one lists no reference |

   go-yaml reads that scalar as a string, lists no tag, and the server refuses the draft for the
   reserved text.
4. The three tests of `experiments/scanner-cross-line/position-extra.test.ts` move into
   `web/src/position.test.ts`: a literal holder with the same name first in a comment and then as a
   tag points at the second; a literal whose only tag is in a comment carries no reference; a
   literal with an indentation indicator and a header comment, at a deeper path, points at its tag.
   They pass on main and on C1c.

The floor for the implementation is: all of `web/src/position.test.ts` (with the one change), the
three tests above, the tests added by the fixes for #593 and #599, and the 34 cases of S5. All 33
valid cases of S5 come out equal to go-yaml for C1c.

## Limitations

- One oracle and one version: go-yaml v3.0.5. A change of version needs a rerun.
- The sets reach the shapes their generators write. S6 is the check for the others and is random.
  A shape the pool never writes is not covered.
- A `!bwref` inside a plain scalar can still show as a reference where the masker does not record
  the scalar's extent (S6, 164 documents). The server refuses each of them.
- A byte order mark is handled where go-yaml drops it, at the start of the stream. One inside a
  tag name or before a colon changes the name in go-yaml; main and C1c both differ from go-yaml
  there in the same way (120 documents of the cross-check, none new).
- The 108 block-text documents found by an earlier sweep (a `!bwref` in block scalar text, extra
  reference, none lost) are extras of plain C1. C1b and C1c close them.
- Known C1c gap, found after the sets were frozen: an alias token right before a colon starts a
  node as a key does. In `a: [&k key, *k:"x # y", !bwref x/foo]` go-yaml lists `x/foo`, because the
  quote is a scalar and its `#` is not a comment. Main and C1c list nothing. The masker does not
  yet treat an alias before `:` as a node start. The implementation issue
  (ginsys/bronzeward#600) carries the fix and a test for this document.
- Timings come from one machine and a few runs each.
- Nothing here measures the editor itself: the experiment calls `references` and nothing else.
