// The fixture binaries the e2e launchers start or run from bin/, and the one way a launcher spawns a
// child (ginsys/bronzeward#980). A spawn that fails (a missing binary, a mode without execute) emits
// 'error' on the child; with no listener Node raises it as an uncaught exception, which ends the
// launcher outside every try and finally, so the children already started outlive it. With a listener the
// child's exitCode is set at once (-2 for ENOENT), the launcher's readiness checks see it, and its own
// cleanup runs.

import { spawn, type ChildProcess, type SpawnOptions } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";

// Every binary a launcher spawns or runs. A name outside it fails tsc at the call.
export const BINARIES = ["bronzeward", "bronzeward-interrupt", "oidc-fixture", "e2eseed.test", "webhost"] as const;
export type Binary = (typeof BINARIES)[number];

// `root` ends in a slash, as the launchers' own root does.
export const binPath = (root: string, name: Binary) => `${root}bin/${name}`;

const runnable = (path: string) => {
  try {
    accessSync(path, constants.X_OK);
    return statSync(path).isFile();
  } catch {
    return false;
  }
};

// The names, of those given, that are not an executable file under root/bin.
export const missingBinaries = (root: string, names: readonly Binary[] = BINARIES): Binary[] =>
  names.filter((n) => !runnable(binPath(root, n)));

const reasons = new WeakMap<ChildProcess, string>();

export function spawnChild(path: string, args: readonly string[], options: SpawnOptions): ChildProcess {
  const p = spawn(path, args, options);
  p.once("error", (e) => reasons.set(p, `${path}: ${e.message}`));
  return p;
}

// Why the child could not start or be signalled, naming its path; undefined while none was raised.
export const died = (p: ChildProcess): string | undefined => reasons.get(p);
