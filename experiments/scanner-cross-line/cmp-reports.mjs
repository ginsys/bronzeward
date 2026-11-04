// Compares two reports of cmp.mjs for the same set and oracle: one for a scanner before a change,
// one for the scanner after it. It prints the counts of both and the ids that changed.
//
// Usage: node cmp-reports.mjs <before report.json> <after report.json>
//
// Both reports must come from the same set and the same oracle, and from a cmp.mjs that writes
// `refusedListedIds` (the counts must also agree on `inputs` and `valid`). Output:
//   - the counts of both reports side by side, with the difference;
//   - lost-fixed: ids lost before and not lost after;
//   - lost-new: ids lost after and not before (a regression: expect none);
//   - extra-new and extra-fixed: the same for the extra names;
//   - refusedListed-new and refusedListed-fixed: ids that go-yaml refuses and the scanner lists a
//     name in, after and not before, and the reverse. They are not an acceptance count; the
//     acceptance is on valid documents.
//   - lost-names-new and extra-names-new: (id, name) pairs that a report lists after and not before.
//     An id can be lost or extra before and after with a different set of names; these lines show a
//     name added to such a document.
// The lines by id and kind are the unit that run/tag-causes counts. The tool exits 1 when lost-new,
// extra-new, lost-names-new or extra-names-new is not empty, 2 on a bad call or reports that do not
// match, and 0 otherwise.
import { readFileSync } from "node:fs";

const [beforePath, afterPath] = process.argv.slice(2);
if (afterPath === undefined) {
  console.error("usage: node cmp-reports.mjs <before report.json> <after report.json>");
  process.exit(2);
}
const read = (p) => JSON.parse(readFileSync(p, "utf8"));
const before = read(beforePath);
const after = read(afterPath);
if (before.refusedListedIds === undefined || after.refusedListedIds === undefined) {
  console.error("a report has no refusedListedIds: write it with the cmp.mjs of this directory");
  process.exit(2);
}
for (const k of ["inputs", "valid"]) {
  if (before.counts[k] !== after.counts[k]) {
    console.error(`the reports differ in ${k}: ${before.counts[k]} and ${after.counts[k]}`);
    process.exit(2);
  }
}

const ids = (report, pick) => new Set(report.rows.filter(pick).map((r) => r.id));
const lost = (r) => r.kind.includes("lost");
const extra = (r) => r.kind.includes("extra");
const minus = (a, b) => [...a].filter((id) => !b.has(id)).sort((x, y) => x - y);

const diffs = {
  "lost-fixed": minus(ids(before, lost), ids(after, lost)),
  "lost-new": minus(ids(after, lost), ids(before, lost)),
  "extra-fixed": minus(ids(before, extra), ids(after, extra)),
  "extra-new": minus(ids(after, extra), ids(before, extra)),
  "refusedListed-fixed": minus(new Set(before.refusedListedIds), new Set(after.refusedListedIds)),
  "refusedListed-new": minus(new Set(after.refusedListedIds), new Set(before.refusedListedIds)),
};

for (const k of Object.keys(before.counts)) {
  // newLost and newExtra compare with the scanner of this checkout, not with the "before" scanner,
  // so they say nothing about a before/after pair; the lost-new and extra-new lines below do.
  if (k === "msPer10k" || k === "newLost" || k === "newExtra") continue;
  const b = before.counts[k];
  const a = after.counts[k];
  console.log(`${k.padEnd(14)} before ${String(b).padStart(8)}  after ${String(a).padStart(8)}  change ${a - b >= 0 ? "+" : ""}${a - b}`);
}
for (const [name, list] of Object.entries(diffs)) {
  console.log(`${name}: ${list.length}${list.length > 0 ? ` ids ${list.join(",")}` : ""}`);
}

// The (id, name) pairs a report holds: "L" for a name the oracle lists and the scanner lacks, "E"
// for a name the scanner lists and the oracle does not. A row holds one document that differs.
const pairs = (report) => {
  const out = new Set();
  for (const row of report.rows) {
    if (!Array.isArray(row.oracle) || !Array.isArray(row.candidate)) continue;
    for (const n of row.oracle) if (!row.candidate.includes(n)) out.add(`${row.id}\tL\t${n}`);
    for (const n of row.candidate) if (!row.oracle.includes(n)) out.add(`${row.id}\tE\t${n}`);
  }
  return out;
};
const pairsBefore = pairs(before);
const newPairs = [...pairs(after)].filter((p) => !pairsBefore.has(p));
const names = {
  "lost-names-new": newPairs.filter((p) => p.split("\t")[1] === "L").map((p) => p.replace(/\t.\t/, ":")),
  "extra-names-new": newPairs.filter((p) => p.split("\t")[1] === "E").map((p) => p.replace(/\t.\t/, ":")),
};
for (const [name, list] of Object.entries(names)) {
  console.log(`${name}: ${list.length}${list.length > 0 ? ` ${list.slice(0, 20).join(",")}` : ""}`);
}
process.exit(diffs["lost-new"].length > 0 || diffs["extra-new"].length > 0 || newPairs.length > 0 ? 1 : 0);
