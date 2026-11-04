// ginsys/bronzeward#602: the edges of the scanner's state, the families behind the hosted review
// findings of that pull request and the earlier ones. A deterministic cross product, no random
// choice. Synthetic input only. Usage: node gen-s8-state-edges.ts <out.json>
//
// Families: (A) blank and comment-only lines before and inside a block scalar header, with and
// without an indentation digit; (B) an anchor or a tag on an empty key, and on a `?` explicit key,
// in block and flow context; (C) a `:` on the line after a quoted, collection, alias or explicit
// key; (D) a comment directly after `]`, `}`, `,`, a closing quote, an alias or a property;
// (E) a byte order mark at the stream start and at the start of a block scalar line. Each family is
// wrapped in nesting depth 1 to 3. The go-yaml oracle decides what each document lists.
import { writeFileSync } from "node:fs";

const docs = new Set<string>();
const TAG = "!bwref x/foo";
const BOM = "﻿";

// Wraps lines of a block mapping in depth 1-3 of `k:` parents, or in a sequence item.
function nest(lines: string[], depth: number, how: "map" | "seq"): string {
  let head = "";
  let pad = "";
  for (let d = 0; d < depth; d++) {
    head += `${pad}k${d}:\n`;
    pad += "  ";
  }
  // In a sequence the first line sits after the "- " and the others are indented under it.
  if (how === "seq") return head + lines.map((l, i) => `${pad}${i === 0 ? "- " : "  "}${l}\n`).join("");
  return head + lines.map((l) => `${pad}${l}\n`).join("");
}
const wrapAll = (lines: string[]) => {
  for (const depth of [0, 1, 2, 3]) for (const how of ["map", "seq"] as const) docs.add(nest(lines, depth, how));
};

// (A) Blank and comment-only lines around a block scalar header.
{
  const gaps = [[], [""], ["# c"], ["  # c"], ["", "# c"], ["    "], ["", ""], ["# c", ""]];
  const headers = ["|", "|2", ">", ">1", "|-", "|2-", ">+", "|1 # h", "|-2"];
  const indents = ["  ", "    ", "      "];
  const bodies = [["text"], ["text", "more"], [""], ["# not a comment"], [TAG]];
  for (const g1 of gaps)
    for (const h of headers)
      for (const ind of indents)
        for (const g2 of [[], [""], ["# c"]])
          for (const body of bodies) {
            const lines = ["a:", ...g1, `${ind}${h}`, ...g2, ...body.map((b) => `${ind}${b}`), `b: ${TAG}`];
            wrapAll(lines);
          }
  // A key and its header on one line, with a gap before a less indented sibling.
  for (const g of gaps)
    for (const h of headers)
      wrapAll([`a: ${h}`, "  text", ...g, `b: ${TAG}`]);
}

// (B) Properties on an empty key and on a `?` explicit key.
{
  const props = ["&e", "!t", "&e !t", "!t &e", "!!str", "!"];
  const values = ["|", ">", "|2", '"x', "'x", "x", "[a,", "{a:", "*e", ""];
  const conts = [['  "x'], ["  x"], ["   y"], [""], ["  # c"], []];
  for (const p of props)
    for (const v of values)
      for (const c of conts) {
        wrapAll([`${p}: ${v}`.trimEnd(), ...c, `- ${TAG}`]);
        wrapAll([`- ${p}: ${v}`.trimEnd(), ...c.map((l) => `  ${l}`), `- ${TAG}`]);
        wrapAll([`? ${p}`, `: ${v}`.trimEnd(), ...c, `b: ${TAG}`]);
        wrapAll([`? ${p} k`, `: ${v}`.trimEnd(), ...c, `b: ${TAG}`]);
        wrapAll([`? ${p}`, ...c, `b: ${TAG}`]);
        docs.add(`a: {${p}: ${v}${v.startsWith("[") || v.startsWith("{") ? "]}" : ""}, ${TAG}}\n`);
        docs.add(`a: {? ${p}, ${TAG}}\n`);
        docs.add(`a: {? ${p}\n  : ${v}, r: ${TAG}}\n`);
      }
}

