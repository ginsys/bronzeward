// Tests the implementation of the C1c candidate needs beyond web/src/position.test.ts. run/web-tests
// copies this file beside web's sources and aims its import at the candidate, as it does for
// position.test.ts. They hold on the scanner on main as well.
import { expect, test } from "vitest";
import { references, spotOfReference } from "./position.ts";

const at = (path: string) => [{ path }];

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
