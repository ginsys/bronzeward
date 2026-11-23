// Where the Firefox legs get their driver (ginsys/bronzeward#1121). Without a binary WebdriverIO asks the network for
// the latest geckodriver at job time, and a 503 there failed a merge-group leg.
import { describe, expect, it } from "vitest";
import { firefoxCapability, geckoDriver } from "./gecko-driver.ts";

const leave = { beforeUnload: "accept" };

describe("geckoDriver", () => {
  it("uses the runner image's own driver when the image names one and the file is usable", () => {
    const d = geckoDriver({ GECKOWEBDRIVER: "/usr/local/share/gecko_driver" }, (p) => p === "/usr/local/share/gecko_driver/geckodriver");
    expect(d).toEqual({ binary: "/usr/local/share/gecko_driver/geckodriver", line: "gecko driver: /usr/local/share/gecko_driver/geckodriver (image)" });
  });

  it("downloads the driver when the image names none, or the file is not usable", () => {
    const download = { binary: undefined, line: "gecko driver: download" };
    expect(geckoDriver({}, () => true)).toEqual(download);
    expect(geckoDriver({ GECKOWEBDRIVER: "" }, () => true)).toEqual(download);
    expect(geckoDriver({ GECKOWEBDRIVER: "/usr/local/share/gecko_driver" }, () => false)).toEqual(download);
  });
});

describe("firefoxCapability", () => {
  it("names the driver binary only when one was chosen, and keeps the browser, the leave prompt and the headless flag", () => {
    expect(firefoxCapability("stable", true, { binary: "/x/geckodriver" })).toEqual({
      browserName: "firefox", browserVersion: "stable", unhandledPromptBehavior: leave,
      "moz:firefoxOptions": { args: ["-headless"] }, "wdio:geckodriverOptions": { binary: "/x/geckodriver" },
    });
    expect(firefoxCapability("esr", false, { binary: undefined })).toEqual({
      browserName: "firefox", browserVersion: "esr", unhandledPromptBehavior: leave, "moz:firefoxOptions": { args: [] },
    });
  });
});
