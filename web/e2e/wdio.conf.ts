// The real-browser harness (decisions B6, B7). It starts the redirect target, on an origin of its
// own, the synthetic provider (provider.ts), and the test static host, which answers every API
// request with a redirect to that target (fixtures/webhost -redirect-api), then runs the specs
// once per browser in BW_BROWSERS.
import { spawn, type ChildProcess } from "node:child_process";
import { createServer, type Server } from "node:http";
import { fileURLToPath } from "node:url";
// The launcher logs and ignores any other error a launcher hook throws, and runs the specs anyway.
import { SevereServiceError } from "webdriverio";
import { FAKE_ISSUER, startProvider } from "./provider.ts";

export const PAGE_ORIGIN = "http://localhost:58443";
export const TARGET_ORIGIN = "http://127.0.0.1:58444";
const web = fileURLToPath(new URL("..", import.meta.url));
const canary = process.env["BW_WEB_CANARY"];
if (canary === undefined || !canary.startsWith("BWSYNTH-")) throw new Error("BW_WEB_CANARY must hold the run's synthetic canary");

const headless = process.env["BW_HEADED"] === undefined;
const capabilities: Record<string, WebdriverIO.Capabilities> = {
  chrome: { browserName: "chrome", "goog:chromeOptions": { args: headless ? ["--headless=new"] : [] } },
  edge: { browserName: "MicrosoftEdge", "ms:edgeOptions": { args: headless ? ["--headless=new"] : [] } },
  // Both Firefoxes are downloaded at their current release, not taken from the host.
  firefox: { browserName: "firefox", browserVersion: "stable", "moz:firefoxOptions": { args: headless ? ["-headless"] : [] } },
  "firefox-esr": { browserName: "firefox", browserVersion: "esr", "moz:firefoxOptions": { args: headless ? ["-headless"] : [] } },
  safari: { browserName: "safari" },
};
const browsers = (process.env["BW_BROWSERS"] ?? "chrome,firefox").split(",");
for (const b of browsers) if (!(b in capabilities)) throw new Error(`unknown browser ${b}`);

let target: Server | undefined;
let provider: Server | undefined;
let host: ChildProcess | undefined;
let hits = 0;

export const config: WebdriverIO.Config = {
  runner: "local",
  specs: ["./*.e2e.ts"],
  maxInstances: 1, // the target's request count is shared
  capabilities: browsers.map((b) => capabilities[b]!),
  baseUrl: PAGE_ORIGIN,
  outputDir: `${web}e2e/out`,
  logLevel: "info",
  framework: "mocha",
  reporters: ["spec"],
  mochaOpts: { timeout: 60_000 },
  ...(process.env["BW_WDIO_CACHE"] !== undefined ? { cacheDir: process.env["BW_WDIO_CACHE"] } : {}),

  // The target counts every request but its own two control routes, and answers with the canary
  // (readable by the page, so the positive control delivers it into the page's memory).
  async onPrepare() {
    target = createServer((req, res) => {
      if (req.url === "/__hits") return res.end(JSON.stringify({ hits }));
      if (req.url === "/__reset") return (hits = 0), res.end();
      hits++;
      res.writeHead(200, { "Cache-Control": "no-store", "Access-Control-Allow-Origin": PAGE_ORIGIN }).end(canary);
    });
    await new Promise<void>((resolve) => target!.listen(58444, "127.0.0.1", resolve));
    // Every token carries the run's canary, so the artifact scan finds any token an artifact keeps.
    ({ server: provider } = await startProvider({
      issuer: FAKE_ISSUER, pageOrigin: PAGE_ORIGIN, clientId: "synthetic-web-client", next: () => ({ access: `${canary}-access`, sub: "h-synthetic" }),
    }));
    // A host left from an earlier run would answer the readiness check below with an old build.
    const hostUrl = `${PAGE_ORIGIN.replace("localhost", "127.0.0.1")}/`;
    if (await fetch(hostUrl).then(() => true, () => false)) throw new SevereServiceError("something already answers on the static host's port");
    host = spawn(`${web}../bin/webhost`, [
      "-dist", `${web}dist`, "-headers", `${web}headers.json`, "-client-config", `${web}e2e/bronzeward-client.json`,
      "-issuer", FAKE_ISSUER, "-provider-origins", TARGET_ORIGIN, "-redirect-api", TARGET_ORIGIN,
    ], { stdio: "inherit" });
    for (let i = 0; ; i++) {
      if (await fetch(hostUrl).then((r) => r.ok, () => false)) break;
      if (i === 50 || host.exitCode !== null) throw new SevereServiceError("the static host did not answer within 10 s");
      await new Promise((r) => setTimeout(r, 200));
    }
  },
  onComplete() {
    host?.kill();
    target?.close();
    provider?.close();
  },
};
