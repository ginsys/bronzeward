// Where the stable Edge leg gets its driver. A hosted runner's image holds a driver that matches its Edge
// (EDGEWEBDRIVER names its directory); WebdriverIO downloads one from Microsoft's CDN at job time unless the
// capability names a binary, and a failed download failed the leg (ginsys/bronzeward#1121 group A). A function
// of its input alone, so that edge-driver.test.ts runs it without a browser. `line` is printed by the config so
// that a CI log shows which of the two a run took.
import { existsSync } from "node:fs";

export function edgeDriver(env: Record<string, string | undefined>, exists: (path: string) => boolean = existsSync) {
  const dir = env["EDGEWEBDRIVER"];
  const binary = dir !== undefined && dir !== "" && exists(`${dir}/msedgedriver`) ? `${dir}/msedgedriver` : undefined;
  return { binary, line: `edge driver: ${binary === undefined ? "download" : `${binary} (image)`}` };
}
