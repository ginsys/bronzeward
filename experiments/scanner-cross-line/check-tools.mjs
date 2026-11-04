// Checks cmp-reports.mjs on small reports that stand for the defect found in review of
// ginsys/bronzeward#605: a name added to a document that already differed passed the exit code.
// Usage: node check-tools.mjs. It needs no oracle, no set and no web build: it writes small reports
// into a temporary directory, runs the tool, and exits 1 if one exit code is not the expected one.
// Every report holds a control document (id 2) that is the same before and after, so a tool that
// ignores its input cannot pass by luck.
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const dir = mkdtempSync(join(tmpdir(), "check-tools-"));
const tool = new URL("cmp-reports.mjs", import.meta.url).pathname;
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

rmSync(dir, { recursive: true, force: true });
process.exit(failed > 0 ? 1 : 0);
