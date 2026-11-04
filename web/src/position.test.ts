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
