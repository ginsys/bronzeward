// Usage: node gen-small-sets.mjs <outdir>
// Writes the three small sets of the investigation (ginsys/bronzeward#688) to <outdir>:
//   matrix.json  180 documents: the matrix of ginsys/bronzeward#657 (18 keys x 10 values)
//   probes.json   22 documents: probe 5 and probe 6 of #657, plus the root and "a:" shapes
//   hyp.json     126 documents: 21 flow keys x 6 contexts
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const outDir = process.argv[2];
if (!outDir) throw new Error("usage: node gen-small-sets.mjs <outdir>");
mkdirSync(outDir, { recursive: true });

const P = "!bwref x/foo";

const V = `${P}\n`;
const keys = ["a", "a b", '"a"', "'a'", "[a]", "{a: b}", "[]", "!t a", "&e a", "*e", "!t", "&e", "", "|\n  x", "[a,\n  b]", "a\n  b", "- a", "a: b"];
const vals = [" [? b]: ", "\n  [? b]: ", " &x\n  [? b]: ", " !t\n  [? b]: ", " &x !t\n  [? b]: ", " &x # c\n  [? b]: ", " &x [? b]: ", "\n\n  [? b]: ", " &x\n  {? b}: ", " &x\n  [b]: "];
const matrix = [];
for (const k of keys) for (const v of vals) matrix.push(`? ${k}\n:${v}${V}`);

const probes = [
  `? &e\n  a\n: [? b]: ${P}\n`,
  `? !t\n  a\n: [? b]: ${P}\n`,
  `? &e\n  !t\n: [? b]: ${P}\n`,
  `? &e !t\n: [? b]: ${P}\n`,
  `? &e\n  !t a\n: [? b]: ${P}\n`,
  `- ? a\n- ? b\n  : [? b]: ${P}\n`,
  `? a\n? b\n: [? b]: ${P}\n`,
  `m:\n  ? a\n  n: c\n  ? b\n  : [? b]: ${P}\n`,
  `- ? a\n- ? b\n  : &x\n    [? b]: ${P}\n`,
  `? a\n: b\n[? c]: ${P}\n`,
  `? a\n: b\n\n[? c]: ${P}\n`,
  `? a\n: [b]\n[? c]: ${P}\n`,
  `? a\n: &x b\n[? c]: ${P}\n`,
  `? a\n: b\nd: e\n[? c]: ${P}\n`,
  `x:\n  ? a\n  : b\n  [? c]: ${P}\n`,
  `- ? a\n  : b\n- [? c]: ${P}\n`,
  `? a\n: b\n{? c}: ${P}\n`,
  `a: b\n[? c]: ${P}\n`,
  `a:\n  [? b]: ${P}\n`,
  `a:\n  {? b}: ${P}\n`,
  `[? b]: ${P}\n`,
  `{? b}: ${P}\n`,
];

const flows = ["[b]", "[? b]", "[]", "{}", "[ ]", "[? b, ? c]", "[? b,]", "[? b: c]", "[? b, c]", "[b, ? c]", "[,]", "[*e]", "['b']", "[[]]", "[[? b]]", "{? b}", "{b: c}", "{? b: c}", "[! b]", "[&e b]", "[!t]"];
const ctx = [
  (f) => `${f}: ${P}\n`,
  (f) => `a:\n  ${f}: ${P}\n`,
  (f) => `? a\n: ${f}: ${P}\n`,
  (f) => `? a\n: b\n${f}: ${P}\n`,
  (f) => `a: b\n${f}: ${P}\n`,
  (f) => `x: 1\n? ${f}\n: ${P}\n`,
];
const hyp = [];
for (const c of ctx) for (const f of flows) hyp.push(c(f));

for (const [name, docs] of [["matrix", matrix], ["probes", probes], ["hyp", hyp]]) {
  writeFileSync(join(outDir, `${name}.json`), JSON.stringify(docs));
  console.log(name, docs.length);
}
