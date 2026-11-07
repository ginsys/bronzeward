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
  // ginsys/bronzeward#633: a document that go-yaml refuses lists nothing, and that includes the
  // embedded documents it holds. A refused document blanks itself and no other, so the documents
  // around it keep their names, held ones too.
  test("an embedded document in a refused document lists nothing, and one in another document does", () => {
    const refusedFirst = "%YAML 1.2\n---\ncfg: 'a: !bwref x/foo'\n---\ncfg: 'b: !bwref x/bar'\n";
    const both = [{ path: "doc[0]/cfg" }, { path: "doc[1]/cfg" }];
    expect(references(refusedFirst, at("doc[0]/cfg"))).toEqual([]);
    expect(spotOfReference(refusedFirst, "x/foo", at("doc[0]/cfg"))).toBeUndefined();
    expect(references(refusedFirst, both)).toEqual(["x/bar"]);
    const refusedLast = "cfg: 'a: !bwref x/foo'\n%YAML 1.2\n---\ncfg: 'b: !bwref x/bar'\n";
    expect(references(refusedLast, both)).toEqual(["x/foo"]);
    expect(spotOfReference(refusedLast, "x/bar", at("doc[1]/cfg"))).toBeUndefined();
  });
  test("a literal holder in a refused document lists nothing", () => {
    expect(references("%YAML 1.2\n---\ncfg: |\n  a: !bwref x/foo\n", at("doc[0]/cfg"))).toEqual([]);
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

  // A marker closes a document. Text on the `...` line is content with no `---`, and a marker inside an
  // open flow collection is an error: go-yaml refuses both, so the document at fault lists nothing.
  test.each([
    ["text on the `...` line", "a: 1\n... !bwref x/foo\n"],
    ["a verbatim tag on the `...` line", "a: 1\n... !<!bwref> x/foo\n"],
    ["a mapping on the `...` line", "a: 1\n... b: !bwref x/foo\n"],
    ["a tag on the `...` line of a document that lists a name", "--- !bwref x/foo\n... !bwref x/bar\n", ["x/foo"]],
    ["a `---` in an open flow sequence", "a: [!bwref x/foo,\n---\nb: 1\n"],
    ["a `---` in an open flow sequence, verbatim tag", "a: [!<!bwref> x/foo,\n---\nb: 1\n"],
    ["a `...` in an open flow sequence", "a: [!bwref x/foo,\n...\nb: 1\n"],
  ] as [string, string, string[]?][])("%s lists %j", (_, text, names = []) => {
    expect(references(text)).toEqual(names);
  });

  test.each([
    ["a comment after `...`", "a: 1\n... # c\n---\nb: !bwref x/foo\n"],
    ["a tab after `...`", "a: 1\n...\t\n---\nb: !bwref x/foo\n"],
    ["a blank after `...`", "[a]\n... \n---\nb: !bwref x/foo\n"],
  ])("%s ends the document and the next one lists", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // The faulty document blanks itself and no other: the document after it lists its names, with its own
  // handle table.
  test.each([
    ["text on the `...` line", "a: 1\n... !bwref x/foo\n---\nb: !bwref x/bar\n"],
    ["a `---` in an open flow mapping", "a: {b: !bwref x/foo,\n---\nc: !bwref x/bar\n"],
    ["a `---` in an open flow sequence", "a: [!bwref x/foo,\n---\nb: !bwref x/bar\n"],
    ["a `...` in an open flow sequence", "a: [!bwref x/foo,\n...\n---\nb: !bwref x/bar\n"],
    ["a directive in an open flow sequence", "a: [!bwref x/foo,\n%YAML 1.1\n---\nb: !bwref x/bar\n"],
  ])("%s refuses its document and the next one lists", (_, text) => {
    expect(references(text)).toEqual(["x/bar"]);
  });

  // ginsys/bronzeward#620: go-yaml refuses a stream in three more shapes, and the document at fault
  // lists nothing. A node takes one tag, so a second tag is an error (parserc.go, `did not find
  // expected key` in a block mapping). A `%` where a token starts cannot start a plain scalar
  // (`found character that cannot start any token`). A quoted scalar refuses a `---` or `...` in
  // the first column (`found unexpected document indicator`).
  test.each([
    ["a tag before a verbatim tag", "a: !other !<!bwref> x/foo\n"],
    ["the same tag twice", "a: !bwref !bwref x/foo\n"],
    ["a tag before the tag", "a: !other !bwref x/foo\n"],
    ["a tag and an anchor before the tag", "a: &e !other !bwref x/foo\n"],
    ["a tag, an anchor, then the tag", "a: !other &e !bwref x/foo\n"],
    ["a tag before the tag in a flow sequence", "[!other !bwref x/foo]\n"],
    ["a tag before the tag in a block sequence", "- !other !bwref x/foo\n"],
    ["a tag before the tag in a flow mapping", "{a: !other !bwref x/foo}\n"],
    ["a tag before the tag over a line break in a flow sequence", "[!other\n !bwref x/foo]\n"],
    // go-yaml takes a comma as a tag character, so the first tag is `!other,` and no comma ends it.
    ["a comma that the first tag holds, in a flow sequence", "[!other, !bwref x/foo]\n"],
  ])("two tags on one node: %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // The first tag can stand alone in its line: the next more indented line goes on with the node.
  test.each([
    ["a tag, then the tag in the next block line", "a: !other\n  !bwref x/foo\n"],
    ["a tag, then an anchor and the tag in the next line", "a: !other\n  &e !bwref x/foo\n"],
    ["a tag, then the anchor and the tag in two lines", "a: !other\n  &e\n  !bwref x/foo\n"],
    ["a tag, a blank line, then the tag", "a: !other\n\n  !bwref x/foo\n"],
    ["a tag, a comment line, then the tag", "a: !other\n  # c\n  !bwref x/foo\n"],
    ["a tag in a block sequence entry, then the tag", "- !other\n  !bwref x/foo\n"],
    ["a tag at the root, then the tag", "!other\n!bwref x/foo\n"],
    ["a tag, then a tag and a flow sequence value", "a: !other\n  !u [!bwref x/foo]\n"],
    ["a tag, then a tag and a flow mapping value", "a: !other\n  !u {k: !bwref x/foo}\n"],
    ["a tag, then a tag and a flow sequence that does not close", "a: !other\n  !u [!bwref x/foo,\n  y]\n"],
    ["a tag, then a tag and a flow value with a colon in a comment", "a: !other\n  !u [!bwref x/foo, # ]: x\n    y]\n"],
    ["a tag, then a tag and a tag-only line", "a: !other\n  !u\n  b: !bwref x/foo\n"],
    // The colon follows text after a closed token, so it closes no key that the tag could belong to.
    ["a tag, then a quoted value, text and a colon", 'a: !other\n  !bwref "x/foo" junk: x\n'],
    ["a tag, then a single quoted value, text and a colon", "a: !other\n  !bwref 'x/foo' junk: x\n"],
    ["a tag, then a quoted value, text and a colon, in a block sequence", '- !other\n  !bwref "x/foo" junk: x\n'],
    ["a tag, then an alias, text and a colon", "a: !other\n  !u *k junk: !bwref x/foo\n"],
    ["a tag, then a quoted value, text and two colons", 'a: !other\n  !bwref "x/foo" junk: k: x\n'],
    ["a tag, then a quoted value, text, a colon and a second colon", 'a: !other\n  !bwref "x/foo" junk: : x\n'],
    ["a tag, then a quoted value that ends its line, then a key in the next line", 'a: !other\n  !bwref "x/foo"\nb: c\n'],
  ])("two tags over a block line break: %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  test("two tags over a block line break blank their document and no other", () => {
    expect(references("a: !other\n  !bwref x/foo\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
    expect(references("a: !other\n  &e !bwref x/foo\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
  });

  // A tag before a key of the next line belongs to the key, so the node above has one tag.
  test.each([
    ["a tagged key in the next line", "a: !other\n  !u b: !bwref x/foo\n"],
    ["a tagged quoted key in the next line", 'a: !other\n  !u "b": !bwref x/foo\n'],
    ["a tagged key in a block sequence entry", "- !other\n  !u b: !bwref x/foo\n"],
    ["a sequence after the tag", "a: !other\n  - !bwref x/foo\n"],
    ["a tagged flow sequence key", "a: !other\n  !u [k]: !bwref x/foo\n"],
    ["a tagged flow mapping key", "a: !other\n  !u {k: v}: !bwref x/foo\n"],
    ["a tagged flow key with an apostrophe in a plain scalar", "a: !other\n  !u [it's]: !bwref 'x/foo'\n"],
    ["a tagged flow key with a bracket in a tag", "a: !other\n  !u [!t[ x]: !bwref x/foo\n"],
    ["a tagged quoted key with a space before the colon", 'a: !other\n  !u "b" : !bwref x/foo\n'],
    ["a tagged key of two plain words", "a: !other\n  !u b c: !bwref x/foo\n"],
    ["a tagged empty key", "a: !other\n  !u : !bwref x/foo\n"],
  ])("one tag per node over a block line break: %s still lists the name", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // A tag or an anchor ends the places where a block entry can start, so a `-` or `?` indicator after
  // one in its line is an error whatever the line is and however many properties come (ginsys/bronzeward#650;
  // go-yaml v3.0.5: "block sequence entries are not allowed in this context", "mapping keys are not
  // allowed in this context"). The first four are the shapes of the issue and its one-tag controls.
  test.each([
    ["two tags and a block sequence entry", "a: !other\n  !u - k: !bwref x/foo\n"],
    ["two tags and an explicit key", "a: !other\n  !u ? b: !bwref x/foo\n"],
    ["one tag and a block sequence entry", "a:\n  !u - k: !bwref x/foo\n"],
    ["one tag and an explicit key", "a:\n  !u ? b: !bwref x/foo\n"],
    ["a tag and a block sequence entry in the first line", "!u - k: !bwref x/foo\n"],
    ["a tag and an explicit key after a key", "a: !u ? b: !bwref x/foo\n"],
    ["a tag after a sequence entry indicator", "- !u - k: !bwref x/foo\n"],
    ["an anchor and a block sequence entry", "&x - k: !bwref x/foo\n"],
    ["an anchor and an explicit key in a continuation line", "a:\n  &x ? b: !bwref x/foo\n"],
    ["a tag, an anchor and a block sequence entry", "a:\n  !u &x - k: !bwref x/foo\n"],
    ["an anchor, a tag and an explicit key over a line break", "a: !other\n  &x !u ? b: !bwref x/foo\n"],
  ])("an indicator after a property: %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // The same indicators with the property alone in its line, or a key whose text only starts with the
  // indicator character, are valid.
  test.each([
    ["a tag alone, then a block sequence", "a:\n  !u\n  - k: !bwref x/foo\n"],
    ["a tag alone, then an explicit key", "a:\n  !u\n  ? b: !bwref x/foo\n"],
    ["a tag and a key that starts with a hyphen", "a:\n  !u -k: !bwref x/foo\n"],
    ["a tag and a key that starts with a question mark", "a:\n  !u ?k: !bwref x/foo\n"],
    ["a sequence entry, then a tagged value", "a:\n  - !bwref x/foo\n"],
    ["a sequence entry, then an explicit key", "a:\n  - ? !bwref x/foo\n"],
    ["a tagged key in a continuation line", "a: !other\n  !u b: !bwref x/foo\n"],
  ])("an indicator after a property: the valid control %s still lists the name", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // A block line that starts with a tab, after any spaces, is an error in go-yaml v3.0.5: it skips a
  // tab as a blank only in flow context or where no simple key can start, and after a line break a
  // simple key can (ginsys/bronzeward#651; "found character that cannot start any token", "found a
  // tab character that violates indentation", "found a tab character where an indentation space is
  // expected"). The first three are the shapes of the issue.
  test.each([
    ["a tag and a tab-led comment", "a: !other\n\t# c\n  !bwref x/foo\n"],
    ["a tag and a tab-only line", "a: !other\n\t\n  !bwref x/foo\n"],
    ["a key and a tab-led comment", "a: 1\n\t# c\nb: !bwref x/foo\n"],
    ["a tab-led key", "a: 1\n\tb: !bwref x/foo\n"],
    ["a tab-led key under a nested key", "a:\n  b: 1\n \tc: !bwref x/foo\n"],
    ["a tab-only line under a nested key", "a:\n  b: !bwref x/foo\n \t\n"],
    ["a tab-led comment under a nested key", "a:\n  b: 1\n \t# c\nc: !bwref x/foo\n"],
    ["a tab-led sequence entry", "a:\n  - 1\n\t- !bwref x/foo\n"],
    ["a tab at the start of the document", "\ta: !bwref x/foo\n"],
    ["a tab-led plain continuation", "a: x\n\ty\nb: !bwref x/foo\n"],
    ["a tab-led line after a block scalar header", "a: |\n\tx\nb: !bwref x/foo\n"],
    ["a tab after spaces, as the first line of a block scalar", "a: |\n  \tx\nb: !bwref x/foo\n"],
    ["a tab-led comment after a block scalar's content", "a: |\n  x\n\t# c\nb: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a content line", "a: 1 # c\n\t# d\nb: !bwref x/foo\n"],
    ["a tab-only line after a comment, then a key", "a: 1\n# c\n\t\nb: !bwref x/foo\n"],
    ["a tab-only line after a comment, at the end", "a: !bwref x/foo\n# c\n\t\n"],
    ["a tab-led comment after a comment on a key", "a: # c\n\t# d\n  b: !bwref x/foo\n"],
    ["a tab-led comment after a comment on an explicit key indicator", "? # c\n\t# d\n  a: !bwref x/foo\n"],
    ["a tab-led comment after a directive and an empty line", "%YAML 1.1\n\n\t# c\n---\na: !bwref x/foo\n"],
    ["two tab-only lines after a directive", "%YAML 1.1\n\t\n\t\n---\na: !bwref x/foo\n"],
    ["a tab-led comment after a marker, past the look-ahead of 512 bytes", `--- # c\n${" ".repeat(510)}\t# d\na: !bwref x/foo\n`],
    ["a tab-led comment past the look-ahead of 512 bytes", `a: 1\n# c\n${" ".repeat(510)}\t# d\nb: !bwref x/foo\n`],
    ["a tab-led comment after dashes inside a plain continuation", "a: x\n  - # c\n\t# d\n  b: !bwref x/foo\n"],
    ["a tab-led comment after dashes inside a nested plain continuation", "a:\n  b: x\n    - # c\n\t# d\n  c: !bwref x/foo\n"],
    ["a tab-only line and a comment, past the look-ahead of 512 bytes",`a: 1\n# c\n${" ".repeat(255)}\t\n${" ".repeat(255)}\t# d\nb: !bwref x/foo\n`],
  ])("a tab-led block line: %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // go-yaml looks ahead for the next comment of a run byte by byte, and stops inside U+0085, U+2028
  // and U+2029: a tab-led line behind one of them is no part of the run. A lone carriage return and
  // a CRLF do not stop it (ginsys/bronzeward#651).
  const unicodeBreaks: [string, string][] = [
    ["U+0085", "\u0085"],
    ["U+2028", " "],
    ["U+2029", " "],
  ];
  const runShapes: [string, (b: string) => string][] = [
    ["a comment on a marker, then a tab-led comment", (b) => `--- # c${b}\t# d\nb: !bwref x/foo\n`],
    ["a comment with a trailing blank on a marker, then a tab-led comment", (b) => `--- # c ${b}\t# d\nb: !bwref x/foo\n`],
    ["a comment, then a tab-led comment", (b) => `a: 1\n# c${b}\t# d\nb: !bwref x/foo\n`],
    ["a comment with a trailing blank, then a tab-led comment", (b) => `a: 1\n# c ${b}\t# d\nb: !bwref x/foo\n`],
    ["a comment on a block entry, then a tab-led comment", (b) => `- # c${b}\t# d\n  a: !bwref x/foo\n`],
    ["a comment, an empty line, then a tab-led comment", (b) => `a: 1\n# c\n${b}\t# d\nb: !bwref x/foo\n`],
    ["a comment, a tab-only line, then a comment", (b) => `a: 1\n# c\n\t${b}# d\nb: !bwref x/foo\n`],
    ["two tab-led comments", (b) => `a: 1\n# c\n\t# d${b}\t# e\nb: !bwref x/foo\n`],
  ];
  test.each(unicodeBreaks.flatMap(([bn, b]) => runShapes.map(([sn, f]) => [`${sn} across ${bn}`, f(b)] as const)))(
    "a tab-led block line: %s lists nothing",
    (_, text) => {
      expect(references(text)).toEqual([]);
    },
  );

  // The same shapes with a lone carriage return or a CRLF are valid, as with a line feed. A directive
  // line ends at any break, and a break before the first comment of a run is no part of the look-ahead.
  test.each([
    ...[
      ["a lone carriage return", "\r"],
      ["a CRLF", "\r\n"],
    ].flatMap(([bn, b]) => runShapes.map(([sn, f]) => [`${sn} across ${bn}`, f(b!)] as const)),
    ...unicodeBreaks.flatMap(([bn, b]) => [
      [`a tab-led comment after a directive line that ends at ${bn}`, `%YAML 1.1${b}\t# c\n---\na: !bwref x/foo\n`] as const,
      [`a tab-led comment after a comment that follows ${bn}`, `a: 1${b}# c\n\t# d\nb: !bwref x/foo\n`] as const,
    ]),
  ])("a tab-led block line: the valid control %s still lists the name", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  test("a tab-led block line refuses its document and the next one lists", () => {
    expect(references("a: !other\n\t# c\n  !bwref x/foo\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
  });

  // A tab that is no block line start, a tab line in a flow collection, a quote or a block scalar's
  // content, and a tab-led comment in a run of comment lines are valid.
  test.each([
    ["a tab in a flow sequence", "a: [1,\n\t2]\nb: !bwref x/foo\n"],
    ["a tab in a flow mapping", "a: {b: 1,\n\tc: 2}\nb: !bwref x/foo\n"],
    ["a tab inside a line", "a:\t1\nb: !bwref x/foo\n"],
    ["a tab in a quoted scalar", 'a: "x\n\ty"\nb: !bwref x/foo\n'],
    ["a tab after spaces in a block scalar's content", "a: |\n  x\n  \ty\nb: !bwref x/foo\n"],
    ["a tab at the content indent of a block scalar", "a: |2\n  \tx\nb: !bwref x/foo\n"],
    ["a tab-led comment after a comment", "a: 1\n# c\n\t# d\nb: !bwref x/foo\n"],
    ["a tab-led comment after a comment at the document start", "# c\n\t# d\na: !bwref x/foo\n"],
    ["a tab-only line between two comments", "a: 1\n# c\n\t\n# d\nb: !bwref x/foo\n"],
    ["a tab-led comment between a tag and its node", "a: !other\n# c\n\t# d\n  !u b: !bwref x/foo\n"],
    ["an indented comment line", "a: 1\n  # c\nb: !bwref x/foo\n"],
    ["a tab-only line after a plain value, past its holder", "k: !bwref x/foo\n \t\n"],
    ["a tab-led comment after a plain value, past its holder", "k: !bwref x/foo\n \t# c\nb: 1\n"],
    ["a tab-only line after a nested plain value", "k:\n  j: !bwref x/foo\n    \t\n"],
    ["a tab-led comment after a comment on a marker", "--- # c\n\t# d\na: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a block entry", "- # c\n\t# d\n  a: !bwref x/foo\n"],
    ["a tab-led comment after a comment on two block entries", "- - # c\n\t# d\n    a: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a marker that follows a plain value", "a: 1\n--- # c\n\t# d\nb: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a marker that follows a top-level plain scalar", "foo\n--- # c\n\t# d\nb: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a marker that follows a nested plain value", "a:\n  b: 1\n--- # c\n\t# d\nc: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a marker that follows a dedented plain continuation", "a: x\n  y\n--- # c\n\t# d\nb: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a block entry that follows a plain entry", "- 1\n- # c\n\t# d\n  b: !bwref x/foo\n"],
    ["a tab-led comment after a comment on a block entry that follows a nested plain entry", "k:\n  - 1\n  - # c\n\t# d\n    b: !bwref x/foo\n"],
    ["a tab-led comment right after a directive","%YAML 1.1\n\t# c\n---\na: !bwref x/foo\n"],
    ["a tab-only line right after a directive", "%YAML 1.1\n\t\n---\na: !bwref x/foo\n"],
    ["a tab-led comment right after a second directive", "%YAML 1.1\n%TAG !a! tag:x\n\t# c\n---\na: !bwref x/foo\n"],
    ["a tab-led comment at the end of the look-ahead of 512 bytes", `a: 1\n# c\n${" ".repeat(509)}\t# d\nb: !bwref x/foo\n`],
    ["a tab-only line and a comment, within the look-ahead of 512 bytes", `a: 1\n# c\n${" ".repeat(250)}\t\n${" ".repeat(250)}\t# d\nb: !bwref x/foo\n`],
  ])("a tab-led block line: the valid control %s still lists the name", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  test.each([
    ["%YAML after a quoted value", 'a: !<!bwref> "x/foo"\n %YAML 1.1\n'],
    ["%TAG after a quoted value", 'a: !bwref "x/foo"\n  %TAG !x! !bw\n'],
    ["%x in a block sequence", "- !bwref x/foo\n- %x\n"],
    ["%b in a flow sequence", "[!bwref x/foo,\n %b]\n"],
    ["a tab, then %x", 'a: !bwref "x/foo"\n\t%x\n'],
    ["%x after a quoted value on the same line", 'a: !bwref "x/foo" %x\n'],
    ["%x after a single quoted value on the same line", "a: !bwref 'x/foo' %x\n"],
    ["%x after an alias", "a: !bwref x/foo\nb: &c y\nd: *c %x\n"],
    ["%x after a flow sequence", "a: !bwref x/foo\nb: [c] %x\n"],
    ["%x after a quote across a flow line break", '[!bwref "x/foo"\n %x]\n'],
    ["%x after a quote that a line break closes", 'a: !bwref "x/foo\n  y" %x\n'],
  ])("an indented percent line: %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  test.each([
    ["a double quote", 'a: !bwref x/foo\nb: "x\n---\ny"\n'],
    ["a single quote", "a: !bwref x/foo\nb: 'x\n---\ny'\n"],
    ["a `...` marker", 'a: !bwref x/foo\nb: "x\n...\ny"\n'],
    ["a `--- c` marker", 'a: !bwref x/foo\nb: "x\n--- c\ny"\n'],
    ["a `---` and a tab", 'a: !bwref x/foo\nb: "x\n---\t\ny"\n'],
  ])("a marker in a quoted scalar: %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  test.each([
    ["a `---` in a double quote", 'a: [!bwref x/foo, "x\n---\ny"]\n---\nb: !bwref x/bar\n'],
    ["a `...` in a double quote", 'a: [!bwref x/foo, "x\n...\ny"]\n---\nb: !bwref x/bar\n'],
    ["a `---` in a double quote after a plain value", 'a: !bwref x/foo\nb: "x\n---\ny"\n---\nc: !bwref x/bar\n'],
    ["a `---` in a single quote", "a: !bwref x/foo\nb: 'x\n---\ny'\n---\nc: !bwref x/bar\n"],
  ])("a marker in a quoted scalar refuses its document and the next one lists: %s", (_, text) => {
    expect(references(text)).toEqual(["x/bar"]);
  });

  // Each of these is a valid stream with one tag on the node, a percent that is text, or a marker that
  // is no marker.
  test.each([
    ["a tag on an empty value, then a key with its own tag", "a: !other\nb: !bwref x/foo\n"],
    ["a tag on an empty value, then a tagged key", "a: !other\n!u k: !bwref x/foo\n"],
    ["a tagged key in a flow mapping", "{!other a: !bwref x/foo}\n"],
    ["a dash between two tags", "- !other\n- !bwref x/foo\n"],
    ["a tagged explicit key and value", "? !other a\n: !bwref x/foo\n"],
    ["a percent that a plain scalar goes on over", "a: x\n %y\nb: !bwref x/foo\n"],
    ["a percent that a plain scalar goes on over in a flow sequence", "[a\n %b, !bwref x/foo]\n"],
    ["a percent in a block scalar", "a: |\n  %x\nb: !bwref x/foo\n"],
    ["a percent in a quoted scalar", 'a: "q\n %x"\nb: !bwref x/foo\n'],
    ["an indented marker in a quoted scalar", 'a: !bwref x/foo\nb: "x\n ---\ny"\n'],
    ["a marker with text on it in a quoted scalar", 'a: !bwref x/foo\nb: "x\n---x\ny"\n'],
    ["a tag in a comment", "a: !bwref x/foo # !other\n"],
    ["a quoted name", 'a: !bwref "x/foo"\nb: 1\n'],
  ])("one tag on a node: %s still lists the name", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  test("a faulty tag, percent line or quote marker blanks its document and no other", () => {
    expect(references("a: !other !bwref x/foo\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
    expect(references("a: !bwref x/foo\n---\nb: !other !bwref y/bar\n")).toEqual(["x/foo"]);
    expect(references('a: !bwref "x/foo"\n  %x\n---\nb: !bwref x/bar\n')).toEqual(["x/bar"]);
    expect(references('a: !bwref x/foo\n---\nb: !bwref "y/bar"\n  %x\n')).toEqual(["x/foo"]);
    expect(references('a: !bwref "x/foo" %x\n---\nb: !bwref x/bar\n')).toEqual(["x/bar"]);
    expect(references('[!bwref "x/foo"\n %x]\n---\nb: !bwref x/bar\n')).toEqual(["x/bar"]);
    expect(references('a: !bwref x/foo\nb: "x\n---\ny"\n---\nc: !bwref x/bar\n')).toEqual(["x/bar"]);
    expect(references('a: !bwref x/foo\n---\nb: !bwref y/bar\nc: "x\n---\ny"\n')).toEqual(["x/foo"]);
  });

  // The directive line inside the open flow collection belongs to the faulty document: it declares no
  // handle for the document after it, which then lists nothing for a name behind that handle.
  test("a directive in an open flow sequence declares nothing for the next document", () => {
    expect(references("a: [!bwref x/foo,\n%TAG !x! !bw\n---\nb: !x!ref x/bar\n")).toEqual([]);
    expect(references("a: [!bwref x/foo,\n%TAG !x! !bw\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
  });

  // In a flow collection a plain scalar goes on over the line break, and go-yaml ends it at a marker
  // or a comment only (yaml_parser_scan_plain_scalar). A `%` in the first column is then text of the
  // scalar, not a directive: go-yaml reads these streams as one document, so the name behind lists.
  test.each([
    ["a plain scalar", "a: [plain\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
    ["a plain scalar after a comma", "a: [x, plain\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
    ["a plain scalar in a flow mapping", "a: {k: plain\n%YAML 1.2\n}\nb: !bwref x/foo\n"],
    ["a plain scalar and a %TAG line", "a: [plain\n%TAG !x! !bw\n]\nb: !bwref x/foo\n"],
    ["a plain scalar, CRLF", "a: [plain\r\n%YAML 1.2\r\n]\r\nb: !bwref x/foo\r\n"],
  ])("a directive-like line after %s in a flow sequence is text", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // The text line declares no handle, so a name behind it is still refused: only the line is text.
  test("a directive-like line that is flow text declares no handle", () => {
    expect(references("a: [plain\n%TAG !x! !bw\n]\nb: !x!ref x/foo\n")).toEqual([]);
  });

  // Any other token before the line makes the `%` start a token, and go-yaml reads a directive token
  // inside the flow collection as an error: the document lists nothing.
  test.each([
    ["a comment", "a: [plain # c\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
    ["a quoted scalar", 'a: ["q"\n%YAML 1.2\n]\nb: !bwref x/foo\n'],
    ["a comma", "a: [plain,\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
    ["a tag", "a: [!bwref\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
    ["an opening bracket", "a: [\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
  ])("a directive line after %s in a flow sequence refuses the document", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // A directive line in the first column ends a plain value: go-yaml ends the scalar at a line less
  // indented than its parent (scannerc.go line 2815) and reads the `%` as a directive token (line 726).
  // The first document is valid and lists its name. An indented line after the directive is content
  // with no `---`, and it refuses the document it opens (ginsys/bronzeward#632).
  test.each([
    ["%YAML 1.1", "a: !bwref x/foo\n%YAML 1.1\n  continuation\n"],
    ["%YAML 1.1 after an anchor", "a: !bwref &e x/foo\n%YAML 1.1\n  continuation\n"],
    ["%TAG", "a: !bwref x/foo\n%TAG !x! !bw\n  continuation\n"],
  ])("a %s line before an indented line ends the value, which lists", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // Controls, no directive line. An indented `%` line is text: go-yaml joins it to the scalar, so the
  // value is `x/foo %not continuation`, which names nothing. A comment line ends the scalar, and
  // go-yaml refuses the indented line after it ("did not find expected key"), so the document lists
  // nothing too. The scanner gives `[]` in both cases.
  test.each([
    ["a comment line", "a: !bwref x/foo\n# c\n  continuation\n"],
    ["an indented % line", "a: !bwref x/foo\n  %not\n  continuation\n"],
  ])("%s before an indented line lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // go-yaml ends a plain scalar in a flow collection at `?` (scannerc.go line 2731), and `?` is a key
  // token there (line 796). A key token right after a scalar, with no comma, is a parser error, and a
  // `%` line after it is a directive token, so the document lists nothing (ginsys/bronzeward#632).
  test.each([
    ["a plain scalar", "a: [plain?\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
    ["a plain scalar after a comma", "a: [x, plain?\n%TAG !x! !bw\n]\nb: !bwref x/foo\n"],
    ["a plain scalar in a flow mapping", "a: {k: plain?\n%YAML 1.2\n}\nb: !bwref x/foo\n"],
    ["a plain scalar with a question mark inside", "a: [pl?ain\n%YAML 1.2\n]\nb: !bwref x/foo\n"],
    ["a plain scalar and a tag", "a: [q? !bwref x/foo]\n"],
    ["a quoted scalar", 'a: ["q"?]\nb: !bwref x/foo\n'],
  ])("a question mark after %s in a flow collection refuses the document", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  test("a question mark after a scalar in a flow collection refuses its document only", () => {
    expect(references("a: [plain?]\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
  });

  // Controls: a `?` that opens a key, at the start of a flow item, is an explicit-key indicator and
  // leaves the document valid; go-yaml v3.0.5 parses each of these and reads the tag.
  test.each([
    ["after a comma in a sequence", "a: [x, ? y]\nb: !bwref x/foo\n"],
    ["first in a sequence", "a: [? y: !bwref x/foo]\n"],
    ["first in a mapping", "a: {? k: !bwref x/foo}\n"],
  ])("a question mark that opens a key %s leaves the document valid", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // In block context a `?` inside a plain scalar is text.
  test("a question mark in a block plain scalar is text", () => {
    expect(references("a: plain?\n  more\nb: !bwref x/foo\n")).toEqual(["x/foo"]);
  });

  test("a line that looks like a directive in a block scalar or a comment declares nothing", () => {
    expect(references("a: |\n  %TAG ! !bw\nb: !ref x/foo\n")).toEqual([]);
    expect(references("# %TAG ! !bw\nb: !ref x/foo\n")).toEqual([]);
  });

  // ginsys/bronzeward#633: two shapes that go-yaml accepts, which the review of #633 called faults.
  // go-yaml skips the blanks after a directive's value, and there can be none, then reads a `#` as a
  // comment (yaml_parser_scan_directive, scannerc.go lines 1676-1699, go.yaml.in/yaml/v3 v3.0.5). It
  // reads `,`, `[` and `]` as URI characters in every tag form (yaml_parser_scan_tag_uri, lines
  // 2105-2121), so a shorthand tag that holds one is another tag, never a refusal. The oracle batch
  // of the comparison tool shows both with go-yaml itself (S8 rows).
  test("a directive comment without a blank, and a flow indicator in a shorthand tag, are no faults", () => {
    expect(references("%YAML 1.1#comment\n---\na: !bwref x/foo\n")).toEqual(["x/foo"]);
    expect(references("a: !bwref] x/foo\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
    expect(references("a: [!bwref, x/foo]\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
  });

  // go-yaml fails the whole stream for a refused document. The scanner blanks that document only (the
  // owner's wording of ginsys/bronzeward#606), so an earlier document still lists its names. The
  // comparison tool counts such a document apart, as one that go-yaml refuses.
  test("a refused document blanks itself and no other", () => {
    expect(references("a: !bwref x/foo\n---\nb: !q!z y\n")).toEqual(["x/foo"]);
    expect(references("%YAML 1.2\n---\na: !bwref x/foo\n---\nb: !bwref x/bar\n")).toEqual(["x/bar"]);
  });

  // ginsys/bronzeward#654: go-yaml allows a simple key only where simple_key_allowed is set. A key's
  // colon sets it to false, so no key, `- ` or `? ` can follow a value on its line (scannerc.go
  // yaml_parser_fetch_value, fetch_block_entry, fetch_key, go.yaml.in/yaml/v3 v3.0.5). A document marker
  // clears it too. A new line, `- `, `? ` and the colon of an explicit key set it again.
  test.each([
    ["a plain key after a plain value", "a: b: !bwref x/foo\n"],
    ["a plain key after a value of several words", "a: b c: !bwref x/foo\n"],
    ["a flow sequence key after a tag", "a: !u [? b]: !bwref x/foo\n"],
    ["a flow sequence key with two items after a tag", "a: !u [b, c]: !bwref x/foo\n"],
    ["a flow sequence key", "a: [b]: !bwref x/foo\n"],
    ["a flow mapping key", "a: {b: c}: !bwref x/foo\n"],
    ["a double-quoted key", 'a: "b": !bwref x/foo\n'],
    ["a single-quoted key", "a: 'b': !bwref x/foo\n"],
    ["an alias key", "a: *x : !bwref x/foo\n"],
    ["a key after an anchor", "a: &x b: !bwref x/foo\n"],
    ["a key after a tag", "a: !t b: !bwref x/foo\n"],
    ["a key after a tab", "a:\tb: !bwref x/foo\n"],
    ["a key after several blanks", "a:   b: !bwref x/foo\n"],
    ["a key in a nested mapping", "a:\n  b: c: !bwref x/foo\n"],
    ["a key in a sequence entry", "- a: b: !bwref x/foo\n"],
    ["a key in a nested sequence entry", "- - a: b: !bwref x/foo\n"],
    ["a key after a value in an explicit key", "? a: b: !bwref x/foo\n"],
    ["a block entry", "a: - b: !bwref x/foo\n"],
    ["an explicit key", "a: ? b: !bwref x/foo\n"],
    ["an explicit value indicator", "a: : !bwref x/foo\n"],
    ["a block entry after a flow sequence", "a: [k] - b\nk: !bwref x/foo\n"],
    ["a block entry after a quoted value", 'a: "q" - b\nk: !bwref x/foo\n'],
    ["an explicit key after a flow mapping", "a: {k: v} ? b\nk: !bwref x/foo\n"],
    ["a key on the line of a document marker", "--- a: !bwref x/foo\n"],
    ["a key in a second document, on its marker line", "c: 1\n--- a: !bwref x/foo\n"],
  ])("a key, entry or value indicator after a value on its line lists nothing: %s", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  test.each([
    ["a key on its own line", "a:\n  b: !bwref x/foo\n"],
    ["a key after a sequence dash", "- a: !bwref x/foo\n"],
    ["a key after two dashes", "- - a: !bwref x/foo\n"],
    ["a key after an explicit key indicator", "? a: !bwref x/foo\n"],
    ["a key after the colon of an explicit key", "? a\n: b: !bwref x/foo\n"],
    ["a key after an explicit key and its colon, nested", "a:\n  ? b\n  : c: !bwref x/foo\n"],
    ["a sequence after the colon of an explicit key", "? a\n: - b: !bwref x/foo\n"],
    ["a key after a value on the line above", "a: b\nc: !bwref x/foo\n"],
    ["a colon with no blank after it", "a: http://x\nb: !bwref x/foo\n"],
    ["a flow collection value", "a: {b: c}\nd: !bwref x/foo\n"],
    ["a flow sequence value", "a: [b, c]\nd: !bwref x/foo\n"],
    ["a flow collection across two lines", "a: {b: c,\n  d: e}\nf: !bwref x/foo\n"],
    ["a quoted colon", 'a: "x: y"\nb: !bwref x/foo\n'],
    ["a colon in a comment", "a: b # c: d\ne: !bwref x/foo\n"],
    ["a block scalar value", "a: |\n  b: c\nd: !bwref x/foo\n"],
    ["a quoted key", '"a b": !bwref x/foo\n'],
    ["a key after a tag", "!t a: !bwref x/foo\n"],
    ["a key after a tag on the key", "a:\n  !t b: !bwref x/foo\n"],
    ["a marker with a comment", "--- # c\na: !bwref x/foo\n"],
    ["a marker with a tag on its line", "--- !t\na: !bwref x/foo\n"],
    ["a scalar on the line of a marker", "--- a\n---\nb: !bwref x/foo\n"],
    ["a key in a second document", "c: 1\n---\na: !bwref x/foo\n"],
  ])("a key, entry or value indicator after a value on its line: the valid control %s still lists the name", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // ginsys/bronzeward#654 leaves a flow key of only `?` items unrefused. go-yaml refuses some of these
  // documents ("did not find expected key") and accepts others by token timing. The scanner lists them
  // all, as it did before. The rule moved to ginsys/bronzeward#657. Each document here is one go-yaml
  // accepts, and a refusal rule for this key must not blank it.
  test.each([
    ["a sibling key after an explicit entry", "? a\n: b\n[? c]: !bwref x/foo\n"],
    ["a sibling key after a blank line", "? a\n: b\n\n[? c]: !bwref x/foo\n"],
    ["a sibling key after a flow sequence value", "? a\n: [b]\n[? c]: !bwref x/foo\n"],
    ["a sibling key after an anchored value", "? a\n: &x b\n[? c]: !bwref x/foo\n"],
    ["a sibling key in a mapping", "x:\n  ? a\n  : b\n  [? c]: !bwref x/foo\n"],
    ["a sibling flow mapping key", "? a\n: b\n{? c}: !bwref x/foo\n"],
    ["a key after the colon of an explicit key", "? a\n: [? b]: !bwref x/foo\n"],
    ["a key after an anchor on the value of an explicit key", "? a\n: &x\n  [? b]: !bwref x/foo\n"],
    ["a key after a tag on the value of an explicit key", "? a\n: !t\n  [? b]: !bwref x/foo\n"],
    ["a key with another item", "a:\n  [? b, c]: !bwref x/foo\n"],
    ["a key under a tag", "a:\n  !t [? b]: !bwref x/foo\n"],
  ])("a flow key with ? items lists the name as before: %s", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // The two shapes of the issue that stay unrefused. go-yaml refuses both, and the scanner lists them
  // as it did before ginsys/bronzeward#657 settles the rule.
  test.each([
    ["a flow sequence key", "a:\n  [? b]: !bwref x/foo\n"],
    ["a flow mapping key", "a:\n  {? b}: !bwref x/foo\n"],
    ["a sibling key after an implicit entry", "a: b\n[? c]: !bwref x/foo\n"],
    ["a sibling key after two entries", "? a\n: b\nd: e\n[? c]: !bwref x/foo\n"],
    ["a key after a sequence entry", "- ? a\n  : b\n- [? c]: !bwref x/foo\n"],
  ])("a flow key of only ? items that go-yaml refuses is not refused yet: %s", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
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

// A plain value that goes on to the next line (ginsys/bronzeward#616). go-yaml ends a plain scalar at
// a document marker, a comment, a colon before a blank, and, in flow context, at a flow indicator
// (scannerc.go lines 2693-2818). In block context a line that is more indented than the parent is
// text, and in flow context the indent test is off (line 2815).
describe("a plain !bwref value that goes on to the next line", () => {
  // The next line is text of the scalar (or an error for a key), so the value is longer than the name.
  test.each([
    ["a comma", "k: !bwref x/foo\n  ,bar\n"],
    ["a closing bracket", "k: !bwref x/foo\n  ]\n"],
    ["a closing brace", "k: !bwref x/foo\n  }\n"],
    ["a dash and a blank", "k: !bwref x/foo\n  - y\n"],
    ["a question mark and a blank", "k: !bwref x/foo\n  ? y\n"],
    ["a key", "k: !bwref x/foo\n  j: v\n"],
    ["a line of no-break spaces", "k: !bwref x/foo\n   \n"],
    ["a dash in a sequence entry", "- !bwref x/foo\n  - y\n"],
  ])("a more indented line with %s in block context lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // A root scalar has no parent: go-yaml measures its continuation lines against column -1, so any line
  // with text goes on, at column 0 and with a no-break space as well. The rows are from the root sweep
  // of experiments/scanner-cross-line/sets/gen-s10-root-scalar.mjs, judged against go-yaml v3.0.5.
  test.each([
    ["a word at column 0 after a marker", "--- !bwref x/foo\nbar\n"],
    ["a no-break space after a marker", "--- !bwref x/foo\n bar\n"],
    ["a no-break space after a marker and an anchor", "--- &e !bwref x/foo\n bar\n"],
    ["a no-break space with no marker, after an anchor", "&e !bwref x/foo\n bar\n"],
    ["a dash at column 0", "!bwref x/foo\n- y\n"],
    ["a dash at column 1", "!bwref x/foo\n - y\n"],
    ["a word at column 0 after a marker line", "---\n!bwref x/foo\nbar\n"],
    ["a word at column 0 after a comment line", "# c\n!bwref x/foo\nbar\n"],
    ["a word at column 0 in the second document", "a: 1\n---\n!bwref x/foo\nbar\n"],
    // go-yaml ends the scalar at a comment line, so the text after it is a document it refuses. The scanner
    // skips the comment line (it is masked), reads the text as the next line and lists nothing too.
    ["a comment line, then a word at column 0", "!bwref x/foo\n# c\nbar\n"],
    ["a comment line, then a dash, after a marker", "--- !bwref x/foo\n# c\n- y\n"],
  ])("%s under a root scalar lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // In flow context go-yaml does not compare the indentation of the next line.
  test.each([
    ["a word at column 0", "[!bwref x/foo\nbar]\n"],
    ["a word and a comma", "[!bwref x/foo\nbar, k: v]\n"],
    ["a word and a colon", "[!bwref x/foo\nbar: z]\n"],
  ])("%s in flow context lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // The scalar ends, and the name lists.
  test.each([
    ["a key at the parent's column", "k: !bwref x/foo\nj: v\n"],
    ["a document marker", "k: !bwref x/foo\n---\n"],
    ["a key in a compact entry", "- k: !bwref x/foo\n  j: v\n"],
    ["a closing bracket in flow context", "[!bwref x/foo\n]\n"],
    ["a comma in flow context", "[!bwref x/foo\n, y]\n"],
    // U+00A0 is no indentation: the line has text at column 0, a key of its own (go-yaml reads it so).
    ["a key that starts with a no-break space", "k: !bwref x/foo\n\u00a0j: v\n"],
    // A tag on its own line under a compact key: the key stands at column 2, not at the line's column 0.
    ["a sibling key of a compact key", "- k:\n    !bwref x/foo\n  j: v\n"],
    ["a sibling entry of a compact dash", "- -\n    !bwref x/foo\n  - y\n"],
  ])("%s ends the value, which lists", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // A directive line in the first column ends the scalar before the text rules read the next line.
  test("a directive line before a comma line ends the value, which lists", () => {
    expect(references("k: !bwref x/foo\n%YAML 1.1\n  ,bar\n")).toEqual(["x/foo"]);
  });

  // The indentation is counted in YAML blanks (U+0020 and tab) on both sides of the comparison: a line that
  // starts with U+00A0 or a byte order mark has text at column 0, so a key that starts with it stands at
  // column 0, and a next line of a space and U+00A0 is more indented than that key and goes on. The rows
  // are from the sweeps S9 and S11 (experiments/scanner-cross-line), judged against go-yaml v3.0.5.
  test.each([
    ["a key that starts with a no-break space", "\u00a0k: !bwref x/foo\n \u00a0bar\n"],
    ["a key that starts with a no-break space, after a blank line", "\u00a0k: !bwref x/foo\n\n \u00a0bar\n"],
    ["a key that starts with a no-break space and an anchor", "\u00a0k: &e !bwref x/foo\n \u00a0bar\n"],
    ["a key that starts with a byte order mark", "\ufeffk: !bwref x/foo\n \u00a0bar\n"],
    ["a nested key that starts with a no-break space", "k:\n\u00a0j: !bwref x/foo\n \u00a0bar\n"],
  ])("a more indented line under %s lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // go-yaml reads a tag that starts its node on a line of its own as the value of the key above it, also
  // after an anchor: `&e !bwref x/foo` under `k:` goes on at the column of the tag, as `!bwref x/foo` does.
  // The holder is the key, not the tag's line. A second tag before !bwref is no node property: go-yaml refuses
  // the document (`k:\n  !x !bwref x/foo`), so no row lists it. The scalar goes on in each form of the line.
  test.each([
    ["spaces", "k:\n  &e !bwref x/foo\n  bar\n"],
    ["a space and a no-break space", "k:\n  &e !bwref x/foo\n  \u00a0bar\n"],
    ["a space and a tab", "k:\n  &e !bwref x/foo\n  \tbar\n"],
    ["a blank line and spaces", "k:\n  &e !bwref x/foo\n\n  bar\n"],
    ["spaces, under a compact key", "- k:\n    &e !bwref x/foo\n    bar\n"],
    ["spaces, under a dash", "-\n  &e !bwref x/foo\n  bar\n"],
    ["a no-break space, under a key at column 1", " k:\n   &e !bwref x/foo\n   \u00a0bar\n"],
    ["a no-break space, under a key that starts with a no-break space", "\u00a0k:\n  &e !bwref x/foo\n  \u00a0bar\n"],
  ])("an anchor before the tag on its own line, then %s, lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // go-yaml measures a root scalar against column -1 also when the tag is indented: a root has no key
  // above, so the first line of the stream may start at any column.
  test.each([
    ["an anchor", "  &e !bwref x/foo\n  \u00a0bar\n"],
    ["no anchor", "  !bwref x/foo\n  \u00a0bar\n"],
    ["no anchor, a word at column 0", " !bwref x/foo\nbar\n"],
  ])("an indented root scalar with %s goes on and lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // go-yaml drops a byte order mark at the start of the stream: it is no text and no column there. A root
  // scalar after it is a root scalar, and a compact key after it has the dash at column 0.
  test("a byte order mark at the start of the stream is no text before a root scalar", () => {
    expect(references("\ufeff!bwref x/foo\nbar\n")).toEqual([]);
  });
  test("a byte order mark at the start of the stream is no column before a compact key", () => {
    expect(references("\ufeff- k: !bwref x/foo\n  y: v\n")).toEqual(["x/foo"]);
  });

  // go-yaml refuses these documents (a line of text at column 0 under a key at column 0, which starts with a
  // no-break space): the line ends the value as a line of ASCII text does, and the name lists. The server
  // lists no reference for a refused document, so this only pins the reading of the scanner.
  test.each([
    ["a word", "k: !bwref x/foo\n\u00a0bar\n"],
    ["a word after an anchor", "k: &e !bwref x/foo\n\u00a0bar\n"],
  ])("a line that starts with a no-break space at the key column (%s) ends the value of a refused document", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // A root scalar after a marker line: the stream-start byte order mark is no text before the marker, a
  // CRLF after the marker is no text, and a property on the marker line (`--- &e`) belongs to the node. In
  // each form the scalar is a root scalar, measured against column -1, so a line of a no-break space and
  // text at column 0 goes on. The rows are from the review of this change and the set S12
  // (experiments/scanner-cross-line), judged against go-yaml v3.0.5.
  test.each([
    ["a byte order mark before the marker", "\ufeff---\n !bwref x/foo\n\u00a0bar\n"],
    ["a byte order mark before the marker, a word at column 0", "\ufeff---\n !bwref x/foo\nbar\n"],
    ["a CRLF marker line", "---\r\n !bwref x/foo\r\n\u00a0bar\r\n"],
    ["a CRLF marker line, indented by two", "---\r\n  !bwref x/foo\r\n\u00a0bar\r\n"],
    ["a byte order mark and a property on the marker line", "\ufeff--- &e\n !bwref x/foo\n\u00a0bar\n"],
    ["a byte order mark, a CRLF and a property on the marker line", "\ufeff--- &e\r\n !bwref x/foo\r\n\u00a0bar\r\n"],
  ])("a root scalar after %s goes on and lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // A line that holds only a node property (`&e`) is no parent: the key above it holds the value, as when
  // the anchor stands on the line of the tag. A line of a space and U+00A0 under a key at column 0 is more
  // indented than the key and goes on. The same holds for a dash and for a key at column 1.
  test.each([
    ["a key", "k:\n &e\n  !bwref x/foo\n \u00a0bar\n"],
    ["a key at column 1", " k:\n  &e\n   !bwref x/foo\n  \u00a0bar\n"],
    ["a key that starts with a no-break space", "\u00a0k:\n &e\n  !bwref x/foo\n \u00a0bar\n"],
    ["a dash", "-\n &e\n  !bwref x/foo\n \u00a0bar\n"],
    ["a key, the tag at the column of the anchor line", "k:\n &e\n !bwref x/foo\n \u00a0bar\n"],
  ])("an anchor on a line of its own under %s, then a more indented no-break space line, lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // The same line above a root scalar: it is no text, so the tag is on a root scalar and a `- ` line goes on
  // (review of this change; go-yaml reads `x/foo - y`). The row without the anchor is the control.
  test.each([
    ["no line of properties", "!bwref x/foo\n- y\n"],
    ["an anchor on a line of its own", "&e\n!bwref x/foo\n- y\n"],
    ["an anchor and a blank line", "&e\n\n!bwref x/foo\n- y\n"],
    ["an anchor after a marker", "---\n&e\n!bwref x/foo\n- y\n"],
    ["an anchor and an indented tag line", "&e\n  !bwref x/foo\n- y\n"],
  ])("a root scalar after %s goes on to a dash line and lists nothing", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // The editor reads a draft while the user types, so the cost of the scan must stay near linear
  // (review of this change). A check that takes too long on the earlier code ends on its own and fails
  // the bound, and does not hang: a test timeout cannot stop a synchronous scan.
  //
  // The comment of a check gives the time of the earlier code where it was measured, with the commit. A
  // check with no such figure is not shown to fail first.
  describe("cost", () => {
    // Reads a draft and fails if the scan takes a second or more.
    const bounded = (text: string) => {
      const start = performance.now();
      const found = references(text);
      expect(performance.now() - start).toBeLessThan(1000);
      return found;
    };

    // A draft of 30,000 entries with a tag alone on its line: the scan back from the tag stops at the key
    // line above it, so it must not read the text before that line. This draft took 27,987 ms on e354c61c.
    test("a draft with 30,000 tags on their own lines lists every one", () => {
      const text = Array.from({ length: 30000 }, (_, i) => `k${i}:\n  !bwref x/foo${i}\n`).join("");
      expect(bounded(text)).toHaveLength(30000);
    });

    // A line of many bangs and a word: a tag on a line of its own below it is not on a root scalar, and
    // the line of bangs is no line of properties only. Matching the line must not try every split of it.
    // This line of 25 bangs took 1,177 ms on e354c61c.
    test("a long line of bangs and a word above a tag ends the scan at once", () => {
      const start = performance.now();
      references(`${"!".repeat(25)} text:\n!bwref x/foo\n- y\n`);
      expect(performance.now() - start).toBeLessThan(1000);
    });

    // The scan back from a tag that starts its line looks for the nearest line above that is less indented.
    // It must not read every line above when none is, or when the lines between are all as indented as the
    // tag. The 3,000 tags at column 0 took 4,329 ms and the 2,000 tags among sibling keys took 1,328 ms on
    // 79806121.

    // A tag at column 0 that is no root scalar: no line is less indented than column 0.
    test("3,000 tags at column 0 under a key are scanned in near linear time", () => {
      bounded(Array.from({ length: 3000 }, (_, i) => `k:\n!bwref x/foo${i}\nbar\n`).join(""));
    });

    // Tags on lines of their own under one key, each below a sibling key at the same indent: the line
    // that holds the key is far above every tag but the first.
    test("2,000 tags among sibling keys under one key are scanned in near linear time", () => {
      bounded(`k:\n${Array.from({ length: 2000 }, (_, i) => `  j${i}: v\n  !bwref x/foo${i}\n`).join("")}`);
    });

    // Many tags under one long key: the line of the key is the parent of each of them, and it is matched once
    // (`known` of the line), not once per tag. The key has 300,000 characters and the 4,000 tags took 2.1 to
    // 2.8 s in six runs on 86c93ac4, where each tag matched it again (a key of 200,000 characters and 3,000
    // tags took 0.9 to 1.6 s there, too close to the bound to fail it every time).
    test("4,000 tags among sibling keys under one key of 300,000 characters are scanned in near linear time", () => {
      bounded(`${"a".repeat(300000)}:\n${Array.from({ length: 4000 }, (_, i) => `  j${i}: v\n  !bwref x/foo${i}\n`).join("")}`);
    });

    // A continuation line with a long run of blanks between two words: trimming it must not try every
    // start of the run. Three paths trim a continuation line: a plain tag, a tag after an anchor (both
    // `continues`) and an anchor after the tag (`anchoredContinues`, whose old pattern was on main).
    // With the run of 60,000 blanks inside the line, the three paths took 3,631 ms, 3,224 ms and 3,288 ms
    // on 79806121, in that order. On 86c93ac4 the anchored path already calls `trimBlanks()`, so its check
    // fails there only with the old pattern put back in `anchoredContinues()` (2,999 ms in one run). With
    // the run at the start of the line, the plain tag took 4.03, 1.52 and 1.35 ms in three runs on 79806121.
    const trimHeads = [
      ["a plain tag", "k: !bwref a\n"],
      ["a tag after an anchor", "k: &e !bwref a\n"],
      ["an anchor after the tag", "k: !bwref &e a\n"],
    ];
    test.each(trimHeads)("a continuation line with 60,000 blanks inside it is trimmed in linear time (%s)", (_, head) => {
      bounded(`${head}  bar${" ".repeat(60000)}baz\n`);
    });

    // The same walks with the long run at the start of the line, and at its end.
    test.each(trimHeads)("a continuation line with 60,000 blanks at its start is trimmed in linear time (%s)", (_, head) => {
      bounded(`${head}${" ".repeat(60000)}bar\n`);
    });
    test.each(trimHeads)("a continuation line with 60,000 blanks at its end is trimmed in linear time (%s)", (_, head) => {
      bounded(`${head}  bar${" ".repeat(60000)}\n`);
    });

    // The search for the parent line of a tag runs a binary search over the stack of open lines, and the stack
    // is as deep as the indentation rises: 1,000 keys, each one column deeper than the one above.
    test("a tag below 1,000 keys of rising indentation is scanned in near linear time", () => {
      const keys = Array.from({ length: 1000 }, (_, i) => `${" ".repeat(i)}k${i}:\n`).join("");
      bounded(`${keys}${" ".repeat(1000)}!bwref x/foo\n${" ".repeat(1000)}bar\n`);
    });

    // A key of 100,000 characters before the tag on its own line (`PLAIN_PARENT`): the pattern must not try
    // every split of a long key. The first input matches. The second one has an anchor after the colon, so
    // the pattern reads the whole key and rejects it.
    test.each([
      ["a match", `${"a".repeat(100000)}: !bwref x/foo\n  bar\n`],
      ["a rejection", `${"a".repeat(100000)}: &e !bwref x/foo\n  bar\n`],
    ])("a key of 100,000 characters before the tag is matched in linear time (%s)", (_, text) => {
      bounded(text);
    });

    // Every other loop and pattern that the scan of a plain tag value runs, on a long input of its kind.
    test.each([
      ["5,000 properties before the tag", `k:\n  ${"!a ".repeat(5000)}!bwref x/foo\n  bar\n`],
      ["a line of 5,000 properties above the tag", `k:\n ${"!a ".repeat(5000)}\n  !bwref x/foo\n  bar\n`],
      ["5,000 anchors before the tag", `k:\n  ${"&e ".repeat(5000)}!bwref x/foo\n  bar\n`],
      ["a line of 5,000 anchors above the tag", `k:\n ${"&e ".repeat(5000)}\n  !bwref x/foo\n  bar\n`],
      ["a key of 100,000 characters above the tag",`${"a".repeat(100000)}:\n  !bwref x/foo\n  bar\n`],
      ["3,000 lines of an anchor and a blank line above a root scalar", `${"&e\n\n".repeat(3000)}!bwref x/foo\n- y\n`],
      ["a marker line with 40,000 blanks before a dash", `--- ${" ".repeat(40000)}- !bwref x/foo\n  bar\n`],
    ])("a long input stays linear: %s", (_, text) => {
      bounded(text);
    });
  });
});
