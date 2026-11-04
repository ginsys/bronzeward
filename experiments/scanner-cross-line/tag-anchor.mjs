// Splits the documents that the server accepts and the scanner loses a name in by two causes: the
// verbatim spelling of the tag, and an anchor between the tag and its value (ginsys/bronzeward#603).
// A document can have both.
//
// Usage: node tag-anchor.mjs <report.json> <work dir>
//
// <report.json> is the report of cmp.mjs for the scanner on main, as run/tag-causes leaves it in
// its work dir (s8.main.report.json). <work dir> is a directory outside the checkout. The server
// answers through serverprobe/ with every name go-yaml reads declared. This tool reads the report
// only. It runs no sweep.
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const [reportPath, workArg] = process.argv.slice(2);
if (workArg === undefined) {
  console.error("usage: node tag-anchor.mjs <report.json> <work dir>");
  process.exit(2);
}
const here = fileURLToPath(new URL(".", import.meta.url));
const work = resolve(workArg);
const lost = JSON.parse(readFileSync(reportPath, "utf8")).rows.filter((r) => r.kind.includes("lost"));

writeFileSync(join(work, "anchor-in.json"), JSON.stringify(lost.map((r) => ({ doc: r.doc, names: r.oracle }))));
execFileSync("go", ["run", ".", join(work, "anchor-in.json"), join(work, "anchor-out.json")], { cwd: join(here, "serverprobe"), stdio: ["ignore", "inherit", "inherit"] });
const server = JSON.parse(readFileSync(join(work, "anchor-out.json"), "utf8"));

const verbatim = (d) => d.includes("!<");
// A tag (verbatim or plain), blanks, then an anchor name: `!bwref &e x/foo`, `!<!bwref> &e x/foo`.
const anchored = (d) => /(?:!<[^>]*>|!bwref)[ \t]+&[0-9A-Za-z_-]+/.test(d);
const cells = new Map();
lost.forEach((r, i) => {
  const key = `${server[i].accepted ? "server accepts" : "server refuses"} | ${verbatim(r.doc) ? "verbatim" : "not verbatim"} | ${anchored(r.doc) ? "anchor after tag" : "no anchor after tag"}`;
  cells.set(key, (cells.get(key) ?? 0) + 1);
});
console.log(`lost documents: ${lost.length}`);
console.log("| server | spelling | anchor | documents |\n|---|---|---|---|");
for (const [key, n] of [...cells].sort()) console.log(`| ${key.split(" | ").join(" | ")} | ${n} |`);
