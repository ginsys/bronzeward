// Conformance cases TLS-02 (web client) and TLS-03 (the read case), conformance/cases/: the API
// answers a 307 to another origin, which the page's CSP admits, so only `redirect: 'error'` keeps
// the read from following it.
import { createHash } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { TRANSPORT_FAILURE } from "../src/request.ts";
import { TARGET_ORIGIN } from "./wdio.conf.ts";

const canary = process.env["BW_WEB_CANARY"]!;
const hits = async (): Promise<number> => ((await (await fetch(`${TARGET_ORIGIN}/__hits`)).json()) as { hits: number }).hits;

describe("an API redirect to another origin", () => {
  beforeEach(async () => {
    await fetch(`${TARGET_ORIGIN}/__reset`);
  });

  it("TLS-02, TLS-03: the read is not sent to the target and the page names no target", async () => {
    await browser.url("/machines?cluster=c1");
    const status = $('[data-testid="status"]');
    await browser.waitUntil(async () => (await status.getText()) !== "Loading");
    expect(await status.getText()).toBe(TRANSPORT_FAILURE);
    const page = await $("body").getText();
    expect(page).not.toContain("58444");
    expect(page).not.toContain("127.0.0.1");
    expect(await hits()).toBe(0);
  });

  // Positive control: the same request without `redirect: 'error'` does reach the target, so the
  // zero above is the page's refusal, not the CSP's or the injector's.
  it("a fetch that follows redirects reaches the target and reads the canary", async () => {
    await browser.url("/");
    // Only the body's digest crosses WebDriver: the harness log records every result.
    const digest = await browser.execute(async () => {
      const body = new TextEncoder().encode(await (await fetch("/api/v1/server")).text());
      return [...new Uint8Array(await crypto.subtle.digest("SHA-256", body))].map((b) => b.toString(16).padStart(2, "0")).join("");
    });
    expect(digest).toBe(createHash("sha256").update(canary).digest("hex"));
    expect(await hits()).toBe(1);
    // HTTP cache: a force-cache read of the canary goes to the target again, so nothing kept it.
    await browser.execute(async (url: string) => {
      await fetch(url, { cache: "force-cache" });
    }, `${TARGET_ORIGIN}/api/v1/server`);
    expect(await hits()).toBe(2);
  });

  // Client contracts §11.5 item 3: no browser storage holds a value; the page uses none.
  afterEach(async function () {
    const storage = await browser.execute(async () => ({
      local: Object.entries(localStorage),
      session: Object.entries(sessionStorage),
      indexedDB: "databases" in indexedDB ? (await indexedDB.databases()).map((d) => d.name ?? "") : ["unreadable"],
      caches: "caches" in self ? await caches.keys() : ["unreadable"],
    }));
    expect(storage).toEqual({ local: [], session: [], indexedDB: [], caches: [] });
    // The worker ID, one per capability, tells the two Firefoxes apart; the version alone may not.
    const { browserName, browserVersion } = browser.capabilities;
    const name = `${browserName}-${browserVersion}-${process.env["WDIO_WORKER_ID"]}-${this.currentTest?.title.slice(0, 20).replace(/\W+/g, "-")}`;
    await browser.saveScreenshot(`e2e/out/${name}.png`);
  });
});
