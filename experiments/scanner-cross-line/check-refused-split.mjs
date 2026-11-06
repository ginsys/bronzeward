// Checks that every document in the "refusedListed-new" line of a cmp-reports.mjs report is a document
// go-yaml refuses. The scanner lists a name for such a document and the server lists none, so it is not an
// error. A row in a document that go-yaml accepts would be one (it would also show in extra-new).
//
//   node check-refused-split.mjs <run dir> [set ...]
//
// <run dir> holds, for each set, reports-<set>.log (the output of cmp-reports.mjs) and <set>.oracle.json
// (the output of the oracle on the set). Sets default to s5 s8 s9 s10 s11 s12. It prints one line per set and
// exits 1 if any listed document is one that go-yaml accepts, or if a report or oracle output is missing.
import { readFileSync } from "node:fs";

const [dir, ...rest] = process.argv.slice(2);
if (!dir) {
  console.error("usage: node check-refused-split.mjs <run dir> [set ...]");
  process.exit(2);
}
const sets = rest.length ? rest : ["s5", "s8", "s9", "s10", "s11", "s12"];
let bad = 0;
for (const s of sets) {
  let log, oracle;
  try {
    log = readFileSync(`${dir}/reports-${s}.log`, "utf8");
    oracle = JSON.parse(readFileSync(`${dir}/${s}.oracle.json`, "utf8"));
  } catch (e) {
    console.log(`${s}: cannot read the report or the oracle output (${e.code ?? e.message})`);
    bad = 1;
    continue;
  }
  const m = log.match(/^refusedListed-new: (\d+)(?: ids (.*))?$/m);
  if (!m) {
    console.log(`${s}: no refusedListed-new line in the report`);
    bad = 1;
    continue;
  }
  const n = Number(m[1]);
  const ids = m[2] ? m[2].split(",").map(Number) : [];
  const valid = oracle.filter((o) => !o.err).length;
  const inValid = ids.filter((i) => !oracle[i] || !oracle[i].err);
  const ok = ids.length === n && inValid.length === 0;
  if (!ok) bad = 1;
  console.log(
    `${s}: ${oracle.length} documents, ${valid} accepted, ${oracle.length - valid} refused by go-yaml; ` +
      `refusedListed-new ${n} (ids read ${ids.length}); in an accepted document: ${inValid.length}${ok ? "" : "  FAIL"}`,
  );
}
process.exit(bad);
