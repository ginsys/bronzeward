// Compares one scanner with go-yaml over one document set.
//
// Usage: node cmp.mjs <set.json> <oracle.json> <candidate module> <report.json>
//
// <set.json> is a JSON array of strings (or of objects with the stream in "doc");
// <oracle.json> is what `go run . <set.json> <oracle.json>` wrote for it; <candidate module> exports
// `references(text)` as web/src/position.ts does. The report holds the counts and every
// document where the candidate differs from go-yaml on a stream go-yaml accepts.
//
// Counts: valid = streams go-yaml accepts. equal = valid streams with the same references.
// lost = valid streams where the candidate lacks a reference go-yaml lists. extra = valid streams
// where it lists one go-yaml does not. A stream can count as both lost and extra. lost counts the
// references that are names (§5.1 grammar), which is all a scanner lists. lostFit is lost on the
// streams the server does not refuse for its !bwref text. badNameDocs counts the valid streams
// where go-yaml also reads a tagged value that is no name (the raw references no scanner lists;
// the server refuses these streams as a bad name). extraUnfit =
// the extra streams (with nothing lost) that the server refuses anyway for a bad name or for
// reserved text (the oracle's "unfit"), so the extra reference shows in a document that never
// gets in. newLost and newExtra count the valid streams where the candidate loses or lists a
// reference that go-yaml does not and the scanner on main gets right (a regression against main).
// refusedListed =
// streams go-yaml refuses where the candidate still lists a reference. msPer10k = the candidate's
// run time per 10000 documents (one run, wall clock; compare sizes, not digits).
// Each row of the report carries `id`, the document's index in the set (counted from 0).
// `refusedListedIds` holds the ids that refusedListed counts; cmp-reports.mjs compares two reports.
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const [setPath, oraclePath, candidatePath, reportPath] = process.argv.slice(2);
if (reportPath === undefined) {
  console.error("usage: node cmp.mjs <set.json> <oracle.json> <candidate module> <report.json>");
  process.exit(2);
}
const set = JSON.parse(readFileSync(setPath, "utf8")).map((e) => (typeof e === "string" ? e : e.doc));
const oracle = JSON.parse(readFileSync(oraclePath, "utf8"));
if (oracle.length !== set.length) throw new Error(`oracle has ${oracle.length} results for ${set.length} documents`);
const candidate = await import(pathToFileURL(resolve(candidatePath)).href);
const main = await import(new URL("candidates/c0-main.mjs", import.meta.url).href);

const counts = { inputs: set.length, valid: 0, equal: 0, lost: 0, lostFit: 0, badNameDocs: 0, extra: 0, extraUnfit: 0, newLost: 0, newExtra: 0, refusedListed: 0, failed: 0 };
const rows = [];
const refusedListedIds = [];
const started = performance.now();
const got = set.map((doc) => {
  try {
    return { tags: candidate.references(doc) };
  } catch (e) {
    return { error: String(e) };
  }
});
const ms = performance.now() - started;
set.forEach((doc, i) => {
  const want = oracle[i];
  const have = got[i];
  if (have.error !== undefined) {
    counts.failed++;
    rows.push({ id: i, kind: "failed", doc, error: have.error, oracle: want.tags, err: want.err });
    return;
  }
  if (want.err !== "") {
    if (have.tags.length > 0) {
      counts.refusedListed++;
      refusedListedIds.push(i);
    }
    return;
  }
  counts.valid++;
  const lost = want.tags.some((t) => !have.tags.includes(t));
  const extra = have.tags.some((t) => !want.tags.includes(t));
  if (!lost && !extra) counts.equal++;
  if (lost) counts.lost++;
  if (lost && !want.unfit) counts.lostFit++;
  if (want.bad.length > 0) counts.badNameDocs++;
  if (extra) counts.extra++;
  if (extra && !lost && want.unfit) counts.extraUnfit++;
  // Against the scanner on main: a document the candidate gets wrong and main gets right.
  const mainTags = main.references(doc);
  const newLost = lost && !want.tags.some((t) => !mainTags.includes(t));
  const newExtra = extra && !mainTags.some((t) => !want.tags.includes(t));
  if (newLost) counts.newLost++;
  if (newExtra) counts.newExtra++;
  if (lost || extra) rows.push({ id: i, kind: lost && extra ? "lost+extra" : lost ? "lost" : "extra", doc, candidate: have.tags, oracle: want.tags, unfit: want.unfit, newVsMain: newLost || newExtra });
});
counts.msPer10k = Math.round((ms / set.length) * 10000);
writeFileSync(reportPath, JSON.stringify({ counts, rows, refusedListedIds }, null, 1) + "\n");
console.log(JSON.stringify(counts));
