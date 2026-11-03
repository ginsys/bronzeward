// Lists the errors the `yaml` package (web's parser) reports for streams go-yaml accepts: the
// limit of alternative 3a (parse, fall back on error).
//
// Usage: node yaml-errors.mjs <work dir>
//
// <work dir> is what run/all wrote (sets/ and oracle/). Prints, per set, how many go-yaml-valid
// streams the package refuses, then the error codes with a count and two shortest examples each.
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";

const { parseAllDocuments } = createRequire(new URL("../../web/package.json", import.meta.url))("yaml");
const work = process.argv[2];
if (work === undefined) {
  console.error("usage: node yaml-errors.mjs <work dir>");
  process.exit(2);
}
const sets = ["s0-indicators", "s1-explicit-key", "s2-context", "s3-review", "s4-families", "s5-cases", "s6-random", "s7-colon-flow"];
const byCode = new Map();
for (const set of sets) {
  const docs = JSON.parse(readFileSync(`${work}/sets/${set}.json`, "utf8")).map((e) => (typeof e === "string" ? e : e.doc));
  const oracle = JSON.parse(readFileSync(`${work}/oracle/${set}.json`, "utf8"));
  let valid = 0;
  let refused = 0;
  docs.forEach((doc, i) => {
    if (oracle[i].err !== "") return;
    valid++;
    const errors = parseAllDocuments(doc, { uniqueKeys: false }).flatMap((d) => d.errors);
    if (errors.length === 0) return;
    refused++;
    const code = [...new Set(errors.map((e) => e.code))].sort().join("+");
    const entry = byCode.get(code) ?? { count: 0, sets: new Set(), examples: [] };
    entry.count++;
    entry.sets.add(set);
    entry.examples.push({ doc, message: errors[0].message.split("\n")[0] });
    byCode.set(code, entry);
  });
  console.log(`${set}: go-yaml accepts ${valid}, the yaml package refuses ${refused}`);
}
console.log();
for (const [code, e] of [...byCode].sort((a, b) => b[1].count - a[1].count)) {
  console.log(`${code}: ${e.count} streams, in ${[...e.sets].join(", ")}`);
  e.examples.sort((a, b) => a.doc.length - b.doc.length);
  for (const x of e.examples.slice(0, 2)) console.log(`  ${JSON.stringify(x.doc)}\n    ${x.message}`);
}
