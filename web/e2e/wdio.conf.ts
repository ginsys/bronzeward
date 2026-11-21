// The real-browser harness (decisions B6, B7). It starts the redirect target, on an origin of its
// own, the synthetic provider (provider.ts), and the test static host, which answers every API
// request with a redirect to that target (fixtures/webhost -redirect-api), then runs the specs
// once per browser in BW_BROWSERS.
import type { ChildProcess } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
// The launcher logs and ignores any other error a launcher hook throws, and runs the specs anyway.
import { remote, SevereServiceError } from "webdriverio";
import { binPath, died, missingBinaries, spawnChild } from "./children.ts";
import { safariKeys } from "./safari-keys.ts";
import { settled } from "./safari-navigation.ts";
import { FAKE_ISSUER, PAGE_LISTEN, PAGE_ORIGIN, TARGET_ORIGIN } from "./ports.ts";
import { printFailure } from "./print-failure.ts";
import { startProvider } from "./provider.ts";

export { PAGE_ORIGIN, TARGET_ORIGIN };
const web = fileURLToPath(new URL("..", import.meta.url));
const root = `${web}../`;
const canary = process.env["BW_WEB_CANARY"];
if (canary === undefined || !canary.startsWith("BWSYNTH-")) throw new Error("BW_WEB_CANARY must hold the run's synthetic canary");

const headless = process.env["BW_HEADED"] === undefined;
// WebDriver's map form of unhandledPromptBehavior: only the leave dialog is accepted, and every
// other prompt keeps the default (dismiss and report), so a case still meets the page's confirm().
// WebdriverIO types the capability as the string form only.
const acceptLeave = { beforeUnload: "accept" } as unknown as string;
// The previous major of each browser, pinned to one build from the vendor's own source (decision
// B6, ginsys/bronzeward#893). previous-majors.json holds the pins and previous-majors.yml runs them.
const previous = JSON.parse(readFileSync(new URL("previous-majors.json", import.meta.url), "utf8")) as {
  chrome: string; edge: { version: string }; firefox: string;
};
const capabilities: Record<string, WebdriverIO.Capabilities> = {
  chrome: { browserName: "chrome", "goog:chromeOptions": { args: headless ? ["--headless=new"] : [] } },
  edge: { browserName: "MicrosoftEdge", "ms:edgeOptions": { args: headless ? ["--headless=new"] : [] } },
  // Both Firefoxes are downloaded at their current release, not taken from the host. A case that
  // leaves a page with a write still open meets its leave guard: Firefox shows that dialog and
  // holds the navigation until it times out, where Chrome and Edge never show it without a user
  // gesture. Firefox accepts it, so each case's own navigation proceeds as in the other browsers.
  firefox: { browserName: "firefox", browserVersion: "stable", unhandledPromptBehavior: acceptLeave, "moz:firefoxOptions": { args: headless ? ["-headless"] : [] } },
  "firefox-esr": { browserName: "firefox", browserVersion: "esr", unhandledPromptBehavior: acceptLeave, "moz:firefoxOptions": { args: headless ? ["-headless"] : [] } },
  safari: { browserName: "safari" },
  "chrome-previous": { browserName: "chrome", browserVersion: previous.chrome, "goog:chromeOptions": { args: headless ? ["--headless=new"] : [] } },
  // The job installs this Edge at the default path; the driver is pinned to the same version, or
  // WebdriverIO would pick the one its own detection finds.
  "edge-previous": {
    browserName: "MicrosoftEdge", "ms:edgeOptions": { args: headless ? ["--headless=new"] : [] },
    // @wdio/types declares no field here; @wdio/utils hands the object to edgedriver's start(),
    // which reads edgeDriverVersion.
    "wdio:edgedriverOptions": { edgeDriverVersion: previous.edge.version } as unknown as WebdriverIO.EdgedriverOptions,
  },
  // A bare version reads as a nightly build id to @puppeteer/browsers; "stable_<version>" is the release archive's.
  "firefox-previous": { browserName: "firefox", browserVersion: `stable_${previous.firefox}`, unhandledPromptBehavior: acceptLeave, "moz:firefoxOptions": { args: headless ? ["-headless"] : [] } },
};
const browsers = (process.env["BW_BROWSERS"] ?? "chrome,firefox").split(",");
for (const b of browsers) if (!(b in capabilities)) throw new Error(`unknown browser ${b}`);

