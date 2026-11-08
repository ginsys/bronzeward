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
  // tagged scalar is an explicit key, which lists no name (ginsys/bronzeward#637). A colon before a
  // flow indicator is text for go-yaml v3.0.5.
  test("flow context: an explicit key ends at the value indicator on the next line", () => {
    expect(references("k: {? !bwref &e x/foo\n: v}\n")).toEqual([]);
    expect(references("k: [? !bwref &e x/foo\n: v]\n")).toEqual([]);
    expect(references("k: {? !bwref &e x/foo\n:]}\n")).toEqual([]);
  });

  // go-yaml v3.0.5 reads a `?` in a flow collection as a key indicator with no blank after it (the probe of
  // ginsys/bronzeward#710 shows the key node tagged `!bwref` for each of these), so the tag is an explicit
  // key's own and lists no name. A `?` before a blank is the control that the scanner already handled.
  test.each([
    ["a flow sequence, a blank after the question mark (control)", "[? !bwref x/foo]\n"],
    ["a flow sequence, no blank", "[?!bwref x/foo]\n"],
    ["a flow sequence, an anchor and no blank", "[?&k !bwref x/foo]\n"],
    ["a flow sequence, an anchor after a blank", "[? &k !bwref x/foo]\n"],
    ["a flow mapping, no blank", "{?!bwref x/foo}\n"],
    ["a flow mapping, a blank (control)", "{? !bwref x/foo}\n"],
    ["after a flow entry, no blank", "[a, ?!bwref x/foo]\n"],
    ["nested in a block value, no blank", "k: [?!bwref x/foo]\n"],
  ])("flow context: an explicit key, %s, lists no name", (_what, doc) => {
    expect(references(doc)).toEqual([]);
  });

  test("block context: a question mark with no blank after it is a plain scalar, so the change stays in flow", () => {
    expect(references("?!bwref x/foo\n")).toEqual([]);
    expect(references("a:\n  ?foo: !bwref x/foo\n")).toEqual(["x/foo"]);
    expect(references("?foo: !bwref x/foo\n")).toEqual(["x/foo"]);
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

  // go-yaml ends a flow plain scalar at a line that starts with `?`, `[` or `{`, and then refuses the
  // document: after an entry the parser needs `,` or the closing bracket (yaml_parser_parse_flow_sequence_entry
  // and yaml_parser_parse_flow_mapping_key, parserc.go). Observed on go.yaml.in/yaml/v3 v3.0.5: all of these
  // rows give "did not find expected ',' or ']'" (or '}'), and the controls with a comma, a word or a colon
  // start parse. The scanner reads such a line as text, goes on and lists nothing, as for a refused
  // document. Ending the value at these starts would list a name for a refused document.
  test.each([
    ["a sequence, `?` start", "k: [!bwref x/foo\n? y]\n"],
    ["a sequence, `[` start", "k: [!bwref x/foo\n[y]]\n"],
    ["a sequence, `{` start", "k: [!bwref x/foo\n{y}]\n"],
    ["a mapping, `{` start", "k: {a: !bwref x/foo\n{y}}\n"],
    ["an anchor in a sequence, `?` start", "k: [!bwref &e x/foo\n? y]\n"],
    ["an anchor in a sequence, `[` start", "k: [!bwref &e x/foo\n[y]]\n"],
    ["an anchor in a sequence, `{` start", "k: [!bwref &e x/foo\n{y}]\n"],
    ["an anchor in a mapping, `?` start", "k: {a: !bwref &e x/foo\n? y}\n"],
    ["an anchor in a mapping, `[` start", "k: {a: !bwref &e x/foo\n[y]}\n"],
    ["an anchor in a mapping, `{` start", "k: {a: !bwref &e x/foo\n{y}}\n"],
    ["an anchor and an explicit key, `?` start", "k: [? !bwref &e x/foo\n? y]\n"],
    ["an anchor and an explicit mapping key, `?` start", "k: {? !bwref &e x/foo\n? y}\n"],
  ])("%s: go-yaml refuses the document, so it lists nothing", (_, text) => {
    expect(references(text), text).toEqual([]);
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

  // A tab after a mapping colon is a blank like a space (ginsys/bronzeward#698): go-yaml v3.0.5 reads the
  // value after it as it reads the value after a space. Every row below was read with go-yaml v3.0.5
  // (probe w698-tab on 4200c858): the rows of the first list give the scalar `x/foo - y`, `x/foo ? y` or
  // refuse the document (the key row); before the change the scanner listed `x/foo` for each of them,
  // and the space controls already listed nothing.
  test.each([
    ["a tab, then the tag", "k:\t!bwref x/foo\n  - y\n"],
    ["a tab, then the tag on its own line", "k:\t\n  !bwref x/foo\n  - y\n"],
    ["a space and a tab", "k: \t!bwref x/foo\n  - y\n"],
    ["a tab, a space and a tab", "k:\t \t!bwref x/foo\n  - y\n"],
    ["two tabs", "k:\t\t!bwref x/foo\n  - y\n"],
    ["a quoted key", '"k":\t!bwref x/foo\n  - y\n'],
    ["a single quoted key", "'k':\t!bwref x/foo\n  - y\n"],
    ["a question mark line", "k:\t!bwref x/foo\n  ? y\n"],
    ["a key line, which go-yaml refuses", "k:\t!bwref x/foo\n  j: v\n"],
    ["a CRLF line break after the tab", "k:\t\r\n  !bwref x/foo\r\n  - y\r\n"],
    ["a nested key, the tag on its own line", "a:\n  k:\t\n    !bwref x/foo\n    - y\n"],
    // The space controls: these listed nothing before the change.
    ["the space control of a question mark line", "k: !bwref x/foo\n  ? y\n"],
    ["the space control of a CRLF line break", "k:\r\n  !bwref x/foo\r\n  - y\r\n"],
    // A plain word and an anchor after the tag listed nothing before the change and pin the same reading.
    ["a plain word line", "k:\t!bwref x/foo\n  bar\n"],
    ["an anchor after the tag", "k:\t!bwref &e x/foo\n  - y\n"],
  ])("a more indented line after a tab and a tag lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The scalar ends at a sibling key, so the name lists, after a tab as after a space. Before the change
  // the pattern for an anchor after the tag (`ANCHORED_PARENT`) did not take the tab, so the scanner read
  // the scalar as longer and listed nothing: a lost name (go-yaml reads `x/foo`).
  test.each([
    ["a tab", "a:\n  k:\t!bwref &e x/foo\n  j: v\n"],
    ["a space (control)", "a:\n  k: !bwref &e x/foo\n  j: v\n"],
  ])("an anchor after the tag, then a sibling key, lists the name (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // An anchor before the tag (`&e !bwref x/foo`) is a property of the value, so the parent of the value is
  // still the key or the dash before it (ginsys/bronzeward#699). The next line then goes on with the scalar
  // as go-yaml reads it (`x/foo - y`, `x/foo ? y`), and the scanner lists no name. Before the change the
  // anchor made the parent unknown (`PLAIN_PARENT`), so a `- ` or `? ` line ended the scalar and the name
  // listed. The rows are the documents of probe w699-probe and w699-probe2 where `references()` listed
  // `x/foo` on 0b3a42da and go-yaml reads a longer scalar. The twins with no anchor of the first row, of
  // the nested key and of the sequence entry listed nothing there (probe rows B2, L4 and Q2).
  test.each([
    ["a key, then a dash line", "k: &e !bwref x/foo\n  - y\n"],
    ["a tab after the colon", "k:\t&e !bwref x/foo\n  - y\n"],
    ["a key, then a question mark line", "k: &e !bwref x/foo\n  ? y\n"],
    ["a nested key", "a:\n  k: &e !bwref x/foo\n    - y\n"],
    ["a double quoted key", '"k": &e !bwref x/foo\n  - y\n'],
    ["a single quoted key", "'k': &e !bwref x/foo\n  - y\n"],
    ["a tab between the anchor and the tag", "k: &e\t!bwref x/foo\n  - y\n"],
    ["two blanks around the anchor", "k:  &e  !bwref x/foo\n  - y\n"],
    ["a sequence entry, then a dash line", "- &e !bwref x/foo\n  - y\n"],
    ["a sequence entry, then a question mark line", "- &e !bwref x/foo\n  ? y\n"],
    ["the second entry of a sequence", "- a\n- &e !bwref x/foo\n  - y\n"],
    ["the anchor on the key line, the tag on the next line", "k: &e\n  !bwref x/foo\n  - y\n"],
    ["the anchor on the dash line, the tag on the next line", "- &e\n  !bwref x/foo\n  - y\n"],
    // A blank after the anchor on the dash line is a space or a tab, as it is on the key line (probe w699-probe3).
    ["a tab after the anchor on the dash line", "- &e\t\n  !bwref x/foo\n  - y\n"],
    ["a tab and a carriage return after the anchor on the dash line", "- &e\t\r\n  !bwref x/foo\r\n  - y\r\n"],
    ["two tabs after the anchor on the dash line", "- &e\t\t\n  !bwref x/foo\n  - y\n"],
    ["a tab and a space after the anchor on the dash line", "- &e\t \n  !bwref x/foo\n  - y\n"],
    ["a space and a tab after the anchor on the dash line", "- &e \t\n  !bwref x/foo\n  - y\n"],
    ["a tab and a comment after the anchor on the dash line", "- &e\t# c\n  !bwref x/foo\n  - y\n"],
    ["a tab after the anchor on the dash line, then a question mark line", "- &e\t\n  !bwref x/foo\n  ? y\n"],
    ["a tab after the anchor on a nested dash line", "a:\n  - &e\t\n    !bwref x/foo\n    - y\n"],
  ])("an anchor before the tag, then a line that goes on with the scalar, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The same documents where the anchor changes nothing: a sibling key or a line that is not more of the
  // scalar ends it, and go-yaml reads `x/foo`. These rows listed the name before the change and pin that
  // the fix does not take it away.
  test.each([
    ["a sibling key", "k: &e !bwref x/foo\nj: v\n"],
    ["a sibling key of a compact key", "- k: &e !bwref x/foo\n  j: v\n"],
    ["the next entry of a sequence", "- &e !bwref x/foo\n- y\n"],
  ])("an anchor before the tag, then a line that ends the scalar, lists the name (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // A plain line goes on with the scalar in every reading, so these rows listed nothing before the change
  // and pin that the fix keeps it so (go-yaml reads `x/foo y`, `x/foo a:b`, `x/foo , y`).
  test.each([
    ["a word", "k: &e !bwref x/foo\n  y\n"],
    ["a colon inside a word", "k: &e !bwref x/foo\n  a:b\n"],
    ["a dash inside a word", "k: &e !bwref x/foo\n  -y\n"],
    ["a question mark inside a word", "k: &e !bwref x/foo\n  ?y\n"],
    ["a comma", "k: &e !bwref x/foo\n  , y\n"],
    ["a closing bracket", "k: &e !bwref x/foo\n  ] y\n"],
    ["the anchor on the key line, a word on the next line", "k: &e\n  !bwref x/foo\n  y\n"],
    ["a sequence entry, a word", "- &e !bwref x/foo\n  y\n"],
  ])("an anchor before the tag, then a plain line, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // A dash followed by a tab is refused by go-yaml ("found character that cannot start any token"). The
  // dash patterns stay space-only, so the scanner reads these as before and lists the name of a refused
  // document, which the server does not list. The rows pin that the change leaves them as they were.
  test.each([
    ["a dash and a tab", "-\t!bwref x/foo\n  - y\n"],
    ["a dash, a space and a tab", "- \t!bwref x/foo\n  - y\n"],
    ["a dash and a tab, the tag on its own line", "k:\n  -\t\n    !bwref x/foo\n    - y\n"],
  ])("%s: go-yaml refuses the document, and the reading of the scanner is unchanged", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // A compact parent (`- k:`, `- - `) holds the scalar at the column of its key or indicator, not at the
  // column of its line. A tag that is itself an explicit key lists no name, whatever follows it
  // (ginsys/bronzeward#637), so its rows are not here. go-yaml reads a `- ` or `? ` line that is more indented
  // than that column as more of the scalar (`x/foo - y`, `x/foo ? y`), so the scanner lists no name
  // (ginsys/bronzeward#706). Before the change that line ended the scalar and the name listed. The rows are
  // the documents of probe w706-probe1 where `references()` listed `x/foo` on 63f37307 and go-yaml reads a
  // longer scalar, 10 of the 19 (the 9 on an explicit key list nothing since ginsys/bronzeward#637). The 10
  // documents that go-yaml refuses stay out: a dash line at the column of the key under `- k:` (with or
  // without an anchor, also nested), and a key line more indented than the collection under `- k:`. The
  // refused documents on an explicit key stay out too.
  test.each([
    ["a compact key, then a dash line", "- k: !bwref x/foo\n    - y\n"],
    ["a compact key, then a question mark line", "- k: !bwref x/foo\n    ? y\n"],
    ["a compact key, then a dash line one column past the key", "- k: !bwref x/foo\n   - y\n"],
    ["a compact key, then two dash lines", "- k: !bwref x/foo\n    - y\n    - z\n"],
    ["a compact key and an anchor", "- k: &e !bwref x/foo\n    - y\n"],
    ["a compact key, a tab and an anchor", "- k:\t&e\t!bwref x/foo\n    - y\n"],
    ["a compact key under a key", "a:\n  - k: !bwref x/foo\n      - y\n"],
    ["a compact quoted key", '- "k": !bwref x/foo\n    - y\n'],
    ["a compact sequence in a sequence", "- - !bwref x/foo\n    - y\n"],
    ["a compact sequence in a sequence, with an anchor", "- - &e !bwref x/foo\n    - y\n"],
  ])("a more indented line that goes on with the scalar under a compact parent lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The same parents where the next line belongs to the collection or ends the scalar: go-yaml reads
  // `x/foo`. These rows listed the name before the change and pin that the fix does not take it away.
  test.each([
    ["a compact key, then the next entry", "- k: !bwref x/foo\n- y\n"],
    ["a compact key, then a question mark line at the column of the key", "- k: !bwref x/foo\n  ? y\n"],
    ["a compact key under a key, then a dash line at the column of the entry", "a:\n  - k: !bwref x/foo\n  - y\n"],
    ["a compact sequence in a sequence, then a dash line at the column of the inner sequence", "- - !bwref x/foo\n  - y\n"],
  ])("a compact parent, then a line that ends the scalar, lists the name (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // A plain line goes on with the scalar in every reading, so these rows listed nothing before the change.
  // They pass on 63f37307 too and discriminate nothing there: they pin that the fix keeps it so.
  test.each([
    ["a compact key, then a word", "- k: !bwref x/foo\n    plain\n"],
    ["a compact key, then a word and a sibling key", "- k: !bwref x/foo\n    plain\n  z: 1\n"],
    ["a compact sequence in a sequence, then a word", "- - !bwref x/foo\n    plain\n"],
  ])("a compact parent, then a plain line, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The tag starts its own line under a compact parent, so the parent is the line above (probe w706-probe2,
  // 25 documents at 63f37307, 3 refused by go-yaml, the rest are these rows, except the 3 on an explicit key,
  // which list nothing since ginsys/bronzeward#637). The column of the compact collection counts as for a tag on
  // the line of the key: `- k:` holds it at the column of `k`, `- -` at the column of the inner dash.
  // go-yaml reads `x/foo - y` or `x/foo ? y` for each of these rows, and the name listed before the change.
  test.each([
    ["a compact key, a deeper dash line", "- k:\n    !bwref x/foo\n      - y\n"],
    ["a compact key, a dash line at the column of the tag", "- k:\n    !bwref x/foo\n    - y\n"],
    ["a compact key, a deeper question mark line", "- k:\n    !bwref x/foo\n      ? y\n"],
    ["an anchor on the parent line, a deeper dash line", "- k: &e\n    !bwref x/foo\n      - y\n"],
    ["an anchor on the parent line, a dash line at the column of the tag", "- k: &e\n    !bwref x/foo\n    - y\n"],
    ["an anchor before the tag, a deeper dash line", "- k:\n    &e !bwref x/foo\n      - y\n"],
    ["a tab after the colon, a deeper dash line", "- k:\t\n    !bwref x/foo\n      - y\n"],
    ["a compact dash, a deeper dash line", "- -\n    !bwref x/foo\n      - y\n"],
    ["a compact dash, a dash line at the column of the tag", "- -\n    !bwref x/foo\n    - y\n"],
    ["a nested compact key, a deeper dash line", "a:\n  - k:\n      !bwref x/foo\n        - y\n"],
    ["a nested compact key, a dash line at the column of the tag", "a:\n  - k:\n      !bwref x/foo\n      - y\n"],
    ["a quoted compact key, a deeper dash line", '- "k":\n    !bwref x/foo\n      - y\n'],
  ])("a tag on its own line, then a more indented line that goes on with the scalar, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The same shapes where the next line belongs to the collection or is its value: go-yaml reads `x/foo`.
  test.each([
    ["a compact key, a question mark line at the column of the key", "- k:\n    !bwref x/foo\n  ? y\n"],
    ["a compact key, the next entry", "- k:\n    !bwref x/foo\n- y\n"],
    ["a compact key, a sibling key", "- k:\n    !bwref x/foo\n  z: 1\n"],
    ["a compact dash, a dash line at the column of the inner dash", "- -\n    !bwref x/foo\n  - y\n"],
    ["a nested compact key, a dash line at the column of the entry", "a:\n  - k:\n      !bwref x/foo\n  - y\n"],
  ])("a tag on its own line, then a line that ends the scalar, lists the name (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // A plain line goes on, and so does a dash line under a key the scanner knows. These pass on 63f37307
  // too and discriminate nothing there.
  test.each([
    ["a compact key, a deeper word", "- k:\n    !bwref x/foo\n    plain\n"],
    ["a known key, a dash line", "k:\n  !bwref x/foo\n  - y\n"],
  ])("a tag on its own line, then a plain or known line, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // An explicit key that holds a collection (`? - `, `? k:`) is a compact parent too: the tag is the entry or
  // the value of that collection, not an explicit key's own tag, so it lists a name unless the scalar goes on
  // (probe w706-probe3, 15 documents on origin/main 2b260fa4, 3 refused by go-yaml, 7 listed `x/foo` where
  // go-yaml reads a longer scalar, 5 agree). The column is that of the last indicator (`? - ` gives 2) or of the key
  // (`? k:` gives 2). The 3 refused documents stay out: a dash line at column 0 or at the key column, and a
  // question mark line at the entry column.
  test.each([
    ["a sequence in an explicit key, a deeper dash line", "? - !bwref x/foo\n    - y\n"],
    ["a sequence in an explicit key, a deeper question mark line", "? - !bwref x/foo\n    ? y\n"],
    ["a mapping in an explicit key, a deeper dash line", "? k: !bwref x/foo\n    - y\n"],
    ["two sequences in an explicit key, a deeper dash line", "? - - !bwref x/foo\n      - y\n"],
    ["a sequence in an explicit key in a sequence entry, a deeper dash line", "- ? - !bwref x/foo\n      - y\n"],
    ["a sequence in an explicit key, the tag on its own line, a deeper dash line", "? -\n    !bwref x/foo\n      - y\n"],
    ["a sequence in an explicit key, an anchor, a deeper dash line", "? - &e !bwref x/foo\n    - y\n"],
  ])("an explicit key that holds a collection, then a more indented line that goes on with the scalar, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The same shapes where the next line belongs to the collection or is the value of the key: go-yaml reads
  // `x/foo`. These rows list the name on origin/main too and pin that the fix keeps it.
  test.each([
    ["a sequence in an explicit key, a dash line at the entry column", "? - !bwref x/foo\n  - y\n"],
    ["a sequence in an explicit key, its value", "? - !bwref x/foo\n: v\n"],
    ["a mapping in an explicit key, its value", "? k: !bwref x/foo\n: v\n"],
    ["two sequences in an explicit key, a dash line at the inner entry column", "? - - !bwref x/foo\n    - y\n"],
  ])("an explicit key that holds a collection, then a line that ends the scalar, lists the name (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // A plain line goes on: this row lists nothing on origin/main too and discriminates nothing there.
  test("an explicit key that holds a sequence, then a deeper word, lists nothing", () => {
    expect(references("? - !bwref x/foo\n    plain\n")).toEqual([]);
  });

  // The parent line of a compact parent can end with an anchor, or with a carriage return after the indicators
  // (`- - &e`, `? - &e`, `- - ` in CRLF), and the tag starts the next line. Only properties follow the last
  // indicator then, so the compact collection is at that indicator, as for `- - !bwref x/foo` (review round 2 of
  // ginsys/bronzeward#699, probe w699-probe-e1: 16 documents at e1d0d1f3, 7 listed `x/foo` where go-yaml reads
  // `x/foo - y` or `x/foo ? y`, 9 agree, none lost a name). go-yaml refuses a document whose tag is the entry at
  // column 2, with the next dash line at column 2 or 0. The cause is the indentation of the tag and not the column
  // of the dash line, so no row of that shape is here.
  test.each([
    ["two dashes, an anchor at the end of the line, a deeper dash line", "- - &e\n    !bwref x/foo\n    - y\n"],
    ["an explicit key and a dash, an anchor at the end of the line, a deeper dash line", "? - &e\n    !bwref x/foo\n    - y\n"],
    ["two dashes, a carriage return after the blank, a deeper dash line", "- - \r\n    !bwref x/foo\r\n    - y\r\n"],
    ["two dashes, an anchor at the end of the line, CRLF, a deeper dash line", "- - &e\r\n    !bwref x/foo\r\n    - y\r\n"],
    ["two dashes, an anchor at the end of the line, a deeper question mark line", "- - &e\n    !bwref x/foo\n    ? y\n"],
    ["a nested sequence in a sequence, an anchor at the end of the line, a deeper dash line", "a:\n  - - &e\n      !bwref x/foo\n      - y\n"],
    ["an explicit key and a dash, an anchor at the end of the line, CRLF", "? - &e\r\n    !bwref x/foo\r\n    - y\r\n"],
  ])("a compact parent whose line ends with an anchor or a carriage return, then a more indented line that goes on with the scalar, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The same shapes where the next line belongs to the collection: go-yaml reads `x/foo`. These rows list the
  // name on e1d0d1f3 too and pin that the fix keeps it.
  test.each([
    ["two dashes, a dash line at the column of the collection", "- - &e\n    !bwref x/foo\n  - y\n"],
    ["two dashes, a dash line at column 0", "- - &e\n    !bwref x/foo\n- y\n"],
    ["two dashes, CRLF, a dash line at the column of the collection", "- - &e\r\n    !bwref x/foo\r\n  - y\r\n"],
    ["an explicit key and a dash, a dash line at the entry column", "? - &e\n    !bwref x/foo\n  - y\n"],
    ["two dashes, a carriage return after the blank, a dash line at the column of the collection", "- - \r\n    !bwref x/foo\r\n  - y\r\n"],
  ])("a compact parent whose line ends with an anchor or a carriage return, then a line that ends the scalar, lists the name (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // These two shapes list nothing on e1d0d1f3 already, as go-yaml does (probe w699-probe-e1, rows R6 and R9), so
  // they pass on both sides and discriminate nothing there. They pin that the fix keeps that.
  test.each([
    ["two dashes, a tab after the anchor at the end of the line", "- - &e\t\n    !bwref x/foo\n    - y\n"],
    ["a dash and an explicit key, an anchor at the end of the line", "- ? &e\n    !bwref x/foo\n    - y\n"],
  ])("a compact parent whose line ends with a tab after an anchor or with an explicit key entry, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // A key line after a plain value ends the scalar only at the column of the holder of the value or left of it
  // (ginsys/bronzeward#699, section 3, was ginsys/bronzeward#705). The holder is the key, at the column after
  // the dash for `- k:`, or the dash itself for a lone `- `. A key line more indented than that is text of the
  // scalar to go-yaml, which then refuses the colon ("mapping values are not allowed in this context"), so the
  // document lists nothing. Probe w699s3-probe-p1: 53 documents at f52d5579 against go-yaml v3.0.5, 28 agree,
  // 25 listed a name where go-yaml refuses. 23 are the rows below: 21 with an anchor, 2 without one in a compact
  // parent. The other 2 are the limits at the end of this group.
  test.each([
    ["a key, a key line two columns deeper", "a:\n  k: !bwref &e x/foo\n    j: v\n"],
    ["a key, a tab after the colon, a key line two columns deeper", "a:\n  k:\t!bwref &e x/foo\n    j: v\n"],
    ["a key, a key line one column deeper", "a:\n  k: !bwref &e x/foo\n   j: v\n"],
    ["a key, a deeper key line with a colon at its end", "a:\n  k: !bwref &e x/foo\n    j:\n"],
    ["a key, a deeper key line with a quoted key", "a:\n  k: !bwref &e x/foo\n    \"j\": v\n"],
    ["a key, a deeper key line with an anchor on the key", "a:\n  k: !bwref &e x/foo\n    &a j: v\n"],
    ["a key, a deeper line that is a value indicator", "a:\n  k: !bwref &e x/foo\n    : v\n"],
    ["a key, a comment line, a deeper key line", "a:\n  k: !bwref &e x/foo\n    # c\n    j: v\n"],
    ["a key, a blank line, a deeper key line", "a:\n  k: !bwref &e x/foo\n\n    j: v\n"],
    ["a key, CRLF, a deeper key line", "a:\r\n  k: !bwref &e x/foo\r\n    j: v\r\n"],
    ["a key, a deeper key line with a tab after the spaces", "a:\n  k: !bwref &e x/foo\n    \tj: v\n"],
    ["a quoted key, a deeper key line", "a:\n  \"k\": !bwref &e x/foo\n    j: v\n"],
    ["a key at column 0, a key line two columns deeper", "k: !bwref &e x/foo\n  j: v\n"],
    ["a dash and a key, a key line two columns deeper than the key","- k: !bwref &e x/foo\n    j: v\n"],
    ["a dash and a key, a key line one column deeper than the key", "- k: !bwref &e x/foo\n   j: v\n"],
    ["a nested dash and a key, a key line two columns deeper than the key", "a:\n  - k: !bwref &e x/foo\n      j: v\n"],
    ["a nested dash and a key, a key line one column deeper than the key", "a:\n  - k: !bwref &e x/foo\n     j: v\n"],
    ["a dash at the column of its key, a key line two columns deeper than the key", "a:\n- k: !bwref &e x/foo\n    j: v\n"],
    ["a lone dash, a key line two columns deeper", "a:\n  - !bwref &e x/foo\n    j: v\n"],
    ["a lone dash, a key line one column deeper", "a:\n  - !bwref &e x/foo\n   j: v\n"],
    ["a lone dash at column 0, a key line two columns deeper", "- !bwref &e x/foo\n  j: v\n"],
  ])("an anchored plain value, then a key line more indented than its holder, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // The same rule without an anchor, in a compact parent: `continues` read the key line as the end of the scalar
  // whatever its column. A key or a lone dash as the parent already lists nothing there.
  test.each([
    ["a dash and a key, a key line two columns deeper", "- k: !bwref x/foo\n    j: v\n"],
    ["a nested dash and a key, a key line two columns deeper than the key", "a:\n  - k: !bwref x/foo\n      j: v\n"],
  ])("a plain value with no anchor in a compact parent, then a key line more indented than its holder, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // A key line at the holder's column or left of it ends the scalar: go-yaml reads `x/foo` and accepts these. They
  // list the name on f52d5579 too and pin that the fix keeps it.
  test.each([
    ["a key, a sibling key line", "a:\n  k: !bwref &e x/foo\n  j: v\n"],
    ["a key, a key line at column 0", "a:\n  k: !bwref &e x/foo\nj: v\n"],
    ["a key at column 0, a sibling key line", "k: !bwref &e x/foo\nj: v\n"],
    ["a quoted key, a sibling key line", "a:\n  \"k\": !bwref &e x/foo\n  j: v\n"],
    ["a dash and a key, a key line at the column of the key", "- k: !bwref &e x/foo\n  j: v\n"],
    ["a dash and a key, the next entry", "- k: !bwref &e x/foo\n- j: v\n"],
    ["a nested dash and a key, a key line at the column of the key", "a:\n  - k: !bwref &e x/foo\n    j: v\n"],
    ["a nested dash and a key, the next entry", "a:\n  - k: !bwref &e x/foo\n  - j: v\n"],
    ["a dash at the column of its key, a key line at the column of the key", "a:\n- k: !bwref &e x/foo\n  j: v\n"],
    ["a lone dash, the next entry", "a:\n  - !bwref &e x/foo\n  - j: v\n"],
    ["a lone dash at column 0, the next entry", "- !bwref &e x/foo\n- j: v\n"],
    ["a lone dash at the column of its key, a key line of the enclosing mapping", "a:\n- !bwref &e x/foo\nj: v\n"],
    ["no anchor, a key, a sibling key line", "a:\n  k: !bwref x/foo\n  j: v\n"],
    ["no anchor, a dash and a key, a key line at the column of the key", "- k: !bwref x/foo\n  j: v\n"],
    ["no anchor, a nested dash and a key, a key line at the column of the key", "a:\n  - k: !bwref x/foo\n    j: v\n"],
    ["a flow mapping, the next line starts with a comma", "a: {k: !bwref &e x/foo\n  , j: v}\n"],
    ["a flow sequence, the next line starts with a comma", "a: [!bwref &e x/foo\n  , j]\n"],
  ])("a plain value, then a key line at or left of its holder, lists the name (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // These list nothing on f52d5579 already, as go-yaml reads them (the value goes on over several lines, or the
  // document is refused), so they pass on both sides and discriminate nothing there. They pin that the fix keeps it.
  test.each([
    ["a deeper dash line", "a:\n  k: !bwref &e x/foo\n    - j\n"],
    ["a deeper word", "a:\n  k: !bwref &e x/foo\n    word\n"],
    ["a deeper word with a colon that no blank follows", "a:\n  k: !bwref &e x/foo\n    http://x\n"],
    ["a deeper key line with no blank after its colon", "a:\n  k: !bwref &e x/foo\n    j:v\n"],
    ["a deeper explicit key line", "a:\n  k: !bwref &e x/foo\n    ? j\n    : v\n"],
    ["no anchor, a key, a deeper key line", "a:\n  k: !bwref x/foo\n    j: v\n"],
    ["no anchor, a lone dash, a deeper key line", "a:\n  - !bwref x/foo\n    j: v\n"],
    ["no anchor, a lone dash at column 0, a deeper key line", "- !bwref x/foo\n  j: v\n"],
    ["a flow mapping, a key line on the next line", "a: {k: !bwref &e x/foo\n    j: v}\n"],
    ["a flow sequence, a key line on the next line", "a: [!bwref &e x/foo\n  j: v]\n"],
    ["a flow sequence with a pair, a key line on the next line", "a: [k: !bwref &e x/foo\n  j: v]\n"],
    ["a flow mapping after a dash, a key line on the next line", "- {k: !bwref &e x/foo\n    j: v}\n"],
  ])("a plain value that goes on over a deeper line, or a refused document, lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  // Two limits of the scalar scan that the key line rule does not touch. go-yaml refuses both documents and the
  // scan lists the name (probe w699s3-probe-p1, rows D-same2 and F-map-valueind). In the first, the key line stands at
  // the column of the dash, where go-yaml wants another dash. A line to the left of the dash may belong to an
  // ancestor, so the scan cannot tell the two apart from the line alone. In the second, the scalar ends where
  // go-yaml ends it, at the value indicator, and the refusal comes after it. These rows pin the reading as a
  // limit, not as the right answer.
  test.each([
    ["a key line at the column of the dash", "a:\n  - !bwref &e x/foo\n  j: v\n"],
    ["a value indicator line in a flow mapping", "a: {k: !bwref &e x/foo\n  : v}\n"],
  ])("a document that go-yaml refuses for a reason outside the key line rule still lists the name (known limit: %s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // The editor reads a draft while the user types, so the cost of the scan must stay near linear
  // (review of this change). A check that takes too long on the earlier code ends on its own and fails
  // the check, and does not hang: a test timeout cannot stop a synchronous scan.
  //
  // The comment of a check gives the time of the earlier code where it was measured, with the commit. A
  // check with no such figure is not shown to fail first.
  describe("cost", () => {
    // The check does not compare a time with a fixed bound, because the speed of a CI runner varies by
    // a factor of nine (ginsys/bronzeward#703: 160 ms locally, 1,468 ms on one runner), more than any
    // bound that still fails a slow scan. It builds the same draft from a size argument about 64 times
    // smaller (rounded, and at least 1) and from the full size argument, reads both in the same process,
    // and compares the time per character of the two drafts. Where the length of a draft is in proportion to its size argument, a
    // linear scan costs the same per character at both sizes (a ratio near 1), and a quadratic one costs
    // 64 times as much (a ratio of 40 to 64 where it was measured, 51 on e354c61c). The limit is 12. A
    // runner that is slow by a constant factor changes both times and not the ratio. Two builders keep
    // no such proportion: the rising indentation has a length that grows with the square of its size
    // argument, and the line of 25 bangs is a case of exponential cost. For them the check asks only
    // that the time per character grows by less than 12 times (a ratio of 12 or more fails).
    //
    // The gap and the limit come from runs of this file at gaps of 4, 16 and 64 (20, 20 and 45 runs).
    // At a gap of 4 the limit was 2.5, and one run of the 20,000-tag flow line read 2.87, because that
    // scan had a small term that grew with the square of the line (repaired in ginsys/bronzeward#719, see
    // the check of 320,000 tags below). At a gap of 64 the
    // highest ratio of any check was 3.15 (that flow line), and the next highest was 1.49. So the
    // limit sits about 4 times above the highest linear ratio and 3 to 5 times below a quadratic one.
    //
    // Three things keep the ratio steady on a noisy runner. The small draft is read first, three times,
    // and its best time counts, which also warms the code. A slow reading of the full draft is read
    // again, up to three times in all, and its best time counts. A full draft that takes less than
    // `floorMs` (100 ms) passes without a ratio, because the timer cannot tell a ratio at that speed
    // from noise, and a quadratic scan does not read these drafts that fast: it takes seconds on each.
    //
    // KNOWN LIMIT: a scan that is slower by a constant factor, 10 times for example, is not found. Its
    // time differs by only 1.1 times from that of a runner that is 9 times slower, which is less than
    // the difference between two runners, so a check of the speed of one run cannot reliably tell the
    // two apart. The cost that
    // grows with the size of the draft is what this describes and finds. A small term that
    // grows with the square of a line is not found either, as long as the ratio stays under 12.
    const GROWTH_LIMIT = 12;
    const GROWTH_GAP = 64;
    const growth = (
      scan: (text: string) => unknown,
      size: number,
      build: (size: number) => string,
      floorMs = 100,
      now: () => number = () => performance.now(),
    ) => {
      const timed = (text: string) => {
        const start = now();
        const found = scan(text);
        return { found, ms: now() - start };
      };
      const small = build(Math.max(1, Math.round(size / GROWTH_GAP)));
      const big = build(size);
      let smallMs = Infinity;
      for (let i = 0; i < 3; i++) smallMs = Math.min(smallMs, timed(small).ms);
      const first = timed(big);
      let bigMs = first.ms;
      const ratio = (ms: number) => ms / big.length / (smallMs / small.length);
      for (let again = 1; again < 3 && bigMs >= floorMs && ratio(bigMs) >= GROWTH_LIMIT; again++) {
        bigMs = Math.min(bigMs, timed(big).ms);
      }
      if (bigMs >= floorMs) {
        expect(
          ratio(bigMs),
          `${bigMs.toFixed(1)} ms for ${big.length} characters against ${smallMs.toFixed(1)} ms for ${small.length}`,
        ).toBeLessThan(GROWTH_LIMIT);
      }
      return first.found;
    };
    const bounded = (size: number, build: (size: number) => string) => growth(references, size, build);
    // Builders of a draft at a given size: `rep` repeats a text n times and `lines` joins n pieces of
    // text, each of which can hold more than one line.
    const rep = (text: string, n: number) => text.repeat(Math.max(1, Math.round(n)));
    const lines = (n: number, line: (i: number) => string) =>
      Array.from({ length: Math.max(1, Math.round(n)) }, (_, i) => line(i)).join("");

    // The check against stand-ins for the scan. A stand-in does not spend time: it adds the work it would
    // do to a counter, and the counter is the clock of the check. So the result does not depend on the
    // speed or the load of the machine that runs the test. The floor is 0 so that the ratio is always
    // checked. A scan that reads the draft once for each character is quadratic and fails the check. A
    // linear scan does not, even when it does 10 times the work of another linear scan.
    const standIn = (work: (length: number) => number) => {
      let ticks = 0;
      return {
        now: () => ticks,
        scan: (text: string) => {
          ticks += work(text.length);
        },
      };
    };
    test("the check fails a quadratic stand-in for the scan", () => {
      const quadratic = standIn((length) => length * length);
      expect(() => growth(quadratic.scan, 20000, (n) => "x".repeat(n), 0, quadratic.now)).toThrow(
        /ms for 20000 characters/,
      );
    });
    // The small draft of this stand-in takes 0.0002 ticks, far under any floor that a timer needs: a
    // time that small is still divided as it is, and does not hide the growth.
    test("the check fails a quadratic stand-in whose small draft takes almost no time", () => {
      const tiny = standIn((length) => (length / 20000) ** 2);
      expect(() => growth(tiny.scan, 20000, (n) => "x".repeat(n), 0, tiny.now)).toThrow(/ms for 20000 characters/);
    });
    test("the check passes a linear stand-in, also one that is 10 times slower", () => {
      const linear = standIn((length) => length);
      const slow = standIn((length) => 10 * length);
      expect(() => growth(linear.scan, 20000, (n) => "x".repeat(n), 0, linear.now)).not.toThrow();
      expect(() => growth(slow.scan, 20000, (n) => "x".repeat(n), 0, slow.now)).not.toThrow();
    });

    // A draft of 30,000 entries with a tag alone on its line: the scan back from the tag stops at the key
    // line above it, so it must not read the text before that line. This draft took 27,987 ms on e354c61c.
    test("a draft with 30,000 tags on their own lines lists every one", () => {
      expect(bounded(30000, (n) => lines(n, (i) => `k${i}:\n  !bwref x/foo${i}\n`))).toHaveLength(30000);
    });

    // One flow line of 320,000 tags: each plain scalar asks whether the next line continues it, and that
    // must read only the blanks after the tag, not the rest of the line up to the next line break. The
    // time per character grew with the length of the line (ginsys/bronzeward#719): this draft took
    // 24,752 ms on e735dc3a, 27.9 times the time per character of a line of 5,000 tags. Half the size
    // (160,000 tags) read 14 against the limit of 12, too close to fail every run. The line of 20,000
    // tags that read 2.87 at a gap of 4 had the same cause.
    test("a flow line of 320,000 tags lists every one", () => {
      expect(bounded(320000, (n) => `[${lines(n, (i) => `${i ? ", " : ""}!bwref x/foo${i}`)}]\n`)).toHaveLength(
        320000,
      );
    });

    // The same line with an anchor on every tag: `anchoredContinues` had the same search of the rest of the line
    // for each tag, and a search back to the start of the line (ginsys/bronzeward#719). On the code before the
    // repair this draft took 13,421 ms, 23.5 times the time per character of a line of 625 tags. It costs more per
    // tag than the plain line, so 40,000 tags fail as clearly as 320,000 plain ones.
    test("a flow line of 40,000 anchored tags lists every one", () => {
      expect(bounded(40000, (n) => `[${lines(n, (i) => `${i ? ", " : ""}!bwref &a${i} x/foo${i}`)}]\n`)).toHaveLength(
        40000,
      );
    });

    // A line of many bangs and a word: a tag on a line of its own below it is not on a root scalar, and
    // the line of bangs is no line of properties only. Matching the line must not try every split of it.
    // This line of 25 bangs took 1,177 ms on e354c61c.
    test("a long line of bangs and a word above a tag ends the scan at once", () => {
      bounded(25, (n) => `${rep("!", n)} text:\n!bwref x/foo\n- y\n`);
    });

    // The scan back from a tag that starts its line looks for the nearest line above that is less indented.
    // It must not read every line above when none is, or when the lines between are all as indented as the
    // tag. The 3,000 tags at column 0 took 4,329 ms and the 2,000 tags among sibling keys took 1,328 ms on
    // 79806121.

    // A tag at column 0 that is no root scalar: no line is less indented than column 0.
    test("3,000 tags at column 0 under a key are scanned in near linear time", () => {
      bounded(3000, (n) => lines(n, (i) => `k:\n!bwref x/foo${i}\nbar\n`));
    });

    // Tags on lines of their own under one key, each below a sibling key at the same indent: the line
    // that holds the key is far above every tag but the first.
    test("2,000 tags among sibling keys under one key are scanned in near linear time", () => {
      bounded(2000, (n) => `k:\n${lines(n, (i) => `  j${i}: v\n  !bwref x/foo${i}\n`)}`);
    });

    // Many tags under one long key: the line of the key is the parent of each of them, and it is matched once
    // (`known` of the line), not once per tag. The key has 300,000 characters and the 4,000 tags took 2.1 to
    // 2.8 s in six runs on 86c93ac4, where each tag matched it again (a key of 200,000 characters and 3,000
    // tags took 0.9 to 1.6 s there, too close to the old bound of 1,000 ms to fail it every time).
    test("4,000 tags among sibling keys under one key of 300,000 characters are scanned in near linear time", () => {
      bounded(4000, (n) => `${rep("a", 75 * n)}:\n${lines(n, (i) => `  j${i}: v\n  !bwref x/foo${i}\n`)}`);
    });

    // A key line after a plain value is measured against the holder column (ginsys/bronzeward#699, section 3):
    // `anchoredHolder` walks the spaces and dashes before the tag, and `compactColumn` the same for a plain value.
    // No figure from earlier code: these rows pass on f52d5579 too, where only their timing check runs, and are
    // not shown to fail there. They pin the bound for the new reading.
    test.each([
      ["an indentation of 100,000 spaces before an anchored value", 100000, (n: number) => `${rep(" ", n)}k: !bwref &e x/foo\n${rep(" ", n + 2)}j: v\n`],
      ["4,000 anchored values, each followed by a deeper key line", 4000, (n: number) => lines(n, (i) => `k${i}: !bwref &e x/foo${i}\n  j: v\n`)],
      ["4,000 plain values in a compact parent, each followed by a deeper key line", 4000, (n: number) => lines(n, (i) => `- k${i}: !bwref x/foo${i}\n    j: v\n`)],
      ["a key of 300,000 characters before an anchored value in a compact parent", 300000, (n: number) => `- ${rep("a", n)}: !bwref &e x/foo\n    j: v\n`],
    ])("a deeper key line after a plain value is scanned in near linear time (%s)", (_, size, build) => {
      bounded(size, build);
    });

    // A continuation line with a long run of blanks between two words: trimming it must not try every
    // start of the run. Three paths trim a continuation line: a plain tag, a tag after an anchor (both
    // `continues`) and an anchor after the tag (`anchoredContinues`, whose old pattern was on main).
    // With the run of 60,000 blanks inside the line, the three paths took 3,631 ms, 3,224 ms and 3,288 ms
    // on 79806121, in that order. On 86c93ac4 the anchored path already calls `trimBlanks()`, so its check
    // fails there only with the old pattern put back in `anchoredContinues()` (2,999 ms in one run).
    const trimHeads = [
      ["a plain tag", "k: !bwref a\n"],
      ["a tag after an anchor", "k: &e !bwref a\n"],
      ["an anchor after the tag", "k: !bwref &e a\n"],
    ];
    test.each(trimHeads)("a continuation line with 60,000 blanks inside it is trimmed in linear time (%s)", (_, head) => {
      bounded(60000, (n) => `${head}  bar${rep(" ", n)}baz\n`);
    });

    // The same walks with the long run at the start of the line, and at its end.
    test.each(trimHeads)("a continuation line with 60,000 blanks at its start is trimmed in linear time (%s)", (_, head) => {
      bounded(60000, (n) => `${head}${rep(" ", n)}bar\n`);
    });
    test.each(trimHeads)("a continuation line with 60,000 blanks at its end is trimmed in linear time (%s)", (_, head) => {
      bounded(60000, (n) => `${head}  bar${rep(" ", n)}\n`);
    });

    // The search for the parent line of a tag runs a binary search over the stack of open lines, and the stack
    // is as deep as the indentation rises: 1,000 keys, each one column deeper than the one above. The draft
    // holds about n squared characters of indentation, so the time per character is what the check compares.
    test("a tag below 1,000 keys of rising indentation is scanned in near linear time", () => {
      bounded(1000, (n) => {
        const keys = lines(n, (i) => `${" ".repeat(i)}k${i}:\n`);
        return `${keys}${rep(" ", n)}!bwref x/foo\n${rep(" ", n)}bar\n`;
      });
    });

    // A key of 100,000 characters before the tag on its own line (`PLAIN_PARENT`): the pattern must not try
    // every split of a long key. The first two match (the second one has one anchor after the colon,
    // ginsys/bronzeward#699). The third one has two anchors after the colon, which go-yaml refuses, so the
    // pattern reads the whole key and rejects it.
    test.each([
      ["a match", (n: number) => `${rep("a", n)}: !bwref x/foo\n  bar\n`],
      ["a match with an anchor", (n: number) => `${rep("a", n)}: &e !bwref x/foo\n  bar\n`],
      ["a rejection", (n: number) => `${rep("a", n)}: &e &f !bwref x/foo\n  bar\n`],
    ])("a key of 100,000 characters before the tag is matched in linear time (%s)", (_, build) => {
      bounded(100000, build);
    });

    // A tab after the colon (ginsys/bronzeward#698): the three parent patterns take `[ \t]+` there. Runs of tabs
    // and a long key must not try every split. These rows pass on 4200c858 too, in 0.5 to 8.5 ms in the probe
    // (node, one run each): they discriminate nothing there, and pin the growth for the new pattern.
    test.each([
      ["a key of 100,000 characters, a match", 100000, (n: number) => `${rep("a", n)}:\t!bwref x/foo\n  bar\n`],
      ["a key of 100,000 characters, a rejection", 100000, (n: number) => `${rep("a", n)}:\t&e !bwref x/foo\n  bar\n`],
      ["100,000 tabs after the colon", 100000, (n: number) => `k:${rep("\t", n)}!bwref x/foo\n  - y\n`],
      ["100,000 tabs after the colon, an anchor after the tag", 100000, (n: number) => `k:${rep("\t", n)}!bwref &e x/foo\n  j: v\n`],
      ["50,000 pairs of a space and a tab after the colon", 50000, (n: number) => `k:${rep(" \t", n)}!bwref x/foo\n  - y\n`],
      ["100,000 tabs inside a plain key", 100000, (n: number) => `k${rep("\t", n)}j:\t!bwref x/foo\n  - y\n`],
      ["100,000 tabs after the key, the tag on its own line", 100000, (n: number) => `k:${rep("\t", n)}\n  !bwref x/foo\n  - y\n`],
      ["2,000 keys with a tab, the tag on its own line", 2000, (n: number) => lines(n, (i) => `k${i}:\t\n  !bwref x/foo${i}\n  - y\n`)],
    ])("a tab after the colon is matched in linear time (%s)", (_, size, build) => {
      bounded(size, build);
    });

    // An anchor before the tag (ginsys/bronzeward#699): the two parent patterns take one anchor and its
    // blanks after the colon or the dash. Long keys, long runs of blanks and many anchors must not try
    // every split. These rows pass on 0b3a42da too (they discriminate nothing there) and pin the bound of
    // the new patterns.
    test.each([
      ["a key of 100,000 characters, the anchor on the key line", 100000, (n: number) => `${rep("a", n)}: &e\n  !bwref x/foo\n  - y\n`],
      ["5,000 anchors after the colon", 5000, (n: number) => `k: ${rep("&e ", n)}!bwref x/foo\n  - y\n`],
      ["5,000 anchors after the dash", 5000, (n: number) => `- ${rep("&e ", n)}!bwref x/foo\n  - y\n`],
      ["100,000 tabs between the anchor and the tag", 100000, (n: number) => `k: &e${rep("\t", n)}!bwref x/foo\n  - y\n`],
      ["50,000 pairs of a space and a tab around the anchor", 50000, (n: number) => `k:${rep(" \t", n)}&e${rep(" \t", n)}!bwref x/foo\n  - y\n`],
      ["100,000 blanks after the anchor on the key line", 100000, (n: number) => `k: &e${rep(" ", n)}\n  !bwref x/foo\n  - y\n`],
      ["100,000 tabs after the anchor on the dash line", 100000, (n: number) => `- &e${rep("\t", n)}\n  !bwref x/foo\n  - y\n`],
      ["50,000 pairs of a space and a tab after the anchor on the dash line", 50000, (n: number) => `- &e${rep(" \t", n)}\n  !bwref x/foo\n  - y\n`],
      ["100,000 tabs after an anchor on the dash line, then a character that ends the match", 100000, (n: number) => `- &e${rep("\t", n)}x\n  !bwref x/foo\n  - y\n`],
      ["2,000 key lines with an anchor, the tag on its own line", 2000, (n: number) => lines(n, (i) => `k${i}: &e\n  !bwref x/foo${i}\n  - y\n`)],
    ])("an anchor before the tag is matched in linear time (%s)", (_, size, build) => {
      bounded(size, build);
    });

    // Many tags under one long key line with an anchor: the line is matched once (`known` of the line). The
    // key (75 characters per tag) and the number of tags grow together with the size argument.
    test("4,000 tags among sibling keys under one key line of 300,000 characters with an anchor are scanned in near linear time", () => {
      bounded(4000, (n) => `${rep("a", n * 75)}: &e\n${lines(n, (i) => `  j${i}: v\n  !bwref x/foo${i}\n`)}`);
    });

    // Every other loop and pattern that the scan of a plain tag value runs, on a long input of its kind.
    test.each([
      ["5,000 properties before the tag", 5000, (n: number) => `k:\n  ${rep("!a ", n)}!bwref x/foo\n  bar\n`],
      ["a line of 5,000 properties above the tag", 5000, (n: number) => `k:\n ${rep("!a ", n)}\n  !bwref x/foo\n  bar\n`],
      ["5,000 anchors before the tag", 5000, (n: number) => `k:\n  ${rep("&e ", n)}!bwref x/foo\n  bar\n`],
      ["a line of 5,000 anchors above the tag", 5000, (n: number) => `k:\n ${rep("&e ", n)}\n  !bwref x/foo\n  bar\n`],
      ["a key of 100,000 characters above the tag", 100000, (n: number) => `${rep("a", n)}:\n  !bwref x/foo\n  bar\n`],
      ["3,000 lines of an anchor and a blank line above a root scalar", 3000, (n: number) => `${rep("&e\n\n", n)}!bwref x/foo\n- y\n`],
      ["a marker line with 40,000 blanks before a dash", 40000, (n: number) => `--- ${rep(" ", n)}- !bwref x/foo\n  bar\n`],
    ])("a long input stays linear: %s", (_, size, build) => {
      bounded(size, build);
    });

    // The test for an explicit key reads the text before the tag on its line, once per tag. A flow line
    // that holds many tags is the shape where that cost could grow with the square of the line. It does,
    // by a small term: the time per character rose 1.0 to 5.9 times from 625 to 40,000 tags in a probe
    // (58 ns to 341 ns per character), and this check read 2.2 to 3.1 over 20 runs at 20,000 tags. A
    // limit of 2.5 failed it once in 20 runs, so the limit is 12. The cause is not traced here.
    test("a flow line of 20,000 tags is scanned in near linear time and lists every name", () => {
      const found = bounded(20000, (n) => `[${lines(n, (i) => `${i ? ", " : ""}!bwref x/foo${i}`)}]\n`);
      expect(found).toHaveLength(20000);
    });

    // The same line with a `?` before each tag: every tag is an explicit key's, so none is listed, and
    // the backward read stops at the `?` of its own entry, not at the start of the line.
    test("a flow line of 10,000 explicit keys is scanned in near linear time and lists nothing", () => {
      const found = bounded(10000, (n) => `[${lines(n, (i) => `${i ? ", " : ""}? !bwref x/foo${i}`)}]\n`);
      expect(found).toEqual([]);
    });

    // The walk crosses line breaks, so the cost per tag grows with the lines between a tag and the token
    // before it. Those runs do not overlap either: 10,000 tags, each on a line after an anchor line of
    // its own `? &k`, are read in time linear in the text.
    test("10,000 explicit keys with the anchor and the tag on separate lines list nothing in near linear time", () => {
      const found = bounded(10000, (n) => lines(n, (i) => `? &k${i}\n\n  !bwref x/foo${i}\n: v\n`));
      expect(found).toEqual([]);
    });

    // The column of a compact collection (`compactColumn`, ginsys/bronzeward#706) is read by index from the text
    // before the tag, or from the parent line once for all the tags under it (`compact` of the line). These
    // rows pass on 63f37307 too (they discriminate nothing there) and pin the bound of the new code.
    test.each([
      ["50,000 dash indicators before the tag", 50000, (n: number) => `${rep("- ", n)}!bwref x/foo\n    - y\n`],
      ["100,000 blanks after the dash", 100000, (n: number) => `-${rep(" ", n)}k: !bwref x/foo\n    - y\n`],
      ["100,000 blanks after the dash, a tag with properties", 100000, (n: number) => `-${rep(" ", n)}&e !bwref x/foo\n    - y\n`],
      ["a compact key of 100,000 characters, the tag on its own line", 100000, (n: number) => `- ${rep("a", n)}:\n    !bwref x/foo\n      - y\n`],
    ])("a compact column is read in linear time (%s)", (_, size, build) => {
      bounded(size, build);
    });

    // Many tags under one long compact parent line: the line is read once (`compact` of the line). The key
    // (75 characters per tag) and the number of tags grow together with the size argument.
    test("4,000 tags among sibling keys under one compact key line of 300,000 characters are scanned in near linear time", () => {
      bounded(4000, (n) => `- ${rep("a", n * 75)}:\n${lines(n, (i) => `    j${i}: v\n    !bwref x/foo${i}\n      - y\n`)}`);
    });
  });
});

// ginsys/bronzeward#637: the server refuses a `!bwref` tag on a key (tag-placement,
// internal/ingest/declare.go), so the editor lists nothing for a tag on an explicit key or an alias key.
describe("a tag on an explicit key or an alias key", () => {
  test.each([
    ["an explicit key", "? !bwref x/foo\n: v\n"],
    ["an explicit key under a mapping", "a:\n  ? !bwref x/foo\n  : v\n"],
    ["an explicit key with an anchor", "? &e !bwref x/foo\n: v\n"],
    ["an alias key of a tagged explicit key", "? &k !bwref x/foo\n: v\n*k : w\n"],
    ["an explicit key after a sequence dash", "- ? !bwref x/foo\n  : v\n"],
    ["an explicit key after a sequence entry", "a:\n  - ? !bwref x/foo\n"],
    ["an explicit key with the tag on the next line", "?\n  !bwref x/foo\n: v\n"],
    ["an explicit key after other entries", "a: b\n? !bwref x/foo\n: v\n"],
    ["an explicit key with a quoted name", '? !bwref "x/foo"\n: v\n'],
    ["an explicit key in a flow mapping", "{? !bwref x/foo : v}\n"],
    ["an explicit key in a flow sequence", "[? !bwref x/foo]\n"],
    ["an explicit key after a flow entry", "a: {b: c, ? !bwref x/foo : v}\n"],
    // go-yaml drops a byte order mark at the start of the stream, so the `?` is at the line start. A second
    // mark is text, and go-yaml refuses the stream (did not find expected key). The oracle reads the first
    // two as unfit and the last two as refused.
    ["an explicit key after a byte order mark", "\ufeff? !bwref x/foo\n: v\n"],
    ["an explicit key with the tag on the next line, after a byte order mark", "\ufeff?\n  !bwref x/foo\n: v\n"],
    ["an explicit key after two byte order marks (go-yaml refuses)", "\ufeff\ufeff? !bwref x/foo\n: v\n"],
    ["an explicit key with the tag on the next line, after two byte order marks (go-yaml refuses)", "\ufeff\ufeff?\n  !bwref x/foo\n: v\n"],
    // The tag can follow the `?` across line breaks, as CRLF or LF, blank lines and anchors. In a block
    // the tag line is more indented than the `?` line. In a flow collection go-yaml needs no
    // indentation. The oracle reads each of these as unfit.
    ["an explicit key with CRLF line breaks", "?\r\n  !bwref x/foo\r\n: v\r\n"],
    ["an explicit key after a sequence dash with CRLF line breaks", "- ?\r\n    !bwref x/foo\r\n  : v\r\n"],
    ["an explicit key with an anchor before the line break", "? &k\n  !bwref x/foo\n: v\n*k : w\n"],
    ["an explicit key with an anchor after the line break", "?\n  &k !bwref x/foo\n: v\n"],
    ["an explicit key with an anchor before a line break after a dash", "- ? &k\n    !bwref x/foo\n  : v\n"],
    ["an explicit key with an anchor and blank lines before the tag", "? &k\n\n\n  !bwref x/foo\n: v\n"],
    ["an explicit key with blank lines before the tag", "?\n\n\n  !bwref x/foo\n: v\n"],
    ["an explicit key with a comment before the tag", "? # c\n  !bwref x/foo\n: v\n"],
    ["a flow sequence key with the tag on the next line", "[?\n !bwref x/foo]\n"],
    ["a flow sequence key with the tag on the next line, not indented", "[?\n!bwref x/foo]\n"],
    ["the first flow mapping key with the tag on the next line", "{?\n !bwref x/foo\n: v}\n"],
    ["a flow mapping key after an entry with the tag on the next line", "{a: b, ?\n !bwref x/foo\n: v}\n"],
    ["the same, not indented", "{a: b, ?\n!bwref x/foo\n: v}\n"],
    ["a flow sequence key after a comma with the tag on the next line", "[a, ?\n !bwref x/foo]\n"],
    ["a flow key with an anchor before the line break", "[? &k\n !bwref x/foo]\n"],
    // The `?` starts its own line inside the flow collection: the block indentation rule does not apply.
    ["a flow sequence key whose question mark starts a line", "[\n?\n!bwref x/foo]\n"],
    ["a flow mapping key whose question mark starts a line", "{\n?\n!bwref x/foo\n: v}\n"],
    ["a flow key after a comma whose question mark starts a line", "[a,\n?\n!bwref x/foo]\n"],
    ["a flow key whose question mark starts an indented line", "k: [\n  ?\n  !bwref x/foo]\n"],
    ["a flow key under a block key with the tag on the next line","k: [?\n  !bwref x/foo]\n"],
    ["a flow key under a nested block key with a less indented tag line", "k:\n  m: [?\n !bwref x/foo]\n"],
    // A `?`, a `-` or a `:` with a blank after it can start a compact collection, so the `?` of the key
    // that holds the tag can follow another entry indicator on its line. The oracle reads the first seven
    // as unfit. It refuses the last four, which stay as pinned best effort.
    ["an explicit key inside an explicit key", "? ? !bwref x/foo\n  : v\n: w\n"],
    ["an explicit key inside an explicit key after a dash", "- ? ? !bwref x/foo\n    : v\n  : w\n"],
    ["an explicit key inside two explicit keys", "? ? ? !bwref x/foo\n    : v\n  : z\n: w\n"],
    ["an explicit key inside an explicit key, with an anchor", "? ? &a !bwref x/foo\n  : v\n: w\n"],
    ["an explicit key inside an explicit key, with the tag on the next line", "? ?\n    !bwref x/foo\n  : v\n: w\n"],
    ["an explicit key after a dash inside an explicit key", "? - ? !bwref x/foo\n    : v\n"],
    ["an explicit key inside the value of an explicit key", "? a\n: ? !bwref x/foo\n  : v\n"],
    ["an explicit key inside an explicit key, with the tag on the next line and the value after it (go-yaml refuses)", "? ?\n  !bwref x/foo\n: w\n"],
    ["an explicit key inside an explicit key in a flow mapping (go-yaml refuses)", "{? ? !bwref x/foo}\n"],
    ["an explicit key inside an explicit key in a flow sequence (go-yaml refuses)", "[? ? !bwref x/foo]\n"],
    ["an explicit key in the value of a key on one line (go-yaml refuses)", "a: ? !bwref x/foo\n    : v\n"],
  ])("lists nothing for %s", (_, text) => {
    expect(references(text)).toEqual([]);
  });
  test.each([
    ["a sequence entry", "- !bwref x/foo\n"],
    ["a mapping value", "a: !bwref x/foo\n"],
    ["a value after blank lines", "a:\n\n\n  !bwref x/foo\n"],
    ["a value after a CRLF line break", "a:\r\n  !bwref x/foo\r\n"],
    ["the value of an explicit key", "? a\n: !bwref x/foo\n"],
    ["the value of an explicit key, after an anchor", "? a\n: &e !bwref x/foo\n"],
    ["the value of an explicit key that has another tag", "? !t a\n: !bwref x/foo\n"],
    ["a sequence entry under an explicit key", "? a\n: - !bwref x/foo\n"],
    ["a value in a flow mapping with an explicit key", "{? a : !bwref x/foo}\n"],
    ["the value of an explicit key that holds an explicit key", "? ? a\n  : v\n: !bwref x/foo\n"],
  ])("still lists the name of %s", (_, text) => {
    expect(references(text)).toEqual(["x/foo"]);
  });

  // Two documents that go-yaml refuses, so the oracle has no answer for them. They pin the reading
  // of the scanner and no go-yaml behavior. A tag line as indented as its `?` line is no key of that
  // `?`, so the name is listed. Two anchors across the line break are skipped as one, so nothing is listed.
  test("the same indentation as the question mark is listed, two anchors across the break are not (go-yaml refuses both)", () => {
    expect(references("?\n!bwref x/foo\n: v\n")).toEqual(["x/foo"]);
    expect(references("? &a\n  &b !bwref x/foo\n: v\n")).toEqual([]);
  });

  // The tag of a value that an alias key repeats (ginsys/bronzeward#672). The server refuses the
  // document at the alias use (tag-placement, the alias branch of internal/ingest/declare.go), so the
  // editor lists no name for that tag, as it lists none for the tag of a key. The anchor and the tag
  // stand on one node, in either order. The latest anchor of the name counts, as go-yaml dereferences
  // it. Another tag of the document stays listed, as for the tag of a key. Each row holds the verdict
  // of probe `w672s1-oracle` and of the server probe (24 documents).
  test.each([
    ["the anchor before the tag", "a: &k !bwref x/foo\n*k : v\n"],
    ["the anchor after the tag", "a: !bwref &k x/foo\n*k : v\n"],
    ["no blank before the colon", "a: &k !bwref x/foo\n*k: v\n"],
    ["an explicit key", "a: &k !bwref x/foo\n? *k\n: v\n"],
    ["an explicit key with no value", "a: &k !bwref x/foo\n? *k\n"],
    ["a flow mapping", "{a: &k !bwref x/foo, *k : v}\n"],
    ["a flow sequence", "[&k !bwref x/foo, *k : v]\n"],
    ["a key with a mapping value", "a: &k !bwref x/foo\n*k :\n  w: 1\n"],
    ["a later document", "a: &k !bwref x/foo\n---\n*k : v\n"],
    ["an earlier document, the tag on a root scalar", "&k !bwref x/foo\n---\n*k : v\n"],
    ["the tag on the line after the anchor", "a: &k\n  !bwref x/foo\n*k : v\n"],
    ["the latest anchor of the name carries the tag", "a: &k 1\nb: &k !bwref x/foo\n*k : v\n"],
    ["the alias is also used as a value", "a: &k !bwref x/foo\nb: *k\n*k : v\n"],
    ["the alias key is in a sequence entry", "a: &k !bwref x/foo\nb:\n  - *k : v\n"],
    ["two alias keys", "a: &k !bwref x/foo\n*k : v\n*k : w\n"],
    // The alias key in flow context. The alias name ends at the colon, so a value indicator follows with
    // or without a blank, and a mapping entry that starts with the alias is a key with or without a value.
    // A row marked guard passed before the fix too: it pins a shape that the first reading of the key
    // already dropped.
    ["a flow mapping question mark", "a: &k !bwref x/foo\nb: {? *k}\n"],
    ["a flow mapping colon with no value", "a: &k !bwref x/foo\nb: {*k:}\n"],
    ["a flow mapping blank and colon with no value", "a: &k !bwref x/foo\nb: {*k :}\n"],
    ["a flow mapping colon and a quoted value", 'a: &k !bwref x/foo\nb: {*k:"v"}\n'],
    ["a flow mapping entry with no value", "a: &k !bwref x/foo\nb: {*k, c: d}\n"],
    ["a flow mapping entry with no value, after an entry", "a: &k !bwref x/foo\nb: {a: [*k, d], *k}\n"],
    ["a flow mapping colon and a plain value", "a: &k !bwref x/foo\nb: {a: 1, *k:v}\n"],
    ["a flow sequence pair with no value", "a: &k !bwref x/foo\nb: [*k:]\n"],
    ["a flow sequence pair with a quoted value", 'a: &k !bwref x/foo\nb: [*k:"v"]\n'],
    ["a flow sequence pair after an entry", "a: &k !bwref x/foo\nb: [a, *k:]\n"],
    ["a flow sequence pair, a blank before the colon and a plain value after it", "a: &k !bwref x/foo\nb: [a, *k :x]\n"],
    ["a flow sequence question mark", "a: &k !bwref x/foo\nb: [? *k]\n"],
    ["a flow mapping question mark with no blank", "a: &k !bwref x/foo\nb: {?*k}\n"],
    ["a flow sequence question mark with no blank", "a: &k !bwref x/foo\nb: [?*k]\n"],
    ["a flow mapping question mark with no blank, after an entry", "a: &k !bwref x/foo\nb: {a: 1, ?*k}\n"],
    ["a flow mapping colon and a value (guard)", "a: &k !bwref x/foo\nb: {*k: v}\n"],
    ["a flow sequence pair (guard)", "a: &k !bwref x/foo\nb: [*k: v]\n"],
    ["a flow mapping question mark and a value (guard)", "a: &k !bwref x/foo\nb: {? *k : v}\n"],
    ["a flow mapping question mark after an entry (guard)", "a: &k !bwref x/foo\nb: {a: 1, ? *k}\n"],
    ["a flow sequence question mark after an entry (guard)", "a: &k !bwref x/foo\nb: [a, ? *k]\n"],
    // The alias is the first node after a `?` that ends its line, in a later line. A blank line or a
    // comment line between them changes nothing.
    ["an explicit key in the next line", "a: &k !bwref x/foo\n?\n  *k\n: v\n"],
    ["an explicit key in the next line, with no value", "a: &k !bwref x/foo\n?\n  *k\n"],
    ["an explicit key in the next line, after a comment", "a: &k !bwref x/foo\n? # c\n  *k\n: v\n"],
    ["an explicit key in the next line, after a blank line", "a: &k !bwref x/foo\n?\n\n  *k\n: v\n"],
    ["an explicit key in the next line, after a comment line", "a: &k !bwref x/foo\n?\n# c\n  *k\n: v\n"],
    ["an explicit key after blanks on its line (guard)", "a: &k !bwref x/foo\n?   *k\n: v\n"],
  ])("a tagged value that an alias key repeats lists nothing (%s)", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  test("another tag of the document stays listed", () => {
    expect(references("a: &k !bwref x/foo\nc: !bwref x/bar\n*k : v\n")).toEqual(["x/bar"]);
  });

  test.each([
    ["an alias used as a value", "a: &k !bwref x/foo\nb: *k\n"],
    ["an alias in a sequence", "- &k !bwref x/foo\n- *k\n"],
    ["a later anchor of the name carries no tag", "a: &k !bwref x/foo\nb: &k 1\n*k : v\n"],
    ["a quoted key that looks like an alias", 'a: &k !bwref x/foo\n"*k": v\n'],
    ["an anchor on the next node, not on the tagged one", "a: &k\nb: !bwref x/foo\n*k : v\n"],
    // The near misses of the flow and explicit key readings: an alias that is a value, a question mark
    // that is text, and a `?` line that a key follows. The server accepts each of these.
    ["a flow mapping value", "a: &k !bwref x/foo\nb: {a: *k}\n"],
    ["a flow mapping value, then an entry", "a: &k !bwref x/foo\nb: {a: *k, c: d}\n"],
    ["a flow sequence entry", "a: &k !bwref x/foo\nb: [*k]\n"],
    ["a flow sequence entry, then another", "a: &k !bwref x/foo\nb: [*k, c]\n"],
    ["a flow sequence in a flow mapping value", "a: &k !bwref x/foo\nb: {a: [*k], c: d}\n"],
    ["a question mark in a quoted key", 'a: &k !bwref x/foo\n? "?"\n: *k\n'],
    ["a question mark in a quoted value", "a: &k !bwref x/foo\nb: '? '\nc: *k\n"],
    ["a question mark line followed by a key", "a: &k !bwref x/foo\n?\nb: *k\n"],
  ])("a tagged value that no alias key repeats stays listed (%s)", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });

  // A known limit: an alias key inside a collection that is a key (`? - *k`). The server refuses the
  // document, go-yaml accepts it, and the scanner lists the name. Only an alias that is the key itself
  // is read.
  test("an alias key inside a collection that is a key is a known limit, the name is listed", () => {
    expect(references("a: &k !bwref x/foo\n? - *k\n: v\n")).toEqual(["x/foo"]);
  });
});

// ginsys/bronzeward#629: the same server rule (tag-placement, internal/ingest/declare.go) covers a tag
// on an implicit key, that is, a `!bwref` value followed by a colon and a blank or a line end. go-yaml
// reads such a tagged key, but the server refuses the document, so the editor lists nothing. The row
// numbers are those of the oracle batch in the refinement comment of #629. The control is the
// sequence entry `- !bwref x/foo`, which the block above already pins as listed. Each row fails when
// the line that skips a tag with a colon after its value is removed, except rows 50 and 51. go-yaml
// refuses those two documents, and they pass without that line too, so another rule holds them.
describe("a tag on an implicit key", () => {
  test.each([
    ["46: a sequence entry key", "- !bwref x/foo:\n"],
    ["47: the same, then a continuation line", "- !bwref x/foo:\n    bar\n"],
    ["57: an anchor after the tag", "- !bwref &e x/foo:\n    bar\n"],
    ["58: an anchor before the tag", "- &e !bwref x/foo:\n    bar\n"],
    ["49: a key in a flow sequence value", "k: [!bwref x/foo:\nbar]\n"],
    ["55: a key in a flow mapping", "{!bwref x/foo: v}\n"],
    ["56: a key in a flow sequence", "[!bwref x/foo: v]\n"],
    ["50: after a value colon (go-yaml refuses)", "k: !bwref x/foo:\n"],
    ["51: after a value colon, then a value (go-yaml refuses)", "k: !bwref x/foo: v\n"],
    ["the nested key", "k:\n  !bwref x/foo: v\n"],
  ])("lists nothing for row %s", (_, text) => {
    expect(references(text)).toEqual([]);
  });

  // Row 48 (ginsys/bronzeward#685, ginsys/bronzeward#672): go-yaml reads the scalar `x/foo:` (a colon
  // before a flow indicator stays in the word), which is no name, and the server refuses the document
  // as a bad name. In flow context a tag with no anchor reads its value with the same word pattern
  // as a tag after an anchor, so both lists are empty.
  test("row 48: a colon that ends a flow sequence keeps the colon in the value, no name is listed", () => {
    expect(references("[!bwref x/foo:]\n")).toEqual([]);
    expect(references("k: [!bwref &e x/foo:]\n")).toEqual([]);
  });

  test("a flow value with a real name still lists it, with or without an anchor", () => {
    expect(references("[!bwref x/foo]\n")).toEqual(["x/foo"]);
    expect(references("k: [!bwref x/foo, y]\n")).toEqual(["x/foo"]);
    expect(references("k: [!bwref &e x/foo]\n")).toEqual(["x/foo"]);
    expect(references("{k: !bwref x/foo}\n")).toEqual(["x/foo"]);
  });
});

// ginsys/bronzeward#672, section 3: an alias that names no anchor. go-yaml v3.0.5 refuses the stream
// (`unknown anchor 'e' referenced`) and the server reads no reference from it. An anchor defines its
// name for the rest of the stream, across `---` and `...`, and a re-defined anchor is fine. Text that
// only looks like an anchor (a comment, a quoted, plain or block scalar) defines nothing. Every row
// holds the verdict of probe `w672s3-oracle` (go.yaml.in/yaml/v3 v3.0.5, 33 documents).
describe("an alias that names no anchor", () => {
  test.each([
    ["an alias before any anchor", "a: *e\nb: !bwref x/foo\n"],
    ["an alias as a root key", "*e: !bwref x/foo\n"],
    ["an anchor that stands after the alias", "a: *e\nb: &e 1\nc: !bwref x/foo\n"],
    ["an alias as a flow key", "[*e]: !bwref x/foo\n"],
    ["an alias in a flow sequence", "a: [*e]\nb: !bwref x/foo\n"],
    ["an alias as an explicit key", "? *e\n: !bwref x/foo\n"],
    ["a name that stands before the alias", "a: !bwref x/foo\nb: *e\n"],
    ["an anchor in a comment", "# &e\na: *e\nb: !bwref x/foo\n"],
    ["an anchor in a quoted scalar", 'a: "&e"\nb: *e\nc: !bwref x/foo\n'],
    ["an anchor in a plain scalar", "a: x &e\nb: *e\nc: !bwref x/foo\n"],
    ["an anchor in a block scalar", "a: |\n  &e x\nb: *e\nc: !bwref x/foo\n"],
    ["an alias before its anchor in one flow sequence", "a: [*e, &e 1]\nc: !bwref x/foo\n"],
    // The four rows below are probes 12, 27, 28 and 32 of experiments/scanner-cross-line/evidence/alias-anchor-scope.md.
    // Probe 12: directives between two documents.
    ["a name defined before a document end marker and directives, then an alias", "%YAML 1.2\n---\na: &e 1\n...\n%YAML 1.2\n---\nb: *e\nc: !bwref x/foo\n"],
    // Probe 27: a document end marker with no `---` after it.
    ["a name defined before a document end marker with no start marker, then an alias", "a: &e 1\n...\nb: *e\nc: !bwref x/foo\n"],
    // Probe 28: an alias whose name starts with a non-ASCII letter. The server and go-yaml refuse the stream.
    ["an alias whose name holds a non-ASCII letter", "a: *é\nb: !bwref x/foo\n"],
    // Probe 32: an alias before its anchor, and no tag anywhere in the document. It records the oracle verdict.
    ["an alias before its anchor with no tag on the anchored value", "a: *e\nb: &e 1\n"],
  ])("lists nothing for %s", (_, text) => {
    expect(references(text), text).toEqual([]);
  });

  test.each([
    ["an anchor before the alias", "a: &e 1\nb: *e\nc: !bwref x/foo\n"],
    ["an anchor on the tagged value", "a: &e !bwref x/foo\nb: *e\n"],
    ["an anchor of an earlier document", "a: &e 1\n---\nb: *e\nc: !bwref x/foo\n"],
    ["an anchor before a document end marker", "a: &e 1\n...\n---\nb: *e\nc: !bwref x/foo\n"],
    ["an anchor re-defined in the same document", "a: &e 1\nb: &e 2\nc: *e\nd: !bwref x/foo\n"],
    ["an anchor re-defined in a later document", "a: &e 1\n---\nb: &e 2\n---\nc: *e\nd: !bwref x/foo\n"],
    ["a second anchor of another name", "a: &e 1\n---\nb: &f 2\n---\nc: *f\nd: !bwref x/foo\n"],
    ["an alias as the root of a document", "a: &e 1\n--- *e\n---\nc: !bwref x/foo\n"],
    ["an anchor on a key", "&e k: 1\nb: *e\nc: !bwref x/foo\n"],
    ["an anchor in a flow sequence", "- !bwref x/foo\n- [&e a, *e]\n"],
    ["an anchor before a flow alias", "- &e a\n- [*e]\n- !bwref x/foo\n"],
    ["an anchor, then a tag directive", "a: &e 1\n%TAG !u! tag:example.com,2000:\n---\nb: *e\nc: !bwref x/foo\n"],
    ["a tagged value in a flow sequence after the alias", "a: &e 1\nb: [*e, !bwref x/foo]\n"],
    ["an anchor of the first document, an alias in the second, the tag in the third", "a: &e 1\n---\nb: *e\n---\nc: !bwref x/foo\n"],
  ])("still lists the name for %s", (_, text) => {
    expect(references(text), text).toEqual(["x/foo"]);
  });
});
