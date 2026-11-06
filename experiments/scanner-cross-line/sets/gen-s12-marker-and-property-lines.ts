// ginsys/bronzeward#616: the lines BEFORE a plain `!bwref` value that the scanner reads to find the holder of the value,
// and the line after it. Two families, a deterministic cross product. Synthetic input only. Usage: node gen-s12-marker-and-property-lines.ts <out.json>
// The set is not in sets/MANIFEST and run/gen-sets does not write it: it is made on demand, and the same input gives the same file.
// Names have five dot parts, shape.lead.properties.offset-line-gap.text, as S10 and S11 have, so that
// judge-s10.mjs reads the set.
//
// Family A, "marker": a root scalar after a document marker line.
//   lead  : "<bom>-<eol>-<marker>-<tag lead>": a byte order mark or none at the start of the stream; LF or CRLF line
//           ends in the whole document; the marker line `---` or `--- &e` (a property on the marker line, the tag on
//           the next line); the tag line starts with 0, 1 or 2 spaces;
//   properties: the tag, or an anchor and then the tag;
//   next line : at column 0, 1 or 2, made of spaces, of spaces then U+00A0 or of spaces then a tab (column 0 with
//           spaces only is an empty line and is left out), directly after ("adj") or after a blank line ("gap");
//   text  : "bar", "- y", "y: v".
// Family B, "pprop": a parent line, then a line that holds only a node property (an anchor), then the tag.
//   shape : the parent: "key" (`k:`), "dash" (`-`) or "compact" (`- k:`);
//   lead  : the parent line starts with 0 or 1 spaces ("sp0", "sp1"), or, for a key, with U+00A0 ("nb": text at
//           column 0);
//   the anchor-only line is 1 or 2 columns past the parent column ("pl1", "pl2"); the tag line is as far in as the
//   anchor line ("tl0") or one more ("tl1");
//   next line : column of the tag line -1, 0 or +1, made of spaces, spaces and U+00A0, or spaces and a tab, adj or gap;
//   text  : "bar", "- y", "y: v".
import { writeFileSync } from "node:fs";

const out = process.argv[2];
if (!out) throw new Error("usage: node gen-s12-marker-and-property-lines.ts <out.json>");
const NB = " ";
const docs: { name: string; doc: string }[] = [];
const kinds: [string, (col: number) => string][] = [
  ["sp", (c) => " ".repeat(c)],
  ["nb", (c) => `${" ".repeat(c)}${NB}`],
  ["tb", (c) => `${" ".repeat(c)}\t`],
];
const gaps: [string, string][] = [["adj", ""], ["gap", "\n"]];
const texts: [string, string][] = [["word", "bar"], ["dash", "- y"], ["key", "y: v"]];

// Family A
for (const [bn, bom] of [["nobom", ""], ["bom", "﻿"]] as const)
  for (const [en, eol] of [["lf", "\n"], ["crlf", "\r\n"]] as const)
    for (const [mn, marker] of [["m0", "---"], ["m1", "--- &e"]] as const)
      for (const tl of [0, 1, 2])
        for (const [pn, p] of [["tag", "!bwref "], ["anchor-tag", "&f !bwref "]] as const)
          for (const nc of [0, 1, 2])
            for (const [kn, kf] of kinds) {
              if (nc === 0 && kn === "sp") continue;
              for (const [gn, gap] of gaps)
                for (const [tn, t] of texts) {
                  // m1 puts an anchor on the marker line; the tag line may not carry a second anchor
                  if (mn === "m1" && pn === "anchor-tag") continue;
                  const lines = [`${bom}${marker}`, `${" ".repeat(tl)}${p}x/foo`, ...(gap ? [""] : []), `${kf(nc)}${t}`];
                  docs.push({
                    name: ["marker", [bn, en, mn, `tl${tl}`].join("-"), pn, [`c${nc}`, kn, gn].join("-"), tn].join("."),
                    doc: lines.join(eol) + eol,
                  });
                }
            }

// Family B
const parents: [string, string, number, string[]][] = [
  ["key", "k:", 0, ["sp0", "sp1", "nb"]],
  ["dash", "-", 0, ["sp0", "sp1"]],
  ["compact", "- k:", 0, ["sp0", "sp1"]],
];
for (const [sn, ptext, , leadNames] of parents)
  for (const ln of leadNames) {
    const lead = ln === "nb" ? NB : ln === "sp1" ? " " : "";
    const pcol = ln === "sp1" ? 1 : 0; // the NBSP-led key stands at column 0, as text
    for (const pl of [1, 2])
      for (const tl of [0, 1]) {
        const acol = pcol + pl;
        const tcol = acol + tl;
        for (const off of [-1, 0, 1])
          for (const [kn, kf] of kinds)
            for (const [gn, gap] of gaps)
              for (const [tn, t] of texts) {
                const nc = tcol + off;
                if (nc < 0) continue;
                const lines = [`${lead}${ptext}`, `${" ".repeat(acol)}&e`, `${" ".repeat(tcol)}!bwref x/foo`, ...(gap ? [""] : []), `${kf(nc)}${t}`];
                docs.push({
                  name: [`pprop-${sn}`, ln, "tag", [`pl${pl}`, `tl${tl}`, `o${off < 0 ? "m1" : off === 0 ? "0" : "p1"}`, kn, gn].join("-"), tn].join("."),
                  doc: lines.join("\n") + "\n",
                });
              }
      }
  }
writeFileSync(out, JSON.stringify(docs));
console.log(`${docs.length} documents written to ${out}`);
