# An alias that names no anchor (ginsys/bronzeward#672, section 3)

An alias is `*name`. An anchor is `&name`. go-yaml v3.0.5 refuses a stream with the error `unknown
anchor 'e' referenced` when an alias names no anchor that stands before it. The server reads no
reference from a refused stream. So the editor must list nothing for it.

## How the table was made

- Set: 33 synthetic streams. The value `V` is `!bwref x/foo`. Streams 1 to 10 are the table of the issue.
  The others probe the scope of an anchor across `---` and `...`, a directive, a re-defined
  anchor, and text that only looks like an anchor (a comment, a quoted, plain or block scalar).
- go-yaml: the oracle of this directory, `go run . <set.json> <result.json>`, on go.yaml.in/yaml/v3 v3.0.5.
- Server: `serverprobe/`, `go run . <cases.json> <result.json>`. Each case declares the name `x/foo`.
- Scanner: `references()` of `web/src/position.ts` after the change, run with node v24.

## What the probes show

- An anchor defines its name for the rest of the stream. It holds across `---` and `...` (rows 10, 11,
  16, 26 and 33). An anchor re-defined later is fine (rows 13 and 14).
- An anchor on a key, in a flow collection, or on the tagged value defines its name (rows 9, 21, 24
  and 25).
- Text that looks like an anchor defines nothing: a comment, a quoted scalar, a plain scalar and a
  block scalar (rows 17, 18, 19 and 23). Each of those streams is refused.
- An alias before its anchor is refused, also in one flow sequence (rows 3 and 20).
- The server agrees with go-yaml on 32 of 33 streams. Row 22 is the exception. go-yaml accepts it and
  the server refuses it, because the alias key repeats a key. That is another cause, so row 22 is no
  test of this section.
- The scanner agrees with go-yaml on 31 of 33 streams. Rows 29 and 30 list `x/foo` where go-yaml
  refuses for another reason: a dot in an anchor name and a comma after an alias in block context.
  Both are a name that go-yaml refuses for another reason, which this issue does not solve. The
  scanner on main lists the same name for both.

## Table

| # | document | go-yaml | server | scanner |
|---|---|---|---|---|
| 1 | `"a: *e\nb: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 2 | `"*e: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 3 | `"a: *e\nb: &e 1\nc: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 4 | `"[*e]: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 5 | `"a: [*e]\nb: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 6 | `"? *e\n: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 7 | `"a: !bwref x/foo\nb: *e\n"` | refuses | refuses | lists nothing |
| 8 | `"a: &e 1\nb: *e\nc: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 9 | `"a: &e !bwref x/foo\nb: *e\n"` | accepts | accepts | lists x/foo |
| 10 | `"a: &e 1\n---\nb: *e\nc: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 11 | `"a: &e 1\n...\n---\nb: *e\nc: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 12 | `"%YAML 1.2\n---\na: &e 1\n...\n%YAML 1.2\n---\nb: *e\nc: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 13 | `"a: &e 1\nb: &e 2\nc: *e\nd: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 14 | `"a: &e 1\n---\nb: &e 2\n---\nc: *e\nd: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 15 | `"a: &e 1\n---\nb: &f 2\n---\nc: *f\nd: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 16 | `"a: &e 1\n--- *e\n---\nc: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 17 | `"# &e\na: *e\nb: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 18 | `"a: \"&e\"\nb: *e\nc: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 19 | `"a: x &e\nb: *e\nc: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 20 | `"a: [*e, &e 1]\nc: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 21 | `"&e k: 1\nb: *e\nc: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 22 | `"&e k: 1\n*e : 2\nc: !bwref x/foo\n"` | accepts | refuses | lists x/foo |
| 23 | `"a: \|\n  &e x\nb: *e\nc: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 24 | `"- !bwref x/foo\n- [&e a, *e]\n"` | accepts | accepts | lists x/foo |
| 25 | `"- &e a\n- [*e]\n- !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 26 | `"a: &e 1\n%TAG !u! tag:example.com,2000:\n---\nb: *e\nc: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
| 27 | `"a: &e 1\n...\nb: *e\nc: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 28 | `"a: *é\nb: !bwref x/foo\n"` | refuses | refuses | lists nothing |
| 29 | `"a: &e.f 1\nb: *e.f\nc: !bwref x/foo\n"` | refuses | refuses | lists x/foo |
| 30 | `"a: &e 1\nb: *e,\nc: !bwref x/foo\n"` | refuses | refuses | lists x/foo |
| 31 | `"a: &e 1\nb: [*e, !bwref x/foo]\n"` | accepts | accepts | lists x/foo |
| 32 | `"a: *e\nb: &e 1\n"` | refuses | refuses | lists nothing |
| 33 | `"a: &e 1\n---\nb: *e\n---\nc: !bwref x/foo\n"` | accepts | accepts | lists x/foo |
