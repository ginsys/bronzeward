// The legs' Firefox is fetched once, by the launcher (ginsys/bronzeward#1121): a worker that asks for it itself sends one
// unretried HEAD, and one failed answer there failed a spec file of a leg.
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { setupBrowser } from "@wdio/utils/node";
import { remote, SevereServiceError } from "webdriverio";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchFirefox } from "./firefox-browser.ts";
import { firefoxCapability } from "./gecko-driver.ts";

afterEach(() => vi.restoreAllMocks());

const fetched = [{ executablePath: "/cache/firefox/firefox", browserVersion: "esr_1" }];
const quiet = () => vi.spyOn(console, "log").mockImplementation(() => {}) && vi.spyOn(console, "error").mockImplementation(() => {});

describe("fetchFirefox", () => {
  it("tries again after a failed fetch and names the fetched binary in the capability, keeping its other options", async () => {
    quiet();
    const setup = vi.fn().mockRejectedValueOnce(new Error("Couldn't find a matching firefox browser")).mockResolvedValue(fetched);
    const waits: number[] = [];
    const capability = firefoxCapability("esr", true, { binary: "/x/geckodriver" });
    await fetchFirefox([capability], "/cache", setup, async (ms) => void waits.push(ms));
    expect(setup).toHaveBeenCalledTimes(2);
    expect(setup).toHaveBeenLastCalledWith({ cacheDir: "/cache" }, [{ browserName: "firefox", browserVersion: "esr" }]);
    expect(waits).toEqual([5000]);
    expect(capability["moz:firefoxOptions"]).toEqual({ args: ["-headless"], binary: "/cache/firefox/firefox" });
    expect(capability.browserVersion).toBe("esr");
  });

  it("stops the run after four failed fetches, naming the channel and the last error", async () => {
    quiet();
    const setup = vi.fn().mockRejectedValue(new Error("no answer"));
    const run = fetchFirefox([firefoxCapability("stable", true, { binary: undefined })], undefined, setup, async () => {});
    await expect(run).rejects.toThrow(SevereServiceError);
    await expect(run).rejects.toThrow("firefox stable could not be fetched in 4 attempts: Error: no answer");
    expect(setup).toHaveBeenCalledTimes(4);
  });

  it("leaves every other browser as it is", async () => {
    const setup = vi.fn();
    const chrome = { browserName: "chrome", "goog:chromeOptions": { args: [] } };
    await fetchFirefox([chrome, { browserName: "safari" }], "/cache", setup);
    expect(setup).not.toHaveBeenCalled();
    expect(chrome).toEqual({ browserName: "chrome", "goog:chromeOptions": { args: [] } });
  });
});

// What the pinned WebdriverIO does with a capability that names a binary, which is what a worker is given.
describe("a Firefox capability with a binary", () => {
  it("is set up without resolving its channel to a version, so with no request to Mozilla", async () => {
    // A lookup would answer the channel's current build id ("esr_<version>esr"), not the channel.
    await expect(setupBrowser({}, [{ browserName: "firefox", browserVersion: "esr", "moz:firefoxOptions": { binary: "/x/firefox" } }]))
      .resolves.toEqual([{ executablePath: "/x/firefox", browserVersion: "esr" }]);
  });

  it("sends the driver the binary and no browserVersion, so no channel name is matched against a version", async () => {
    const dir = mkdtempSync(`${tmpdir()}/bw-gecko-`);
    const driver = `${dir}/geckodriver`;
    // A stand-in driver: it answers the two requests of a session and keeps the body of the one that opens it.
    writeFileSync(driver, `#!${process.execPath}
const port = /--port[= ](\\d+)/.exec(process.argv.join(" "))[1];
require("node:http").createServer((req, res) => {
  let body = "";
  req.on("data", (d) => (body += d));
  req.on("end", () => {
    if (req.method === "POST" && req.url === "/session") require("node:fs").writeFileSync(${JSON.stringify(`${dir}/session.json`)}, body);
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ value: req.method === "POST" ? { sessionId: "s", capabilities: { browserName: "firefox" } } : null }));
  });
}).listen(Number(port));
`);
    chmodSync(driver, 0o755);
    try {
      const capability = firefoxCapability("esr", true, { binary: driver });
      capability["moz:firefoxOptions"] = { ...capability["moz:firefoxOptions"], binary: "/x/firefox" };
      const session = await remote({ capabilities: capability, logLevel: "silent" });
      await session.deleteSession();
      const sent = JSON.parse(readFileSync(`${dir}/session.json`, "utf8")).capabilities.alwaysMatch;
      expect(sent.browserName).toBe("firefox");
      expect(sent["moz:firefoxOptions"]).toEqual({ args: ["-headless"], binary: "/x/firefox" });
      expect(sent).not.toHaveProperty("browserVersion");
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }, 30_000);
});
