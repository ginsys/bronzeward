// The planted control and the file walk of collide.mjs's leak scan, apart from it so that
// scripts/test/collide-scan-test.mjs runs them without a fixture (ginsys/bronzeward#509). Both fail
// closed, as session scan does: a scan that cannot prove it read everything must not pass.
import { closeSync, openSync, readdirSync, realpathSync, rmSync, statSync, writeSync } from "node:fs";
import { join } from "node:path";

// plant <path> <text>: the control file, created at the path. A file or a link already there is
// refused and kept: writing through it would make another file the control, and the scan's cleanup
// would delete that file.
export function plant(path, text) {
  let fd;
  try {
    // wx is O_CREAT | O_EXCL: it fails on any existing name, a link included, and follows no link.
    fd = openSync(path, "wx", 0o600);
  } catch (e) {
    if (e.code === "EEXIST") throw new Error(`leak scan: ${path} exists, and this scan did not plant it; read it, remove it, and run t5 collide again`);
    throw e;
  }
  try {
    writeSync(fd, text);
  } catch (e) {
    rmSync(path, { force: true });
    throw e;
  } finally {
    closeSync(fd);
  }
}

// files <dir>: every regular file under the directory, through symbolic links as grep -R reads
// them, so that a file linked into the directory is read too. A link that cannot be followed fails
// the scan. A directory reached a second time (a link loop) is read once.
export function files(dir, seen = new Set()) {
  const real = realpathSync(dir);
  if (seen.has(real)) return [];
  seen.add(real);
  const found = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    let s = e;
    if (e.isSymbolicLink()) {
      try {
        s = statSync(p);
      } catch (err) {
        throw new Error(`leak scan: cannot follow the link ${p} (${err.code}), so the scan cannot read everything`);
      }
    }
    if (s.isDirectory()) found.push(...files(p, seen));
    else if (s.isFile()) found.push(p);
  }
  return found;
}
