// Checks cmp-reports.mjs on small reports that stand for the defect found in review of
// ginsys/bronzeward#605: a name added to a document that already differed passed the exit code.
// Usage: node check-tools.mjs. It needs no oracle, no set and no web build: it writes small reports
// into a temporary directory, runs the tool, and exits 1 if one exit code is not the expected one.
// Every report holds a control document (id 2) that is the same before and after, so a tool that
// ignores its input cannot pass by luck.
import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = mkdtempSync(join(tmpdir(), "check-tools-"));
const tool = fileURLToPath(new URL("cmp-reports.mjs", import.meta.url));
let failed = 0;
let n = 0;

const exitOf = (before, after) => {
  const p = (r) => {
    const file = join(dir, `r${n++}.json`);
    writeFileSync(file, JSON.stringify(r));
    return file;
  };
  try {
    execFileSync("node", [tool, p(before), p(after)], { stdio: "pipe" });
    return 0;
  } catch (e) {
    return e.status;
  }
};
const report = (rows, counts = {}) => ({ counts: { inputs: 3, valid: 3, ...counts }, refusedListedIds: [], rows });
const extra = (id, candidate, oracle = []) => ({ id, kind: "extra", candidate, oracle });
const lost = (id, candidate, oracle) => ({ id, kind: "lost", candidate, oracle });
const control = extra(2, ["c/foo"]);
const expect = (name, got, want) => {
  if (got !== want) failed++;
  console.log(`${got === want ? "ok  " : "FAIL"} ${name}: exit ${got}, want ${want}`);
};

expect("an unchanged report passes", exitOf(report([extra(1, ["y/foo"]), control]), report([extra(1, ["y/foo"]), control])), 0);
expect("a name added to an extra document fails", exitOf(report([extra(1, ["y/foo"]), control]), report([extra(1, ["y/foo", "x/foo"]), control])), 1);
expect("a name lost in a document that already lost one fails", exitOf(report([lost(1, ["x/foo"], ["x/foo", "y/foo"]), control]), report([lost(1, [], ["x/foo", "y/foo"]), control])), 1);
expect("a new extra document fails", exitOf(report([control]), report([extra(1, ["y/foo"]), control])), 1);
expect("a new lost document fails", exitOf(report([control]), report([lost(1, [], ["y/foo"]), control])), 1);
expect("a name no longer extra passes", exitOf(report([extra(1, ["y/foo", "x/foo"]), control]), report([extra(1, ["y/foo"]), control])), 0);
expect("a lost document that is fixed passes", exitOf(report([lost(1, [], ["y/foo"]), control]), report([control])), 0);
expect("reports with different counts are refused", exitOf(report([control]), report([control], { valid: 4 })), 2);

// A scanner that throws leaves a row of kind "failed" with no candidate. The document differs in no
// name, so only the count and the id show the regression.
const crash = (id) => ({ id, kind: "failed", doc: "x", error: "boom", oracle: [], err: "" });
expect("a new scanner failure fails", exitOf(report([control]), report([crash(1), control], { failed: 1 })), 1);
expect("a scanner failure that stays passes", exitOf(report([crash(1), control], { failed: 1 }), report([crash(1), control], { failed: 1 })), 0);
expect("a scanner failure that is gone passes", exitOf(report([crash(1), control], { failed: 1 }), report([control])), 0);
expect("a failure in another document fails", exitOf(report([crash(1), control], { failed: 1 }), report([crash(3), control], { failed: 1 })), 1);

// A scanner that threw lists no name, so on a valid document it lost every name the oracle lists.
// A document it then recovers into a partial list has lost no name that is new
// (ginsys/bronzeward#610). The reverse pair is a regression. A failed row of a refused document
// has no accepted name, so it is no baseline of a loss.
const crashOn = (id, oracle, err = "") => ({ id, kind: "failed", doc: "x", error: "boom", oracle, err });
const both = ["x/foo", "y/foo"];
const partial = lost(1, ["x/foo"], both);
expect("a crash that recovers into a partial list passes", exitOf(report([crashOn(1, both), control], { failed: 1 }), report([partial, control])), 0);
expect("a partial list that turns into a crash fails", exitOf(report([partial, control]), report([crashOn(1, both), control], { failed: 1 })), 1);
expect("a crash that recovers into an empty list passes", exitOf(report([crashOn(1, both), control], { failed: 1 }), report([lost(1, [], both), control])), 0);
expect("a crash that recovers into a list with an extra name fails", exitOf(report([crashOn(1, both), control], { failed: 1 }), report([extra(1, [...both, "z/foo"], both), control])), 1);
expect("a crash on a refused document is no baseline of a loss", exitOf(report([crashOn(1, both, "yaml: bad"), control], { failed: 1 }), report([partial, control])), 1);

// The counts above stand for what cmp.mjs writes. This case runs cmp.mjs itself on a set of one
// document that the oracle accepts, once with a scanner that throws and once with one that works.
// A crash must not change `valid`: if it did, the two reports would differ in a count and the tool
// would exit 2 for both pairs. The nested run skips this case, as the copy holds no cmp.mjs.
if (process.env.CHECK_TOOLS_NESTED === undefined) {
  const cmp = fileURLToPath(new URL("cmp.mjs", import.meta.url));
  const files = { set: "set.json", oracle: "oracle.json", throws: "throws.mjs", works: "works.mjs" };
  const path = (k) => join(dir, files[k]);
  writeFileSync(path("set"), JSON.stringify(["a: 1\n"]));
  writeFileSync(path("oracle"), JSON.stringify([{ tags: [], err: "", unfit: false, bad: [] }]));
  writeFileSync(path("throws"), 'export const references = () => { throw new Error("boom"); };\n');
  writeFileSync(path("works"), "export const references = () => [];\n");
  const run = (k) => {
    const out = join(dir, `e2e-${k}.json`);
    execFileSync("node", [cmp, path("set"), path("oracle"), path(k), out], { stdio: "pipe" });
    return out;
  };
  const exit2 = (before, after) => {
    try {
      execFileSync("node", [tool, before, after], { stdio: "pipe" });
      return 0;
    } catch (e) {
      return e.status;
    }
  };
  const [thrown, worked] = [run("throws"), run("works")];
  expect("cmp.mjs: a scanner that starts to throw fails", exit2(worked, thrown), 1);
  expect("cmp.mjs: a scanner that stops throwing passes", exit2(thrown, worked), 0);
}

// The checkout path can hold a space or a non-ASCII character. A copy of both tools in such a
// directory must give the same results. The nested run skips this case.
if (process.env.CHECK_TOOLS_NESTED === undefined) {
  const spaced = join(dir, "a b");
  mkdirSync(spaced);
  for (const f of ["cmp-reports.mjs", "check-tools.mjs"]) copyFileSync(fileURLToPath(new URL(f, import.meta.url)), join(spaced, f));
  let got = 0;
  try {
    execFileSync("node", [join(spaced, "check-tools.mjs")], { stdio: "pipe", env: { ...process.env, CHECK_TOOLS_NESTED: "1" } });
  } catch (e) {
    got = e.status;
  }
  expect("a copy in a directory with a space passes", got, 0);
}

rmSync(dir, { recursive: true, force: true });
process.exit(failed > 0 ? 1 : 0);