// Each chrome and edge session gets a profile directory of its own, under one directory per run
// that the run removes when it ends: left to itself the driver makes one in TMPDIR per session and
// never removes it (#430). The launcher makes the run's directory; its workers inherit the name.
const profiles = (process.env["BW_BROWSER_PROFILES"] ??= mkdtempSync(`${tmpdir()}/bw-browser-profiles-`));
export function removeProfiles() {
  rmSync(profiles, { recursive: true, force: true });
}
function ownProfile(capabilities: WebdriverIO.Capabilities) {
  const options = capabilities.browserName === "chrome" ? (capabilities["goog:chromeOptions"] ??= {})
    : capabilities.browserName === "MicrosoftEdge" ? (capabilities["ms:edgeOptions"] ??= {}) : undefined;
  if (options === undefined) return;
  const dir = `${profiles}/${randomUUID()}`;
  mkdirSync(dir, { recursive: true });
  options.args = [...(options.args ?? []), `--user-data-dir=${dir}`];
}

// A second session of the running browser, for a case with two browsers on one page (ETAG-01,
// #339): the running session's requested capabilities with a profile of its own, its log in the
// artifact directory the scan reads.
export async function secondSession(requested: WebdriverIO.Capabilities): Promise<WebdriverIO.Browser> {
  const capabilities = structuredClone(requested);
  for (const k of ["goog:chromeOptions", "ms:edgeOptions"] as const) {
    const o = capabilities[k];
    if (o?.args !== undefined) o.args = o.args.filter((a) => !a.startsWith("--user-data-dir="));
  }
  ownProfile(capabilities);
  return remote({
    capabilities, logLevel: "info", outputDir: `${web}e2e/out`,
    ...(process.env["BW_WDIO_CACHE"] !== undefined ? { cacheDir: process.env["BW_WDIO_CACHE"] } : {}),
  });
}

