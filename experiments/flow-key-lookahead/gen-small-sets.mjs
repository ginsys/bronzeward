// Usage: node gen-small-sets.mjs <outdir>
// Writes the five small sets of the investigation (ginsys/bronzeward#688) to <outdir>:
//   matrix.json  180 documents: the matrix of ginsys/bronzeward#657 (18 keys x 10 values)
//   probes.json   22 documents: probe 5 and probe 6 of #657, plus the root and "a:" shapes
//   hyp.json     126 documents: 21 flow keys x 6 contexts
//   small.json    45 documents: 15 small flow keys of only ? and : items x 3 contexts
//   edge.json     14 documents: a byte order mark, three Unicode line breaks, an astral key, a tab after a comment,
//                 two shorthand tags, a tab in a plain scalar continuation, a byte order mark after the first
//                 line and two byte order marks at the start
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

// Characters whose width or meaning differs between a JavaScript string and go-yaml: an initial byte order
// mark (go-yaml drops it before it counts a column), the line breaks U+0085, U+2028 and U+2029, and a key of
// astral characters (go-yaml counts characters, a JavaScript string counts UTF-16 units).
const [BOM, NEL, LS, PS, EMOJI] = [0xfeff, 0x85, 0x2028, 0x2029, 0x1f600].map((c) => String.fromCodePoint(c));
const edge = [
  `${BOM}? a\n: b\n[? b]: ${V}`,
  `${BOM}a: b\n[? c]: ${V}`,
  `${BOM}k: ${V} bar\n`,
  `a: b${NEL}[? c]: ${V}`,
  `a: b${LS}[? c]: ${V}`,
  `a: b${PS}[? c]: ${V}`,
  `[? ${EMOJI.repeat(511)}]: ${V}`,
  `[? ${"x".repeat(511)}]: ${V}`,
  // A tab after a comment line: go-yaml reads the run of comments in one step and accepts it, the model makes
  // no claim ("unsupported"). Two shorthand tags that swallow "]" and "," as go-yaml does (the URI set of
  // yaml_parser_scan_tag_uri has no verbatim condition): the first is refused by the parser for a reason that
  // is not a misplaced token (model "ok"), the second is refused by the bug (model "misplaced").
  `a: 1\n# c\n\t# d\nb: ${V}`,
  `[? !t]: ${V}`,
  `[? !t, x]: ${V}`,
  // A plain scalar that continues on the next line, with a tab inside the continuation (no flow key, so no bug
  // decides it): go-yaml folds the line break first and then accepts the tab, so the model must accept it too.
  `a:\n  b: [x\n y\tz]\n`,
  // A byte order mark after the first line: go-yaml tests the start of its buffer (is_bom ignores the offset,
  // yamlprivateh.go:103), so a mark at column 0 of a later line stays in the plain scalar and both oracles accept
  // this document. The model must accept it too.
  `\n${BOM}[? b]: ${V}`,
  // Two marks at the start: go-yaml drops the first one at the stream start, then its buffer-start test finds the
  // second one and skips at column 0. It refuses this document by the bug, and it refuses longer documents of this
  // shape for other reasons (set S8 holds some). The model makes no claim ("unsupported").
  `${BOM}${BOM}[? b]: ${V}`,
];

// Small flow keys of only `?` and `:` items, in three contexts.
const small = [];
for (const k of ["[?]", "[:]", "{?}", "{:}", "[? ]", "[:b]", "{:b}", "[: b]", "[?,]", "[,?]", "[? b]", "[?b]", "[:,]", "[?:]", "[? ? b]"]) {
  for (const c of [`${k}: ${V}`, `a:\n  ${k}: ${V}`, `a: b\n${k}: ${V}`]) small.push(c);
}

for (const [name, docs] of [["matrix", matrix], ["probes", probes], ["hyp", hyp], ["small", small], ["edge", edge]]) {
  writeFileSync(join(outDir, `${name}.json`), JSON.stringify(docs));
  console.log(name, docs.length);
}
