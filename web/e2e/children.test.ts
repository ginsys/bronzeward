import { execFile } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { promisify } from "node:util";
import { afterEach, expect, test } from "vitest";
import { BINARIES, binPath, died, missingBinaries, spawnChild } from "./children.ts";

// ginsys/bronzeward#980: a fixture binary that cannot start must fail the run by name, and no child the
// run already started may outlive the launcher.

const roots: string[] = [];
const pids: number[] = [];
afterEach(() => {
  for (const pid of pids.splice(0)) {
    try {
      process.kill(pid, "SIGKILL");
    } catch {
      // Already gone.
    }
  }
  for (const r of roots.splice(0)) rmSync(r, { recursive: true, force: true });
});

// A root with a bin/ holding each name given as an executable script (mode 0755), or as a file that is not
// executable (0644).
function root(files: Record<string, "run" | "plain">): string {
  const r = `${mkdtempSync(`${tmpdir()}/bw-children-`)}/`;
  roots.push(r);
  mkdirSync(`${r}bin`);
  for (const [name, mode] of Object.entries(files)) {
    writeFileSync(`${r}bin/${name}`, "#!/bin/sh\nexec sleep 30\n");
    chmodSync(`${r}bin/${name}`, mode === "run" ? 0o755 : 0o644);
  }
  return r;
}

const alive = (pid: number) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};
const gone = async (pid: number) => {
  for (let i = 0; i < 50 && alive(pid); i++) await new Promise((r) => setTimeout(r, 100));
  return !alive(pid);
};
const closed = (p: ReturnType<typeof spawnChild>) => new Promise<void>((resolve) => p.once("close", () => resolve()));

test("missingBinaries lists a binary that is absent and one that is not executable, in the tuple's order", () => {
  const all = Object.fromEntries(BINARIES.map((n) => [n, "run" as const]));
  expect(missingBinaries(root(all))).toEqual([]);
  const { webhost: _w, ["oidc-fixture"]: _o, ...rest } = all;
  expect(missingBinaries(root({ ...rest, "oidc-fixture": "plain" }))).toEqual(["oidc-fixture", "webhost"]);
});

test("the static host's launcher asks for webhost alone", () => {
  expect(missingBinaries(root({}), ["webhost"])).toEqual(["webhost"]);
  expect(missingBinaries(root({ webhost: "run" }), ["webhost"])).toEqual([]);
});

test("a binary that cannot start is named, with an exit code set at once, and starts no process", async () => {
  const r = root({});
  const p = spawnChild(binPath(r, "webhost"), [], { stdio: "ignore" });
  await new Promise((resolve) => p.once("error", resolve));
  expect(died(p)).toContain(`${r}bin/webhost`);
  expect(died(p)).toContain("ENOENT");
  // The launcher's existing readiness checks read exitCode (auth.conf.ts ready, wdio.conf.ts).
  expect(p.exitCode).not.toBeNull();
  expect(p.pid).toBeUndefined();
});

test("a child that runs has no death recorded, and is counted alive until it is stopped", async () => {
  const r = root({ bronzeward: "run" });
  const p = spawnChild(binPath(r, "bronzeward"), [], { stdio: "ignore" });
  pids.push(p.pid!);
  // The positive control: the count sees a live child, and sees a pid that is not one.
  expect(alive(p.pid!)).toBe(true);
  expect(alive(2 ** 22 + 1)).toBe(false);
  expect(died(p)).toBeUndefined();
  const stopped = closed(p);
  p.kill();
  await stopped;
  expect(await gone(p.pid!)).toBe(true);
});

// The old shape: a spawn with no 'error' listener. The control that this test can fail: the launcher dies
// of the unhandled event and the child it had started is still running.
test("without a listener the failed spawn ends the launcher and leaves its live child running", async () => {
  const r = root({ bronzeward: "run" });
  const script = `
    const { spawn } = require("node:child_process");
    const live = spawn(${JSON.stringify(`${r}bin/bronzeward`)}, [], { stdio: "ignore" });
    console.log(live.pid);
    spawn(${JSON.stringify(`${r}bin/webhost`)}, [], { stdio: "ignore" });
    setTimeout(() => {}, 5000);`;
  let out = "";
  let code: number | undefined;
  try {
    ({ stdout: out } = await promisify(execFile)(process.execPath, ["-e", script]));
  } catch (e) {
    const x = e as { stdout: string; code: number; stderr: string };
    [out, code] = [x.stdout, x.code];
    expect(x.stderr).toContain("ENOENT");
  }
  const pid = Number(out.trim());
  pids.push(pid);
  expect(code).toBe(1);
  expect(alive(pid)).toBe(true);
});
