# A plain `!bwref` value that goes on to the next line

Date: 5 November 2026. Issue: ginsys/bronzeward#616. Status: design note for the scanner change. No code in this note.

The scanner is named by function, because its line numbers move. Section 3 describes `web/src/position.ts` on main at commit bffb30ed, before the change. Line numbers for `scannerc.go` are those of go-yaml v3.0.5, the parser of the server.

## 1. Question

The editor lists the `!bwref` names of a draft with the scanner in `web/src/position.ts`. A plain value can go on over a line break. The scanner must decide, for each tag, where the value ends. go-yaml decides the same. This note names the shapes where the two agree, the shapes where they differ and the shapes that stay unread.

Out of scope, as the issue says: a tagged key, a verbatim tag and `%TAG` handles (ginsys/bronzeward#615).

## 2. How go-yaml ends a plain scalar

`yaml_parser_scan_plain_scalar` (`scannerc.go:2693-2818`) reads on over line breaks. It stops at these places only:

- `---` or `...` at column 0, followed by a blank (`2708-2717`).
- A `#` at the start of a word (`2720`).
- A colon followed by a blank (`2728`).
- In flow context only: `,` `?` `[` `]` `{` `}` (`2729-2733`).
- A line less indented than the parent plus one, in block context only (`2815`). A root scalar has no parent: its continuation lines are measured against column -1, so every next line with text goes on.

A blank is a space or a tab (`yamlprivateh.go:118-121`). A no-break space (U+00A0) is text. In block context a comma, a bracket, a brace, `- ` and `? ` are text at the start of a continuation line. In flow context the indent test is off.

When a colon followed by a blank ends the scalar on a continuation line, the colon is a value indicator with no valid simple key. The parser refuses it with "mapping values are not allowed in this context" (`scannerc.go:1409-1411`). The oracle lists no name for these documents.

## 3. How the scanner reads it on main

- `readTags()` reads the value of each `!bwref` property with a pattern. The word of a plain value excludes `, [ ] { }` and a colon before a blank.
- A colon after the value makes it a key, and the scanner skips the tag.
- A value with no anchor goes to `continues()`. A value after an anchor goes to `anchoredContinues()`.
- `continues()` ends the scalar at the end of the text and at a marker. It ends it at a line no more indented than the holding node.
- It also ends the scalar at a line that starts with a comma, a closing bracket or a closing brace. The same holds for `-` or `?` followed by a blank, and for a line with a colon and a blank.
- It has no flow case. It skips lines that hold only Unicode spaces, because it trims with `trim()`.
- `anchoredContinues()` is conservative on purpose. For a parent shape it cannot read, it says "continues" and nothing is listed. It reads blanks as YAML does.

## 4. Shapes

The counts come from sweep run `w616-s1`, which compared the scanner on main (bffb30ed) with the scanner at the change (05b7a6ef) on the sets S8 and S9 of `experiments/scanner-cross-line`. The command was `bash sweep-616.sh w616-s1`, a script that builds the go-yaml oracle, generates both sets and checks them against the manifest, runs `cmp.mjs` before and after, and then runs `cmp-reports.mjs` for each set.

| Set | Inputs | Parsed by go-yaml | Lost, before and after | Extra before | Extra after |
|---|---|---|---|---|---|
| S8 | 245696 | 66336 | 24 and 24 | 1195 | 1158 |
| S9 | 38208 | 13707 | 3865 and 3865 | 2248 | 628 |

| Shape | go-yaml | Scanner on main | Class |
|---|---|---|---|
| Value at the end of the text, or before a marker | Scalar ends | Lists the name | Read, agrees |
| Next line less or equally indented than the holding node | Scalar ends | Lists the name | Read, agrees |
| Next line more indented, ordinary text | Scalar goes on, name is longer | Lists none | Read, agrees |
| Block, next line more indented and starts with `,` `]` `}` | Text, scalar goes on | Ends the scalar, lists the name | Read wrongly: extra |
| Block, the same with `- ` or `? ` | Text, scalar goes on | Ends the scalar, lists the name | Read wrongly: extra |
| Block, a more indented line of Unicode spaces only | Text, scalar goes on | Skips the line, lists the name | Read wrongly: extra |
| Block, a more indented line with a colon and a blank | Document refused | Ends the scalar, lists the name | Read wrongly: extra |
| Block, a tab (or a space and a tab) after the parent's colon, then `- ` or `? ` on the next line | A tab is a blank like a space: the scalar goes on | The parent patterns took spaces only: the parent was not known and the line ended the scalar, so it lists the name | Read wrongly: extra. Fixed by `[ \t]+` after the colon (ginsys/bronzeward#698) |
| Block, a tab after the parent's colon, an anchor after the tag, then a sibling key | The scalar ends at the sibling: the name lists | The anchored pattern took spaces only: the scalar was read as going on, so it lists none | Read wrongly: lost. Fixed in the same way (ginsys/bronzeward#698) |
| Block, a tab after a dash (`-` then a tab, then the tag) | Document refused (`found character that cannot start any token`) | Lists the name | Refused, unchanged: the dash patterns take spaces only |
| Block, one anchor before the tag (`k: &e !bwref x/foo`, `- &e !bwref x/foo`, a quoted or nested key, a tab or two blanks around the anchor), then `- y` or `? y` | The anchor is a property of the value, so the parent is the key or the dash: the scalar goes on | The anchor made the parent unknown, so the line ended the scalar and it listed the name | Read wrongly: extra, with a space and with a tab. Fixed: `PLAIN_PARENT` takes one anchor and its blanks (ginsys/bronzeward#699). A space or a tab follows the anchor, on a key line or a dash line. Pinned by 21 unit tests with the next row, red on 0b3a42da (13 rows) and on 63f37307 (8 rows) and green after |
| Block, the anchor on the parent line and the tag on the next line (`k: &e` or `- &e`, then `  !bwref x/foo`, then `  - y`) | The scalar goes on | `PLAIN_PARENT_LINE` knew no anchor after the colon or the dash, so the line ended the scalar and it listed the name | Read wrongly: extra. Fixed in the same way: `PLAIN_PARENT_LINE` takes one anchor (ginsys/bronzeward#699) |
| Block, two anchors before the tag (`k: &a &e !bwref x/foo`), then `- y` | Document refused (`did not find expected key`) | Lists the name, as before | Refused, unchanged. The patterns take one anchor only |
| Block, a compact parent (`- k: !bwref x/foo`) or an explicit key (`? !bwref x/foo`), then a more indented `- y`, with or without an anchor | The scalar goes on | The parent is unknown by design, so the line ends the scalar and it lists the name | Read wrongly: extra. Not changed by ginsys/bronzeward#699, as the twin with no anchor lists the name on main too. Tracked in ginsys/bronzeward#706 |
| Block, the next line starts with a no-break space at column 0 (` j: v`) | A key of its own at column 0, scalar ends | Ends the scalar, lists the name | Read, agrees. Pinned by a unit test: S8 and S9 hold no such line |
| Block, the tag starts its line under a compact parent (`- k:` then `    !bwref x/foo`, then `  j: v`) | Scalar ends at the sibling key | Ends the scalar, lists the name | Read, agrees. Pinned by two unit tests: S8 and S9 hold no such shape |
| Root scalar (the tag on the line of `---`, or the first text of a document, anchors only before it), next line with text at any column | Measured against column -1: the scalar goes on, `- ` and `? ` are text | Reads the column against the holder 0 or the line above, ends the scalar at column 0 | Read wrongly: extra. Fixed by `isRoot()` |
| Root scalar, next line is a key (`y: v`) | Document refused | Ends the scalar, lists the name | Refused, lists none after the change |
| Flow, next line at any indent, such as `bar]` at column 0 | Scalar goes on, indent test is off | Compares the indent, lists the name | Read wrongly: extra |
| Flow, next line starts with `,`, `]`, `}` or a colon and a blank | Scalar ends | Lists the name | Read, agrees |
| Flow, next line starts with `?`, `[` or `{` | Scalar ends, document refused: go-yaml v3.0.5 gives "did not find expected ',' or ']'" (or '}') for all ten documents of `experiments/scanner-cross-line/flowprobe`, and the four controls parse (`experiments/scanner-cross-line/evidence/flow-refusal.txt`) | At c32920f9 it lists the name for `k: [!bwref x/foo\n[y]]\n` and for `k: {a: !bwref x/foo\n{y}}\n`. The branch scanner, unchanged since b688cb9c, reads the line as text, goes on and lists none for both | Refused, and the branch lists none, which agrees with go-yaml. Ending the value here would list a name for a refused document. Pinned by 12 unit tests |
| Anchor after the tag (`!bwref &e x/foo`), any next line | Lists the name after a scalar that ends | Lists none for a parent shape it cannot read | Unread by decision |
| Tag on an implicit key (`!bwref x/foo:`) | Tagged key, the server refuses it, oracle lists none | Lists none | Read, agrees. Pinned by unit tests, ginsys/bronzeward#629 and #637 |

Evidence for the anchor rows (ginsys/bronzeward#699). Two probes (runs `w699-probe` and `w699-probe2`) ran 50 documents through `references()` at main 0b3a42da and through go.yaml.in/yaml/v3 v3.0.5 unmodified, on node v24.22.1. The documents cover a key, a nested key, quoted keys, a sequence entry, a compact key, an explicit key, a root scalar and a flow context. Each has one anchor before the tag, on the line of the key or on the line above, with blanks or tabs around it. The next line is `- y`, `? y`, a sibling key, a more indented key or plain text. For 13 documents `references()` listed `x/foo` where go-yaml reads `x/foo - y` or `x/foo ? y`. Each of the 13 is a unit test now. go-yaml refuses 7 documents, and they stay out. The compact and explicit-key documents list the name with no anchor too, so they belong to ginsys/bronzeward#706. The unit tests of `position.test.ts` were red on main (13 failed, 776 passed, run `w699-red1`) and green with the change (789 passed, run `w699-green1`).

A review of this change found one more document. It has a tab after the anchor on a dash line, and the tag is on the next line. The dash branch of `PLAIN_PARENT_LINE` took spaces only after the anchor. The parent was unknown, so the name listed.

A third probe (run `w699-probe3`) ran 47 documents on the branch head 63f37307. They mix tabs and blanks after the anchor, on a key line and on a dash line. The tag is on the anchor line or on the next line. Some have a comment after the anchor, other anchor names or two anchors.

For 8 documents `references()` listed `x/foo` where go-yaml reads a longer scalar. All 8 have a tab after the anchor on a dash line. The key line already took tabs. Of the other 39 documents, 29 agree with go-yaml and go-yaml refuses 10. Each of the 8 is a unit test now. The 10 refused documents stay out.

The unit tests were red on 63f37307 (8 failed, 792 passed, run `w699-red2`). They are green with the change: 800 passed in `position.test.ts` and 1751 passed in the whole web suite (run `w699-green2`). The 3 cost rows for tabs after the anchor on a dash line pass on 63f37307 too. They discriminate nothing there. They pin the bound of the pattern.

In run `w616-s1`, S9 held 2248 extra documents on main. At 05b7a6ef the change fixes 1620 of them and leaves 628. It lists no new extra name and loses no document. On S8 it fixes 37 extra documents and leaves 1158. The 467 documents of S8 and the 908 of S9 that the oracle refuses stop listing a name, and no refused document starts listing one. The remaining extra documents are outside this issue, as sections 5 and 7 say.

## 5. Shapes left unread, and why

- The anchor after the tag. ginsys/bronzeward#607 made the rule conservative on purpose (`anchoredContinues()`). The acceptance of #616 asks for no newly lost and no newly extra document against main. It does not ask for a lost count.
- The tag on an implicit key. The server refuses it (`tag-placement` in `internal/ingest/declare.go`). The editor lists nothing, and the oracle lists no name either, so the two agree (ginsys/bronzeward#629 and #637). One shape stays a known gap: `[!bwref x/foo:]` in flow context, where the scanner lists `x/foo` and the server refuses the document as a bad name (ginsys/bronzeward#685).
- A parent key that is quoted with a doubled or an escaped quote inside (`'a''b': !bwref x/foo`, then `  - y`). `PLAIN_PARENT` does not read it as a known parent. A next line that starts with `- `, `? ` or holds a colon before a blank then ends the value, so the editor lists the name and go-yaml lists none. Main reads it the same way, so the change adds no extra pair. Reading such a key is a follow-up that needs new scanner scope.
- A root scalar where the anchor comes after the tag (`!bwref &e x/foo`) stays with the anchor rule above, ginsys/bronzeward#607. A key parent with an anchor before the tag (`k: &e !bwref x/foo`) was read the same as on main at that change. Both groups are named "same on main" in the root-scalar sweep (section 6). Since ginsys/bronzeward#699 the key parent reads the anchor as a property of the value, as the table in section 4 says.
- A tab in a continuation line (`scannerc.go:2783`) is not covered by this note.
- A comment line between the value and the next text is not covered by this note.

## 6. Evidence gaps

- The reasons in section 4 are read from name lists and from the go-yaml source. No oracle run isolated each shape. The code phase reads the oracle row of each shape before it writes the expected list.
- The report groups the next lines by kind, anchor place and context. It does not split by indent or by a blank line before.
- The sets S8 and S9 hold no line that starts with a no-break space at column 0, no tag on its own line under a compact parent, and no root scalar with a next line at column 0. The "no newly lost" claim for them rests on the oracle rows, the unit tests of `web/src/position.test.ts`, and the generated root-scalar set S10 (section 8).
- The counts are those of run `w616-s1`: at 05b7a6ef against main bffb30ed. The counting rules of the sweep changed since the issue was written, so they differ from the counts in the issue.
- The report of run `w616-s1` does not split the 628 extra documents of S9 or the 1158 of S8 at 05b7a6ef by shape. This note does not claim which shapes remain.

## 7. Decision enabled

The scanner change has two rules: the block text rules, and the flow case with no indent test. The shapes of section 5 stay unread and are named here.

## 8. The root scalar set S10

The set S10 (`experiments/scanner-cross-line/sets/gen-s10-root-scalar.ts`) holds the whole cross product: 12 contexts, three property orders, a space or a tab, six next lines and three texts. It has 1152 documents, and 666 of them are parsed by go-yaml v3.0.5. `node sets/gen-s10-root-scalar.ts s10.json` writes it, with sha256 `bc8293d2d0af073629e840731c7f0d31b7cba94fa4053ccdbf083f24cceedd5b`. `judge-s10.mjs` counts extra and lost names beside the oracle.

go-yaml measures the continuation lines of a root scalar against column -1. In each of the nine root contexts that go-yaml parses (the line of `---`, the first text of a stream, after a marker line, after a comment, after a `%YAML` directive with the marker on its own line or on the tag's line, and in a second document after `...` with a marker or after `---`), the tag only and the anchor before the tag give the same result: every next line with text goes on, at column 0 and at column 1 too, also a line that starts with U+00A0, `- ` or `? `. A key line makes go-yaml refuse the document. A second document after `...` with no `---` is refused whole.

| Scanner | Extra | Lost | Newly extra | Newly lost |
|---|---|---|---|---|
| Main | 242 | 26 | | |
| At the change | 2 | 26 | 0 | 0 |

The counts at 58ba0408 (sweep `w616-r2`) cover the 666 documents that go-yaml parses. A refused document counts as no extra name. The scanner lists a name for 244 of the 486 refused documents on main and 64 at the change. The change fixes 240 extra documents and starts none. What is left is outside this issue, and the scanner on main reads each of these documents the same way. The 2 extra documents have a key parent and an anchor before the tag on the line of the key (`k: &e !bwref x/foo`). The 26 lost documents have an anchor after the tag (`!bwref &e x/foo`), the decision of ginsys/bronzeward#607.

Two limits of the set. go-yaml v3.0.5 refuses `%YAML 1.2`, so the two directive contexts use `%YAML 1.1`. The context of a second document after `...` with no `---` is refused whole too, so it gives no evidence about the continuation of a root scalar.

A comment line after a root scalar ends it in go-yaml (`!bwref x/foo`, then `# c`, then `bar`). The text after the comment is then a document that go-yaml refuses, so the oracle lists no name. The scanner skips the comment line, because it is masked, reads `bar` as the next line and lists no name either. No pair can come from this case. S10 holds no comment as a next line. Two unit tests pin it (`web/src/position.test.ts`).

## 9. The indent offset set S11, and the failure that it found

- `continues()` measured the next line with `column()`, which counts YAML blanks (a space or a tab). The holder came from `holder()`, which counts with `indent()`, a measure that skips Unicode spaces. A line that starts with U+00A0 or a byte order mark (BOM) then compared against the wrong column. A U+00A0 or a BOM is text in go-yaml, not indentation. The change now measures the holder with `plainHolder()`, which uses `column()`. `holder()` stays for block scalars.
- An anchor or a tag before the tag (`&e !bwref x/foo` on its own line) hid the parent key from the scanner, so the tag line's own indent became the holder. This is a defect of main for plain ASCII too. For `"k:\n  &e !bwref x/foo\n  bar"` go-yaml reads `bar` as part of the value of `k`, so the value goes on and go-yaml lists no name. Main lists `x/foo`. The change lists none. The holder (`plainHolder()`) and the parent line (`aboveLines()`) now skip node properties that stand before the tag. `anchoredContinues()` differs from main only in its trim: main has the inline `next.replace(/^[ \t]+|[ \t\r]+$/g, "")`, and it now calls `trimBlanks()`.

- An indented root scalar (`  !bwref x/foo`, then any line with text) is a root scalar, measured against column -1. `isRoot()` accepts leading blanks.
- A BOM at the start of the stream is dropped by go-yaml (no text, no column). The scanner drops a BOM at offset 0 only, in `aboveLines()`, `plainHolder()`, `continues()` and in the scan back to the last marker of `isRoot()`. A second BOM right after it, at column 0, is one blank column for go-yaml: the oracle lists no name for `"﻿﻿!bwref x/foo\nbar\n"` (a root scalar, the value goes on) and lists `x/foo` for `"﻿﻿k: !bwref x/foo\n j: v\n"` (the key stands at column 1, so the next line is a sibling). `readTags()` turns the second BOM into a blank before the scans read the text. A third BOM stays text. The rows are `616-two-boms-root` and `616-two-boms-key` in S5.

A second tag before `!bwref` makes go-yaml refuse the document: a node takes one tag. The oracle (go-yaml v3.0.5) refuses `"k:\n  !t !bwref x/foo\n  bar\n"`, `"k: !t !bwref x/foo\n"` and, with the tags the other way round, `"k:\n  !bwref !t x/foo\n  bar\n"`, each with "did not find expected key". It lists no name for them, and the scanner lists none either (it blanks a document with a second tag on one node, ginsys/bronzeward#620). No pair can come from this shape.

S11 (`experiments/scanner-cross-line/sets/gen-s11-indent-offsets.ts`) holds 3888 documents, and go-yaml parses 1696 of them. Its sha256 is `f462167f9e28366ef5cc58c36c388d4f63fc8297e251ee4c2f20677c02d50fa0`. It varies the column of the node and the column of the next line together, with spaces, U+00A0 or a tab, so that a mix of the two measures cannot hide.

- The scan back to the last marker in `isRoot()` now uses `MARKER` and drops the BOM, and a line of Unicode spaces only counts as text in it.
- `plainHolder()` and the parent of a tag skip a line of node properties only (`propertiesOnly()`).
- `isRoot()` now skips a line of node properties only as well.
- Two patterns and one scan took more than linear time, which matters because the editor reads the draft while the user types. `isRoot()` split the whole text before the tag for each tag that stands alone on its line. It now walks back by line offsets and stops at the first line with text or at a marker. `propertiesOnly()` let a property start right after another one and let `![^ \t]*` read further `!` characters. `startsLine()` split a run of blanks between two properties in more than one way. Both patterns now need a blank after each property, so each line has one reading. The scanner tests hold a 30,000-entry draft and a long line of bangs.

- Three loops and patterns were still more than linear, and a second look at every loop and pattern of the change found no other. The walk back from a tag that starts its line to its parent line read every line in between: all lines above a tag at column 0, and all siblings above a tag among many (`k:\n` and then 2,000 times `  j${i}: v\n  !bwref x/foo${i}\n`). Main had the same walk in `holder()`. `aboveLines()` now finds the nearest less indented line of every line in one pass, with a stack, and `plainHolder()` and the parent of a tag read the answer. `continues()` trimmed the next line with `/^[ \t]+|[ \t\r]+$/g`, which starts again at every blank of a long run inside the text. Both now use `trimBlanks()`, two walks by index. The first pattern of `isRoot()` shares its blanks between `---[ \t]+` and `[ \t]*`. No document reaches it that way: the oracle refuses a block entry, a key or an explicit key after `---` on its line, and the scanner reads no tag in a refused document. The scanner tests keep a bound of one second for each of these inputs, and for long inputs of the other patterns (5,000 properties, a key of 100,000 characters, 3,000 lines of an anchor and a blank line, a marker line with 40,000 blanks).

  The review of that change found one more cost. Each tag under one long parent line matched `PLAIN_PARENT_LINE` against that line again. The pattern is new in this change (main c32920f9 has no `PLAIN_PARENT_LINE`: 0 lines of main's `web/src/position.ts` name it, and the same read finds `bomStream` on 2 lines and `ANCHORED_PARENT` on 2). `aboveLines()` now keeps the answer on the line (`known`), so each parent line is matched once. `k: &e !bwref a` calls `continues()`, while an anchor after the tag (`k: !bwref &e a`) calls `anchoredContinues()`. The tests now cover the three paths with a run of 60,000 blanks inside the line, at its start and at its end, a tag below 1,000 keys of rising indentation (the stack of `aboveLines()` is as deep as the indentation) and a key of 100,000 characters before the tag, once with a match and once with a rejection.

  The test of the long parent line was too close to its bound: on 86c93ac4, 2 of 6 runs passed at 947 and 998 ms. It now has 4,000 tags under a key of 300,000 characters. All 6 runs on 86c93ac4 took 2.1 to 2.8 s and failed the bound. A line of 5,000 anchors before the tag (`k:\n  &e &e … !bwref x/foo\n  bar\n`) calls `startsLine()`. A line of 5,000 anchors above the tag calls `propertiesOnly()`. go-yaml v3.0.5 refuses both inputs (`yaml: line 1: did not find expected key`), and a single anchor parses. The scanner still reads them, because the editor scans drafts that go-yaml refuses. Both inputs are now tests.

  The comment of each cost test gives the time of the earlier code where it was measured, with the commit. A test with no such figure is not shown to fail first.

S12 (`experiments/scanner-cross-line/sets/gen-s12-marker-and-property-lines.ts`) covers both places, each with spaces, U+00A0 and a tab in the next line. It holds 3456 documents, and go-yaml parses 1972 of them. Its sha256 is `4d03bb84d870342650a85924730923f6f88c5e21e6b591865fd40ef035672967`. The sweep `w616-r2` ran an S12 of 3240 documents (sha256 `7e8bfc05676d128233b9eb23fd4adc79dcd9bd06c71e9ff5d9f3f24d18eddbc2`), and not the set of 3456 documents above. At 58ba0408 (sweep `w616-r2`), S12 has 0 new lost and 0 new extra documents, and the extra count falls from 1024 on main to 0.

Known effect, class B, in the sweep `w616-r2` at 58ba0408. The change does not start any extra or lost name. It does list a name for documents that go-yaml refuses: 48 on S9, 6 on S10, 56 on S11 and 16 on S12. In every one of the documents of S9, S10 and S11 a line that starts with U+00A0 stands at the column of the node, right after the value (`"k: !bwref x/foo\n bar\n"`). go-yaml ends the scalar at that line, because U+00A0 is text at column 0, and then refuses the document ("could not find expected ':'"). The scanner now ends the scalar there too and lists the name. Main skipped the line and listed none. Main already lists the name for the same document with an ASCII `bar` at column 0. A refused document lists nothing on the server, so these rows are not errors. The gate of the issue (no new lost, no new extra) does not count them.

Counts of the sweep `w616-r2` at commit 58ba0408 against main c32920f9, on the sets S5, S8, S9, S10, S11 and S12. The sweep `w616-r2` ran an S5 of 45 documents (sha256 `b0fa0f81c4fe767c7917ca0f1da674d1b79b993de8bde43cb8304ce7855c6709`), and not the current set of 48. The check below shows that no refused-listed row is in a document that go-yaml accepts. Run it from the root of the repository, with the directory of a sweep as the argument. That directory holds `reports-<set>.log`, the output of `cmp-reports.mjs`, and `<set>.oracle.json`, the output of the oracle on the pinned set.

```sh
node experiments/scanner-cross-line/check-refused-split.mjs <sweep dir> s5 s8 s9 s10 s11 s12
```

It reads the ids on the `refusedListed-new` line of each report and looks each one up in the oracle output. The result for the sweep `w616-r2` is 0 documents in an accepted document, for 0 of 0 on S5, 0 of 0 on S8, 0 of 48 on S9, 0 of 6 on S10, 0 of 56 on S11 and 0 of 16 on S12. The oracle refuses all 126 documents. The exit code is 1 if one id is in an accepted document or a file is missing.

| Set | Newly lost | Extra on main | Extra at the change | Newly extra | Refused, listed, new |
|---|---|---|---|---|---|
| S5 | 0 | 0 | 0 | 0 | 0 |
| S8 | 0 | 1195 | 1158 | 0 | 0 |
| S9 | 0 | 2248 | 270 | 0 | 48 |
| S10 | 0 | 242 | 2 | 0 | 6 |
| S11 | 0 | 790 | 96 | 0 | 56 |
| S12 | 0 | 1024 | 0 | 0 | 16 |

The S9 count of section 4 (628 extra after) is from the first sweep and is out of date. The 270 above supersedes it.
