// PAGE-01, PAGE-02 (conformance/cases/) and the machines and clusters views: the fixture page
// against a disposable deployment through the injector, with synthetic clusters and machines
// recorded through the API as h-author.
import { randomBytes } from "node:crypto";
import { $, $$, browser, expect } from "@wdio/globals";
import { REVOKED } from "../../src/app.tsx";
import { EPOCH_CHANGED, TRANSPORT_FAILURE } from "../../src/request.ts";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { answers, faults, fixtureSignIn, mint, param, release, seen, storageEmpty, where } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;

async function disposable(q = "start=normal"): Promise<string> {
  const res = await fetch(`${CONTROL}/disposable?${q}`, { method: "POST" });
  expect(res.status).toBe(200);
  return res.text();
}

let keys = 0;
async function write(at: string, path: string, body: object): Promise<{ id: string }> {
  const res = await fetch(`${at}/api/v1${path}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${mint("h-author")}`, "Content-Type": "application/json", "Idempotency-Key": `inventory-${Date.now()}-${keys++}` },
    body: JSON.stringify(body),
  });
  expect(res.status).toBe(201);
  return (await res.json()) as { id: string };
}

// Synthetic values only: a random cluster ID and documentation-range endpoints.
const cluster = (at: string, name: string) =>
  write(at, "/clusters", { name, endpoint: `https://${name}.example.test:6443`, contract: "v1.13", talosClusterId: randomBytes(32).toString("base64url") + "=" });
async function machines(at: string, cl: string, n: number, from = 0): Promise<string[]> {
  const ids: string[] = [];
  for (let i = from; i < from + n; i++) {
    ids.push((await write(at, "/machines", { cluster: cl, talosNodeId: `synthetic-node-${i}`, platform: "metal", talosEndpoint: `192.0.2.${i % 250 + 1}:50000` })).id);
  }
  return ids.sort();
}

const rows = async () => $$("main tbody tr th a").map((a) => a.getAttribute("title"));
const count = () => $('[data-testid="count"]').getText();
const shows = (n: number) => browser.waitUntil(async () => (await rows()).length === n, { timeout: 20_000, timeoutMsg: `the list did not show ${n} rows` });
// The page has asked the injector for `path` at least `n` times since this sign-in.
const asked = (path: string, n: number) =>
  browser.waitUntil(async () => (await seen()).filter((p) => p === path).length >= n, { timeout: 20_000, timeoutMsg: `${path} was not asked ${n} times` });
// Read in the page on each try: one state's message replaces the element of the one before it.
const statusIs = (text: string) =>
  browser.waitUntil(async () => (await browser.execute(() => document.querySelector('[data-testid="status"]')?.textContent ?? "")) === text, { timeout: 20_000, timeoutMsg: `the status did not show ${text}` });

