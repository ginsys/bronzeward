# A model of go-yaml's token lookahead for a flow key of only `?` items

7 November 2026. Outcome record for [#688](https://github.com/ginsys/bronzeward/issues/688).
The web reference scanner (`web/src/position.ts`) lists a reference name in some documents that
go-yaml v3.0.5 refuses. The server reads no reference from a refused document. So the page can
show a name that the server never sees. [#657](https://github.com/ginsys/bronzeward/issues/657)
asks the scanner to refuse these documents too. Three character-level attempts in
[#654](https://github.com/ginsys/bronzeward/issues/654) each failed on a new edge. This record
tests one idea: a model of the go-yaml lookahead predicts the refusals exactly. It changes no
product code. It is a recommendation, not a decision.

## Scope and decision enabled

The class is a flow collection used as an implicit block key, such as `[? b]: V`. Every entry of
it starts with `?` or `:`. go-yaml refuses the document in some places and accepts it in others.
The cause is a go-yaml defect, not a YAML rule. Section "Cause" gives the lines.

Decision enabled: #657 either ports the model into the scanner, or closes as a documented known
gap. Section "Recommendation" gives the advice.

## Pinned versions and inputs

| Item | Value |
|---|---|
| go-yaml (oracle) | `go.yaml.in/yaml/v3 v3.0.5`, from `go list -m go.yaml.in/yaml/v3` in `experiments/scanner-cross-line` |
| Scanner | `web/src/position.ts` at 3c207865. This record does not change it. |
| Model | `experiments/flow-key-lookahead/model.mjs`, a port of the token queue, the simple keys and the indent stack of `scannerc.go` |
| Oracle "real" | the oracle of `experiments/scanner-cross-line`, on unmodified go-yaml |
| Oracle "patched" | the same oracle on go-yaml with one guarded delete (`go-yaml-guard.patch`) |
| Oracle "counters" | `instr-oracle` on go-yaml with two counters (`go-yaml-instrument.patch`) |
| Run head | 32e7bc5e, with a clean checkout. The oracle outputs of s13, S9 and S8 come from the run with stamp w688-l3 at 77eff0e0. The oracles and the sets did not change after it. The model compare of those three sets is the run with stamp w688-gbom2, on the saved outputs, with the model file of 8878180d. It is also the run that measured the five small sets through the three oracles again, edge set included (13 documents, exit 0). The run with stamp w688-m4 holds the earlier model compare at 9941e4b8, which the S8 notes use as the baseline. The commit after 32e7bc5e changes this record only. |

All sets are synthetic. The small sets come from `gen-small-sets.mjs`. The flow-key set comes from
`gen-flow-key-set.mjs`, which is the generator in the body of #657 (named S12 there). The scanner
work in [#616](https://github.com/ginsys/bronzeward/issues/616) uses the name S12 for another set.
So this record calls the flow-key set s13. A PR that commits it gives it a number in
`sets/MANIFEST`. S8 and S9 come from `experiments/scanner-cross-line/sets`.

## Cause

These lines of `scannerc.go` in go-yaml v3.0.5 decide the outcome.

- `yaml_parser_fetch_more_tokens` (664-675) reads ahead while fewer than 3 tokens are unread. It
  also reads ahead while the head token is a registered simple key.
- `yaml_parser_save_simple_key` (925, 933) registers a possible key under its token number.
- `yaml_parser_increase_flow_level` (959-966) gives a new flow level a slot. The slot holds the
  token number of the opening bracket, the same number as the registration of the bracket.
- An entry that starts with a scalar, a quote, an alias, a tag, an anchor or a collection saves a
  new key in that slot and replaces the number.
- `yaml_parser_decrease_flow_level` (983) closes the level. It deletes the registration under the
  number of the slot.
- Inside a flow collection, `?` and `:` set `simple_key_allowed` to false (1352, 1421). A
  collection where every entry starts with `?` or `:` saves nothing. Its closing bracket then
  deletes the registration of the outer key, and that key is still possible.

After the delete, go-yaml can consume the `[` before it reads the `:`. Then
`yaml_parser_fetch_value` inserts the KEY token at `token_number - tokens_parsed` (1386). That
position is negative. `yaml_insert_token` (`apic.go` 40-43) appends a token at the end of the
queue when the position is negative. So the KEY lands after the closing bracket of the key.
`yaml_parser_roll_indent` does the same for BLOCK-MAPPING-START (1018-1021). The VALUE token is
appended last. The queue then reads KEY, BLOCK-MAPPING-START, VALUE after the key. The parser
reports "did not find expected key". At the document root it reports "did not find expected
<document start>".

One more path gives the same result. In `a: b\n[? c]: V` the parser looks at the head token `[`
before the scanner reads `:`. It finds no KEY and refuses at once. The counters see no negative
insert there, because the scan stops at the refusal. The model event covers both paths: go-yaml
consumes `[` while its key is still possible, and then the `:` resolves that key.

## The model

`model(doc)` is a pure function. It ports the subset of the scanner that decides the queue:

- the token queue with its head, and the count of consumed tokens
- the flow level, the indent stack and `simple_key_allowed`
- the stack of simple keys with their token numbers, and the map from token number to key

It ports the scan of scalars, anchors, aliases, tags and comments only as far as they move the
position and set `simple_key_allowed`. It does not model values.

The model reads characters, as go-yaml does, and not UTF-16 units. It drops an initial byte order
mark before it counts a column (`readerc.go` 47-73). A mark after that one stays in the text,
because `is_bom` tests the start of the buffer and ignores its offset (`yamlprivateh.go:103`,
called at `scannerc.go:1545`). It treats CR, LF, U+0085, U+2028 and U+2029 as line breaks
(`yamlprivateh.go` 124-130).

The driver follows the parser. Before the parser consumes a token, it runs
`yaml_parser_fetch_more_tokens` once. Then it removes the head token. The model does the same, and
it scans to the end of the stream.

The verdict "misplaced" means that a KEY or BLOCK-MAPPING-START was inserted at a negative
position. The verdict "error" means a scanner error. The verdict "ok" means neither. The verdict
"unsupported" means that the model does not port the input. This covers a directive, a tab
that follows a comment line in the same run of comments, and a document that starts with two
byte order marks.

### Why the context decides

A trace of two documents shows the rule.

- In `a: b\n[? c]: V`, the colon of an implicit key sets `simple_key_allowed` to false. The value
  `b` is not registered. When `]` is read, the head is `[`, it has no registration, and four
  tokens are unread. go-yaml consumes `[`.
- In `? a\n: b\n[? c]: V`, the colon of an explicit key sets `simple_key_allowed` to true. The
  value `b` is registered. It stays at the head, so go-yaml keeps reading until it reads the `:`.
  The KEY lands in the right place.

This confirms hypothesis H1 of #688 and corrects its wording. The refusal needs three facts: the
registration of the outer key is gone, three or more tokens are unread, and the head token is not
a registered key. The count of tokens inside the key is not the rule. A key `{?}` triggers the
defect, and `[?]` does too, but go-yaml refuses `[?]` for another reason in both runs.

## Reproduction

You need go, node 24 or later, and a work directory outside the checkout. The module cache must
hold go-yaml v3.0.5.

```sh
cd experiments/flow-key-lookahead
./selftest.sh                                 # compare.mjs must fail on planted failures
./build-oracles.sh <work>                     # the three oracles, from go-yaml in the module cache
node gen-small-sets.mjs <work>                # matrix, probes, hyp, small and edge .json
./run-compare.sh <work> <work>/matrix.json    # likewise the other small sets
node gen-flow-key-set.mjs <work>/s13.json     # then run-compare.sh on it
```

`lane.sh` runs s13, S9 and S8 one after the other, on sets that you made first. The run of this
record (stamp w688-l3 at 77eff0e0) took 22 seconds. The host check before that run required 16384
MiB of free memory and passed with 20206 MiB. The lowest free memory during that run was 19.4 GiB.
Earlier measurements on 25200 documents gave about 120 MB for the model comparison and under 20 MB
for each oracle.

`compare.mjs` prints one line of counts and then the differing documents. The bug decides a
document when the real oracle refuses it and the patched oracle accepts it. The model passes a set
when it meets three conditions:

- It reports "misplaced" for every document that the bug decides (none missed).
- It predicts a refusal, by "misplaced" or by "error", only for documents that the real oracle
  refuses (no false refusal).
- A model run with the guard switched on reports "misplaced" for no document.

The exit status is the pass rule. `compare.mjs` exits 1 when a condition fails, and it stops with
an error on an empty set or when an oracle file has a different length. `selftest.sh` plants two
false refusals (one by a "misplaced" prediction, one by a scanner error), a missed document, an
empty set and a length mismatch. It requires a nonzero exit for each, and exit 0 for a passing
case.

The patched oracle can refuse a document for another reason, such as `[,]`. The model can report "misplaced" there, because the bug fires there too.

An earlier version of `compare.mjs` always exited 0, and it counted only "misplaced" as a
prediction. A review of this record found both defects. All numbers below come from the fixed
version.

## Results

On the documents that it compares, the model matches go-yaml in every set below. Each row has 0
missed documents and 0 false refusals, and the guarded model reports "misplaced" for 0 documents.
That result covers the "agree" column only. The documents in the last two columns were not
compared. "Control" is the count of documents that go-yaml accepts with a name, which shows that
the run reads names at all. "Agree" is the count of compared documents on which the model and
go-yaml agree. "Not compared" is the count of documents where the model makes no claim. These are
documents that both oracles refuse for another reason and that the model calls "ok", and
documents that the model calls "unsupported": a directive, or a tab after a comment line. In each
row, Agree plus the last two columns equals Documents. The runs print the same counts for the five
small sets and for s13, S9 and S8.

| Set | Documents | Control | Decided by the bug | Model "misplaced" | Agree | Not compared: both refuse, model "ok" | Not compared: model "unsupported" |
|---|---|---|---|---|---|---|---|
| Matrix of #657 (18 keys x 10 values) | 180 | 90 | 80 | 80 | 170 | 10 | 0 |
| Probe 5 and 6 of #657, plus root and `a:` shapes | 22 | 13 | 9 | 9 | 22 | 0 | 0 |
| Hypothesis set (21 flow keys x 6 contexts) | 126 | 90 | 18 | 21 | 113 | 13 | 0 |
| Small keys of `?` and `:` (below) | 45 | 0 | 15 | 45 | 45 | 0 | 0 |
| Edge set: BOM, Unicode line breaks, astral key, tab after a comment, shorthand tags, tab in a plain scalar continuation, BOM after the first line | 13 | 3 | 7 | 7 | 11 | 1 | 1 |
| s13, the flow-key set of #657 | 23247 | 15099 | 8085 | 8085 | 23184 | 63 | 0 |
| S9 | 38208 | 5955 | 0 | 0 | 27021 | 11187 | 0 |
| S8 | 245696 | 53570 | 0 | 0 | 139874 | 94597 | 11225 |

Notes on the rows. The counts of the table and of the notes on the five small sets, s13 and S9
come from the run with stamp w688-gbom2 at 32e7bc5e, unless a note names another commit.

- Matrix: the other 10 documents use the key `*e`. Both refuse them, with "unknown anchor". That
  is [#689](https://github.com/ginsys/bronzeward/issues/689), not this defect.
- Hypothesis set: the 3 extra documents have the key `[,]`. go-yaml refuses them in both runs. The
  model says the defect fires, and the counters agree for 2 of them. In the third, the parser
  refuses first.
- Small keys: 15 keys of `?` and `:` in 3 contexts. Of 45 documents, 15 are decided by the bug
  (`{?}`, `[?,]`, `[? b]`, `[?b]` and `[?:]`). The other 30 refuse in both runs for another
  reason, such as `[?]` and `[:b]`. The model reports "misplaced" for all 45 and the real run
  refuses all 45. `gen-small-sets.mjs` writes the set with the contexts `K: V`, `a:\n  K: V` and
  `a: b\nK: V`, where `V` is `!bwref x/foo\n`.
- Edge set: 13 documents. 8 have an initial byte order mark, the line breaks U+0085, U+2028 and
  U+2029, and a key of 511 astral characters with an ASCII twin. 6 of these 8 are decided by the
  bug and 2 are accepted. Before the first repair, the model missed 4 of the 6 and refused 2
  accepted documents. One has a byte order mark before `? a`, then `: b`, then `[? b]: !bwref
  x/foo` (verdict "misplaced"). The other has a byte order mark before `k: !bwref x/foo` and ` bar`
  on the next line (verdict "error"). The 5 other documents come from the hosted and local reviews:
  - `a: 1`, `# c`, a tab and `# d`, then `b: !bwref x/foo`. go-yaml accepts it. The model calls it
    "unsupported", so it is not compared. The model before this repair reported a scanner error,
    which is a false refusal.
  - `[? !t]: !bwref x/foo`. A shorthand tag swallows the `]` in go-yaml, so the parser refuses the
    document for a reason that is not the bug. Both oracles refuse it and the model says "ok". It
    is one "not compared" document.
  - `[? !t, x]: !bwref x/foo`. A shorthand tag swallows the `,` too, and the bug decides the
    document. The model says "misplaced", so it is the 7th document that the bug decides.
  - `a:`, then `  b: [x`, then ` y`, a tab and `z]`, each on its own line. No flow key is involved, so
    the bug decides nothing. go-yaml accepts the document. The model before this repair reported a
    scanner error, "found a tab character that violates indentation", which is a false refusal.
    The model set its leading-break flag at the first line break (`model.mjs:290`) and never
    cleared it, so it checked the tab after `y`. go-yaml clears the flag when it reads the next
    character (`scannerc.go:2738-2753`), so it does not check that tab. The repair clears the flag
    at the same place (`model.mjs:281`). The same flag also sets the simple-key flag at the end of
    the scalar (`model.mjs:296`, `scannerc.go:2830`). That is why the repair moves counts in S9
    and S8 (see the notes below).
  - A line break, then a byte order mark, then `[? b]: !bwref x/foo`. Both oracles accept it and
    name `x/foo`. The model before this repair skipped a mark at column 0 of every line, so it
    reported "misplaced", which is a false refusal. go-yaml leaves a mark after the first one in
    the text (see the model description). The repair deletes the skip. The document is the third
    document with a name that go-yaml accepts in this set, and it is compared and agrees.
- s13: the count of 8085 equals the 8085 names that the scanner on main lists in documents that
  go-yaml refuses (#657). The counters saw the event in all 8085. 63 documents refuse in both runs
  for another reason, and the model says "ok" for them. They are outside the class and are the 63
  "not compared" documents.
- S9: no document is decided by the bug. The model reports a scanner error for 13314 documents,
  and go-yaml refuses all of them. The model says "ok" for the other 24894 documents. go-yaml
  accepts 13707 of them and refuses 11187 for a reason that the model does not port. The 11187
  are the "not compared" documents. (Counts: `model:error` 13314 and `model:ok` 24894 in the first
  line of the w688-m4 output for S9 at 9941e4b8, and 13707 + 11187 = 24894. The run w688-gbom2
  at 32e7bc5e prints the same counts.)
- S9 before the repair: the model at 749194e7 reported a scanner error for 12606 documents. go-yaml
  accepted 120 of them, so the model gave 120 false refusals, with document 33458 among them. That
  document is `k: !bwref x/foo` and ` bar` on the next line, after a byte order mark. The old
  comparator did not count a scanner error as a false refusal, so it hid all 120. The repaired
  model gives 0 on the same set.
- S8: no document is decided by the bug and the model reports "misplaced" for none. The model
  reports a scanner error for 77244 documents, and go-yaml refuses all of them. The model reports
  "unsupported" for 11225 documents: 11148 with a directive, 72 with a tab after a comment
  line and 5 that start with two byte order marks. go-yaml refuses 7519 of them and accepts
  3706. These 11225 and 94597 documents that both oracles refuse for another reason are the
  105822 "not compared" documents. That is 43.07% of S8. (Counts: `model:error` 77244 and
  `not compared` 94597 in the first line of the compare output for S8 at 8878180d, and
  11225 + 94597 = 105822. At 9941e4b8 the same line held 11220 and 94598.)
- The repair of the leading-break flag (9941e4b8) moves 744 documents of S9 and 21064 documents
  of S8 from the verdict "ok" to a scanner error. These are the only verdict changes in the two
  sets, and go-yaml and the patched oracle refuse all of them. In the ten samples that the
  comparison printed (the first five of each set), the model's new error is the go-yaml reason
  "mapping values are not allowed in this context". The S9 samples are `k: !bwref x/foo` with a
  continuation line `bar, k: v]`. The S8 samples are a quoted key `"k"`, then `:!bwref x/bar` on
  the next line, then `b: !bwref x/foo`. The change is 12570 + 744 =
  13314 scanner errors in S9 and 56180 + 21064 = 77244 in S8, and 744 and 21064 fewer "not
  compared" documents. No document is a false refusal or a miss.
- S8, the error text: the repair of 9941e4b8 also changes the text of the scanner error for 3253 documents
  (S9: none), with the verdict "error" before and after. The old text was "found unexpected end of
  stream" for 2436 of them, "could not find expected ':'" for 481, "found character that cannot
  start any token" for 288 and "found unexpected document indicator" for 48. All 3253 now read
  "mapping values are not allowed in this context". go-yaml and the patched oracle refuse all of
  them. The new text equals the go-yaml reason in 3218 of them (2408 + 474 + 288 + 48). In the
  other 35, go-yaml refuses for another reason, and the old text matched it in none. No count of
  this record uses the text of an error.
- The repair of the byte order mark (8878180d) deletes the skip of a mark at column 0 of a later
  line and makes a document that starts with two marks "unsupported". The compare at 8878180d,
  read against the compare at 9941e4b8, moves these counts and no other: in S8, "unsupported"
  11220 to 11225, "agree" 139878 to 139874 and "not compared: both refuse, model ok" 94598 to
  94597. The 5 documents that move are the S8 documents that start with two marks. For go-yaml,
  4 of them are accepted by both oracles and 1 is refused by both, and none is decided by the
  bug. S9 and s13 hold none, and no count of s13, S9 or the four other small sets moves. S8 holds
  410 documents with a mark after the first character (the 5 included), and the deletion of the
  skip alone changes no verdict among them. The 5 move because of the new "unsupported" rule.
- S8, the counters: 3806 documents are 2902 plus 904. These are the documents with the key
  `instr hit:true` and a model verdict that is not "misplaced" (`compare.mjs:65`). A hit means that
  the counter `consumed` is above 0 (`compare.mjs:64`): go-yaml consumed a head token while a
  simple key stood on it. In 2902 of them the model says "ok". In 904 it reports a scanner error.
  The repair of 9941e4b8 moved one of these documents from "ok" to a scanner error (2903 and 903
  before, in the run w688-l3 at 77eff0e0).
  None is a miss, for three reasons:
  - The count of a misplaced insert that the model does not predict (`compare.mjs:66`, the key
    `instr misplaced but model not`) is 0. The key is absent from the count line. The `instr` keys
    add up to 245696, so the counters read every document.
  - The count of a hit where go-yaml accepts the document (`compare.mjs:67`, the key `instr hit,
    real accepts`) is 0. The key is absent too.
  - Both oracles refuse all 3806 documents. The bug decides none of them, because the patched
    oracle refuses them too.

## Limits

- Only go-yaml v3.0.5. A later go-yaml can fix the delete. Then a scanner that ports the model
  would refuse documents that the server accepts, and it would lose names.
- The model scans the whole stream, so it predicts that the defect fires. It does not predict the
  parser error that comes first in some documents.
- The model does not port directives. S8 holds 11148 documents with a directive that the model
  calls unsupported. go-yaml refuses 7446 of them and accepts 3702 (run w688-l3 at 77eff0e0). None is
  decided by the bug, but the model proves nothing for them.
- The model does not port a tab that follows a comment line. go-yaml reads a run of comment lines
  in one step (`yaml_parser_scan_comments`) and accepts a tab inside the run. The model skips one
  comment at a time, so it reported a scanner error for a document that go-yaml accepts (the edge
  document `a: 1`, `# c`, a tab and `# d`, then `b: V`). The model now calls such a document
  "unsupported". S8 holds 72 of them, and go-yaml refuses all 72 for another reason (run w688-m4
  at 9941e4b8). s13 and S9 hold none. The model makes no claim for a tab inside a run of comments.
- A tab inside the continuation of a plain scalar is ported. The model before the repair refused
  it (the edge document `a:`, `  b: [x`, ` y`, a tab and `z]`), and no document of S9 or S8 showed
  that as a false refusal, so the sets alone did not catch it. The edge document pins it now. A
  tab in another shape is tested only by what S9, S8 and the edge set hold.
- The model does not check the characters that the go-yaml reader refuses, such as control
  characters, and it does not read UTF-16 input. The oracle receives UTF-8.
- The sets come from the families in #657 and #654. The review of this record found two gaps
  outside them: the line breaks U+0085, U+2028 and U+2029, and keys of astral characters. The
  edge set covers them now. Another shape outside the families is not tested.
- The byte order mark was not a gap in the sets. S9 held it, and the model failed on it: 120 false
  refusals (see the S9 note). The test existed and its failure was ignored, because the old
  comparator did not count a scanner error as a false refusal. The repair fixes the comparator and
  the model.
- A document that starts with two byte order marks is a residual limit. go-yaml tests the start of its
  buffer, not the current offset, so a second mark right behind the first one makes its test true at
  every column-0 pass. The model does not port that, so it declares the shape "unsupported". If the bug
  decided such a document, `compare.mjs:49` would count it as missed, because the model predicts
  nothing. The edge set had such a document as its 14th (`${BOM}${BOM}[? b]: ...`). The bug decides it
  (go-yaml refuses it, the guarded build accepts it) and the model leaves it unsupported, so the
  comparison counted it as missed and exited 1 (run w688-gbom at 8878180d). The edge set dropped it
  (7eba3c8f). The verdict "unsupported" for a two-mark document is pinned in the self-test
  instead. S8 holds 5 such documents and none is decided by the bug (4 accepted by both oracles, 1
  refused by both). S9 and s13 hold 0 (run w688-gbom2 at 32e7bc5e).
- Only the edge set (2 documents), S9 (3456 documents) and S8 (2486 documents) hold tab characters. The flow-key families hold
  none. S9 holds 1584 documents with a byte order mark and S8 holds 420 (10 with the mark at the start
  only and 410 with a mark after the first character), counted by the run w688-bom at 1aa97028.
- The self-test does not plant a guard-on misplaced document, because a plant needs a broken
  model. Only a reading of `compare.mjs` shows the "no misplaced insert with the guard on" leg.
- At 32e7bc5e the model has 453 lines. The experiment as a whole adds 952 lines under `experiments`, of which
  64 are patch files and 47 are the self-test. This is more than the 600-line budget. The
  generators and the Go oracles are the reproduction that an investigation must land, so they
  stay.

## Alternatives

- A character-level rule on the text of the key. Three attempts in #654 failed on a new edge. The
  decision depends on the registration state and on the queue, so no rule on the text decides it.
- A fix in go-yaml. It is outside this project. The guarded delete in `go-yaml-guard.patch` makes
  80 of the 90 refused matrix cells accepted, so it is a candidate for an upstream report.
- The known gap: keep the 5 pinned rows in `position.test.ts` (at 3c207865) and record the evidence.

## Recommendation

On the documents that it compares, the model predicts the go-yaml refusals of this class exactly,
with 0 missed documents and 0 false refusals. The result covers the compared documents only. It
says nothing about the documents that were never compared:

- s13: 63 of 23247 documents were never compared.
- S9: 11187 of 38208 documents were never compared.
- S8: 105822 of 245696 documents were never compared. This is 43.07% of S8. A result of 0 and 0
  over the other 139874 documents is no evidence for the whole set. Of the 105822, 11225 are
  documents that the model does not port: 11148 have a directive, 72 have a tab after a
  comment line and 5 start with two byte order marks. The other 94597 are documents that both
  oracles refuse for another reason, and the model says "ok" for them.
- The edge set: 2 of 13 documents were never compared. One has a tab after a comment line, which
  the model does not port. In the other, both oracles refuse a shorthand tag that swallows `]`.

The result is not a proof for every document, and the review found two gaps that the sets did not
cover. S9 and S8 hold no document that the bug decides, so they test one thing: that the model
refuses no document that go-yaml accepts. The model before the repair failed that test on S9 with
120 false refusals, and the comparison did not report it. This is why the result stands on the
repaired comparison and not on the first run. It also shows that a port needs a check that can
fail. So #657 can port the model only with a differential check in CI. The check runs the model
and the oracles on the sets above and on a generator with Unicode input, and it fails on any
difference. It also prints the count of documents that it did not compare.

The port needs the token queue, the simple-key stack and the indent stack inside the scanner,
about the size of `model.mjs`. It must tie the rule to the go-yaml pin. An upgrade of go-yaml that
fixes the defect makes the port refuse documents that go-yaml accepts.

If the owner finds the size of the port too high for a hint in an editor, the known gap is a safe
choice. The scanner lists a name in a document that go-yaml refuses, and the server reads no
reference from it. Either way, #689 stays a separate issue.
