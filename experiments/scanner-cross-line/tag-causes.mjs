// Groups the documents where a scanner differs from go-yaml by the spelling of the tag, and splits
// them by whether the server accepts the document (ginsys/bronzeward#603).
//
// Usage: node tag-causes.mjs <report.json> <work dir>
//
// <report.json> is a report of cmp.mjs. <work dir> is a directory outside the checkout. The server
// answers through serverprobe/ with every name go-yaml reads declared. A document that go-yaml
// refuses is not in a report: the server refuses it too, and it reads no reference.
// A document with two spellings counts under the first that matches, in the order of SPELLINGS.
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

const [reportPath, workArg] = process.argv.slice(2);
if (workArg === undefined) {
  console.error("usage: node tag-causes.mjs <report.json> <work dir>");
  process.exit(2);
}
const here = new URL(".", import.meta.url).pathname;
const work = resolve(workArg);
const { counts, rows } = JSON.parse(readFileSync(reportPath, "utf8"));

const SPELLINGS = [
  ["verbatim, !<...>", (d) => d.includes("!<")],
  ["secondary handle, !!bwref", (d) => /!!bwref/.test(d)],
  ["named handle, !x!bwref", (d) => /![^\s!<]+!bwref/.test(d)],
  ["plain !bwref", (d) => d.includes("!bwref")],
];
const spelling = (d) => SPELLINGS.find(([, test]) => test(d))?.[0] ?? "other";
const directive = (d) => (/%(TAG|YAML)/.test(d) ? "directive" : "no directive");

writeFileSync(join(work, "causes-in.json"), JSON.stringify(rows.map((r) => ({ doc: r.doc, names: r.oracle }))));
execFileSync("go", ["run", ".", join(work, "causes-in.json"), join(work, "causes-out.json")], { cwd: join(here, "serverprobe"), stdio: ["ignore", "inherit", "inherit"] });
const server = JSON.parse(readFileSync(join(work, "causes-out.json"), "utf8"));

const cells = new Map();
rows.forEach((r, i) => {
  const key = `${spelling(r.doc)} | ${directive(r.doc)}`;
  const c = cells.get(key) ?? { lostAccepted: 0, lostRefused: 0, extraAccepted: 0, extraRefused: 0 };
  const side = server[i].accepted ? "Accepted" : "Refused";
  if (r.kind.includes("lost")) c[`lost${side}`]++;
  if (r.kind.includes("extra")) c[`extra${side}`]++;
  cells.set(key, c);
});
const out = ["| spelling | directive | lost, server accepts | lost, server refuses | extra, server accepts | extra, server refuses |", "|---|---|---|---|---|---|"];
for (const [key, c] of [...cells].sort()) {
  const [s, d] = key.split(" | ");
  out.push(`| ${s} | ${d} | ${c.lostAccepted} | ${c.lostRefused} | ${c.extraAccepted} | ${c.extraRefused} |`);
}
console.log(JSON.stringify(counts));
console.log(out.join("\n"));