// (C) A `:` on the line after a quoted, collection, alias or explicit key.
{
  const keys = ['"k"', "'k'", "[k]", "{k: v}", "*e", "? k", '? "k"', "? [k]", "&e k", '!t "k"'];
  const colons = [":!bwref x/foo", ": !bwref x/foo", " :!bwref x/foo", ":", ": x", ":[!bwref x/foo]", ':"x # y"'];
  for (const k of keys)
    for (const c of colons) {
      for (const tail of ["}", ", r: !bwref x/foo}"]) {
        docs.add(`a: {${k}\n  ${c}${tail}\n`);
        docs.add(`a: {z: 1, ${k}\n  ${c}${tail}\n`);
      }
      docs.add(`a: [${k}\n  ${c}, ${TAG}]\n`);
      docs.add(`a: [${k}\n\n  ${c}, ${TAG}]\n`);
      docs.add(`a: [${k}\n  # c\n  ${c}, ${TAG}]\n`);
      wrapAll([`${k}`, `${c}`, `b: ${TAG}`]);
      wrapAll([`&e ${k}`, `  ${c}`, `b: ${TAG}`]);
    }
}

// (D) A comment right after a token, with a quote or a comma in the comment.
{
  const before = ["[]", "{}", ",", '"q"', "'q'", "*e", "&e", "!t", ":", "]", "}", "[a]", "x", "x ", "?"];
  const comments = ['# , "', "# ,", "#", "# '", "#x", '#"'];
  for (const b of before)
    for (const c of comments) {
      docs.add(`a: [${b}${c}\n  , ${TAG}]\n`);
      docs.add(`a: [[${b}${c}\n  , ${TAG}]]\n`);
      docs.add(`a: {${b}${c}\n  , r: ${TAG}}\n`);
      docs.add(`a: [${b} ${c}\n  , ${TAG}]\n`);
      docs.add(`a: [x, ${b}${c}\n  ${TAG}]\n`);
      wrapAll([`a: ${b}${c}`, `b: ${TAG}`]);
      wrapAll([`a: [${b}${c}`, `  ${TAG}]`]);
    }
}

// (E) A byte order mark at the stream start, and at the start of a block scalar's line.
{
  const bodies = [`a: ${TAG}\n`, `a: [${TAG}]\n`, `"a": ${TAG}\n`, `${BOM}a: ${TAG}\n`, `a: |\n  ${TAG}\nb: ${TAG}\n`];
  for (const b of bodies) {
    docs.add(BOM + b);
    docs.add(`${BOM}${BOM}${b}`);
    docs.add(` ${BOM}${b}`);
    docs.add(`\n${BOM}${b}`);
    docs.add(`# c\n${BOM}${b}`);
  }
  for (const h of ["|", ">", "|2"])
    for (const line of [`${BOM}[${TAG}]`, `${BOM}${TAG}`, `${BOM}a: ${TAG}`, `  ${BOM}a: ${TAG}`]) {
      docs.add(`cfg: ${h}\n  ${line}\nb: ${TAG}\n`);
      docs.add(`cfg: ${h}\n\n  ${line}\nb: ${TAG}\n`);
    }
}

// (F) Every three lines from a pool of lines that change the state, then a line with a tag. The pool
// holds a key with each kind of value start, a header, a blank line, a comment and a continuation.
{
  const pool = [
    "a:", "a: |", "a: >2", "- a:", "&e:", "? &e", "? k", ":", ": x", "  :!bwref x/foo", '  "k"', '"k":', "a: {? \"k\"",
    "a: [[]# , \"", "a: [", "a: {", "  x]", "  }", ", y]", "", "  ", "# c", "  # c", "    t", "  t", "   \"x", "- \"x", "  'x",
    "- &e: |", "  - b: 1", "--- x", "...", "k: 'a", "  b' # c",
  ];
  const tails = [`z: ${TAG}`, `- ${TAG}`, `  ${TAG}]`, `  , ${TAG}]`];
  for (const a of pool)
    for (const b of pool)
      for (const c of pool)
        for (const t of tails) docs.add(`${a}\n${b}\n${c}\n${t}\n`);
}

writeFileSync(process.argv[2]!, JSON.stringify([...docs]));
console.log(docs.size);
