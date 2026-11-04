import { readFileSync } from "node:fs";
import { describe, expect, test } from "vitest";
import { references, spotOfReference } from "./position.ts";

// ginsys/bronzeward#569: the server reads an embedded document's references from its decoded text
// (internal/ingest/declare.go walkEmbedded), so a quoted or block scalar that holds one carries
// them although its tags are no tags in the outer text.
describe("the references of a declared embedded document", () => {
  const at = (path: string) => [{ path }];

  test("a single-quoted, double-quoted, literal and folded embedded document carries its references", () => {
    expect(references("cfg: 'a: !bwref x/sq'\n", at("doc[0]/cfg"))).toEqual(["x/sq"]);
    expect(references('cfg: "a: !bwref \\"x/dq\\"\\nb: 1\\n"\n', at("doc[0]/cfg"))).toEqual(["x/dq"]);
    expect(references("cfg: |\n  a: !bwref x/lit\n  b: 1\n", at("doc[0]/cfg"))).toEqual(["x/lit"]);
    expect(references("cfg: >-\n  a: !bwref x/fold\n", at("doc[0]/cfg"))).toEqual(["x/fold"]);
  });

  test("an undeclared embedded document carries none", () => {
    expect(references("cfg: 'a: !bwref x/sq'\n")).toEqual([]);
    expect(references("cfg: 'a: !bwref x/sq'\nother: 'b: 1'\n", at("doc[0]/other"))).toEqual([]);
  });

  test("a path in a later document, and the outer references first, each once", () => {
    const text = "a: !bwref x/out\n---\ncfg: 'a: !bwref x/in'\nb: !bwref x/out\n";
    expect(references(text, at("doc[1]/cfg"))).toEqual(["x/out", "x/in"]);
    expect(references(text, at("doc[0]/cfg"))).toEqual(["x/out"]);
  });

  test("a scalar that is no string, a tag other than !!str, or a path into an embedded document holds none", () => {
    expect(references("cfg: 5\n", at("doc[0]/cfg"))).toEqual([]);
    expect(references("cfg: !custom 'a: !bwref x/t'\n", at("doc[0]/cfg"))).toEqual([]);
    expect(references("cfg: !!str 'a: !bwref x/t'\n", at("doc[0]/cfg"))).toEqual(["x/t"]);
    expect(references("cfg: 'a: !bwref x/t'\n", at("doc[0]/cfg|/a"))).toEqual([]);
    expect(references("cfg:\n  a: !bwref x/map\n", at("doc[0]/cfg"))).toEqual(["x/map"]);
  });

  test("a declared embedded document's references come from its decoded text only", () => {
    // Folded, the comment line and the next line are one comment line.
    const folded = "cfg: >-\n  # note\n  a: !bwref x/hidden\n";
    expect(references(folded, at("doc[0]/cfg"))).toEqual([]);
    expect(spotOfReference(folded, "x/hidden", at("doc[0]/cfg"))).toBeUndefined();
    // Undeclared, the text of a block scalar is text: its tag is no tag (go-yaml lists none).
    expect(references(folded)).toEqual([]);
    expect(references("cfg: |\n  # note\n  a: !bwref x/lit\n", at("doc[0]/cfg"))).toEqual(["x/lit"]);
  });

  // As on the server (go-yaml and internal/ingest/path.go), each of these reaches the document.
  test("a quoted scalar with the non-specific tag holds an embedded document", () => {
    expect(references("cfg: ! 'a: !bwref x/ns'\n", at("doc[0]/cfg"))).toEqual(["x/ns"]);
  });
  test("a path step matches an alias key by its anchored key", () => {
    expect(references("key: &k cfg\n*k : 'a: !bwref x/key'\n", at("doc[0]/cfg"))).toEqual(["x/key"]);
  });
  test("a path that ends at an alias reaches the anchored scalar", () => {
    expect(references("src: &s 'a: !bwref x/val'\ncfg: *s\n", at("doc[0]/cfg"))).toEqual(["x/val"]);
  });
  test("a lone carriage return breaks a line", () => {
    expect(references("b: 1\rcfg: 'a: !bwref x/cr'\r", at("doc[0]/cfg"))).toEqual(["x/cr"]);
  });
  // As go-yaml v3.0.5 does, a lone carriage return or U+0085 in a folded document folds to a
  // blank. U+2028 and U+2029 in a folded or quoted holder are the known difference in position.ts.
  test("U+2028 and U+2029 break a line between fields, and a folded break folds", () => {
    expect(references("b: 1\u2028cfg: 'a: !bwref x/ls'\n", at("doc[0]/cfg"))).toEqual(["x/ls"]);
    expect(references("cfg: 'a: !bwref x/one'\u2028other: 1\n", at("doc[0]/cfg"))).toEqual(["x/one"]);
    expect(references("cfg: 'a: !bwref x/two'\u2029other: 1\n", at("doc[0]/cfg"))).toEqual(["x/two"]);
    expect(references("cfg: >-\n  # note\r  a: !bwref x/cr\n", at("doc[0]/cfg"))).toEqual([]);
    expect(references("cfg: >-\n  # note\u0085  a: !bwref x/nel\n", at("doc[0]/cfg"))).toEqual([]);
  });

  // Positions are compared as numbers, so a failure prints no document text.
  test("a reference only an embedded document carries points at the scalar that holds it, or at its tag as written", () => {
    const text = "a: 1\ncfg: 'a: !bwref x/sq'\n";
    expect(spotOfReference(text, "x/sq", at("doc[0]/cfg"))).toEqual({ start: 10, end: 26 });
    expect(spotOfReference(text, "x/sq")).toBeUndefined();
    const literal = "cfg: |\n  a: !bwref x/lit\n";
    expect(spotOfReference(literal, "x/lit", at("doc[0]/cfg"))).toEqual({ start: 12, end: 24 });
    // Folded, the first tag is in the comment, so the scalar is the spot, never that tag.
    const folded = "cfg: >-\n  # note\n  a: !bwref x/same\n\n  b: !bwref x/same\n";
    expect(spotOfReference(folded, "x/same", at("doc[0]/cfg"))).toEqual({ start: 5, end: folded.length });
  });

  // ginsys/bronzeward#600: a literal holder is scanned as the document it holds, with its header blank.
  test("a literal holder: the same name first in a comment, then as a tag, points at the real tag", () => {
    const text = "cfg: |\n  # !bwref x/same\n  a: !bwref x/same\n";
    const start = text.indexOf("a: !bwref") + 3;
    expect(references(text, at("doc[0]/cfg"))).toEqual(["x/same"]);
    expect(spotOfReference(text, "x/same", at("doc[0]/cfg"))).toEqual({ start, end: start + "!bwref x/same".length });
  });
  test("a literal holder whose only tag is in a comment carries no reference", () => {
    const only = "cfg: |\n  # !bwref x/gone\n  a: 1\n";
    expect(references(only, at("doc[0]/cfg"))).toEqual([]);
    expect(spotOfReference(only, "x/gone", at("doc[0]/cfg"))).toBeUndefined();
  });
  test("a literal holder with an indentation indicator and a header comment points at its tag", () => {
    const deep = "k:\n  cfg: |2 # h\n      a: !bwref x/deep\n";
    const start = deep.indexOf("!bwref");
    expect(spotOfReference(deep, "x/deep", at("doc[0]/k/cfg"))).toEqual({ start, end: start + "!bwref x/deep".length });
  });
  // The block's lines are scanned without the block's indentation, so a marker or a flow collection
  // inside it reads as at the top of a document.
  test("a literal holder whose text holds a document marker points at the tag after it", () => {
    const text = "cfg: |\n  ---\n  [!bwref x/foo]\n";
    const start = text.indexOf("!bwref");
    expect(references(text, at("doc[0]/cfg"))).toEqual(["x/foo"]);
    expect(spotOfReference(text, "x/foo", at("doc[0]/cfg"))).toEqual({ start, end: start + "!bwref x/foo".length });
  });
  test("an indentation indicator with a first line deeper than the indent cuts only the indent", () => {
    const text = "k:\n  cfg: |2\n      a: 1\n      b: !bwref x/deep\n";
    const start = text.indexOf("!bwref");
    expect(spotOfReference(text, "x/deep", at("doc[0]/k/cfg"))).toEqual({ start, end: start + "!bwref x/deep".length });
  });
  test("a blank or shorter line inside the block loses at most the indent, spaces only", () => {
    const text = "cfg: |\n  a: 1\n \n\n    \n  b: !bwref x/blank\n";
    const start = text.indexOf("!bwref");
    expect(spotOfReference(text, "x/blank", at("doc[0]/cfg"))).toEqual({ start, end: start + "!bwref x/blank".length });
  });
  test("a lone carriage return breaks a line before the indentation is cut", () => {
    const text = "cfg: |\n  a: 1\r  ---\r  [!bwref x/foo]\n  ---\n  b: !bwref x/foo\n";
    const start = text.indexOf("!bwref");
    expect(spotOfReference(text, "x/foo", at("doc[0]/cfg"))).toEqual({ start, end: start + "!bwref x/foo".length });
  });
  // The first content line is the document's first line, so a byte order mark at its start is the
  // stream's and is dropped (ginsys/bronzeward#602).
  test("a literal holder whose text starts with a byte order mark points at its tag", () => {
    const text = "cfg: |\n  ﻿[!bwref x/foo]\n";
    const start = text.indexOf("!bwref");
    expect(references(text, at("doc[0]/cfg"))).toEqual(["x/foo"]);
    expect(spotOfReference(text, "x/foo", at("doc[0]/cfg"))).toEqual({ start, end: start + "!bwref x/foo".length });
  });
  // Three losses the hosted reviews found against main (ginsys/bronzeward#602).
  test("a flow collection as a block key keeps its first column, so the holder's quote is text", () => {
    const text = '[a, b]: |\n  "x\nfoo: !bwref x/foo\n';
    const start = text.indexOf("!bwref");
    expect(references(text, [])).toEqual(["x/foo"]);
    expect(spotOfReference(text, "x/foo", [])).toEqual({ start, end: start + "!bwref x/foo".length });
  });
  test("U+00A0 after a document marker is text: the line is no marker", () => {
    const text = '--- a: |\n  "x\nb: !bwref x/foo\n';
    expect(references(text, [])).toEqual(["x/foo"]);
  });
  test("tag-like text in a declared literal's comment does not hide the tag after it", () => {
    const text = "cfg: |\n  # !bwref \"\n  a: 1\nb: !bwref x/foo\nc: '\"'\n";
    expect(references(text, at("doc[0]/cfg"))).toEqual(["x/foo"]);
  });
  test("a CRLF marker ends the document, and a marker line in a literal holder keeps the tag's place", () => {
    expect(references("---\r\n!bwref x/foo\r\n", [])).toEqual(["x/foo"]);
    const text = "cfg: |\r\n  ---\r\n  !bwref x/foo\r\n";
    const start = text.indexOf("!bwref");
    expect(spotOfReference(text, "x/foo", at("doc[0]/cfg"))).toEqual({ start, end: start + "!bwref x/foo".length });
  });
  test("a tag spelled through a handle that resolves to !bwref is read, from where it is spelled", () => {
    const text = "%TAG !r! !\n---\na: !r!bwref x/foo\n";
    const start = text.indexOf("!r!bwref");
    expect(references(text, [])).toEqual(["x/foo"]);
    expect(spotOfReference(text, "x/foo", [])).toEqual({ start, end: start + "!r!bwref x/foo".length });
  });
  test("a CRLF after a block scalar header's comment does not stop the header", () => {
    const text = 'a: | # c\r\n  [x\r\nb: ?"y # !bwref ghost\r\nc: !bwref x/foo\r\n';
    expect(references(text, [])).toEqual(["x/foo"]);
  });
  test("a tag whose name is on the next line is placed across the removed indentation", () => {
    const text = "cfg: |\n  a: !bwref\n   x/next\n";
    const start = text.indexOf("!bwref");
    expect(spotOfReference(text, "x/next", at("doc[0]/cfg"))).toEqual({ start, end: text.indexOf("x/next") + "x/next".length });
  });
});

