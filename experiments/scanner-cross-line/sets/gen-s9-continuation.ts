// ginsys/bronzeward#605: what follows a plain `!bwref` value at the end of its line. A deterministic
// cross product, no random choice. Synthetic input only. Usage: node gen-s9-continuation.ts <out.json>
//
// A value is `!bwref x/foo` or `!bwref x/foo:`, with no anchor, an anchor before the tag, or an
// anchor after it. It stands in 24 contexts (a block key, a nested key, a sequence entry, a nested
// sequence, a compact mapping, an explicit key, a sequence in an explicit key, the root after `---`,
// a key led by U+00A0 or a byte order mark, a flow sequence, a flow mapping, an explicit key in flow,
// ...). In a flow context the collection
// also closes on the line of the value. The next line is one of 33 shapes: a second word, a flow indicator, a value indicator, a sequence or key
// entry, a comment, a document marker, a no-break space (U+00A0) alone or with a comment, a tab, and
// more. It is indented 0, 1, 2 or 4 spaces, with or without a blank line before it. The go-yaml oracle
// decides what each document lists. The set is not in sets/MANIFEST and run/gen-sets does not write it:
// it is made on demand, and the same input gives the same file.
import { writeFileSync } from "node:fs";

const NBSP = "\u00a0";
const BOM = "\ufeff";
const anchors: [string, string][] = [
  ["", ""],
  ["&e ", ""],
  ["", "&e "],
];
// [builds the first line from the tagged value, text that closes the open flow collection]
const contexts: [(v: string) => string, string][] = [
  [(v) => `k: ${v}`, ""],
  [(v) => `k:\n  j: ${v}`, ""],
  [(v) => `- ${v}`, ""],
  [(v) => `- - ${v}`, ""],
  [(v) => `- k: ${v}`, ""],
  [(v) => `- - k: ${v}`, ""],
  [(v) => `k:\n  - ${v}`, ""],
  [(v) => `? ${v}`, ""],
  [(v) => `? k\n: ${v}`, ""],
  [(v) => `k: [${v}`, "]"],
  [(v) => `k:\n  j: [${v}`, "]"],
  [(v) => `{k: ${v}`, "}"],
  [(v) => `k: [a, ${v}`, "]"],
  [(v) => `- [${v}`, "]"],
  [(v) => `k: {? ${v}`, "}"],
  [(v) => `k: [? ${v}`, "]"],
  [(v) => `k: {a: b, ? ${v}`, "}"],
  [(v) => `? - ${v}`, ""],
  [(v) => `? - - ${v}`, ""],
  [(v) => `--- ${v}`, ""],
  // A key that starts with U+00A0 or a byte order mark has text at column 0, not indentation.
  [(v) => `${NBSP}k: ${v}`, ""],
  [(v) => `${BOM}k: ${v}`, ""],
  [(v) => `k:\n${NBSP}j: ${v}`, ""],
  [(v) => `- ${NBSP}k: ${v}`, ""],
];
const nexts = [
  "bar",
  ",bar",
  "]",
  "bar, k: v]",
  "bar: v",
  `bar:${NBSP}z`,
  `${NBSP}bar`,
  NBSP,
  `${NBSP} # c`,
  `bar${NBSP}baz`,
  "- y",
  "? y",
  "k: v",
  "bar,baz",
  "bar]",
  ", bar",
  "bar # c",
  "# c",
  "bar:",
  ": v",
  ":",
  ":,",
  ":]",
  ":}",
  ":v",
  "---",
  "...",
  "--- x",
  "\t",
  "\tbar",
  "\t# c",
  "'q'",
  "|",
];
const indents = ["", " ", "  ", "    "];
const before = ["", "\n"];

const values = ["x/foo", "x/foo:"];
// Text after the value on its own line in a flow context, before the collection closes.
const tails = ["", ", y", ",bar", " bar"];

const docs: { name: string; doc: string }[] = [];
contexts.forEach(([build, close], c) => {
  for (const [b, a] of anchors) {
    for (const value of values) {
      const tagged = `${b}!bwref ${a}${value}`;
      for (const n of nexts) {
        for (const ind of indents) {
          for (const gap of before) {
            const ends = /[\]}]$/.test(n) || n === "# c" || n === "bar # c" || n.startsWith("---") || n === "...";
            docs.push({ name: `c${c}`, doc: `${build(tagged)}\n${gap}${ind}${n}${ends ? "" : close}\n` });
          }
        }
      }
      if (close !== "") for (const tail of tails) docs.push({ name: `c${c}-same-line`, doc: `${build(tagged)}${tail}${close}\n` });
    }
  }
});
writeFileSync(process.argv[2]!, JSON.stringify(docs));
console.log(docs.length);
