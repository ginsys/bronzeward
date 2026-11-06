// Judges two scanners beside the go-yaml oracle over the root-scalar sweep set.
// Usage: node judge-root-scalar.mjs <set.json> <oracle.json> <mainDir> <headDir> [--rows]
// <mainDir> and <headDir> each hold web/src/position.ts (or a copy named position.ts).
// An oracle row "lists" the names in `tags`. A row where go-yaml reads the tagged value as a longer
// scalar has `bad` set and no tag, and a refused document has `err` set; both list nothing.
import { readFileSync } from "node:fs";

const [setFile, oracleFile, mainDir, headDir, flag] = process.argv.slice(2);
const docs = JSON.parse(readFileSync(setFile, "utf8"));
const want = JSON.parse(readFileSync(oracleFile, "utf8"));
const names = async (dir) => {
  const m = await import(`${dir}/position.ts`);
  return (doc) => m.references(doc).map((r) => r.name ?? r);
};
const main = await names(mainDir);
const head = await names(headDir);
const esc = (s) => s.replace(/\t/g, "\\t").replace(/\n/g, "\\n").replace(/ /g, "\\u00a0");
const key = (a) => JSON.stringify([...a].sort());

const rows = docs.map((d, i) => {
  const o = want[i].tags ?? [];
  return { name: d.name, doc: d.doc, oracle: o, err: want[i].err, main: main(d.doc), head: head(d.doc) };
});

// A document go-yaml refuses (`err`) reads no reference on the server, so a name the scanner lists for it
// is "refused-listed", which is no error. Only the valid documents count as extra or lost.
const tally = { rows: rows.length, valid: 0, refused: 0, mainOk: 0, headOk: 0, mainExtra: 0, headExtra: 0, mainLost: 0, headLost: 0, headNewExtra: 0, headNewLost: 0, mainRefusedListed: 0, headRefusedListed: 0 };
const group = new Map();
for (const r of rows) {
  const refused = !!r.err;
  const mo = key(r.main) === key(r.oracle);
  const ho = key(r.head) === key(r.oracle);
  const mExtra = !refused && r.main.some((n) => !r.oracle.includes(n));
  const hExtra = !refused && r.head.some((n) => !r.oracle.includes(n));
  const mLost = !refused && r.oracle.some((n) => !r.main.includes(n));
  const hLost = !refused && r.oracle.some((n) => !r.head.includes(n));
  tally.valid += !refused;
  tally.refused += refused;
  tally.mainRefusedListed += refused && r.main.length > 0;
  tally.headRefusedListed += refused && r.head.length > 0;
  tally.mainOk += !refused && mo;
  tally.headOk += !refused && ho;
  tally.mainExtra += mExtra;
  tally.headExtra += hExtra;
  tally.mainLost += mLost;
  tally.headLost += hLost;
  tally.headNewExtra += hExtra && !mExtra;
  tally.headNewLost += hLost && !mLost;
  r.verdict = refused ? "refused" : `${mo ? "main-ok" : mExtra ? "main-extra" : "main-lost"}/${ho ? "head-ok" : hExtra ? "head-extra" : "head-lost"}`;
  const [c, p, s, n, t] = r.name.split(".");
  const k = `${c}.${p}.${s}.${n}`;
  const g = group.get(k) ?? [];
  g.push(`${t}:${r.verdict}`);
  group.set(k, g);
}
console.log(JSON.stringify(tally));
if (flag === "--rows") {
  for (const r of rows) {
    console.log(
      [r.name.padEnd(36), esc(r.doc).padEnd(40), "oracle", JSON.stringify(r.oracle), r.err ? "ERR" : "", "main", JSON.stringify(r.main), "head", JSON.stringify(r.head), r.verdict].join(" "),
    );
  }
}
