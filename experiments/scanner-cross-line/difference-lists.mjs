// Writes the full list of differing documents of each report, one line per document.
//
// Usage: node difference-lists.mjs <work dir> <out dir>
//
// Reads <work dir>/report/<candidate>.<set>.json (written by cmp.mjs) and writes
// <out dir>/<candidate>.<set>.tsv for every report of a candidate that loses no name on any set
// and that has at least one row. A line has four
// tab-separated fields, with no document text:
//   id         the document's index in the set (counted from 0; sets/MANIFEST pins the sets)
//   kind       lost, extra, lost+extra, or failed
//   go-yaml    the names go-yaml lists, as a JSON array
//   candidate  the names the candidate lists, as a JSON array
// A fifth field, `new`, marks a document that the scanner on main gets right.
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const [work, out] = process.argv.slice(2);
if (out === undefined) {
  console.error("usage: node difference-lists.mjs <work dir> <out dir>");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
let files = 0;
let lines = 0;
const reports = readdirSync(join(work, "report")).sort().filter((f) => f.endsWith(".json"));
// Only a candidate that never loses a name (lost = 0 on every set) gets lists: it is the part of the
// gate that a design must meet, and the lists then hold its extras. The others lose names by the
// thousands (main loses 5232 on S1 alone): the table counts them, differences/ shows 20 each.
// c0q is the failure case of issue 592, not a candidate: its S0 documents are in
// quote-carry-subset.txt.
const lossy = new Set();
for (const f of reports) {
  if (JSON.parse(readFileSync(join(work, "report", f), "utf8")).counts.lost > 0) lossy.add(f.split(".")[0]);
}
for (const f of reports) {
  const candidate = f.split(".")[0];
  if (lossy.has(candidate) || candidate.startsWith("c0q-")) continue;
  const rows = JSON.parse(readFileSync(join(work, "report", f), "utf8")).rows;
  if (rows.length === 0) continue;
  const text = rows
    .map((r) => `${r.id}\t${r.kind}\t${JSON.stringify(r.oracle ?? [])}\t${JSON.stringify(r.candidate ?? [])}\t${r.newVsMain ? "new" : "-"}\n`)
    .join("");
  writeFileSync(join(out, f.replace(/\.json$/, ".tsv")), text);
  files++;
  lines += rows.length;
}
console.log(`${files} lists, ${lines} documents`);
