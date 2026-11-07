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
| Run head | 8cb8d066, one commit on 3c207865. The runs of s13, S9 and S8 used this head with a clean checkout. |

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
`yaml_parser_fetch_value` inserts the KEY token at `token_number - tokens_parsed`. That position
is negative. The insert function appends a token at a negative position, so the KEY lands after
the VALUE token. `yaml_parser_roll_indent` does the same for BLOCK-MAPPING-START (1018-1021). The
parser then reports "did not find expected key". At the document root it reports "did not find
expected <document start>".

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

The driver follows the parser. Before the parser consumes a token, it runs
`yaml_parser_fetch_more_tokens` once. Then it removes the head token. The model does the same, and
it scans to the end of the stream.

The verdict "misplaced" means that a KEY or BLOCK-MAPPING-START was inserted at a negative
position. The verdict "error" means a scanner error. The verdict "ok" means neither. The verdict
"unsupported" means a directive, which the model does not port.

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
./build-oracles.sh <work>                     # the three oracles, from go-yaml in the module cache
node gen-small-sets.mjs <work>                # matrix.json, probes.json, hyp.json
./run-compare.sh <work> <work>/matrix.json    # likewise probes.json and hyp.json
node gen-flow-key-set.mjs <work>/s13.json     # then run-compare.sh on it
```

`lane.sh` runs s13, S9 and S8 one after the other, on sets that you made first. The run of this
record took 15 seconds. The model comparison used about 120 MB on 25200 documents, and each oracle
used under 20 MB. The host check before the run required a flat 16384 MiB of free memory and
passed.

`compare.mjs` prints one line of counts and then the differing documents. The bug decides a
document when the real oracle refuses it and the patched oracle accepts it. The model passes when
it meets two conditions. It reports "misplaced" for every such document (none missed). It reports
"misplaced" only for documents that the real oracle refuses (no false refusal). The patched oracle
can refuse a document for another reason, such as `[,]`. The model can report "misplaced" there,
because the bug fires there too. A model run with the guard switched on must report "misplaced"
for no document.

## Results

The model needed no rework. Every row below has 0 missed and 0 false refusals, and the guarded
model reports "misplaced" for 0 documents. "Control" is the count of documents that go-yaml
accepts with a name, which shows that the run reads names at all.

| Set | Documents | Control | Decided by the bug | Model "misplaced" |
|---|---|---|---|---|
| Matrix of #657 (18 keys x 10 values) | 180 | 90 | 80 | 80 |
| Probe 5 and 6 of #657, plus root and `a:` shapes | 22 | 13 | 9 | 9 |
| Hypothesis set (21 flow keys x 6 contexts) | 126 | 90 | 18 | 21 |
| Small keys of `?` and `:` (below) | 45 | 0 | 15 | 45 |
| s13, the flow-key set of #657 | 23247 | 15099 | 8085 | 8085 |
| S9 | 38208 | 5955 | 0 | 0 |
| S8 | 245696 | 53570 | 0 | 6 |

Notes on the rows:

- Matrix: the other 10 documents use the key `*e`. Both refuse them, with "unknown anchor". That
  is [#689](https://github.com/ginsys/bronzeward/issues/689), not this defect.
- Hypothesis set: the 3 extra documents have the key `[,]`. go-yaml refuses them in both runs. The
  model says the defect fires, and the counters agree for 2 of them. In the third, the parser
  refuses first.
- Small keys: 15 keys of `?` and `:` in 3 contexts. Of 45 documents, 15 are decided by the bug (`{?}`,
  `[?,]`, `[? b]`, `[?b]` and `[?:]`). The other 30 refuse in both runs for another reason,
  such as `[?]` and `[:b]`. The model reports "misplaced" for all 45 and the real run refuses all
  45. The set is a one-line generator:
  `for k of ["[?]","[:]","{?}","{:}","[? ]","[:b]","{:b}","[: b]","[?,]","[,?]","[? b]","[?b]","[:,]","[?:]","[? ? b]"]`
  with the contexts `K: V`, `a:\n  K: V` and `a: b\nK: V`, where `V` is `!bwref x/foo\n`.
- s13: the count of 8085 equals the 8085 names that the scanner on main lists in documents that
  go-yaml refuses (#657). The counters saw the event in all 8085. 63 documents refuse in both runs
  for another reason, and the model says "ok" for them. They are outside the class.
- S9: no document is decided by the bug. 12606 documents have a scanner error in the model. go-yaml
  refuses all of them too.
- S8: no document is decided by the bug. 6 documents are "misplaced" and go-yaml refuses them in
  both runs for another reason.

## Limits

- Only go-yaml v3.0.5. A later go-yaml can fix the delete. Then a scanner that ports the model
  would refuse documents that the server accepts, and it would lose names.
- The model scans the whole stream, so it predicts that the defect fires. It does not predict the
  parser error that comes first in some documents.
- The model does not port directives. S8 holds 8136 documents with a directive. In 4896 of them
  go-yaml refuses the document, and in 3240 it accepts the document. None is decided by the bug,
  but the model proves nothing for them.
- The sets are built from the families in #657 and #654. A shape outside them is not tested. The
  sets hold no tab characters.
- The model has about 430 lines. The experiment as a whole is about 770 non-test lines. This is
  more than the 600-line budget. The generators and the Go oracles are the reproduction that an
  investigation must land, so they stay.

## Alternatives

- A character-level rule on the text of the key. Three attempts in #654 failed on a new edge. The
  decision depends on the registration state and on the queue, so no rule on the text decides it.
- A fix in go-yaml. It is outside this project. The guarded delete in `go-yaml-guard.patch` makes
  80 of the 90 refused matrix cells accepted, so it is a candidate for an upstream report.
- The known gap: keep the 5 pinned rows in `position.test.ts` and record the evidence.

## Recommendation

The model meets the pass rule of #688. It differs from go-yaml on 0 documents of every set, with no
rework. So #657 can port it. The port needs the token queue, the simple-key stack and the indent
stack inside the scanner, about the size of `model.mjs`. It must tie the rule to the go-yaml pin
and keep the oracle sets in CI, because an upgrade of go-yaml that fixes the defect makes the port
refuse documents that go-yaml accepts.

If the owner finds the size of the port too high for a hint in an editor, the known gap is a safe
choice. The scanner lists a name in a document that go-yaml refuses, and the server reads no
reference from it. Either way, #689 stays a separate issue.
