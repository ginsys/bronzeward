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
    // Undeclared, the text is scanned as it is written, as before.
    expect(references(folded)).toEqual(["x/hidden"]);
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

  // Positions are compared as numbers, so a failure prints no document text.
  test("a reference only an embedded document carries points at the scalar that holds it, or at its tag as written", () => {
    const text = "a: 1\ncfg: 'a: !bwref x/sq'\n";
    expect(spotOfReference(text, "x/sq", at("doc[0]/cfg"))).toEqual({ start: 10, end: 26 });
    expect(spotOfReference(text, "x/sq")).toBeUndefined();
    const literal = "cfg: |\n  a: !bwref x/lit\n";
    expect(spotOfReference(literal, "x/lit", at("doc[0]/cfg"))).toEqual({ start: 12, end: 24 });
  });
});
