# A plain `!bwref` value that goes on to the next line

Date: 5 November 2026. Issue: ginsys/bronzeward#616. Status: design note for the scanner change. No code in this note.

Line numbers for `web/src/position.ts` are those of commit 28a9c367 on main. Line numbers for `scannerc.go` are those of go-yaml v3.0.5, the parser of the server.

## 1. Question

The editor lists the `!bwref` names of a draft with the scanner in `web/src/position.ts`. A plain value can go on over a line break. The scanner must decide, for each tag, where the value ends. go-yaml decides the same. This note names the shapes where the two agree, the shapes where they differ and the shapes that stay unread.

Out of scope, as the issue says: a tagged key, a verbatim tag and `%TAG` handles (ginsys/bronzeward#615).

## 2. How go-yaml ends a plain scalar

`yaml_parser_scan_plain_scalar` (`scannerc.go:2693-2818`) reads on over line breaks. It stops at these places only:

- `---` or `...` at column 0, followed by a blank (`2708-2717`).
- A `#` at the start of a word (`2720`).
- A colon followed by a blank (`2728`).
- In flow context only: `,` `?` `[` `]` `{` `}` (`2729-2733`).
- A line less indented than the parent plus one, in block context only (`2815`).

A blank is a space or a tab (`yamlprivateh.go:118-121`). A no-break space (U+00A0) is text. In block context a comma, a bracket, a brace, `- ` and `? ` are text at the start of a continuation line. In flow context the indent test is off.

When a colon followed by a blank ends the scalar on a continuation line, the colon is a value indicator with no valid simple key. The parser refuses it with "mapping values are not allowed in this context" (`scannerc.go:1409-1411`). The oracle lists no name for these documents.

## 3. How the scanner reads it

- `tags()` (`position.ts:104-181`) reads the value of each `!bwref` property with a pattern. The word of a plain value excludes `, [ ] { }` and a colon before a blank (`:115`, `:130`).
- A colon after the value makes it a key, and the scanner skips the tag (`:129`, `:168`).
- A value with no anchor goes to `continues()`. A value after an anchor goes to `anchoredContinues()` (`:175`).
- `continues()` (`:233-241`) ends the scalar at the end of the text and at a marker. It ends it at a line no more indented than the holding node (`:235-238`).
- It also ends the scalar at a line that starts with a comma, a closing bracket or a closing brace. The same holds for `-` or `?` followed by a blank, and for a line with a colon and a blank (`:239-240`).
- It has no flow case. It skips lines that hold only Unicode spaces, because it trims with `trim()` (`:191`, `:235`, `:239`).
- `anchoredContinues()` (`:264-275`) is conservative on purpose. For a parent shape it cannot read, it says "continues" and nothing is listed (`:266`). It reads blanks as YAML does (`:245`).

## 4. Shapes

The counts come from the S9 report of the sweep on main, read by kind and next-line shape: 38208 inputs, 13707 parsed by go-yaml, 3865 lost, 2248 extra. They are an input to the design. The code phase counts again at its own base.

| Shape | go-yaml | Scanner today | Class |
|---|---|---|---|
| Value at the end of the text, or before a marker | Scalar ends | Lists the name | Read, agrees |
| Next line less or equally indented than the holding node | Scalar ends | Lists the name | Read, agrees |
| Next line more indented, ordinary text | Scalar goes on, name is longer | Lists none | Read, agrees |
| Block, next line more indented and starts with `,` `]` `}` | Text, scalar goes on | Ends the scalar, lists the name | Read wrongly: extra |
| Block, the same with `- ` or `? ` | Text, scalar goes on | Ends the scalar, lists the name | Read wrongly: extra |
| Block, a more indented line of Unicode spaces only | Text, scalar goes on | Skips the line, lists the name | Read wrongly: extra |
| Block, a more indented line with a colon and a blank | Document refused | Ends the scalar, lists the name | Read wrongly: extra |
| Flow, next line at any indent, such as `bar]` at column 0 | Scalar goes on, indent test is off | Compares the indent, lists the name | Read wrongly: extra |
| Flow, next line starts with a flow indicator | Scalar ends | Lists the name | Read, agrees |
| Anchor after the tag (`!bwref &e x/foo`), any next line | Lists the name after a scalar that ends | Lists none for a parent shape it cannot read | Unread by decision: 1463 lost |
| Tag on an implicit key (`!bwref x/foo:`) | Tagged key, oracle lists the name | Lists none | Unread by decision: 1201 lost, ginsys/bronzeward#629 |

Extra rows by the report: 1124 documents with no anchor and 1124 with the anchor before the tag. Both use the same path (`position.ts:175`). 1100 of the 1124 have the value `x/foo`. The other 24 have `x/foo:` on the same line.

## 5. Shapes left unread, and why

- The anchor after the tag. #607 made the rule conservative on purpose (`position.ts:253-263`). The acceptance of #616 asks for no newly lost and no newly extra document against main. It does not ask for a lost count.
- The tag on an implicit key. ginsys/bronzeward#615 says the server refuses it. The editor lists nothing, and the oracle lists the name. ginsys/bronzeward#629 holds the decision.
- A tab in a continuation line (`scannerc.go:2783`) is not covered by this note.
- A comment line between the value and the next text is not covered by this note.

## 6. Evidence gaps

- The reasons in section 4 are read from name lists and from the go-yaml source. No oracle run isolated each shape. The code phase reads the oracle row of each shape before it writes the expected list.
- The report groups the next lines by kind, anchor place and context. It does not split by indent or by a blank line before.
- The counts are those of main at the time of the sweep. The counting rules of the sweep changed since the issue was written.

## 7. Decision enabled

The scanner change has two rules, tested one at a time on S9: the block text rules, and the flow case with no indent test. Each lands with no newly lost and no newly extra pair on S8 and S9, read with `cmp-reports.mjs`. The shapes of section 5 stay unread and are named here.
