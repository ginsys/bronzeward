// Where the stable Edge leg gets its driver. A hosted runner's image holds a driver that matches its Edge
// (EDGEWEBDRIVER names its directory); WebdriverIO downloads one from Microsoft's CDN at job time unless the
// capability names a binary, and a failed download failed the leg (ginsys/bronzeward#1121 group A). Functions
// of their input alone, so that edge-driver.test.ts runs them without a browser. `line` is printed by the
// launcher hooks so that a CI log shows which of the two a run took.
import { accessSync, constants, statSync } from "node:fs";

// A regular file the process can execute, a symlink followed: a directory or a file without the bit would
// turn off the download and then fail at spawn.
export function executableFile(path: string): boolean {
  try {
    if (!statSync(path).isFile()) return false;
    accessSync(path, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

// The runner image's driver when the image names its directory in `variable` and `file` there is usable, else the
// download. One mechanism for every browser whose driver the image carries (Edge here, Firefox in gecko-driver.ts).
export function imageDriver(
  env: Record<string, string | undefined>, variable: string, file: string, label: string, usable: (path: string) => boolean = executableFile,
) {
  const dir = env[variable];
  const binary = dir !== undefined && dir !== "" && usable(`${dir}/${file}`) ? `${dir}/${file}` : undefined;
  return { binary, line: `${label} driver: ${binary === undefined ? "download" : `${binary} (image)`}` };
}

export const edgeDriver = (env: Record<string, string | undefined>, usable?: (path: string) => boolean) =>
  imageDriver(env, "EDGEWEBDRIVER", "msedgedriver", "edge", usable);

// Whether BW_BROWSERS names one of the legs, by whole name: "edge-previous" is another leg than "edge".
export function legHas(browsers: string | undefined, ...legs: string[]): boolean {
  return browsers?.split(",").some((b) => legs.includes(b)) ?? false;
}

export function edgeCapability(headless: boolean, choice: { binary: string | undefined }): WebdriverIO.Capabilities {
  return {
    browserName: "MicrosoftEdge",
    "ms:edgeOptions": { args: headless ? ["--headless=new"] : [] },
    ...(choice.binary !== undefined ? { "wdio:edgedriverOptions": { binary: choice.binary } } : {}),
  };
}
