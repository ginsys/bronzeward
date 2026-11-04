// The case table of the tag handle report (ginsys/bronzeward#603): for each case, what go-yaml
// v3.0.5 lists, whether the server accepts the document, and what the scanner lists.
//
// Usage: node tag-handles.mjs <cases.json> <work dir> <pre-602 commit> <table.md>
//
// <cases.json> is sets/tag-handle-cases.json. <work dir> is an empty directory outside the checkout.
// The scanners are web/src/position.ts of this checkout ("main") and the same file at <pre-602
// commit> ("before"), which the run copies beside web's sources and removes after. Needs go, node 24
// and web/node_modules. go-yaml is the oracle of this directory; the server is serverprobe/, which
// asks internal/ingest.Stored about the document with every name go-yaml reads declared.
import { execFileSync } from "node:child_process";
import { readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const [casesPath, workArg, before, tablePath] = process.argv.slice(2);
if (tablePath === undefined) {
  console.error("usage: node tag-handles.mjs <cases.json> <work dir> <pre-602 commit> <table.md>");
  process.exit(2);
}
const here = fileURLToPath(new URL(".", import.meta.url));
const work = resolve(workArg);
const cases = JSON.parse(readFileSync(casesPath, "utf8"));

const run = (cwd, args) => execFileSync("go", args, { cwd, stdio: ["ignore", "inherit", "inherit"] });
writeFileSync(join(work, "cases.json"), JSON.stringify(cases));
run(here, ["run", ".", join(work, "cases.json"), join(work, "oracle.json")]);
const oracle = JSON.parse(readFileSync(join(work, "oracle.json"), "utf8"));
writeFileSync(join(work, "probe-in.json"), JSON.stringify(cases.map((c, i) => ({ doc: c.doc, names: oracle[i].tags }))));
run(join(here, "serverprobe"), ["run", ".", join(work, "probe-in.json"), join(work, "probe-out.json")]);
const server = JSON.parse(readFileSync(join(work, "probe-out.json"), "utf8"));

const main = await import(pathToFileURL(join(here, "../../web/src/position.ts")).href);
const beforeFile = join(here, "../../web/src/zz-pre602-position.ts");
let old;
try {
  writeFileSync(beforeFile, execFileSync("git", ["show", `${before}:web/src/position.ts`], { cwd: here, encoding: "utf8", maxBuffer: 1 << 24 }));
  old = await import(pathToFileURL(beforeFile).href);
} finally {
  rmSync(beforeFile, { force: true });
}

const show = (l) => (l.length === 0 ? "none" : l.join(", "));
// How a scanner's list differs from go-yaml's: = (same), lost, extra, or both.
const diff = (got, want) => {
  const lost = want.some((t) => !got.includes(t));
  const extra = got.some((t) => !want.includes(t));
  return lost && extra ? "lost+extra" : lost ? "lost" : extra ? "extra" : "=";
};
const rows = ["| case | source | go-yaml | server | main | before |", "|---|---|---|---|---|---|"];
const tally = { accepted: { lost: 0, extra: 0, same: 0 }, "server-refused": { lost: 0, extra: 0, same: 0 }, "parse error": { lost: 0, extra: 0, same: 0 } };
cases.forEach((c, i) => {
  const o = oracle[i];
  const refused = o.err !== ""; // go-yaml parse error
  const want = o.tags;
  const m = main.references(c.doc);
  const b = old.references(c.doc);
  const cell = (got) => (refused ? (got.length > 0 ? `extra ${show(got)}` : "=") : `${show(got)} (${diff(got, want)})`);
  const k = refused ? "parse error" : server[i].accepted ? "accepted" : "server-refused";
  rows.push(`| ${c.name} | ${c.source} | ${refused ? "parse error" : show(want)} | ${refused ? "n/a" : k} | ${cell(m)} | ${cell(b)} |`);
  const d = refused ? (m.length > 0 ? "extra" : "=") : diff(m, want);
  for (const key of d === "=" ? ["same"] : d.split("+")) tally[k][key]++; // a lost+extra case counts under both
});
// The yaml version each side runs, and the last commit that changed each scanner.
const listed = (cwd) => execFileSync("go", ["list", "-m", "go.yaml.in/yaml/v3"], { cwd, encoding: "utf8" }).trim();
const lastChange = execFileSync("git", ["log", "-1", "--format=%h", "--", ":/web/src/position.ts"], { cwd: here, encoding: "utf8" }).trim();
const header = [
  `oracle module: ${listed(here)}`,
  `server probe module: ${listed(join(here, "serverprobe"))}`,
  `main = web/src/position.ts, last changed at ${lastChange}. before = web/src/position.ts at ${before}.`,
  "go-yaml parse error: go-yaml fails to parse the document. The server column is n/a, because the server reads no reference.",
  "server-refused: internal/ingest refuses a document that go-yaml parses.",
  "",
];
writeFileSync(tablePath, `${header.join("\n")}\n${rows.join("\n")}\n`);
console.log(rows.join("\n"));
console.log(JSON.stringify(tally));