describe("the machines", () => {
  it("empty: a deployment with no machine says so, as a status message", async () => {
    const at = await disposable();
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await expect($('[data-testid="status"]')).toHaveText("No machines recorded yet.");
    expect(await $('[data-testid="status"]').getAttribute("role")).toBe("status");
    await expect($("main h2")).toHaveText("Machines");
  });

  it("W2: endpoint, node ID and state per machine; the state filter in the browser, the cluster filter on the server, both in the query", async () => {
    const at = await disposable();
    const [a, b] = [await cluster(at, "lab-a"), await cluster(at, "lab-b")];
    const ids = await machines(at, a.id, 3);
    const other = await machines(at, b.id, 1, 3);
    await write(at, `/machines/${ids[0]}/freezes`, { reason: "synthetic incident" });
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await shows(4);
    expect(await count()).toBe("Showing 4 of 4.");
    const row = await $(`a[title="${ids[0]}"]`).parentElement().parentElement().$$("td").map((td) => td.getText());
    expect(row).toEqual([expect.stringMatching(/^192\.0\.2\.\d+:50000$/), expect.stringMatching(/^node synthetic-node-\d$/), "none", "none", "none", "Frozen, Not handed over"]);
    await $("label*=State").$("select").selectByAttribute("value", "frozen");
    await shows(1);
    expect(await rows()).toEqual([ids[0]]);
    expect(await count()).toBe("Showing 1 of 4.");
    expect(await param("state")).toBe("frozen");
    await $("label*=Cluster").$("select").selectByVisibleText("lab-b");
    await expect($('[data-testid="status"]')).toHaveText("No machine matches this filter.", { wait: 20_000 });
    await $("label*=State").$("select").selectByAttribute("value", "");
    await shows(1);
    expect(await rows()).toEqual(other);
    expect(await param("cluster")).toBe(b.id);
    expect(await answers()).toContain(`/machines?cluster=${b.id} 200`);
    await expect($("main")).toHaveText(expect.stringContaining("Health is not checked."));
  });

  it("links: a machine's path opens its item, in the page, after a reload and after sign-in in a new tab", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [id] = await machines(at, cl.id, 1);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await $(`a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(`Machine ${id}`);
    expect(await where()).toBe(`${FIXTURE_PAGE}/machines/${id}`);
    await $(`a[title="${cl.id}"]`).click();
    await expect($("main h2")).toHaveText("Cluster lab-a");
    await $("a=Machines in this cluster").click();
    await shows(1);
    expect(await param("cluster")).toBe(cl.id);
    // A new tab on the path takes this tab's sign-in (B1); over WebDriver BiDi newWindow does not
    // switch to it.
    const first = await browser.getWindowHandle();
    const tab = await browser.newWindow(`${FIXTURE_PAGE}/machines/${id}`);
    await browser.switchToWindow("handle" in tab ? tab.handle : tab.contextId);
    await expect($("main h2")).toHaveText(`Machine ${id}`, { wait: 20_000 });
    expect(await where()).toBe(`${FIXTURE_PAGE}/machines/${id}`);
    await browser.closeWindow();
    await browser.switchToWindow(first);
    // Loaded with no other tab open, the page signs in again (the token is in memory only) and
    // returns to the path (B2).
    await browser.url(`${FIXTURE_PAGE}/clusters/${cl.id}`);
    await expect($("main h2")).toHaveText("Cluster lab-a", { wait: 20_000 });
    expect(await where()).toBe(`${FIXTURE_PAGE}/clusters/${cl.id}`);
  });

  it("a delayed answer after a filter change is not rendered", async () => {
    const at = await disposable();
    const [a, b] = [await cluster(at, "lab-a"), await cluster(at, "lab-b")];
    await machines(at, a.id, 2);
    const late = await machines(at, b.id, 1, 2);
    // The unfiltered list is held 6 s; the filtered one answers at once.
    expect((await fetch(`${CONTROL}/next?human=h-viewer`)).ok).toBe(true);
    await faults({ upstream: at, hold: { path: "/machines", query: "", ms: 6000 } });
    await browser.url(`${FIXTURE_PAGE}/machines`);
    await $("label*=Cluster").$("select").$("option=lab-b").waitForExist({ timeout: 20_000 });
    await $("label*=Cluster").$("select").selectByVisibleText("lab-b");
    await shows(1);
    await browser.waitUntil(async () => (await answers()).includes("/machines 200"), { timeout: 20_000, timeoutMsg: "the held answer did not arrive" });
    await browser.pause(500);
    expect(await rows()).toEqual(late);
    expect(await count()).toBe("Showing 1 of 1.");
  });

  it("a state filter changed while the list is read reads it again under the new filter", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const ids = await machines(at, cl.id, 2);
    await write(at, `/machines/${ids[1]}/freezes`, { reason: "synthetic incident" });
    // The list's answers are held until released. From one state filter to another the server's
    // query and the read of every page stay the same; only the view changes, and the client
    // discards the answer read under the old one, so the list is asked again before any answers.
    expect((await fetch(`${CONTROL}/next?human=h-viewer`)).ok).toBe(true);
    await faults({ upstream: at, held: "/machines" });
    await browser.url(`${FIXTURE_PAGE}/machines?state=pending`);
    await asked("/machines", 1);
    await statusIs("Loading machines");
    await $("label*=State").$("select").selectByAttribute("value", "frozen");
    await asked("/machines", 2);
    await release();
    await shows(1);
    expect(await rows()).toEqual([ids[1]]);
    expect(await count()).toBe("Showing 1 of 2.");
  });

  it("a refresh keeps the list shown: a read again without answer leaves it under the offline banner", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const ids = await machines(at, cl.id, 2);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await shows(2);
    // The online event reads every resource again: the list's read finds no answer, and the
    // identity's and the cluster filter's answers are held until after, so none ends the banner.
    await faults({ upstream: at, redirect: "/machines", held: ["/identity", "/clusters"] });
    await browser.execute(() => void dispatchEvent(new Event("online")));
    await $('[data-testid="offline"]').waitForExist({ timeout: 10_000 });
    expect(await rows()).toEqual(ids);
    expect(await $('[data-testid="status"]').isExisting()).toBe(false);
    await faults({ upstream: at });
    await release();
  });

  it("each list keeps its own data: the machine list's first read without answer is disconnected, though the cluster filter's answered", async () => {
    const at = await disposable();
    await cluster(at, "lab-a");
    // The machine list's read is held until the cluster filter shows its answer, then finds none.
    expect((await fetch(`${CONTROL}/next?human=h-viewer`)).ok).toBe(true);
    await faults({ upstream: at, redirect: "/machines", held: "/machines" });
    await browser.url(`${FIXTURE_PAGE}/machines`);
    await asked("/machines", 1);
    await $("label*=Cluster").$("select").$("option=lab-a").waitForExist({ timeout: 20_000 });
    await release();
    await statusIs(TRANSPORT_FAILURE);
    expect(await $('[data-testid="offline"]').isExisting()).toBe(false);
  });

  it("a refresh holds Show more: while the list is read again, no next page is read", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    await machines(at, cl.id, 60);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await shows(50);
    // The online event reads the list again, held until released; the 50 rows stay shown.
    await faults({ upstream: at, held: "/machines" });
    await browser.execute(() => void dispatchEvent(new Event("online")));
    await asked("/machines", 2);
    await expect($("button=Show more")).toBeDisabled();
    await browser.execute(() => document.querySelector<HTMLButtonElement>('[data-testid="count"] button')!.click());
    await release();
    await expect($("button=Show more")).toBeEnabled({ wait: 10_000 });
    expect((await seen()).filter((p) => p === "/machines")).toHaveLength(2);
    expect(await count()).toBe("Showing 50; more pages to read. Show more");
  });

  it("a revocation on a next page a refresh superseded still ends the page", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    await machines(at, cl.id, 60);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await shows(50);
    // The next page is held and then refused identity-revoked; the online event's newer reads
    // answer first, so only the superseded read carries the revocation.
    await faults({ upstream: at, held: "/machines", revoked: "/machines" });
    await $("button=Show more").click();
    await asked("/machines", 2);
    await faults({ upstream: at });
    await browser.execute(() => void dispatchEvent(new Event("online")));
    await browser.waitUntil(async () => (await answers()).includes("/machines 200"), { timeout: 20_000, timeoutMsg: "the newer list read did not answer" });
    await release();
    await statusIs(REVOKED);
    expect(await $('[data-testid="principal"]').isExisting()).toBe(false);
  });
});

describe("cursor pages", () => {
  it("PAGE-01: a list over three pages shows every item once, in order", async function () {
    this.timeout(120_000);
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const ids = await machines(at, cl.id, 120);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await shows(50);
    expect(await count()).toBe("Showing 50; more pages to read. Show more");
    // One page at a time: while the next page is read (held until released), Show more is disabled
    // and a second click reads nothing.
    await faults({ upstream: at, held: "/machines" });
    await $("button=Show more").click();
    await asked("/machines", 2);
    await expect($("button=Show more")).toBeDisabled();
    await browser.execute(() => document.querySelector<HTMLButtonElement>('[data-testid="count"] button')!.click());
    await faults({ upstream: at });
    await release();
    await shows(100);
    expect((await answers()).filter((a) => a.startsWith("/machines?cursor="))).toHaveLength(1);
    await $("button=Show more").click();
    await shows(120);
    expect(await rows()).toEqual(ids);
    expect(await count()).toBe("Showing 120 of 120.");
    // The state filter reads every page first.
    await $("label*=State").$("select").selectByAttribute("value", "not-handed-over");
    await expect($('[data-testid="count"]')).toHaveText("Showing 120 of 120.", { wait: 20_000 });
    expect(await rows()).toEqual(ids);
  });

  it("PAGE-02: a cursor used after a recovery entry is refused 400 cursor-invalid; the list restarts and the change is shown", async function () {
    this.timeout(120_000);
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const ids = await machines(at, cl.id, 60);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await shows(50);
    // The same database under a recovery start, and its entry (synthetic values only).
    expect(await disposable("start=recovery&keep")).toBe(at);
    const now = new Date(Date.now() - 60_000).toISOString();
    const entry = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `page-02-${Date.now()}-entry` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-page-02", takenAt: now }], stoppedAt: now, reason: "PAGE-02 synthetic restore" }),
    });
    expect(entry.status).toBe(201);
    await $("button=Show more").click();
    await expect($('[data-testid="epoch-changed"]')).toHaveText(`${EPOCH_CHANGED}.`, { wait: 20_000 });
    await shows(50);
    expect(await rows()).toEqual(ids.slice(0, 50));
    expect((await answers()).filter((a) => a.startsWith("/machines"))).toEqual(["/machines 200", expect.stringMatching(/^\/machines\?cursor=\S+ 400$/), "/machines 200"]);
    expect(await count()).toBe("Showing 50; more pages to read. Show more");
  });
});

afterEach(storageEmpty);
