// fixtures/participants/leakscan.mjs, the leak scan of `session t5 collide` (ginsys/bronzeward#509):
// the planted control is a new file or nothing, and the walk reads linked files and fails on a
// link it cannot follow. No fixture: each case builds its own directory.
import { strict as assert } from "node:assert";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, test } from "node:test";
import { files, plant } from "../../fixtures/participants/leakscan.mjs";

let dir;
beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "collide-scan-"));
});
afterEach(() => rmSync(dir, { recursive: true, force: true }));

test("plant creates the control, readable by its owner only", () => {
  const c = join(dir, "control");
  plant(c, "planted X\n");
  assert.equal(readFileSync(c, "utf8"), "planted X\n");
  assert.equal(statSync(c).mode & 0o777, 0o600);
});

test("plant refuses an existing file and keeps it", () => {
  const c = join(dir, "control");
  writeFileSync(c, "someone else's\n");
  assert.throws(() => plant(c, "planted X\n"), /exists, and this scan did not plant it/);
  assert.equal(readFileSync(c, "utf8"), "someone else's\n");
});

test("plant refuses a link to a file and writes nothing through it", () => {
  const target = join(dir, "target");
  const c = join(dir, "control");
  writeFileSync(target, "kept\n");
  symlinkSync(target, c);
  assert.throws(() => plant(c, "planted X\n"), /exists, and this scan did not plant it/);
  assert.equal(readFileSync(target, "utf8"), "kept\n");
});

test("plant refuses a dangling link and creates no file at its target", () => {
  const target = join(dir, "absent");
  const c = join(dir, "control");
  symlinkSync(target, c);
  assert.throws(() => plant(c, "planted X\n"), /exists, and this scan did not plant it/);
  assert.equal(existsSync(target), false);
});

test("files reads a file and a directory linked in from outside the root", () => {
  const root = join(dir, "root");
  const outside = join(dir, "outside");
  mkdirSync(join(root, "sub"), { recursive: true });
  mkdirSync(outside);
  writeFileSync(join(root, "sub", "plain"), "a");
  writeFileSync(join(outside, "token-file"), "BWSYNTH-token");
  symlinkSync(join(outside, "token-file"), join(root, "linked-file"));
  symlinkSync(outside, join(root, "linked-dir"));
  const got = files(root).map((p) => p.slice(root.length + 1)).sort();
  assert.deepEqual(got, ["linked-dir/token-file", "linked-file", "sub/plain"]);
});

test("files fails on a link it cannot follow", () => {
  const root = join(dir, "root");
  mkdirSync(root);
  writeFileSync(join(root, "plain"), "a");
  symlinkSync(join(dir, "absent"), join(root, "dangling"));
  assert.throws(() => files(root), /cannot follow the link .*dangling \(ENOENT\)/);
});

test("files reads a directory in a link loop once and ends", () => {
  const root = join(dir, "root");
  mkdirSync(join(root, "sub"), { recursive: true });
  writeFileSync(join(root, "sub", "plain"), "a");
  symlinkSync(root, join(root, "sub", "back"));
  assert.deepEqual(files(root).map((p) => p.slice(root.length + 1)), ["sub/plain"]);
});
