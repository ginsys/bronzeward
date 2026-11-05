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
// TAG is the tag a document ends with. PROBE is the tag at the place that a family probes, or in
// block scalar text. The two names differ, so a loss at the probe is not hidden by the other tag.
const TAG = "!bwref x/foo";
const PROBE = "!bwref x/bar";
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
  const bodies = [["text"], ["text", "more"], [""], ["# not a comment"], [PROBE]];
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
        // The closer matches the bracket the value opens: `[` closes with `]` and `{` with `}`.
        docs.add(`a: {${p}: ${v}${v.startsWith("[") ? "]" : v.startsWith("{") ? "}" : ""}, ${TAG}}\n`);
        docs.add(`a: {? ${p}, ${TAG}}\n`);
        docs.add(`a: {? ${p}\n  : ${v}, r: ${TAG}}\n`);
      }
}

// (C) A `:` on the line after a quoted, collection, alias or explicit key.
{
  const keys = ['"k"', "'k'", "[k]", "{k: v}", "*e", "? k", '? "k"', "? [k]", "&e k", '!t "k"'];
  const colons = [`:${PROBE}`, `: ${PROBE}`, ` :${PROBE}`, ":", ": x", `:[${PROBE}]`, ':"x # y"'];
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

// (C2) The siblings of (C): a plain key, one or two lines long, then a gap of blank and comment
// lines (a comment may follow a space or a tab), then a `:` line, in flow sequence and flow map.
{
  const keys = ["k", "k\n  more", "k # c", "k\t# c", "x, k", "? k", "? k\n  more", "&e k", "!t k"];
  const gaps = [[], [""], ["", ""], ["# c"], ["  # c"], ["\t# c"], [" \t# c"], ["", "# c"], ["# c", ""], ["# c", "# d"]];
  const colons = [`:${PROBE}`, `: ${PROBE}`, ` :${PROBE}`, `:\t${PROBE}`, ":", ": x", `:[${PROBE}]`, ':"x # y"'];
  for (const k of keys)
    for (const g of gaps)
      for (const c of colons) {
        const mid = [...g, `  ${c}`].join("\n");
        docs.add(`a: [${k}\n${mid}, ${TAG}]\n`);
        docs.add(`a: {${k}\n${mid}, r: ${TAG}}\n`);
        docs.add(`a: {${k}\n${mid}}\nb: ${TAG}\n`);
        docs.add(`a: [[${k}\n${mid}], ${TAG}]\n`);
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
  const bodies = [`a: ${TAG}\n`, `a: [${TAG}]\n`, `"a": ${TAG}\n`, `${BOM}a: ${TAG}\n`, `a: |\n  ${PROBE}\nb: ${TAG}\n`];
  for (const b of bodies) {
    docs.add(BOM + b);
    docs.add(`${BOM}${BOM}${b}`);
    docs.add(` ${BOM}${b}`);
    docs.add(`\n${BOM}${b}`);
    docs.add(`# c\n${BOM}${b}`);
  }
  for (const h of ["|", ">", "|2"])
    for (const line of [`${BOM}[${PROBE}]`, `${BOM}${PROBE}`, `${BOM}a: ${PROBE}`, `  ${BOM}a: ${PROBE}`]) {
      docs.add(`cfg: ${h}\n  ${line}\nb: ${TAG}\n`);
      docs.add(`cfg: ${h}\n\n  ${line}\nb: ${TAG}\n`);
    }
}

// (G) What follows a document marker, an indicator or a tag: a space, a tab, a line end, U+00A0 and
// other Unicode spaces. go-yaml counts only the first three as blank; the others are text.
const firstOfG = docs.size;
{
  const seps = [" ", "\t", "\u00a0", "\u2003", "\u3000", "\ufeff", "\u200b", "\r", "\r\n", "\u0085", "\u2028", "\u2029", ""];
  for (const s of seps) {
    for (const m of ["---", "..."]) {
      docs.add(`${m}${s}a: |\n  "x\nb: ${TAG}\n`);
      docs.add(`${m}${s}${PROBE}\nb: ${TAG}\n`);
      docs.add(`${m}${s}\nb: ${TAG}\n`);
      // A tag that starts the line after a marker: a plain scalar's text if the marker is no marker.
      docs.add(`${m}${s}\n${TAG}\n`);
      docs.add(`${m}${s}\n${PROBE}\nb: ${TAG}\n`);
      docs.add(`a: x\n${m}${s}\n${TAG}\n`);
      docs.add(`a: |\n  x\n${m}${s}\n${TAG}\n`);
      docs.add(`a: |\n  "x\n${m}${s}b: ${TAG}\n`);
      docs.add(`a: |\n  ${m}${s}b\nc: ${TAG}\n`);
      docs.add(`a: "x\n${m}${s}b: ${TAG}\n`);
      docs.add(`${m}${s}[a, b]: |\n  "x\nb: ${TAG}\n`);
      wrapAll([`${m}${s}a: |`, `  "x`, `b: ${TAG}`]);
    }
    for (const ind of ["-", "?", ":"]) {
      docs.add(`${ind}${s}a: |\n  "x\nb: ${TAG}\n`);
      docs.add(`${ind}${s}"x\nb: ${TAG}\n`);
      docs.add(`k:\n  ${ind}${s}a: |\n    "x\n  b: ${TAG}\n`);
    }
    docs.add(`a:${s}"x\nb: ${TAG}\n`);
    docs.add(`a:${s}|\n  "x\nb: ${TAG}\n`);
    docs.add(`a:${s}&e\n  "x\nb: ${TAG}\n`);
    docs.add(`a: [b:${s}c, ${TAG}]\n`);
    docs.add(`a: {b:${s}${PROBE}, c: ${TAG}}\n`);
    docs.add(`a: !bwref${s}x/foo\n`);
    docs.add(`a: !bwref${s}${PROBE}\nb: ${TAG}\n`);
    docs.add(`a: !bwref ${PROBE}${s}y\nb: ${TAG}\n`);
    docs.add(`a: !bwref ${PROBE}${s}\nb: ${TAG}\n`);
    docs.add(`a: !bwref ${PROBE}${s}: v\nb: ${TAG}\n`);
    docs.add(`!bwref ${PROBE}${s}: v\nb: ${TAG}\n`);
    docs.add(`a: !bwref "${PROBE}"${s}: v\nb: ${TAG}\n`);
    docs.add(`a: !bwref ${PROBE}:${s}v\nb: ${TAG}\n`);
    docs.add(`a: ${s}!bwref ${PROBE}\nb: ${TAG}\n`);
    docs.add(`${s}a: !bwref ${PROBE}\nb: ${TAG}\n`);
    docs.add(`a: |\n${s}  "x\nb: ${TAG}\n`);
    docs.add(`a:\n${s}  b: |\n    "x\n  c: ${TAG}\n`);
  }
}

// (H) Text that looks like a tag before a real one: in a comment, a quote or a plain scalar of a
// block, a flow collection or a block scalar; then a line that closes the quote it opened.
{
  const fakes = ['!bwref "', "!bwref '", "!bwref |", "!bwref >2", "!bwref x/zzz", '!bwref "a" "', "!bwref  ", "!bwref"];
  const closers = ["c: '\"'", 'c: "\'"', 'c: "x"', "c: x", "- '\"'", "c: |", "c: >-"];
  for (const f of fakes)
    for (const cl of closers) {
      for (const lead of [`# ${f}`, `a: x # ${f}`, `x # ${f}`, `a: "p" # ${f}`, `a: [x] # ${f}`]) {
        docs.add(`${lead}\nb: ${TAG}\n${cl}\n`);
        docs.add(`cfg: |\n  ${lead}\n  a: 1\nb: ${TAG}\n${cl}\n`);
        docs.add(`cfg: |\n  ${lead}\nb: ${PROBE}\n${cl}\n`);
        docs.add(`cfg: >\n  ${lead}\n  a: 1\nb: ${TAG}\n${cl}\n`);
        docs.add(`k:\n  cfg: |-\n    ${lead}\n  b: ${TAG}\n  ${cl}\n`);
      }
      docs.add(`a: "${f}"\nb: ${TAG}\n${cl}\n`);
      docs.add(`a: '${f.replaceAll("'", "''")}'\nb: ${TAG}\n${cl}\n`);
      docs.add(`a: x ${f}\nb: ${TAG}\n${cl}\n`);
      docs.add(`a: [x ${f}, ${TAG}]\n${cl}\n`);
      docs.add(`a: |\n  ${f}\n  b: ${TAG}\n${cl}\n`);
    }
}

// (I) A flow collection as a block key, then a block scalar holder or a value that opens a quote.
{
  const keys = ["[a, b]", "{a: 1, b: 2}", "[[a], b]", '["q", b]', "[*e, b]", "[a,\n b]", "&e [a, b]", "!t {a, b}", "[a, [b, c], d]", "{a, b}", "[a]", "[]", "{}"];
  const vals = ["|", "|2", ">", "|-", '"x', "'x", "[a,", "x"];
  for (const k of keys)
    for (const v of vals) {
      const body = v.startsWith('"') || v.startsWith("'") || v.startsWith("[") || v === "x" ? ["  y"] : ['  "x', "  'y"];
      wrapAll([`${k}: ${v}`, ...body, `foo: ${TAG}`]);
      docs.add(`? ${k}\n: ${v}\n${body.join("\n")}\nfoo: ${TAG}\n`);
      docs.add(`${k}: ${v}\n${body.join("\n")}\nfoo: ${TAG}\n`);
      docs.add(`- ${k}: ${v}\n${body.map((b) => `  ${b}`).join("\n")}\n- ${TAG}\n`);
    }
}

// (K) Main matches the text `!bwref` wherever a tag has it, so a tag spelled through a handle counts
// there. Every spelling at every place a property can stand: where the scanner is stricter than main,
// it must still list what main lists.
{
  const spells = ["!bwref", "!r!bwref", "!!bwref", "!x!y!bwref", "!bwrefx", "!bwref!", "!<!bwref>", "!<tag:x,2000:bwref>", "!a!bwref,", "!bwref:", "!bwref\u00a0"];
  for (const s of spells) {
    const at = [
      `a: ${s} x/foo`,
      `a: &e ${s} x/foo`,
      `a: ${s} &e x/foo`,
      `- ${s} x/foo`,
      `? ${s} x/foo\n: v`,
      `? k\n: ${s} x/foo`,
      `a: [${s} x/foo]`,
      `a: {k: ${s} x/foo}`,
      `${s} x/foo`,
      `a: ${s} "x/foo"`,
      `a: ${s} 'x/foo'`,
      `a: ${s} |\n  x/foo`,
      `a: ${s}\n  x/foo`,
      `${s} x/foo: v`,
      `a: !!str ${s} x/foo`,
    ];
    for (const p of at) {
      docs.add(`${p}\nb: ${TAG}\n`);
      docs.add(`${p.replaceAll("x/foo", "x/bar")}\nb: ${TAG}\n`);
      docs.add(`k:\n  ${p.replaceAll("\n", "\n  ")}\n`);
    }
  }
}

// (L) Directives: a %TAG handle that maps to `!`, a redefinition of `!` and `!!`, a directive after
// `...`, and the verbatim form. These spell `!bwref` through a handle that maps to `!`, which
// resolves to the same tag, so the scanner must list what main lists (ginsys/bronzeward#615 added
// (M), where a handle changes what the tag is).
{
  const dirs = ["%TAG !r! !", "%TAG !! !", "%TAG ! !x-", "%TAG !r! tag:x,2000:", "%TAG !r! !x-", "%YAML 1.1", "%YAML 1.1\n%TAG !r! !", "%FOO bar"];
  const spells = ["!r!bwref", "!!bwref", "!bwref", "!<!bwref>", "!r!bwref!"];
  for (const d of dirs)
    for (const s of spells) {
      docs.add(`${d}\n---\na: ${s} x/foo\n`);
      docs.add(`${d}\n---\n${s} x/foo\n`);
      docs.add(`${d}\n---\na: ${s} ${PROBE}\nb: ${TAG}\n`);
      docs.add(`a: 1\n...\n${d}\n---\na: ${s} x/foo\n`);
      docs.add(`---\na: 1\n...\n${d}\n---\nb: ${s} x/foo\n`);
      docs.add(`${d}\n---\na: 1\n---\nb: ${s} x/foo\n`);
      docs.add(`${d}\n---\na: |\n  ${s} x/zzz\nb: ${s} x/foo\n`);
    }
}

// (M) ginsys/bronzeward#615: the tag that a spelling resolves to, as go-yaml resolves it. (M1) A
// handle whose prefix holds part of the name (`%TAG !x! !bw`, then `!x!ref`) names `!bwref`, and a
// handle whose prefix does not (`!x!bwref` over `!bw`) names another tag. (M2) A percent escape in a
// tag, a verbatim tag or a prefix decodes to the character, and a bad one (not hex, cut short, a
// byte that is no UTF-8 start or continuation) fails the stream. Each is placed where a property can
// stand, and the documents around it show that the others still list.
{
  const decls = [
    ["%TAG !x! !bw", ["!x!ref", "!x!bwref", "!x!r", "!x!"]],
    ["%TAG !x! !b", ["!x!wref", "!x!ref"]],
    ["%TAG !x! !bwr", ["!x!ef", "!x!ref"]],
    ["%TAG ! !bw", ["!ref", "!bwref", "!"]],
    ["%TAG !! !bw", ["!!ref", "!!bwref"]],
    ["%TAG !x! !other", ["!x!bwref", "!x!ref"]],
    ["%TAG !x! !bw%72", ["!x!ef", "!x!ref"]],
    ["%TAG !x! !bw%7", ["!x!ef"]],
    ["%TAG !x! !bw%ZZ", ["!x!ef"]],
    ["%TAG !x! !bw%C3%A9", ["!x!ef"]],
    ["%TAG !x! !bw%FF", ["!x!ef"]],
    ["%TAG !x! !bw # note", ["!x!ref"]],
    ["%TAG !x! !bw#note", ["!x!ref"]],
    ["%TAG !x! !bw\n%TAG !y! !wref", ["!x!ref", "!y!wref", "!z!ref"]],
    ["%YAML 1.2", ["!bwref"]],
    // ginsys/bronzeward#633: a comment after the version with no blank, one blank and a tab.
    ["%YAML 1.1#note", ["!bwref"]],
    ["%YAML 1.1 #note", ["!bwref"]],
    ["%YAML 1.1\t#note", ["!bwref"]],
    ["%YAML 1.1x", ["!bwref"]],
  ] as const;
  const bare = ["!bw%72ef", "!bw%7ef", "!bw%ZZef", "!bwref%", "!bw%C3%A9ref", "!bw%FFref", "!<!bw%72ef>", "!<!bwref>", "!<!bwref", "!<>", "!<!bw%ZZef>", "!bwref}", "!bwref]", "!bwref,", "!bwref!", "!bw!ref", "!!bwref", "!x!bwref", "!bw ref"];
  const place = (s: string) => [`a: ${s} x/foo`, `a: &e ${s} x/foo`, `a: ${s} &e x/foo`, `- ${s} x/foo`, `a: [${s} x/foo]`, `a: {k: ${s} x/foo}`, `a: ${s} "x/foo"`];
  for (const [d, spells] of decls)
    for (const s of spells)
      for (const p of place(s)) {
        docs.add(`${d}\n---\n${p}\nb: ${TAG}\n`);
        docs.add(`${d}\n---\n${p}\n---\nb: ${TAG}\n`);
        docs.add(`${d}\n---\na: 1\n---\n${p}\n`);
      }
  for (const s of bare)
    for (const p of place(s)) {
      docs.add(`${p}\nb: ${TAG}\n`);
      docs.add(`${p}\n---\nb: ${TAG}\n`);
      docs.add(`${p.replaceAll("x/foo", "x/bar")}\n...\n---\nb: ${TAG}\n`);
    }
  // A directive line where it is none, and one that stands in the wrong place.
  for (const d of ["%TAG !x! !bw", "%YAML 1.1", "%TAG !x! !bw # note"]) {
    docs.add(`a: |\n  ${d}\nb: !x!ref x/foo\n`);
    docs.add(`a: "p\n${d}\n"\nb: !bwref x/foo\n`);
    docs.add(`# ${d}\nb: !x!ref x/foo\n`);
    docs.add(`a: 1\n${d}\n---\nb: !x!ref x/foo\n`);
    docs.add(`a: x\n${d}\nb: ${TAG}\n`);
    docs.add(`${d}\n${d}\n---\nb: !x!ref x/foo\n`);
    docs.add(`a: 1\n...\n${d}\nb: ${TAG}\n`);
    docs.add(`a: 1\n...\nb: ${TAG}\n`);
  }
  // A directive in the first column after content (it ends the document in go-yaml when no flow
  // collection is open and no plain scalar goes on), and the spots where it is an error. The
  // second document is read with the table the directive declares.
  const ends = [
    "a: 1", "a: x", "a:\n  b: 1", "a: |\n  x", "a: >\n  x", "- a", "- - a", "[a]", "{a: 1}", '"a"', "'a'", "? a", "? a\n: b",
    "--- |\n x", "--- a: 1", "a: 1\n# c", "a: 1\n\n", "a: &e x", `a: ${TAG} x/bar`, "a: [1,", "a: {b:", "a: 'x", "a", "--- a", "a: 1\n  b", "- a\n  b",
  ];
  for (const d of ["%TAG !x! !bw", "%YAML 1.1", "%YAML 1.2", "%TAG !x! !bw\n%TAG !x! !b", "%FOO x"])
    for (const c of ends) {
      docs.add(`${c}\n${d}\n---\nb: !x!ref x/foo\n`);
      docs.add(`${c}\n${d}\n---\nb: ${TAG}\n`);
      docs.add(`${c}\n${d}\nb: !x!ref x/foo\n`);
      docs.add(`${c}\n${d}\n...\nb: !x!ref x/foo\n`);
      docs.add(`${c}\n${d}\n---\nb: !x!ref x/foo\n%TAG !x! !other\n---\nc: !x!ref x/foo\nd: ${TAG}\n`);
    }
  // A marker or a directive inside an open flow collection refuses the document that is open, and
  // the document after it is read on its own. Text on the `...` line is content with no `---`.
  const opens = ["a: [1,", "a: {b:", "a: [!<!bwref> x/foo,", "a: [!bwref x/foo,", "a: {b: !bwref x/foo,", "- [a,", "[a,\n b"];
  const follows = ["---\nb: !bwref x/bar\n", "...\n---\nb: !bwref x/bar\n", "%YAML 1.1\n---\nb: !bwref x/bar\n", "%TAG !x! !bw\n---\nb: !x!ref x/bar\n", "---\nb: 1\n", "...\n"];
  for (const o of opens) for (const f of follows) docs.add(`${o}\n${f}`);
  // A `%` line in the first column inside an open flow collection. After an open plain scalar it is
  // text of that scalar, since go-yaml ends the scalar at a marker or a comment only, so the stream is
  // one valid document. After a comment, a quote, a comma, a tag or an opening bracket it is a
  // directive token, which is an error.
  const flowHeads = [
    ["a: [plain", "]"], ["a: [x, plain", "]"], ["a: {k: plain", "}"], ["a: [plain # c", "]"], ['a: ["q"', "]"],
    ["a: [plain,", "]"], ["a: [!bwref", "]"], ["a: [!bwref x/foo", "]"], ["a: [", "]"], ["a: [plain x/foo", "]"],
  ] as const;
  for (const [h, closer] of flowHeads)
    for (const t of ["%YAML 1.2", "%YAML 1.1", "%TAG !x! !bw", "%FOO x"]) {
      docs.add(`${h}\n${t}\n${closer}\nb: !bwref x/foo\n`);
      docs.add(`${h}\n${t}\n${closer}\n---\nb: !bwref x/foo\n`);
      docs.add(`${h}\n${t}\n${closer}\nb: !x!ref x/foo\n`);
    }
  const onMarker = ["!<!bwref> x/foo", "!bwref x/foo", "x", "# c", "", "\t", "!x!ref x/foo", "--- b"];
  for (const c of ["a: 1", "a: |\n  x", "- a", "--- !bwref x/foo", "[a]"])
    for (const t of onMarker) {
      docs.add(`${c}\n... ${t}\n`);
      docs.add(`${c}\n... ${t}\n---\nb: !bwref x/bar\n`);
      docs.add(`${c}\n... ${t}\n%TAG !x! !bw\n---\nb: !x!ref x/foo\n`);
    }
}

// (N) ginsys/bronzeward#620: shapes that go-yaml refuses and the scanner blanks with their document.
// (N1) A second tag on one node, with an anchor or a verbatim spelling between or around the two,
// in each place a property can stand. (N2) A `%` where a token starts, in the middle of a document
// (an indented line, after an indicator, in a flow collection); a `%` that continues a scalar or sits
// in a scalar's text is the control. (N3) A `---` or `...` marker in the first column of a quoted
// scalar, in block and flow context; an indented marker or text after the dashes is the control. Each
// shape sits alone, before a `---`, and after one, so that the documents around it show that the
// others still list.
{
  // The shape names x/foo and the document after it names x/bar, so a list tells them apart.
  const around = (p: string) => [`${p}\nb: ${PROBE}\n`, `${p}\n---\nb: ${PROBE}\n`, `a: 1\n---\n${p}\n---\nb: ${PROBE}\n`];
  // (N1) The split form is in flow context only: a tag on one block line and another on the next is
  // a gap that the scanner documents.
  const twoTags = ["!other !bwref", "!bwref !bwref", "!<!bwref> !other", "!other !<!bwref>", "!x !y !bwref", "&e !other !bwref", "!other &e !bwref", "!other !bwref &e"];
  const tagPlaces = (s: string) => [`a: ${s} x/foo`, `- ${s} x/foo`, `[${s} x/foo]`, `{a: ${s} x/foo}`, `a: [${s} x/foo]`, `? ${s} x/foo\n: 1`, `${s} x/foo`];
  for (const s of twoTags) for (const p of tagPlaces(s)) for (const d of around(p)) docs.add(d);
  for (const p of ["[!other\n !bwref x/foo]", "{a: !other\n !bwref x/foo}", "a: [!other\n !bwref x/foo]", "[!other, !bwref x/foo]", "{!other a: !bwref x/foo}"])
    for (const d of around(p)) docs.add(d);
  // (N1) Controls: one tag per node, and tags on different nodes.
  for (const p of ["a: !other\nb: !bwref x/foo", "a: !other\n!u k: !bwref x/foo", "- !other\n- !bwref x/foo", "? !other a\n: !bwref x/foo", "a: &e !bwref x/foo", "a: !bwref &e x/foo", "!u k: !bwref x/foo"])
    for (const d of around(p)) docs.add(d);
  // (N2) A `%` where a token starts.
  const percent = [
    'a: !bwref "x/foo"\n  %YAML 1.1',
    'a: !bwref "x/foo"\n %TAG !x! !bw',
    "- !bwref x/foo\n- %x",
    "[!bwref x/foo,\n %b]",
    'a: !bwref "x/foo"\n\t%x',
    "a: !bwref x/foo\nb: %x",
    "a: [!bwref x/foo, %x]",
    "a: {b: !bwref x/foo, c: %x}",
    "a: !bwref x/foo\n? %x\n: 1",
    // Controls: a `%` that continues a plain scalar, sits in a scalar's text or follows a comment.
    "a: x\n %y\nb: !bwref x/foo",
    "[a\n %b, !bwref x/foo]",
    "a: |\n  %x\nb: !bwref x/foo",
    'a: "q\n %x"\nb: !bwref x/foo',
    "a: !bwref x/foo # %x",
    "a: x%y\nb: !bwref x/foo",
  ];
  for (const p of percent) for (const d of around(p)) docs.add(d);
  // (N3) A marker in a quoted scalar.
  for (const q of ['"', "'"]) {
    for (const m of ["---", "...", "--- c", "---\t", "... c", "...\t"]) {
      for (const p of [`a: !bwref x/foo\nb: ${q}x\n${m}\ny${q}`, `a: [!bwref x/foo, ${q}x\n${m}\ny${q}]`, `a: !bwref x/foo\nb: ${q}${m}\ny${q}`, `a: ${q}x\n${m}\ny${q}\nb: !bwref x/foo`])
        for (const d of around(p)) docs.add(d);
    }
    // Controls: an indented marker, text after the dashes and a short run of them.
    for (const m of [" ---", "  ...", "---x", "...x", "--", "-- -"])
      for (const d of around(`a: !bwref x/foo\nb: ${q}x\n${m}\ny${q}`)) docs.add(d);
  }
}

// (J) The line ends of (G), (H), (I), (K), (L), (M) and (N) as a CRLF, a lone carriage return, U+0085, U+2028 and U+2029.
// go-yaml counts each as a line break, and a CRLF as one.
for (const d of [...docs].slice(firstOfG)) for (const b of ["\r\n", "\r", "\u0085", " ", " "]) docs.add(d.replaceAll("\n", b));

// (F) Every three lines from a pool of lines that change the state, then a line with a tag. The pool
// holds a key with each kind of value start, a header, a blank line, a comment and a continuation.
{
  const pool = [
    "a:", "a: |", "a: >2", "- a:", "&e:", "? &e", "? k", ":", ": x", `  :${PROBE}`, '  "k"', '"k":', "a: {? \"k\"",
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
