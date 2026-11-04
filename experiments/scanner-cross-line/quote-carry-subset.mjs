// The failure cases of issue 592: the valid documents of the #560 set (S0) that the plain quote
// carry (candidates/c0q-quote-carry.ts) gets wrong and the scanner on main gets right. It prints
// each document with its id, then how many of them every other candidate reads as go-yaml does.
//
// Usage: node quote-carry-subset.mjs <work dir>
//
// Reads <work dir>/report/*.s0-indicators.json, written by run/all.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const work = process.argv[2];
if (work === undefined) {
  console.error("usage: node quote-carry-subset.mjs <work dir>");
  process.exit(2);
}
const set = "s0-indicators";
const read = (c) => JSON.parse(readFileSync(join(work, "report", `${c}.${set}.json`), "utf8"));
const carry = read("c0q-quote-carry");
const broken = carry.rows.filter((r) => r.newVsMain);
const ids = new Set(broken.map((r) => r.id));
console.log(`Valid documents of S0: ${carry.counts.valid}. The plain quote carry (c0q-quote-carry) gets ${broken.length} of them wrong that main gets right.`);
console.log("id, then the document as a JSON string, then the names go-yaml lists:");
for (const r of broken) console.log(`${r.id}\t${JSON.stringify(r.doc)}\t${JSON.stringify(r.oracle)}`);
console.log("");
console.log(`Each candidate over these ${broken.length} documents (equal = lists the names go-yaml lists, no more and no fewer):`);
for (const f of readdirSync(join(work, "report")).sort()) {
  const m = /^(.*)\.s0-indicators\.json$/.exec(f);
  if (m === null) continue;
  const rows = read(m[1]).rows.filter((r) => ids.has(r.id));
  const lost = rows.filter((r) => r.kind.startsWith("lost")).length;
  const extra = rows.filter((r) => r.kind.endsWith("extra")).length;
  console.log(`${m[1]}: equal ${broken.length - rows.length}, lost ${lost}, extra ${extra}`);
}
