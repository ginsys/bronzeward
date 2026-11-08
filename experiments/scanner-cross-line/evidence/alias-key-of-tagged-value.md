# An alias key that repeats the anchor of a tagged value (ginsys/bronzeward#672, section 1)

An alias key is an alias in the place of a mapping key: `*k : v`. The server refuses a document in
which an alias key names an anchor on a `!bwref` node (`tag-placement`, the alias branch in
`internal/ingest/declare.go`). It does so for every node kind. So the editor must list no name for
the tagged value, because the server declares nothing for it.

## How the table was made

- Set: 25 synthetic documents. The value `V` is `!bwref x/foo`. Documents 1 to 4 are the shapes of
  the issue. The others probe where the alias sits (block key, explicit key, flow mapping, flow
  sequence, a later document), which anchor is the latest, and a second tag in the document.
- go-yaml: the oracle of this directory, `go run . <set.json> <result.json>`, on go.yaml.in/yaml/v3
  v3.0.5, after the change of this section.
- Server: `serverprobe/`, `go run . <cases.json> <result.json>`. Each case declares `x/foo` and `x/bar`.
- Scanner: `references()` of `web/src/position.ts` after the change, run with node v24.

## What the probes show

- The server refuses every document where an alias key names the latest anchor of the name, and
  that anchor is on a `!bwref` node (documents 1 to 4, 7 to 10, 12 to 15, 17, 18, 20 and 21).
- The latest anchor decides. A later plain anchor of the same name clears the tag (document 11). A
  later tagged anchor sets it (document 12).
- An alias as a value, a quoted key `"*k"` and a name with no alias key stay accepted (documents 5,
  6, 16 and 25).
- The tag stays per tag, not per document. In document 17 the scanner drops `x/foo` and keeps
  `x/bar`, the same as the oracle, which lists `x/bar` and marks the document.
- Document 24 has no anchor. It is the case of section 3, and the scanner lists nothing.

## Open limits

The scanner lists `x/foo` in three documents that the server refuses. The scanner on main lists the
same name for each, so none of them is a new loss or a new extra name.

- Document 19, a multi-line flow key: `{a: &k !bwref x/foo, *k` then ` : v}`. go-yaml refuses it.
- Document 22, an alias inside a key collection: `? - *k` then `: v`. go-yaml accepts it and the
  server refuses it. A unit row in `web/src/position.test.ts` pins the result.
- Document 23, a bare `*k` as a block entry. go-yaml refuses it.

## Flow and explicit-key spellings (review of the first head)

The local review of the first head found two spellings that the first reading missed. A second probe
(14 documents, then 22 and 17 more for the near misses) confirmed both against go-yaml and the server. Every
document below is refused by the server and read as unfit by the oracle.

- In flow context the alias name ends at the colon. The scanner now reads a colon after the alias as a
  value indicator whatever follows it, an entry of a flow mapping that starts with the alias (after `{`
  or `,`) as a key with or without a value, and a `?` after a flow bracket or comma as the key indicator,
  with or without a blank after it (`{?*k}`, `[?*k]`, `{a: 1, ?*k}`).
  Documents: `b: {? *k}`, `b: {*k:}`, `b: {*k :}`, `b: {*k:"v"}`, `b: {*k, c: d}`,
  `b: {a: [*k, d], *k}`, `b: {a: 1, *k:v}`, `b: [*k:]`, `b: [*k:"v"]`, `b: [a, *k:]`, `b: [a, *k :x]` and
  `b: [? *k]`, each after `a: &k !bwref x/foo`.
- An explicit key whose node starts on a later line. The scanner now carries "the last token with content
  was a `?` that ends its line" across blank and comment lines. Documents: `?` then `  *k` then `: v`,
  the same with no value, with a comment after the `?`, with a blank line and with a comment line before
  the alias.
- Near misses that the server accepts and the scanner still lists: an alias as a flow mapping value
  (`{a: *k}`, `{a: *k, c: d}`, `{a: [*k], c: d}`), an alias in a flow sequence (`[*k]`, `[*k, c]`), a `?`
  in a quoted key or value, and a `?` line followed by a key (`?` then `b: *k`).
- Still a limit: an alias that is the first node of a line inside a flow collection (`{a: 1,` then
  ` *k}`). The server refuses it and go-yaml accepts it, like document 19.

## Table

| # | document | go-yaml | server | scanner |
|---|---|---|---|---|
| 1 | `"a: &k !bwref x/foo\n*k : v\n"` | refuses | refuses | lists nothing |
| 2 | `"a: !bwref &k x/foo\n*k : v\n"` | refuses | refuses | lists nothing |
| 3 | `"a: &k !bwref x/foo\n? *k\n: v\n"` | refuses | refuses | lists nothing |
| 4 | `"a: &k !bwref x/foo\n*k: v\n"` | refuses | refuses | lists nothing |
| 5 | `"a: &k !bwref x/foo\nb: *k\n"` | `x/foo` | accepts | `x/foo` |
| 6 | `"- &k !bwref x/foo\n- *k\n"` | `x/foo` | accepts | `x/foo` |
| 7 | `"{a: &k !bwref x/foo, *k : v}\n"` | refuses | refuses | lists nothing |
| 8 | `"[&k !bwref x/foo, *k : v]\n"` | refuses | refuses | lists nothing |
| 9 | `"a: &k !bwref x/foo\n*k :\n  w: 1\n"` | refuses | refuses | lists nothing |
| 10 | `"&k !bwref x/foo\n---\n*k : v\n"` | refuses | refuses | lists nothing |
| 11 | `"a: &k !bwref x/foo\nb: &k 1\n*k : v\n"` | `x/foo` | accepts | `x/foo` |
| 12 | `"a: &k 1\nb: &k !bwref x/foo\n*k : v\n"` | refuses | refuses | lists nothing |
| 13 | `"a: &k\n  !bwref x/foo\n*k : v\n"` | refuses | refuses | lists nothing |
| 14 | `"a: &k !bwref x/foo\n---\n*k : v\n"` | refuses | refuses | lists nothing |
| 15 | `"a: &k !bwref x/foo\nb: *k\n*k : v\n"` | refuses | refuses | lists nothing |
| 16 | `"a: &k !bwref x/foo\n\"*k\": v\n"` | `x/foo` | accepts | `x/foo` |
| 17 | `"a: &k !bwref x/foo\nc: !bwref x/bar\n*k : v\n"` | `x/bar`, marked | refuses | `x/bar` |
| 18 | `"a: &k !bwref x/foo\nb:\n  - *k : v\n"` | refuses | refuses | lists nothing |
| 19 | `"{a: &k !bwref x/foo, *k\n : v}\n"` | refuses | refuses | `x/foo` (limit) |
| 20 | `"a: &k !bwref x/foo\n*k : v\n*k : w\n"` | refuses | refuses | lists nothing |
| 21 | `"a: &k !bwref x/foo\n? *k\n"` | refuses | refuses | lists nothing |
| 22 | `"a: &k !bwref x/foo\n? - *k\n: v\n"` | `x/foo` | refuses | `x/foo` (limit) |
| 23 | `"a: &k !bwref x/foo\n*k\n"` | refuses | refuses | `x/foo` (limit) |
| 24 | `"a: !bwref x/foo\n*k : v\n"` | refuses | refuses | lists nothing |
| 25 | `"a: &k\nb: !bwref x/foo\n*k : v\n"` | `x/foo` | accepts | `x/foo` |
