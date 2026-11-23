// Where the Firefox legs get their driver. A hosted runner's image holds a geckodriver (GECKOWEBDRIVER names its
// directory); without a binary in the capability WebdriverIO asks the network for the latest release at job time, and
// a 503 there failed a merge-group leg (ginsys/bronzeward#1121). The Firefox itself is still downloaded at its channel.
// Functions of their input alone, so that gecko-driver.test.ts runs them without a browser. `line` is printed by the
// launcher hooks so that a CI log shows which of the two a run took.
import { imageDriver } from "./edge-driver.ts";

// WebDriver's map form of unhandledPromptBehavior: only the leave dialog is accepted, and every other prompt keeps the
// default (dismiss and report), so a case still meets the page's confirm(). WebdriverIO types the capability as the
// string form only.
export const acceptLeave = { beforeUnload: "accept" } as unknown as string;

export const geckoDriver = (env: Record<string, string | undefined>, usable?: (path: string) => boolean) =>
  imageDriver(env, "GECKOWEBDRIVER", "geckodriver", "gecko", usable);

// A Firefox of the channel the leg names ("stable" or "esr"), downloaded; the driver is the image's where one was chosen.
export function firefoxCapability(version: "stable" | "esr", headless: boolean, choice: { binary: string | undefined }): WebdriverIO.Capabilities {
  return {
    browserName: "firefox", browserVersion: version, unhandledPromptBehavior: acceptLeave,
    "moz:firefoxOptions": { args: headless ? ["-headless"] : [] },
    ...(choice.binary !== undefined ? { "wdio:geckodriverOptions": { binary: choice.binary } } : {}),
  };
}
