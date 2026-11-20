// fixtures/participants/compare.mjs, the comparison of the operation page with the server's events
// (ginsys/bronzeward#359). Each rule has a case with an injected defect as its control: only that
// rule fails, and a clean run passes every rule. No fixture and no browser.
import { strict as assert } from "node:assert";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { compare, flow } from "../../fixtures/participants/compare.mjs";

const E = "ep_abcdefghijklmnopqrstuvwxyz";
const op = (n, type, data = {}, at = `2026-11-20T08:00:0${n}Z`) => ({ id: `${E}:${n}`, type, at, data });
const server = [
  op(1, "commitment"),
  op(2, "ownership-transition"),
  op(3, "attempt"),
  op(4, "operation-state", { from: "committed", to: "sending" }),
  op(5, "operation-state", { from: "sending", to: "verifying" }),
  op(6, "operation-state", { from: "verifying", to: "completed" }),
];
// The page's clock, as the driver evaluates it in the browser: the server's time as shown.
const timeOf = (at) => at.slice(11, 19);
const line = (e) => (e.type === "operation-state" ? `operation-state: ${e.data.from} → ${e.data.to}` : e.type);
const row = (e, received) => ({ id: e.id, received, text: `${received ? "received " : ""}${received ? "09:59:59" : timeOf(e.at)} ${line(e)}` });
const added = (e) => ({ op: "add", ...row(e, true) });
const run = () => ({
  // Three rows before the outage, three after it, then the end of the watch.
  log: [added(server[0]), added(server[1]), added(server[2]), { op: "mark", name: "stop" }, { op: "mark", name: "restart" }, added(server[3]), added(server[4]), added(server[5])],
  final: server.map((e) => row(e, false)),
});
const verdict = (r, o = {}) => Object.fromEntries(compare({ server, timeOf, outage: true, ...r, ...o }).map((x) => [x.rule, x.ok]));
const only = (v, rule) => assert.deepEqual(Object.entries(v).filter(([, ok]) => !ok).map(([k]) => k), [rule]);

test("a clean run passes every rule, and each rule is present", () => {
  const v = verdict(run());
  assert.deepEqual(Object.keys(v), ["R1", "R2", "R3", "R4", "R5"]);
  assert.ok(Object.values(v).every(Boolean), JSON.stringify(v));
});

test("R1: an event shown twice without a clear between fails, and a clear between lets it show again", () => {
  const r = run();
  r.log.splice(6, 0, added(server[2]));
  // A repeat is also not a later event of the server's order, so R2 fails with it.
  assert.equal(verdict(r)["R1"], false);
  const again = run();
  again.log.splice(6, 0, { op: "clear" }, added(server[2]));
  assert.equal(verdict(again)["R1"], true);
});

test("R2: a received id the server never sent, or one out of the server's order, fails", () => {
  const stray = run();
  stray.log.push({ op: "add", id: `${E}:99`, received: true, text: "received 09:59:59 x" });
  only(verdict(stray), "R2");
  const swapped = run();
  [swapped.log[0], swapped.log[1]] = [swapped.log[1], swapped.log[0]];
  only(verdict(swapped), "R2");
  // A clear does not erase what was seen before it, and the list may start again from the first event after it.
  const cleared = run();
  cleared.log.splice(1, 0, { op: "add", id: `${E}:99`, received: true, text: "received 09:59:59 x" }, { op: "clear" });
  only(verdict(cleared), "R2");
  const restart = run();
  restart.log.splice(3, 0, { op: "clear" }, added(server[0]));
  assert.equal(verdict(restart)["R2"], true);
});

test("R3: a missing row, a leftover received prefix and a time from the receipt each fail", () => {
  const missing = run();
  missing.final.splice(2, 1);
  // The rows after the gap move up a place, so R5's alignment fails with it.
  assert.equal(verdict(missing)["R3"], false);
  const prefix = run();
  prefix.final[0] = row(server[0], true);
  only(verdict(prefix), "R3");
  const receipt = run();
  receipt.final[1] = { ...receipt.final[1], text: `09:59:59 ${line(server[1])}` };
  only(verdict(receipt), "R3");
});