// The documents of position-cases.json and what go-yaml lists in each, written by `mise run
// web:position-cases` from the go-yaml oracle. Never edited by hand.
describe("the references of the documents the go-yaml oracle read", () => {
  const file = JSON.parse(readFileSync(new URL("./position-cases.json", import.meta.url), "utf8")) as {
    oracle: string;
    cases: { name: string; source: string; doc: string; want?: string[] }[];
  };

  test("every case has an expectation from the oracle", () => {
    expect(file.oracle).toMatch(/^go\.yaml\.in\/yaml\/v3 v\d/);
    expect(file.cases.length).toBeGreaterThan(100);
    expect(file.cases.filter((c) => !Array.isArray(c.want)).map((c) => c.name)).toEqual([]);
  });
  test.each(file.cases.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    expect(references(c.doc)).toEqual(c.want);
  });

  // Indentation is U+0020 only: a line that starts with U+00A0 is a new key, not block text.
  test("a block scalar ends at a line indented with U+00A0", () => {
    expect(references("a: |\n x\n\u00a0b: !bwref x/foo\n")).toEqual(["x/foo"]);
  });
  test("a line of U+00A0 alone, past the indentation, is block text", () => {
    expect(references("a: |\n  x\n  \u00a0\nb: !bwref x/foo\n")).toEqual(["x/foo"]);
  });
});

