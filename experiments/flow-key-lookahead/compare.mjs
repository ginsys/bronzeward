// Usage: node compare.mjs <docs.json> <real.oracle.json> <patched.oracle.json> [<instr.oracle.json>] [--show N]
// Compares model.mjs with go-yaml v3.0.5 (real) and with the same go-yaml with the guarded delete
// (patched). The documents that the bug decides are those that real refuses and patched accepts.
// The model passes when both hold: it reports "misplaced" for every such document (none missed),
// and it reports "misplaced" only for documents that real refuses (no false refusal). A document
// that the patched go-yaml also refuses for another reason (for example "[,]") may be "misplaced":
// the bug fires there too, and the real run refuses it either way.
//
// The optional instrumented run (unpatched go-yaml with counters) gives the event itself: "consumed"
// counts head tokens that go-yaml consumed while a simple key still stood on them, "misplaced"
// counts inserts at a negative queue position. A document with consumed > 0 is one the bug hit,
// whether or not go-yaml refuses it for another reason first.
//
// Output: one JSON line of counts, then up to N differing documents (default 10).
import { readFileSync } from "node:fs";
import { model } from "./model.mjs";

const args = process.argv.slice(2);
const showAt = args.indexOf("--show");
const show = showAt >= 0 ? Number(args[showAt + 1]) : 10;
const [docsFile, realFile, patchedFile, instrFile] = args.filter((a, i) => !a.startsWith("--") && args[i - 1] !== "--show");
const instr = instrFile ? JSON.parse(readFileSync(instrFile, "utf8")) : null;
const docs = JSON.parse(readFileSync(docsFile, "utf8")).map((d) => (typeof d === "string" ? d : d.doc));
const real = JSON.parse(readFileSync(realFile, "utf8"));
const patched = JSON.parse(readFileSync(patchedFile, "utf8"));
if (real.length !== docs.length || patched.length !== docs.length) throw new Error("length mismatch");

const count = {};
const bump = (k) => (count[k] = (count[k] ?? 0) + 1);
const differ = [];
const guardBad = [];
docs.forEach((doc, i) => {
  const r = real[i].err ? "REF" : "ACC";
  const p = patched[i].err ? "REF" : "ACC";
  const bug = r === "REF" && p === "ACC";
  const m = model(doc);
  bump(`real:${r} patched:${p}`);
  if (r === "ACC" && real[i].tags.length > 0) bump("control: accepted with a name");
  bump(`model:${m.verdict}`);
  const predicted = m.verdict === "misplaced";
  if (bug) bump("bug-decided");
  const missed = bug && !predicted;
  const falseRefusal = predicted && r === "ACC";
  if (predicted && r === "REF" && p === "REF") bump("misplaced, other cause refuses too");
  if (!missed && !falseRefusal) bump("agree");
  else {
    bump("differ");
    bump(missed ? "missed" : "false refusal");
    differ.push({ i, doc, real: real[i].err, patched: patched[i].err, model: m });
  }
  if (instr) {
    const hit = instr[i].consumed > 0;
    bump(`instr hit:${hit} model:${predicted ? "misplaced" : m.verdict}`);
    if (instr[i].misplaced > 0 && !predicted) bump("instr misplaced but model not");
    if (hit && !predicted && r === "ACC") bump("instr hit, real accepts");
  }
  if (model(doc, { guardDelete: true }).verdict === "misplaced") guardBad.push(i);
  // Refused for another reason in both oracles: the model's claim does not cover them.
  if (r === "REF" && p === "REF") bump(`other-refusal, model:${m.verdict}`);
});
count["patched model misplaced"] = guardBad.length;
console.log(JSON.stringify({ total: docs.length, ...count }));
for (const d of differ.slice(0, show)) console.log(JSON.stringify(d));
