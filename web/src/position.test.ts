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

  test("a reference only an embedded document carries points at the scalar that holds it", () => {
    const text = "a: 1\ncfg: 'a: !bwref x/sq'\n";
    const s = spotOfReference(text, "x/sq", at("doc[0]/cfg"));
    expect(text.slice(s!.start, s!.end)).toBe("'a: !bwref x/sq'");
    expect(spotOfReference(text, "x/sq")).toBeUndefined();
  });
});
