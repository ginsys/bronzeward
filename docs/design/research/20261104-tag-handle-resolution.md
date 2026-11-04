# How the web reference scanner reads a tag spelled through a handle

4 November 2026. Outcome record for [#603](https://github.com/ginsys/bronzeward/issues/603).
The web scanner that lists the `!bwref` references of a draft matches the text of a tag. go-yaml,
the parser of the server, first resolves a tag through the `%TAG` directives of its document. This
record lists where the two disagree, which of those documents the server accepts, and what it
would cost to close the gap. It changes no product code. It is a recommendation, not a decision.

## Scope and decision enabled

The scanner is `web/src/position.ts`. It serves the editor only: it shows which references a
draft uses and where. The server decides what a draft declares (`internal/ingest/declare.go`). A
name that the scanner lists and go-yaml does not read is a wrong hint. A name that go-yaml reads
and the scanner does not list is a hidden reference: the editor shows the draft as not using a
name that it uses.

The server limits the damage. It accepts `!bwref` and the YAML core tags only (`refTag` at line 41
and `coreTags` at line 45). It refuses every other tag (`RuleLocalTag`, lines 148 and 149). So a
wrong hint is expected in documents that the server refuses anyway. A hidden reference matters
in a document that the server accepts. The cases are therefore split by the verdict of the
server. In every input of this record, the wrong hints fall on refused documents. This is a
measured result, not a proof.

Decision enabled: the owner chooses one of three options. The scanner resolves tags like go-yaml
(A), the gap is documented as accepted (B), or the scanner recognises one more spelling (C).

## Pinned versions and inputs

| Item | Value |
|---|---|
| go-yaml (oracle) | `go.yaml.in/yaml/v3 v3.0.5`, from `go list -m go.yaml.in/yaml/v3` in `experiments/scanner-cross-line` |
| go-yaml (server probe) | `go.yaml.in/yaml/v3 v3.0.5`, the same command in `experiments/scanner-cross-line/serverprobe` |
| Scanner, column "main" | `web/src/position.ts` at 3e00e342 |
| Scanner, column "before" | `web/src/position.ts` at 1cf21917, the last commit before [#602](https://github.com/ginsys/bronzeward/pull/602) |
| Case file | `sets/tag-handle-cases.json`, 47 cases, all synthetic |
| Sweep | set S8, 227,762 synthetic documents, 61,086 of them accepted by go-yaml |

The server probe is a second Go module. It calls `ingest.Stored` with every name go-yaml reads
declared. So an undeclared name never causes a refusal in this record. `Stored` hides the rule
behind its verdict, so the probe reports accepted or refused and nothing finer.

## Reproduction

You need go, node 24 or later, and `web/node_modules` (`mise run web:install`). Use an empty work
directory outside the checkout.

```sh
cd experiments/scanner-cross-line
node tag-handles.mjs sets/tag-handle-cases.json <work dir> 1cf21917 evidence/tag-handles.md
run/tag-causes <work dir> 1cf21917
node tag-anchor.mjs <work dir>/s8.main.report.json <work dir>
```

The first command writes the case table (`evidence/tag-handles.md`). The second runs set S8
through the oracle and both scanners and prints the grouped counts (`evidence/tag-causes.txt`).
Run `run/tag-causes` first, then `tag-anchor.mjs` on the same work dir, because the third
command reads the report of the second for the scanner on main. It splits the 420 lost
documents by server verdict, verbatim spelling and an anchor after the tag
(`evidence/tag-anchor.txt`). It runs no sweep.
The sweep took under three minutes and wrote about 35 MiB. `mise run go` builds and tests the
server probe.

## Case table

The full table is `evidence/tag-handles.md`. The cases that matter are below. "x/foo" is the
name that go-yaml reads. "lost" means the scanner lists nothing. "extra" means the scanner lists a
name that go-yaml does not. Main and the earlier commit give the same result on every row shown.

| Case (source) | go-yaml | Server | Scanner |
|---|---|---|---|
| `a: !<!bwref> x/foo` (issue) | x/foo | accepts | lost |
| `a: !<!bwref> x/foo` inside a flow collection | x/foo | accepts | lost |
| `%TAG !x! !bw`, then `a: !x!ref x/foo` | x/foo | accepts | lost |
| `%TAG ! !bw`, then `a: !ref x/foo` | x/foo | accepts | lost |
| `%TAG !! !bw`, then `a: !!ref x/foo` | x/foo | accepts | lost |
| `a: !bw%72ef x/foo` | x/foo | accepts | lost |
| `a: !<!bw%72ef> x/foo` | x/foo | accepts | lost |
| `%TAG !x! !`, then `a: !x!bw%72ef x/foo` | x/foo | accepts | lost |
| `%TAG !x! !`, then `a: !x!bwref x/foo` (handle maps to the primary prefix) | x/foo | accepts | listed |
| `%YAML 1.1` or `%TAG` before a plain `!bwref` (issue) | x/foo | accepts | listed |
| `a: !!bwref x/foo` (issue) | none (parses) | refuses | extra x/foo |
| `%TAG ! !x-`, then `a: !bwref x/foo` (issue) | none (parses) | refuses | extra x/foo |
| `%TAG !r! tag:x,2000:`, then `a: !r!bwref x/foo` | none (parses) | refuses | extra x/foo |
| Two documents, the second uses a handle that only the first declares | parse error | refuses | extra x/foo |
| `%YAML 1.2` or `%FOO` before a plain `!bwref` | parse error | refuses | extra x/foo |
| `a: !bwref &e x/foo` (found by the sweep, see below) | x/foo | accepts | lost |
| `!bwref x/foo: v` (tagged key) | x/foo | refuses | lost |

Tally of the 47 cases, with the server verdict as the row and the scanner on main as the column.

| Server | Scanner lists the same | Scanner loses a name | Scanner adds a name |
|---|---|---|---|
| Accepts (22 cases) | 10 | 12 | 0 |
| Refuses (25 cases) | 8 | 1 | 16 |

Of the 12 accepted losses, 9 are about how a tag resolves: the verbatim form, a prefix that
splits the name, and a percent escape. The other 3 are the tag-then-anchor shape, which has
nothing to do with handles (see the separate finding). The single refused loss is the tagged key.

## Sweep of set S8

Set S8 writes a list of tag spellings (plain, through a handle, verbatim) at many places where a
property can stand, with and without `%TAG` and `%YAML` directives. The section after the facts says which spellings it leaves out. The oracle reads 61,086 of its 227,762 documents as valid. The
table groups the differences of the scanner on main by the spelling of the tag.

| Spelling | Directive | Lost, server accepts | Lost, server refuses | Extra, server accepts | Extra, server refuses |
|---|---|---|---|---|---|
| plain `!bwref` | none | 12 | 12 | 0 | 1,141 |
| plain `!bwref` | yes | 0 | 0 | 0 | 29 |
| verbatim `!<...>` | none | 120 | 24 | 0 | 0 |
| verbatim `!<...>` | yes | 168 | 84 | 0 | 0 |
| secondary handle `!!bwref` | none | 0 | 0 | 0 | 120 |
| secondary handle `!!bwref` | yes | 0 | 0 | 0 | 222 |
| named handle `!x!bwref` | yes | 0 | 0 | 0 | 61 |

Totals on main: 420 documents lose a name (300 that the server accepts and 120 that it refuses)
and 1,573 documents list an extra name (none that the server accepts). The scanner before #602
gives 538 lost (341 accepted) and 16,318 extra (none accepted). So the scanner before #602 loses
118 more documents and lists extra names in 14,745 more.

These totals cover the 61,086 documents that go-yaml parses. They do not count the documents that
go-yaml refuses. In 85,552 of those, the scanner on main still lists a name, and in 165,870 the
scanner before #602 does. Such a listing is an extra name by the definition above, but the server
also refuses these documents, so the sweep does not count them. Only the case table counts a
listing in a refused document (the 16 refused cases with an extra name).

Three facts follow.

1. In S8, the directive causes no loss. The documents that carry a directive lose a name in 252
   cases, and every one is a verbatim spelling. A directive before a plain `!bwref` loses nothing.
   Issue 603 says that 420 documents of S8 are affected by a directive. That number is the whole
   loss count of the scanner on main. A directive is present in 252 of those, and the verbatim
   spelling causes them.
2. No extra name falls on a document that the server accepts. The sweep found none in 1,573
   documents, and the 16 extra cases of the case table are all refused. A likely reason is that
   the scanner lists a tag only if its text ends in `!bwref`. A handle that changes the meaning
   of that text then gives a tag that is neither `!bwref` nor a core tag. This reason is not
   proven in general.
3. Of the 300 accepted losses on main, 288 are verbatim spellings and 12 are the plain
   tag-then-anchor shape. Of the 288, 12 also have an anchor after the tag (`a: !<!bwref> &e
   x/foo`), so two gaps cause them. In all, 24 accepted losses carry the anchor shape and 276
   do not. The other 120 losses are in documents that the server refuses. The table above groups
   a document by its tag spelling, so those 12 count as verbatim there. `evidence/tag-anchor.txt`
   holds the split: 12 plain with an anchor, 12 verbatim with an anchor, 276 verbatim without.

## What the sweep does not reach

The spellings of the S8 generator (`sets/gen-s8-state-edges.ts`, lines 230 and 262) keep the
word `bwref` whole. None puts part of the name in a handle prefix, and none uses a percent
escape. The case table shows that these two shapes exist and that the server accepts them. They
are tag resolution losses that no S8 count includes. So the 288 verbatim documents are a floor
for the accepted losses of tag resolution, not the whole number. The implementation must add
these shapes to the generator before it claims a gate.

## Separate finding: a tag followed by an anchor

The scanner loses a reference in `a: !bwref &e x/foo` and its variants (the anchor on the next
line, the value quoted, any line end that go-yaml breaks on). The server accepts these documents
and go-yaml reads x/foo. The anchor first (`a: &e !bwref x/foo`) works.

Cause, traced in `web/src/position.ts`: the pattern at line 121 matches `!bwref`, a blank, and
then a plain scalar made of words. It reads `&e x/foo` as that scalar. The name check at line
141 then rejects it, so nothing is recorded. The tag has no handle, so #603 does not own this
case. The sweep found 24 such documents that the server accepts: 12 with a plain tag and 12
with a verbatim tag. The tagged key `!bwref x/foo: v`
is also lost, but the server refuses it (`RuleTagPlacement`, line 113), so the document is
refused anyway.

Draft issue body, for the owner to file:

> **Problem.** The web reference scanner loses a `!bwref` reference when an anchor stands between
> the tag and the value. go-yaml v3.0.5 reads the reference and the server accepts the document.
> The editor then shows the draft as not using the name.
>
> **Repros.** `a: !bwref &e x/foo`, `a: !bwref &e` with `x/foo` on the next line, and
> `a: !bwref &e "x/foo"`. The control `a: &e !bwref x/foo` works. Set S8 holds 24 documents with
> the same-line form that the server accepts, 12 with a plain tag and 12 with a verbatim tag.
> The case file has the same-line form as `tag-then-anchor`. It also has the next-line and quoted
> variants, which S8 does not write, as `tag-then-anchor-next-line` and `tag-then-anchor-quoted`.
>
> **Cause.** The pattern after the tag reads `&e x/foo` as one plain scalar, and the name check
> rejects it. The fix reads any properties (`&anchor`, a second tag) between the tag and the
> value.
>
> **Decision enabled.** Whether to fix it in the scanner or to accept it. It needs no tag
> resolution and does not depend on the answer to #603.

## Alternatives

**A. Resolve tags like go-yaml.** The scanner keeps a table of handles per document. The table
starts with the defaults (`!` maps to `!` and `!!` maps to the core prefix). A `%TAG` directive
changes one entry. The directives stand before the `---` of their document and last until that
document ends. The next document starts with the defaults again. The scanner also reads the verbatim
form and decodes percent escapes before it compares the result with `!bwref`.
- Effect, by design and not measured: it would close the 9 accepted tag resolution losses in the
  case table. It would also remove the extra names of the 8 refused cases where go-yaml parses
  the document and resolves the tag to another tag. In the other 8 refused cases go-yaml fails
  to parse the document. Resolution would remove the extra name there only if the cause is the
  tag, for example a handle that no directive declares. It would not remove it where a directive
  is invalid, such as `%YAML 1.2` or `%FOO`. That needs directive validation and no listing
  after a parse error. This record did not sort the 8 cases further.
- Cost: state for the directive area of a document. The scanner already carries state from line
  to line (`Carry`). It also needs the new S8 families above and a regression test per rule. The
  size is not measured, because no prototype exists. Today the scanner treats handles in one
  line (line 483). Directive validation, if wanted, is extra.
- Risk: the directive area is easy to misread. The case with a directive after `...`
  (`directive-after-document-end`) agrees with go-yaml today and must keep agreeing. A
  directive-like line in a literal block or in a comment declares nothing. In those two cases
  (`directive-in-literal-text` and `directive-in-comment`) the handle stays undeclared and
  go-yaml refuses the document. The scanner lists an extra name today, so they are existing gaps
  that A would close. They are not regression controls.

**B. Document the gap as accepted.** The scanner stays as it is. The editor specification and the
scanner header say that a verbatim tag, a handle that holds part of the name, and a percent escape
are not recognised.
- Closes nothing. It costs no code.
- Risk: the editor shows a draft as not using a name that the server counts as used. The server
  still enforces the declaration (`RuleUnusedName`, line 182). Ordinary drafts write `!bwref`,
  and every spelling at issue is a deliberate choice.

**C. Recognise the verbatim `!<!bwref>` only.** The scanner adds one spelling.
- Effect, by design and not measured: it would close 3 of the 9 accepted losses in the case
  table. It would close 276 of the 288 that S8 generates, if the scanner reads exactly
  `!<!bwref>`. The other 12 also have an anchor after the tag and need the anchor fix too.
- Cost: a few lines and one S8 family.
- Risk: it leaves the prefix split and the percent escape open. Closing one shape at a time is the
  pattern that [#598](https://github.com/ginsys/bronzeward/issues/598) ended: six separate fixes
  each moved the line one case further.

## Recommendation

Choose A if the scanner is to match go-yaml on every tag spelling in a document that the server
accepts. Of the three options, it is the only one designed to close the three kinds of accepted
tag loss, and it does so with one rule, the one go-yaml applies. Make it one change with its own commit, the new generator
families, and this gate: no loss that a tag spelling causes in a document go-yaml accepts, no new
loss and no new extra name compared with main. The gate leaves out the tag-then-anchor gap. That
gap is a separate fix. Until it lands, a sweep of whole documents still shows its 24 losses.

Choose B if the owner prefers to keep the scanner as it is. The evidence supports that choice
too: the spellings are deliberate and the server enforces its own check. Do not choose C alone.
It looks cheap and leaves two known shapes.

The family B fix in `gen-s8-state-edges.ts` is already done in this change (the closer now
matches the opening bracket). The regenerated set S8 has the same 227,762 documents, and its
hash changed in `MANIFEST`.

## Limitations

- The oracle is go-yaml v3.0.5, the version the server pins. Another version can read a corner
  case differently.
- The server verdict comes from `ingest.Stored` with every name declared. It does not say which
  rule refused a document.
- Set S8 reaches only the shapes its generator writes. The case file adds the shapes found by
  reading go-yaml. No random set was run for tags. A shape that nobody thought of can still exist.
- The cost of A is an estimate. No prototype was written, because product code is out of scope.
- The editor was not run. "The editor shows the draft as not using the name" repeats the
  statement of the record for #598 and was not re-measured.
