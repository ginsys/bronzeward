// ginsys/bronzeward#616: the line after a plain `!bwref` value, with the key column and the column of that
// line varied together, and the controls for it. A deterministic cross product, no random choice. Synthetic
// input only. Usage: node gen-s11-indent-offsets.ts <out.json>
// It writes a JSON array of {name, doc} for the go-yaml oracle. The cross product is:
//   shape     : where the tag stands. "key" (`k: `), "key-above" (`k:` on the line above, the tag alone on its
//               line), "dash" (`- `), "compact" (`- k: `), "root" (no key, the tag first on its line) and
//               "marker" (`--- `, the tag on the marker line);
//   lead      : the text before the node on its first line: 0, 1, 2 or 3 spaces ("sp0".."sp3"), or one U+00A0
//               ("nb": text at column 0, so the key stands at column 0) or a byte order mark ("bom", only at
//               the start of the stream). "marker" has "sp0" only;
//   properties: the tag only, or an anchor and then the tag;
//   next line : its column is the node column plus an offset of -1, 0, +1 or +2 ("om1", "o0", "op1", "op2";
//               a negative column is left out), made of spaces ("sp"), of spaces and then U+00A0 before the
//               text ("nb") or of spaces and then a tab ("tb"), and either directly after the tag line ("adj")
//               or after a blank line ("gap");
//   text      : "bar", "- y", "y: v".
// The node column is the column of the key, the dash, or the tag: the lead, in YAML blanks. The name of a
// document has five parts, shape.lead.properties.offset-line-gap.text, so that judge-s10.mjs reads this set as
// it reads S10. The go-yaml oracle decides what each document lists. The set is not in sets/MANIFEST and
// run/gen-sets does not write it: it is made on demand, and the same input gives the same file.
import { writeFileSync } from "node:fs";

const out = process.argv[2];
if (!out) throw new Error("usage: node gen-s11-indent-offsets.ts <out.json>");

const NB = "\u00a0";
const leads: [string, string, number][] = [
  ["sp0", "", 0],
  ["sp1", " ", 1],
  ["sp2", "  ", 2],
  ["sp3", "   ", 3],
  ["nb", NB, 0],
  ["bom", "\ufeff", 0],
];
// Each shape is [name, the text before the tag, given the lead and the node column]. For "key-above" the
// node is the key on the line above, and the tag stands alone on the next line, two columns further in.
const shapes: [string, (lead: string, col: number) => string][] = [
  ["key", (lead) => `${lead}k: `],
  ["key-above", (lead, col) => `${lead}k:\n${" ".repeat(col + 2)}`],
  ["dash", (lead) => `${lead}- `],
  ["compact", (lead) => `${lead}- k: `],
  ["root", (lead) => lead],
  ["marker", () => "--- "],
];
const props: [string, string][] = [
  ["tag", "!bwref "],
  ["anchor-tag", "&e !bwref "],
];
const offsets: [string, number][] = [
  ["om1", -1],
  ["o0", 0],
  ["op1", 1],
  ["op2", 2],
];
const lines: [string, (col: number) => string][] = [
  ["sp", (col) => " ".repeat(col)],
  ["nb", (col) => `${" ".repeat(col)}${NB}`],
  ["tb", (col) => `${" ".repeat(col)}\t`],
];
const gaps: [string, string][] = [
  ["adj", ""],
  ["gap", "\n"],
];
const texts: [string, string][] = [
  ["word", "bar"],
  ["dash", "- y"],
  ["key", "y: v"],
];

const docs: { name: string; doc: string }[] = [];
for (const [sn, head] of shapes)
  for (const [ln, lead, col] of leads) {
    if (sn === "marker" && ln !== "sp0") continue;
    for (const [pn, p] of props)
      for (const [on, off] of offsets)
        for (const [cn, cl] of lines)
          for (const [gn, gap] of gaps)
            for (const [tn, t] of texts) {
              const c = col + off;
              if (c < 0) continue;
              const first = `${head(lead, col)}${p}x/foo`;
              docs.push({ name: [sn, ln, pn, [on, cn, gn].join("-"), tn].join("."), doc: `${first}\n${gap}${cl(c)}${t}\n` });
            }
  }
writeFileSync(out, JSON.stringify(docs));
console.log(`${docs.length} documents written to ${out}`);