test("R4: no received row before the stop, or none after the restart, fails; no outage skips the rule", () => {
  const before = run();
  before.log = before.log.filter((x, i) => !(x.op === "add" && i < 3));
  only(verdict(before), "R4");
  const after = run();
  after.log = after.log.filter((x, i) => !(x.op === "add" && i > 4));
  assert.equal(verdict(after)["R4"], false);
  assert.equal(verdict(after, { outage: false })["R4"], undefined);
});

test("R5: a state row that does not name its state, or the cause the event holds, fails", () => {
  const state = run();
  state.final[4] = { ...state.final[4], text: `${timeOf(server[4].at)} operation-state: sending → sending` };
  only(verdict(state), "R5");
  const withCause = server.map((e, i) => (i === 5 ? { ...e, data: { ...e.data, cause: "a completion observation matched" } } : e));
  const r = run();
  assert.equal(verdict(r, { server: withCause })["R5"], false);
  r.final[5] = { ...r.final[5], text: `${r.final[5].text} (a completion observation matched)` };
  // The line the page showed on receipt names the cause too, so the final row alone is not enough.
  assert.equal(verdict(r, { server: withCause })["R5"], false);
  r.log[7] = { ...r.log[7], text: `${r.log[7].text} (a completion observation matched)` };
  assert.equal(verdict(r, { server: withCause })["R5"], true);
  // The transition is named in its direction, and a line the page showed first and replaced later counts too.
  const reversed = run();
  reversed.final[4] = { ...reversed.final[4], text: `${timeOf(server[4].at)} operation-state: ${server[4].data.to} → ${server[4].data.from}` };
  only(verdict(reversed), "R5");
  const wrongReceipt = run();
  wrongReceipt.log[5] = { ...wrongReceipt.log[5], text: "received 09:59:59 operation-state: wrong" };
  only(verdict(wrongReceipt), "R5");
});

test("flow: one commitment, the expected attempts, one approval entry, the transition before the attempt", () => {
  const ok = flow({ events: server, approvals: 1, attempts: 1 });
  assert.ok(ok.every((x) => x.ok), JSON.stringify(ok));
  const two = flow({ events: [...server, op(7, "attempt")], approvals: 1, attempts: 1 });
  assert.equal(two.find((x) => x.rule === "F2").ok, false);
  assert.equal(flow({ events: server, approvals: 0, attempts: 1 }).find((x) => x.rule === "F3").ok, false);
  const early = [server[0], server[2], server[1], ...server.slice(3)];
  assert.equal(flow({ events: early, approvals: 1, attempts: 1 }).find((x) => x.rule === "F4").ok, false);
  assert.equal(flow({ events: server.slice(1), approvals: 1, attempts: 1 }).find((x) => x.rule === "F1").ok, false);
});

test("the command line prints one line per rule, exits 0 on a clean web-watch.json and 1 with a FAIL line on a defect", () => {
  const dir = mkdtempSync(join(tmpdir(), "watch-compare-"));
  try {
    const times = Object.fromEntries(server.map((e) => [e.at, timeOf(e.at)]));
    const file = join(dir, "web-watch.json");
    const script = fileURLToPath(new URL("../../fixtures/participants/compare.mjs", import.meta.url));
    writeFileSync(file, JSON.stringify({ server, times, ...run() }));
    const clean = spawnSync("node", [script, file, "--outage", "--flow", "1", "1"], { encoding: "utf8" });
    assert.equal(clean.status, 0, clean.stdout);
    assert.deepEqual(clean.stdout.trim().split("\n").map((l) => l.split("\t").slice(0, 2).join(" ")), ["R1 ok", "R2 ok", "R3 ok", "R4 ok", "R5 ok", "F1 ok", "F2 ok", "F3 ok", "F4 ok"]);
    const bad = run();
    bad.final.pop();
    writeFileSync(file, JSON.stringify({ server, times, ...bad }));
    const broken = spawnSync("node", [script, file, "--outage"], { encoding: "utf8" });
    assert.equal(broken.status, 1);
    assert.match(broken.stdout, /^R3\tFAIL\t/m);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