let target: Server | undefined;
let provider: Server | undefined;
let host: ChildProcess | undefined;
let clientDir: string | undefined;
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
  beforeSession(_config, capabilities) {
    ownProfile(capabilities as WebdriverIO.Capabilities);
  },
  // safaridriver answers "Get Element Text" with the DOM's text, which keeps a block's trailing space
  // and puts no line break between block items, where the other drivers answer the rendered text the
  // specification names. In Safari alone, the rendered text comes from the page (innerText), so that
  // every assertion on a text stays as strict as it is in the other four browsers.
  async before(capabilities, _specs, browser) {
    if ((capabilities as WebdriverIO.Capabilities).browserName?.toLowerCase() !== "safari") return;
    // An override is not wrapped as the commands are, so it waits for the element itself: an assertion
    // on an element that appears after the call must wait for it, as it does with the driver's own text.
    await browser.overwriteCommand("getText", async function (this: WebdriverIO.Element) {
      await this.waitForExist();
      // innerText puts a tab between table cells, where the other drivers' text has a space. The
      // other drivers leave out what is not shown, transparent text included, which checkVisibility
      // does not test by default: a transparent element or ancestor gives no text, and the text nodes
      // under a transparent descendant are emptied for the read and put back, in one synchronous step.
      // Only the text goes, so every box stays and a block boundary still breaks the line.
      return browser.execute((e: HTMLElement) => {
        const transparent = (x: Element) => getComputedStyle(x).opacity === "0";
        for (let p: Element | null = e; p; p = p.parentElement) if (transparent(p)) return "";
        if (!e.checkVisibility({ visibilityProperty: true })) return "";
        const texts = new Map<Text, string>();
        for (const t of [...e.querySelectorAll<HTMLElement>("*")].filter(transparent)) {
          const walker = document.createTreeWalker(t, NodeFilter.SHOW_TEXT);
          for (let n = walker.nextNode() as Text | null; n; n = walker.nextNode() as Text | null) texts.set(n, n.data);
        }
        for (const n of texts.keys()) n.data = "";
        try {
          const text = e.innerText.replace(/\t/g, " ");
          // Only where text was emptied, the space before a transparent box stays in innerText, where a
          // collapsed one would not: it goes, at a line's end and at either end of the whole. Any other
          // text is returned as innerText has it, preformatted whitespace included.
          return texts.size === 0 ? text : text.replace(/ +$/gm, "").replace(/^[ \n]+|[ \n]+$/g, "");
        } finally {
          for (const [n, data] of texts) n.data = data;
        }
      }, this as unknown as HTMLElement);
    }, { attachToElement: true });
    // safaridriver answers "Navigate To" while the new document is still `interactive` (1088 of 1759
    // navigations on the runner), and a find that follows can fail with "Stale element found when trying to
    // create the node handle", which an assertion's wait does not retry (ginsys/bronzeward#1028: 14 runs of
    // 180 without this wait, none with it). The command returns once the document is complete, as the
    // other four drivers' does; the read is an execute, which creates no node handle. safari-navigation.ts
    // holds the wait.
    await browser.overwriteCommand("url", async function (this: WebdriverIO.Browser, orig: (path: string, options?: unknown) => Promise<unknown>, path: string, options?: unknown) {
      const result = await orig(path, options);
      await settled(() => this.execute(() => document.readyState), path, (until, timeout) => this.waitUntil(until, { timeout }));
      return result;
    });
    // A key sequence sent as one action loses its spaces and its repeated characters in safaridriver
    // ("machine:\n  nodeLabels:" arrives as "machine:\n nodeLabels:"), where the same keys one at a time
    // arrive whole. safari-keys.ts decides the calls: text goes one character at a time, as an array of
    // characters or as a string, and Tab goes as Option+Tab.
    await browser.overwriteCommand("keys", async function (origKeys: (v: string | string[]) => Promise<void>, value: string | string[]) {
      for (const k of safariKeys(value)) await origKeys(k);
    });
    // safaridriver clicks where the element was when the command started, so a click on a link that a
    // list read above it is about to push down lands on the link now under that point (measured on the
    // runner: a click on Imports opened a draft). The click waits until the element holds still.
    await browser.overwriteCommand("click", async function (this: WebdriverIO.Element, orig: (o?: unknown) => Promise<void>, options?: unknown) {
      await this.waitForExist();
      // Still means the same box in three reads in a row, 200 ms: one equal pair can fall just before a shift.
      let last = "";
      let still = 0;
      for (let i = 0; i < 20 && still < 2; i++) {
        const now = await browser.execute((e: HTMLElement) => { const r = e.getBoundingClientRect(); return `${r.x},${r.y},${r.width},${r.height}`; }, this as unknown as HTMLElement);
        still = now === last ? still + 1 : 0;
        last = now;
        if (still < 2) await browser.pause(100);
      }
      (globalThis as { bwStillClicks?: number }).bwStillClicks = ((globalThis as { bwStillClicks?: number }).bwStillClicks ?? 0) + 1;
      return orig(options);
    }, { attachToElement: true });
    // Safari's click on an option fails as not interactable while the select is out of view, and when it
    // works it can set the value without the input and change events the page listens to (measured on
    // the runner: the second choice of a form left the page's choice unmade). Its type-ahead cannot
    // pick an option whose label holds a space (the space opens the popup). So the choice is made as
    // the page hears one: the value set and the two events raised, the select scrolled to first.
    for (const name of ["selectByAttribute", "selectByVisibleText", "selectByIndex"] as const) {
      await browser.overwriteCommand(name, async function (this: WebdriverIO.Element, _orig: unknown, a: string | number, b?: string | number) {
        await this.waitForExist();
        await this.scrollIntoView({ block: "center" });
        const error = await browser.execute((e: HTMLSelectElement, how: string, x: string | number, y: string | number | undefined) => {
          const o = [...e.options];
          const hit = how === "selectByIndex" ? o[Number(x)] : how === "selectByVisibleText" ? o.find((p) => p.text.trim() === String(x).trim()) : o.find((p) => String(p.getAttribute(String(x))) === String(y));
          if (hit === undefined) return "no such option";
          // The driver's own click leaves a disabled select or option as it is.
          if (e.disabled || hit.disabled || (hit.parentElement instanceof HTMLOptGroupElement && hit.parentElement.disabled)) return "the select or the option is disabled";
          if (e.value === hit.value) return "";
          e.value = hit.value;
          e.dispatchEvent(new Event("input", { bubbles: true }));
          e.dispatchEvent(new Event("change", { bubbles: true }));
          return e.value === hit.value ? "" : "the page took the choice back";
        }, this as unknown as HTMLSelectElement, name, a, b);
        if (error !== "") throw new Error(`${name}(${String(a)}${b === undefined ? "" : `, ${String(b)}`}): ${error}`);
      }, { attachToElement: true });
    }
  },

  // The target counts every request but its own two control routes, and answers with the canary
  // (readable by the page, so the positive control delivers it into the page's memory).
  async onPrepare() {
    // First, before anything starts: a binary that is not there fails the run by name.
    if (missingBinaries(root, ["webhost"]).length > 0) throw new SevereServiceError(`not an executable file: bin/webhost (mise run browser builds it)`);
    target = createServer((req, res) => {
      if (req.url === "/__hits") return res.end(JSON.stringify({ hits }));
      if (req.url === "/__reset") return (hits = 0), res.end();
      hits++;
      res.writeHead(200, { "Cache-Control": "no-store", "Access-Control-Allow-Origin": PAGE_ORIGIN }).end(canary);
    });
    await new Promise<void>((resolve) => target!.listen(Number(new URL(TARGET_ORIGIN).port), "127.0.0.1", resolve));
    // Every token carries the run's canary, so the artifact scan finds any token an artifact keeps.
    // Severe, so that a provider port held by an earlier run stops the run instead of its specs.
    ({ server: provider } = await startProvider({
      issuer: FAKE_ISSUER, pageOrigin: PAGE_ORIGIN, clientId: "synthetic-web-client", next: () => ({ access: `${canary}-access`, sub: "h-synthetic" }),
    }).catch((e: unknown) => {
      throw new SevereServiceError(`the synthetic provider did not start: ${String(e)}`);
    }));
    // A host left from an earlier run would answer the readiness check below with an old build.
    const hostUrl = `${PAGE_ORIGIN.replace("localhost", "127.0.0.1")}/`;
    if (await fetch(hostUrl).then(() => true, () => false)) throw new SevereServiceError("something already answers on the static host's port");
    // The client configuration names this run's issuer, so it is written for the run.
    clientDir = mkdtempSync(`${tmpdir()}/bw-client-`);
    const clientConfig = `${clientDir}/bronzeward-client.json`;
    writeFileSync(clientConfig, JSON.stringify({ issuer: FAKE_ISSUER, audience: "bronzeward", cliClientId: "synthetic-cli-client", webClientId: "synthetic-web-client" }));
    host = spawnChild(binPath(root, "webhost"), [
      "-listen", PAGE_LISTEN, "-dist", `${web}dist`, "-headers", `${web}headers.json`, "-client-config", clientConfig,
      "-issuer", FAKE_ISSUER, "-provider-origins", TARGET_ORIGIN, "-redirect-api", TARGET_ORIGIN,
    ], { stdio: "inherit" });
    for (let i = 0; ; i++) {
      const answered = await fetch(hostUrl).then((r) => r.ok, () => false);
      // Read after the request settles: a failed spawn is reported while the request is in flight, and
      // fails the run even where something else answers on the host's port.
      const why = died(host);
      if (why !== undefined) throw new SevereServiceError(why);
      if (answered) break;
      if (i === 50 || host.exitCode !== null) throw new SevereServiceError("the static host did not answer within 10 s");
      await new Promise((r) => setTimeout(r, 200));
    }
  },
  onComplete: () => printFailure(() => {
    host?.kill();
    target?.close();
    provider?.close();
    if (clientDir !== undefined) rmSync(clientDir, { recursive: true, force: true });
    removeProfiles();
  }),
};