// ginsys/bronzeward#605: go-yaml reads one anchor between the tag and the value (`!bwref &e x/foo`).
// Every document below was checked against go-yaml v3.0.5 (experiments/scanner-cross-line).
describe("an anchor between the tag and the value", () => {
  test("the value on the same line, plain", () => {
    expect(references("a: !bwref &e x/foo\n")).toEqual(["x/foo"]);
  });
  test("the value on the next line", () => {
    expect(references("a: !bwref &e\n  x/foo\n")).toEqual(["x/foo"]);
  });
  test("a double-quoted value", () => {
    expect(references('a: !bwref &e "x/foo"\n')).toEqual(["x/foo"]);
  });
  test("a single-quoted value", () => {
    expect(references("a: !bwref &e 'x/foo'\n")).toEqual(["x/foo"]);
  });
  test("the anchor on the line after the tag", () => {
    expect(references("a: !bwref\n  &e x/foo\n")).toEqual(["x/foo"]);
  });
  test("a block scalar value", () => {
    expect(references("a: !bwref &e |-\n  x/foo\n")).toEqual(["x/foo"]);
  });
  test("a sequence entry and a flow collection", () => {
    expect(references("- !bwref &e x/foo\n")).toEqual(["x/foo"]);
    expect(references("a: [!bwref &e x/foo]\n")).toEqual(["x/foo"]);
    expect(references("{a: !bwref &e x/foo}\n")).toEqual(["x/foo"]);
  });
  test("a comment after the anchor, and a tab after it", () => {
    expect(references("a: !bwref &e # c\n  x/foo\n")).toEqual(["x/foo"]);
    expect(references("a: !bwref &e\tx/foo\n")).toEqual(["x/foo"]);
  });
  test("the anchor before the tag is the control and keeps working", () => {
    expect(references("a: &e !bwref x/foo\n")).toEqual(["x/foo"]);
  });

  // go-yaml refuses each of these documents or reads no name from it, so the scanner lists none.
  test("a second anchor, before or after the tag", () => {
    expect(references("a: &a !bwref &b x/foo\n")).toEqual([]);
    expect(references("a: !bwref &a &b x/foo\n")).toEqual([]);
  });
  test("a second tag is not read", () => {
    expect(references("a: !bwref !x x/foo\n")).toEqual([]);
    expect(references("a: !bwref &e !x x/foo\n")).toEqual([]);
  });
  test("an anchor name go-yaml does not accept, and an anchor with no value", () => {
    expect(references("a: !bwref &e.1 x/foo\n")).toEqual([]);
    expect(references("a: !bwref &e: x/foo\n")).toEqual([]);
    expect(references("a: !bwref &e\nb: c\n")).toEqual([]);
  });
  test("a tagged key stays unlisted, with or without the anchor", () => {
    expect(references("!bwref x/foo: v\n")).toEqual([]);
    expect(references("!bwref &e x/foo: v\n")).toEqual([]);
  });

  // The guard against a second anchor looks back 4096 characters. Past that it misses the first
  // anchor, so the scanner lists a name in a document that go-yaml refuses (a known limit; the
  // same document with the anchor within reach lists none).
  test("a first anchor beyond the look-back is missed", () => {
    expect(references(`a: [&a${"\n".repeat(4100)}  !bwref &b x/foo]\n`)).toEqual(["x/foo"]);
    expect(references("a: [&a\n\n  !bwref &b x/foo]\n")).toEqual([]);
    expect(references(`a: &a${"\n".repeat(4100)}  !bwref &b x/foo\n`)).toEqual([]);
  });
});

