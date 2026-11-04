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
  test("a tag spelled through a handle is read as main read it, by its !bwref", () => {
    const text = "%TAG !r! !\n---\na: !r!bwref x/foo\n";
    const start = text.indexOf("!bwref");
    expect(references(text, [])).toEqual(["x/foo"]);
    expect(spotOfReference(text, "x/foo", [])).toEqual({ start, end: start + "!bwref x/foo".length });
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
    expect(references(`a: &a${"\n".repeat(4100)}  !bwref &b x/foo\n`)).toEqual(["x/foo"]);
    expect(references("a: &a\n\n  !bwref &b x/foo\n")).toEqual([]);
  });
});

// ginsys/bronzeward#605: where a plain value ends depends on the context. In block context a comma,
// bracket or brace is text, so `x/foo,bar` is one scalar and no name. In flow context it ends the
// value. Each case below is checked against go-yaml v3.0.5, with no anchor, an anchor before the tag
// and an anchor after it.
describe("a plain value in block and in flow context", () => {
  const withAnchors = (doc: (before: string, after: string) => string) => [doc("", ""), doc("&e ", ""), doc("", "&e ")];

  test.each([
    ["a comma", "x/foo,bar"],
    ["a comma and a blank", "x/foo, bar"],
    ["a closing bracket", "x/foo]"],
    ["a closing brace", "x/foo}"],
    ["a colon and a comma", "x/foo:,y"],
    ["a second word", "x/foo bar"],
  ])("block context: %s makes one scalar, which is no name", (_what, value) => {
    for (const doc of withAnchors((b, a) => `k: ${b}!bwref ${a}${value}\n`)) expect(references(doc), doc).toEqual([]);
    for (const doc of withAnchors((b, a) => `- ${b}!bwref ${a}${value}\n`)) expect(references(doc), doc).toEqual([]);
  });

  test("block context: the value on the next line, and a continuation line", () => {
    for (const doc of withAnchors((b, a) => `k: ${b}!bwref ${a}\n  x/foo,bar\n`)) expect(references(doc), doc).toEqual([]);
    for (const doc of withAnchors((b, a) => `k: ${b}!bwref ${a}x/foo\n  bar\n`)) expect(references(doc), doc).toEqual([]);
  });

  // A line after the value continues it when go-yaml reads one scalar: in block context if it is more
  // indented than the holding node, whatever it starts with except a key; in flow context also with
  // no indentation, unless it starts with a flow indicator.
  test.each([
    ["a comma", "  ,bar"],
    ["a closing bracket", "  ]bar"],
    ["a dash", "  - y"],
    ["a question mark", "  ? y"],
    ["a second word", "  bar"],
  ])("block context: a continuation line that starts with %s is text", (_what, next) => {
    for (const doc of withAnchors((b, a) => `k: ${b}!bwref ${a}x/foo\n${next}\n`)) expect(references(doc), doc).toEqual([]);
  });

  test("flow context: a continuation line needs no indentation, and a flow indicator ends the value", () => {
    for (const doc of withAnchors((b, a) => `k: [${b}!bwref ${a}x/foo\nbar]\n`)) expect(references(doc), doc).toEqual([]);
    for (const doc of withAnchors((b, a) => `k: [${b}!bwref ${a}x/foo\n  , bar]\n`)) expect(references(doc), doc).toEqual(["x/foo"]);
    for (const doc of withAnchors((b, a) => `k: [${b}!bwref ${a}x/foo\n]\n`)) expect(references(doc), doc).toEqual(["x/foo"]);
  });

  test("block context after a closed flow collection", () => {
    expect(references("a: [x]\nb: !bwref y/foo,z\n")).toEqual([]);
  });

  test.each([
    ["a comma", "[!bwref x/foo, bar]"],
    ["a comma with no blank", "[!bwref x/foo,bar]"],
    ["a closing bracket", "[!bwref x/foo]"],
    ["a closing brace", "{k: !bwref x/foo}"],
    ["a flow map", "{k: !bwref x/foo, j: l}"],
    ["a nested collection", "[[!bwref x/foo, y], z]"],
    ["the value on the next line", "[!bwref\n  x/foo, y]"],
  ])("flow context: %s ends the value", (_what, flow) => {
    expect(references(`k: ${flow}\n`), flow).toEqual(["x/foo"]);
    expect(references(`k: ${flow.replace("!bwref", "&e !bwref")}\n`), flow).toEqual(["x/foo"]);
    expect(references(`k: ${flow.replace("!bwref", "!bwref &e")}\n`), flow).toEqual(["x/foo"]);
  });

  test("flow context: a second word and a continuation line make one scalar, which is no name", () => {
    expect(references("k: [!bwref x/foo bar]\n")).toEqual([]);
    expect(references("k: [!bwref &e x/foo\n  bar]\n")).toEqual([]);
  });
});
