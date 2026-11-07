// Usage: node gen-flow-key-set.mjs <out.json>
// The flow-key set of ginsys/bronzeward#657 (called S12 there, provisionally S13 in this investigation;
// the scanner work of #616 uses S12 for another set). The generator is copied unchanged from the body of #657:
// 23247 documents after duplicates are removed.
// S12 (ginsys/bronzeward#654): a flow key of only `?` items (and neighbours) at the value position of an
// explicit key. The explicit key takes many forms. The value position holds properties alone, a comment
// or a sequence entry before the flow key. Every document is wrapped at three nesting levels and the flow
// key is indented by one, two or four spaces. The oracle decides which are valid.
import { writeFileSync } from "node:fs";
const out = process.argv[2];
const V = "!bwref x/foo\n";
const keys = [
  "a", "a b", '"a"', "'a'", "!t", "&e", "!t &e", "&e !t", "a\n  b",
  "[a]", "{a: b}", "[]", "!t a", "&e a", "&e !t a", "", "|\n  x", "[a,\n  b]", "- a", "a: b", "? c",
];
// What stands between the colon and the flow key. {i} is the indent of the flow key line.
const tails = [
  " ", "\n{i}", "\n\n{i}", " &x ", " !t ", " &x\n{i}", " !t\n{i}", " &x !t\n{i}", " !t &x\n{i}",
  " &x # c\n{i}", " # c\n{i}", " &x\n{i}# c\n{i}", " - ", "\n{i}- ", " &x\n{i}- ",
];
const flows = [
  "[? b]", "{? b}", "[? b,]", "[? b, ? c]", "[? b: c]", "[b]", "[? b, c]", "[b, ? c]", "!t [? b]", "[[? b]]",
];
const nests = ["", "map", "seq"];
const indents = [" ", "  ", "    "];
const wrap = (doc, nest) => {
  if (nest === "") return doc;
  const lines = doc.split("\n");
  if (nest === "map") return "m:\n" + lines.map((l) => (l === "" ? l : "  " + l)).join("\n");
  return "- " + lines.map((l, n) => (n === 0 || l === "" ? l : "  " + l)).join("\n");
};
const docs = [];
const seen = new Set();
for (const k of keys) for (const t of tails) for (const f of flows) for (const ind of indents) for (const nest of nests) {
  const doc = wrap(`? ${k}\n:${t.replaceAll("{i}", ind)}${f}: ${V}`, nest);
  if (seen.has(doc)) continue;
  seen.add(doc);
  docs.push(doc);
}
writeFileSync(out, JSON.stringify(docs));
console.log(docs.length);