// ginsys/bronzeward#605: where a plain value after an anchor ends. In block context a comma, bracket
// or brace is text, so `x/foo,bar` is one scalar and no name. In flow context it ends the value. The
// value is read as a reference only when the scalar clearly ends. A shape where a more indented line
// can continue it names nothing, as on main. Each document that names a reference was checked
// against go-yaml v3.0.5 (experiments/scanner-cross-line).
describe("a plain value after an anchor, in block and in flow context", () => {
  test.each([
    ["a comma", "x/foo,bar"],
    ["a comma and a blank", "x/foo, bar"],
    ["a closing bracket", "x/foo]"],
    ["a closing brace", "x/foo}"],
    ["a colon and a comma", "x/foo:,y"],
    ["a second word", "x/foo bar"],
  ])("block context: %s makes one scalar, which is no name", (_what, value) => {
    expect(references(`k: !bwref &e ${value}\n`)).toEqual([]);
    expect(references(`- !bwref &e ${value}\n`)).toEqual([]);
  });

  test("block context: the value on the next line, and a continuation line", () => {
    expect(references("k: !bwref &e\n  x/foo,bar\n")).toEqual([]);
    expect(references("k: !bwref &e x/foo\n  bar\n")).toEqual([]);
  });

  test.each([
    ["a comma", "  ,bar"],
    ["a closing bracket", "  ]bar"],
    ["a dash", "  - y"],
    ["a question mark", "  ? y"],
    ["a second word", "  bar"],
    ["a no-break space", "  \u00a0bar"],
    ["a no-break space and a colon", "  bar:\u00a0z"],
    ["a no-break space alone", "  \u00a0"],
  ])("block context: a more indented line that starts with %s continues the scalar", (_what, next) => {
    expect(references(`k: !bwref &e x/foo\n${next}\n`)).toEqual([]);
  });

  test("block context: the scalar ends at the end of the text, a marker, a key or a less indented line", () => {
    for (const doc of [
      "k: !bwref &e x/foo",
      "k: !bwref &e x/foo\n",
      "k: !bwref &e x/foo\n---\n",
      "k: !bwref &e x/foo\n...\n",
      "k: !bwref &e x/foo\nj: v\n",
      "k: !bwref &e x/foo\n\n# c\nj: v\n",
      "a:\n  k: !bwref &e x/foo\nj: v\n",
      "- !bwref &e x/foo\n- y\n",
      "k:\n  - !bwref &e x/foo\n  - y\n",
      "- k: !bwref &e x/foo\n  j: v\n",
    ])
      expect(references(doc), doc).toEqual(["x/foo"]);
    // go-yaml lists x/foo for an explicit key. The scanner lists none: no key or entry stands before it.
    expect(references("? !bwref &e x/foo\n: v\n")).toEqual([]);
  });

  // go-yaml lists x/foo for each of these, as for the same documents with the anchor before the tag.
  // The scanner lists none: a more indented line can continue the scalar, and the compact column of
  // `- - x` and `? - x` is not read here. The same documents lose the name on main.
  test("block context: a compact collection names nothing", () => {
    expect(references("- - !bwref &e x/foo\n  - y\n")).toEqual([]);
    expect(references("? - !bwref &e x/foo\n  - bar\n: v\n")).toEqual([]);
  });

  // go-yaml lists x/foo for the two root scalars that end at the end of the text or at a marker. The
  // scanner lists none: a root scalar has no key or entry on its line (a lost name, as on main).
  test("block context: a value on the line of a document marker is the root scalar", () => {
    expect(references("--- !bwref &e x/foo\nbar\n")).toEqual([]);
    expect(references("--- !bwref &e x/foo\n")).toEqual([]);
    expect(references("--- !bwref &e x/foo\n---\n")).toEqual([]);
    expect(references("!bwref &e x/foo\nbar\n")).toEqual([]);
    expect(references("!bwref &e x/foo\n")).toEqual([]);
  });

  // The anchored path lists a name only for a tag on the line of a key (or of a sequence entry), where
  // the scalar clearly ends. A root scalar and a tag that starts its line list nothing, whatever
  // marker, comment or directive stands before it (ginsys/bronzeward#605, round 16). go-yaml v3.0.5
  // reads each of these as one scalar `x/foo bar`, so it lists no name.
  test.each([
    ["after a marker, indented by two spaces", "---\n  !bwref &e x/foo\nbar\n"],
    ["after a marker, indented by one space", "---\n !bwref &e x/foo\nbar\n"],
    ["after a marker and a comment line", "---\n# c\n  !bwref &e x/foo\nbar\n"],
    ["after a marker and an indented comment line", "---\n  # c\n !bwref &e x/foo\nbar\n"],
    ["with no marker, indented by two spaces", "  !bwref &e x/foo\nbar\n"],
    ["with no marker, indented by one space", " !bwref &e x/foo\nbar\n"],
    ["after a directive and a marker", "%YAML 1.1\n---\n  !bwref &e x/foo\nbar\n"],
    ["after a directive and a marker, at column 0", "%YAML 1.1\n---\n!bwref &e x/foo\nbar\n"],
    ["after a document end marker and a marker", "a: 1\n...\n---\n  !bwref &e x/foo\nbar\n"],
    ["in a second document", "a: 1\n---\n  !bwref &e x/foo\nbar\n"],
    ["on the line of a marker", "--- !bwref &e x/foo\nbar\n"],
  ])("a root scalar %s lists nothing", (_what, doc) => {
    expect(references(doc), doc).toEqual([]);
  });

  // go-yaml lists x/foo for these, or refuses the document. The scanner lists none: a tag that starts
  // its line has no key on its line, so the anchored path does not read it (a lost name, as on main).
  test.each([
    ["under a key, before a key", "k:\n  !bwref &e x/foo\nj: v\n"],
    ["as a root scalar at the end of the text", "---\n  !bwref &e x/foo\n"],
    ["as a root scalar before a marker", "---\n  !bwref &e x/foo\n---\n"],
    ["after a document end marker, with no marker", "a: 1\n...\n  !bwref &e x/foo\nbar\n"],
  ])("a tag that starts its line %s lists nothing", (_what, doc) => {
    expect(references(doc), doc).toEqual([]);
  });

  // A sequence entry `- !bwref &e x/foo` is the same one-line form as a key. go-yaml v3.0.5: a more
  // indented line continues the scalar (no name), a line at the dash's column ends the sequence (the
  // document is refused when it is no entry), a second dash is a compact sequence (go-yaml lists the
  // name, the scanner does not), and a line one column past the key of `- k:` continues the value.
  test.each([
    ["an entry, then a more indented line", "- !bwref &e x/foo\n  bar\n", []],
    ["an entry, then a line at the dash's column", "- !bwref &e x/foo\nbar\n", ["x/foo"]],
    ["an entry, then an entry", "- !bwref &e x/foo\n- bar\n", ["x/foo"]],
    ["an entry, then the end", "- !bwref &e x/foo\n", ["x/foo"]],
    ["a key under a key, then entries", "k:\n- !bwref &e x/foo\n- y\n", ["x/foo"]],
    ["an indented entry, then an entry", "k:\n  - !bwref &e x/foo\n  - y\n", ["x/foo"]],
    ["a second dash, then the end", "- - !bwref &e x/foo\n", []],
    ["a second dash, then an entry", "- - !bwref &e x/foo\n- y\n", []],
    ["a key in an entry, then a line past the key's column", "- k: !bwref &e x/foo\n   bar\n", []],
    ["a key in an entry, then a line at the key's column", "- k: !bwref &e x/foo\n  bar\n", []],
    ["a key in an entry, then a key", "- k: !bwref &e x/foo\n  j: v\n", ["x/foo"]],
    ["a key after a marker, then a key", "---\nk: !bwref &e x/foo\nj: v\n", ["x/foo"]],
  ])("a sequence entry: %s", (_what, doc, names) => {
    expect(references(doc), doc).toEqual(names);
  });

  test("flow context: a continuation line needs no indentation, and a flow indicator ends the value", () => {
    expect(references("k: [!bwref &e x/foo\nbar]\n")).toEqual([]);
    expect(references("k: [!bwref &e x/foo\n  , bar]\n")).toEqual(["x/foo"]);
    expect(references("k: [!bwref &e x/foo\n]\n")).toEqual(["x/foo"]);
  });

  test("flow context: a later entry does not hide the continuation of the scalar", () => {
    expect(references("k: [!bwref &e x/foo\nbar, k: v]\n")).toEqual([]);
    expect(references("k: [!bwref &e x/foo\n bar:]\n")).toEqual([]);
  });

  test("flow context: a no-break space is text, not a blank", () => {
    expect(references("k: [!bwref &e x/foo\n  \u00a0\n  ]\n")).toEqual([]);
  });

  // A flow line that starts with the value indicator (a colon before a blank) ends the scalar, so the
  // tagged scalar is an explicit key. A colon before a flow indicator is text for go-yaml v3.0.5.
  test("flow context: an explicit key ends at the value indicator on the next line", () => {
    expect(references("k: {? !bwref &e x/foo\n: v}\n")).toEqual(["x/foo"]);
    expect(references("k: [? !bwref &e x/foo\n: v]\n")).toEqual(["x/foo"]);
    expect(references("k: {? !bwref &e x/foo\n:]}\n")).toEqual([]);
  });

  test("flow context: a colon before a flow indicator stays in the value, which is no name", () => {
    expect(references("k: [!bwref &e x/foo:]\n")).toEqual([]);
    expect(references("k: {!bwref &e x/foo:, j: v}\n")).toEqual([]);
  });

  test.each([
    ["a comma", "[!bwref &e x/foo, bar]"],
    ["a comma with no blank", "[!bwref &e x/foo,bar]"],
    ["a closing bracket", "[!bwref &e x/foo]"],
    ["a closing brace", "{k: !bwref &e x/foo}"],
    ["a flow map", "{k: !bwref &e x/foo, j: l}"],
    ["a nested collection", "[[!bwref &e x/foo, y], z]"],
    ["the value on the next line", "[!bwref &e\n  x/foo, y]"],
  ])("flow context: %s ends the value", (_what, flow) => {
    expect(references(`k: ${flow}\n`), flow).toEqual(["x/foo"]);
  });

  test("flow context: a second word and a continuation line make one scalar, which is no name", () => {
    expect(references("k: [!bwref &e x/foo bar]\n")).toEqual([]);
    expect(references("k: [!bwref &e x/foo\n  bar]\n")).toEqual([]);
  });

  test("block context after a closed flow collection", () => {
    expect(references("a: [x]\nb: !bwref &e y/foo,z\n")).toEqual([]);
  });

  // `!!bwref` and `!r!bwref` are other tags, so go-yaml lists no name, but the scan records their
  // `!bwref` tail. A value after an anchor is read only for a tag that starts at the tail.
  test("a tag spelled through a handle names nothing after an anchor", () => {
    expect(references("a: !!bwref &e x/foo\n")).toEqual([]);
    expect(references("a: !r!bwref &e x/foo\n")).toEqual([]);
    expect(references("a: !!bwref &e x/bar\nb: !bwref x/foo\n")).toEqual(["x/foo"]);
    expect(references("k:\n  a: !!bwref &e x/foo\n")).toEqual([]);
  });

  // go-yaml v3.0.5 lists x/foo for a tag right after the colon of a quoted key. The scanner lists none,
  // as on main: a remaining lost name, never an extra one. A plain key makes `a:!bwref` one scalar.
  test("a tag right after a colon names nothing after an anchor", () => {
    expect(references('{"a":!bwref &e x/foo}\n')).toEqual([]);
    expect(references('{"a": !bwref &e x/foo}\n')).toEqual(["x/foo"]);
    expect(references("{a:!bwref &e x/foo}\n")).toEqual([]);
  });

  // A key that starts with U+00A0 has text at column 0, not indentation, so a line with one space
  // continues the value (go-yaml lists none) and a key line ends it. Checked against go-yaml v3.0.5.
  // Main lists none after an anchor, so none is the old result. A stream that starts with a byte order
  // mark is below.
  test.each([
    ["no-break space", " "],
  ])("indentation after a %s at column 0 is counted in spaces", (_, lead) => {
    expect(references(`${lead}k: !bwref &e x/foo\n bar\n`)).toEqual([]);
    expect(references(`${lead}k: !bwref &e x/foo\n  bar\n`)).toEqual([]);
    expect(references(`${lead}k: !bwref &e x/foo\n bar\nj: v\n`)).toEqual([]);
    expect(references(`${lead}k: !bwref &e x/foo\nj: v\n`)).toEqual(["x/foo"]);
    expect(references(`${lead}k: !bwref &e x/foo\n j: v\n`)).toEqual(["x/foo"]);
    expect(references(`${lead}k: !bwref &e x/foo\n`)).toEqual(["x/foo"]);
  });

  // go-yaml drops a byte order mark at the start of the stream before it reads any text. The anchored
  // path does not model that: in such a stream it reads no value after an anchor, as on main. That
  // closes every shape at once, such as a root value that goes on at column 0 (go-yaml reads `x/foo
  // bar`) or an indented tag after an empty first line. A name that go-yaml lists in such a stream is
  // lost, as on main. A byte order mark at the start of a later line is text: the anchored path is
  // unchanged there (ginsys/bronzeward#605, hosted and local review).
  describe("a byte order mark at the start of the stream", () => {
    const bom = String.fromCharCode(0xfeff);
    test.each([
      [`${bom}--- !bwref &e x/foo\nbar\n`, []],
      [`${bom} !bwref &e x/foo\nbar\n`, []],
      [`${bom}\n !bwref &e x/foo\nbar\n`, []],
      [`${bom}\n  !bwref &e x/foo\nbar\n`, []],
      [`${bom}!bwref &e x/foo\nbar\n`, []],
      [`${bom}k: !bwref &e x/foo\n`, []],
      [`${bom}--- !bwref &e x/foo\n`, []],
      [`${bom}a: !bwref x/foo\n`, ["x/foo"]],
      [`${bom}--- !bwref x/foo\n`, ["x/foo"]],
      ["--- !bwref &e x/foo\nbar\n", []],
      [" !bwref &e x/foo\nbar\n", []],
      ["\n !bwref &e x/foo\nbar\n", []],
      ["k: !bwref &e x/foo\n", ["x/foo"]],
      ["--- !bwref &e x/foo\n", []],
      [`a: 1\n...\n${bom}--- !bwref &e x/foo\nbar\n`, []],
      [`a: 1\n---\n${bom} !bwref &e x/foo\nbar\n`, []],
      [`a: 1\n---\n${bom}!bwref &e x/foo\n`, []],
    ])("%j lists %j", (text, names) => {
      expect(references(text)).toEqual(names);
    });
  });

  // A value with no anchor after the tag is read as before: these two lists match main and go-yaml.
  test("a value with no anchor after the tag is read as before", () => {
    expect(references("- - !bwref x/foo\n  - y\n")).toEqual(["x/foo"]);
    expect(references("? - !bwref x/foo\n  - bar\n: v\n")).toEqual(["x/foo"]);
    expect(references("- - &e !bwref x/foo\n  - y\n")).toEqual(["x/foo"]);
  });
});

// ginsys/bronzeward#615: go-yaml resolves a tag through the %TAG directives of its document before it
// compares it with !bwref. Every list below was read from go-yaml v3.0.5 (the oracle of
// experiments/scanner-cross-line): a stream it refuses lists nothing, and a stream it accepts lists
// the names shown. A directive stands before the `---` of its document and ends with it.
describe("a tag is resolved the way go-yaml does", () => {
  test.each([
    ["verbatim, plain", "a: !<!bwref> x/foo\n"],
    ["verbatim, in a flow mapping", "{a: !<!bwref> x/foo}\n"],
    ["verbatim, in a flow sequence", "[!<!bwref> x/foo]\n"],
    ["verbatim, after an anchor", "a: &e !<!bwref> x/foo\n"],
    ["verbatim, before an anchor", "a: !<!bwref> &e x/foo\n"],
    ["a primary handle that holds part of the name", "%TAG ! !bw\n---\na: !ref x/foo\n"],
    ["a secondary handle that holds part of the name", "%TAG !! !bw\n---\na: !!ref x/foo\n"],
    ["a named handle that holds part of the name", "%TAG !x! !bw\n---\na: !x!ref x/foo\n"],
    ["a named handle, in a flow mapping", "%TAG !x! !bw\n---\n{a: !x!ref x/foo}\n"],
    ["a percent escape in the tag", "a: !bw%72ef x/foo\n"],
    ["a percent escape in a verbatim tag", "a: !<!bw%72ef> x/foo\n"],
    ["a percent escape in a prefix", "%TAG !x! !bw%72\n---\na: !x!ef x/foo\n"],
    ["a comment after the directive", "%TAG !x! !bw # note\n---\na: !x!ref x/foo\n"],
    ["%YAML 1.1", "%YAML 1.1\n---\na: !bwref x/foo\n"],
  ])("lists the name of %s", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  test.each([
    ["a secondary handle", "a: !!bwref x/foo\n"],
    ["a named handle that holds part of the name twice", "%TAG !x! !bw\n---\na: !x!bwref x/foo\n"],
    ["a named handle that maps to another tag", "%TAG !x! !other\n---\na: !x!bwref x/foo\n"],
    ["an undeclared named handle", "a: !x!bwref x/foo\n"],
    ["%YAML 1.2", "%YAML 1.2\n---\na: !bwref x/foo\n"],
    ["an unknown directive", "%FOO bar\n---\na: !bwref x/foo\n"],
    ["a %TAG handle declared twice", "%TAG !x! !a\n%TAG !x! !b\n---\na: !bwref x/foo\n"],
    ["a %YAML directive declared twice", "%YAML 1.1\n%YAML 1.1\n---\na: !bwref x/foo\n"],
    ["directives with no document start", "%YAML 1.1\na: !bwref x/foo\n"],
    ["a bad percent escape in a prefix", "%TAG !x! tag:a%ZZ\n---\na: !bwref x/foo\n"],
  ])("lists nothing for %s", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // A tag that go-yaml cannot read fails the whole stream, so an earlier name in the same document
  // is not listed either.
  test.each([
    ["a bad percent escape in a tag", "a: !bwref x/foo\nb: !<!bw%ZZ> y\n"],
    ["a bad percent escape that ends a tag", "a: !bwref x/foo\nb: !bw%ZZref y\n"],
    ["an undeclared handle", "a: !bwref x/foo\nb: !q!z y\n"],
    ["an undeclared handle after a directive", "%TAG !x! !bw\n---\na: !x!ref x/foo\nb: !y!bwref x/bar\n"],
    ["a character that ends a tag before a blank", "a: !bwref x/foo\nb: {c: !bwref}\n"],
    ["a character that ends a tag at the line end", "a: !bwref x/foo\nb: !bwref}\n"],
  ])("lists nothing in a document with %s", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  test("the table starts again at every document", () => {
    // The second document has the default handles: `!ref` is another tag there.
    expect(references("%TAG ! !bw\n---\na: 1\n---\nb: !ref x/bar\n")).toEqual([]);
    expect(references("%TAG !x! !bw\n---\na: !x!ref x/foo\n---\nb: !bwref x/bar\n")).toEqual(["x/foo", "x/bar"]);
  });

  test("a directive after the end of a document declares a table for the next one", () => {
    expect(references("a: 1\n...\n%TAG ! !bw\n---\nb: !ref x/foo\n")).toEqual(["x/foo"]);
    expect(references("a: 1\n...\n%TAG ! !bw\nb: !ref x/foo\n")).toEqual([]);
  });

  // A directive in the first column after block content, outside a flow collection, ends the document
  // and opens the next one (go-yaml v3.0.5 accepts these, and lists the name of the last document).
  test.each([
    ["a mapping", "a: 1\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["a mapping, CRLF", "a: 1\r\n%TAG !x! !bw\r\n---\r\nb: !x!ref x/foo\r\n"],
    ["a mapping, CR", "a: 1\r%TAG !x! !bw\r---\rb: !x!ref x/foo\r"],
    ["a mapping with a comment after the directive", "a: 1\n%TAG !x! !bw # note\n---\nb: !x!ref x/foo\n"],
    ["a mapping, after a comment and a blank line", "a: 1\n# c\n\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["a nested mapping", "a:\n  b: 1\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["a sequence", "- a\n%TAG !x! !bw\n---\n- !x!ref x/foo\n"],
    ["a closed flow sequence", "[a]\n%TAG !x! !bw\n---\n!x!ref x/foo\n"],
    ["a quoted scalar", '"a"\n%TAG !x! !bw\n---\n!x!ref x/foo\n'],
    ["a plain value", "a: x\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["a block scalar", "a: |\n  x\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["a block scalar after a document start", "--- |\n x\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["an explicit key", "? a\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["a %YAML directive", "a: 1\n%YAML 1.1\n---\nb: !bwref x/foo\n"],
  ])("a directive after %s ends the document", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  test("the document that a directive ends keeps its own names, with its own table", () => {
    expect(references("a: !bwref x/bar\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n")).toEqual(["x/bar", "x/foo"]);
    // The handle is declared after the first document, so the first document has no `!x!` handle.
    expect(references("a: !x!ref x/bar\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n")).toEqual(["x/foo"]);
  });

  // go-yaml refuses these streams: a plain scalar goes on over the directive line, a directive in an
  // open flow collection is an error, and the directive needs its `---`.
  test.each([
    ["a plain scalar that goes on", "a\n%TAG !x! !bw\n---\n!x!ref x/foo\n"],
    ["a plain scalar after a document start", "--- a\n%TAG !x! !bw\n---\n!x!ref x/foo\n"],
    ["an open flow sequence", "a: [1,\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n"],
    ["a directive with no document start", "a: 1\n%TAG !x! !bw\nb: !x!ref x/foo\n"],
    ["a directive and a document end with no start", "a: 1\n%TAG !x! !bw\n...\nb: !x!ref x/foo\n"],
    ["%YAML 1.2", "a: 1\n%YAML 1.2\n---\nb: !bwref x/foo\n"],
  ])("a directive after %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  test("a line that looks like a directive in a block scalar or a comment declares nothing", () => {
    expect(references("a: |\n  %TAG ! !bw\nb: !ref x/foo\n")).toEqual([]);
    expect(references("# %TAG ! !bw\nb: !ref x/foo\n")).toEqual([]);
  });

  // go-yaml fails the whole stream for a refused document. The scanner blanks that document only (the
  // owner's wording of ginsys/bronzeward#606), so an earlier document still lists its names. The
  // comparison tool counts such a document apart, as one that go-yaml refuses.
  test("a refused document blanks itself and no other", () => {
    expect(references("a: !bwref x/foo\n---\nb: !q!z y\n")).toEqual(["x/foo"]);
    expect(references("%YAML 1.2\n---\na: !bwref x/foo\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
  });

  // The spot of a reference runs from the first character of its tag, as it is spelled, to the end of
  // its name.
  test("the spot of a name starts at the tag as it is spelled", () => {
    const text = "%TAG !x! !bw\n---\na: !x!ref x/foo\n";
    expect(spotOfReference(text, "x/foo")).toEqual({ start: text.indexOf("!x!ref"), end: text.indexOf("x/foo") + 5 });
    const verbatim = "a: !<!bwref> x/foo\n";
    expect(spotOfReference(verbatim, "x/foo")).toEqual({ start: 3, end: verbatim.indexOf("\n") });
  });
});
