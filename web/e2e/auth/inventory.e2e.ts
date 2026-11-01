// PAGE-01, PAGE-02 (conformance/cases/) and the machines and clusters views: the fixture page
// against a disposable deployment through the injector, with synthetic clusters and machines
// recorded through the API as h-author. The deployment has no Talos node, so W3's machine and
// observation reads with drift, releases and observations are synthetic bodies the injector
// serves in place of the server's, typed by the generated API types.
import { randomBytes } from "node:crypto";
import { $, $$, browser, expect } from "@wdio/globals";
import type { components } from "../../src/api/v1";
import { REVOKED } from "../../src/app.tsx";
import { short } from "../../src/list.tsx";
import { FROZEN, HEALTH, type Observation, sentences } from "../../src/machine.tsx";
import type { Machine } from "../../src/machines.tsx";
import { EPOCH_CHANGED, TRANSPORT_FAILURE } from "../../src/request.ts";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { answers, faults, fixtureSignIn, holding, mint, param, release, seen, storageEmpty, where } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;

async function disposable(q = "start=normal"): Promise<string> {
  const res = await fetch(`${CONTROL}/disposable?${q}`, { method: "POST" });
  expect(res.status).toBe(200);
  return res.text();
}

let keys = 0;
async function write(at: string, path: string, body: object, human = "h-author"): Promise<{ id: string }> {
  const res = await fetch(`${at}/api/v1${path}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${mint(human)}`, "Content-Type": "application/json", "Idempotency-Key": `inventory-${Date.now()}-${keys++}` },
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

  it("a cluster filter in the query stays shown while the clusters are not read, and their read without answer is shown", async () => {
    const at = await disposable();
    const [a, b] = [await cluster(at, "lab-a"), await cluster(at, "lab-b")];
    const [id] = await machines(at, a.id, 1);
    await machines(at, b.id, 1, 1);
    // A deep link filtered to lab-a; the cluster filter's read is held, then finds no answer.
    expect((await fetch(`${CONTROL}/next?human=h-viewer`)).ok).toBe(true);
    await faults({ upstream: at, redirect: "/clusters", held: "/clusters" });
    await browser.url(`${FIXTURE_PAGE}/machines?cluster=${a.id}`);
    await asked("/clusters", 1);
    await shows(1);
    expect(await rows()).toEqual([id]);
    const select = $("label*=Cluster").$("select");
    expect(await select.getValue()).toBe(a.id);
    await release();
    await statusIs(TRANSPORT_FAILURE);
    expect(await select.getValue()).toBe(a.id);
    expect(await rows()).toEqual([id]);
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
    // The read again is the third list request and the second first-page answer.
    await asked("/machines", 3);
    await browser.waitUntil(async () => (await answers()).filter((a) => a === "/machines 200").length === 2, { timeout: 20_000, timeoutMsg: "the newer list read did not answer" });
    await release();
    await statusIs(REVOKED);
    expect(await $('[data-testid="principal"]').isExisting()).toBe(false);
  });
});

const texts = (sel: string) => $$(sel).map((el) => el.getText());
const timelineReads = async (id: string) => (await answers()).filter((a) => a.startsWith(`/machines/${id}/timeline`)).length;

describe("the machine detail (W3)", () => {
  it("the §2.4 sentences, each observation's unread causes and the timeline, never a healthy state", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [id] = await machines(at, cl.id, 1);
    await write(at, `/machines/${id}/freezes`, { reason: "synthetic incident" });
    // Synthetic reads: the drift opened by obs_k, observed (highest basis that read the digest)
    // obs_m equal to the selected release, and a later read obs_p that could not read.
    const rel = (c: string) => `rel_${c.repeat(26)}`;
    const obs = (c: string, basis: number, at: string, unread: Observation["unread"] = {}): Observation => ({
      id: `obs_${c.repeat(26)}`, machine: id!, basis, revision: basis + 1, purpose: "scheduled", publicationGeneration: null, plan: null,
      operation: null, endpoint: "192.0.2.1:50000", controller: `ctl_${"c".repeat(26)}`, startedAt: at, at, access: null, smbiosUuid: null,
      talosNodeId: "synthetic-node-0", talosClusterId: null, assignmentEvidence: null, runningVersion: null, resourceVersion: null, health: null, unread,
    });
    const k = obs("k", 2, "2026-10-26T08:41:00Z");
    const m1 = obs("m", 4, "2026-10-26T08:46:00Z");
    const p = obs("p", 5, "2026-10-26T08:51:00Z", { configDigest: { code: "unreachable", cause: "node unreachable" } });
    const machine: Machine = {
      id: id!, cluster: cl.id, hardware: { smbiosUuid: null, talosNodeId: "synthetic-node-0", serial: null }, platform: "metal", talosEndpoint: "192.0.2.1:50000",
      desired: rel("b"), applied: { release: rel("a"), source: "operation" }, pendingConvergence: true,
      observed: { observation: m1.id, at: m1.at, release: rel("b") }, frozen: true, scopeState: "normal",
      openDrift: { revision: 3, observation: k.id, lastObservation: m1.id, lateLanding: null },
    };
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at, bodies: { [`/machines/${id}`]: machine, [`/machines/${id}/observations`]: { items: [k, m1, p] } } })).toMatch(SERVER);
    await $(`a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(`Machine ${id}`);
    const said = await texts('[data-testid="sentences"] li');
    expect(said).toEqual(sentences(machine, [p, m1, k]));
    expect(said[0]).toMatch(new RegExp(`^Drifted since 2026-10-2\\d \\d\\d:\\d\\d: a read differed from what Bronzeward last verified \\(${short(rel("a"))}\\)\\.$`));
    expect(said).toContain("It equals the selected release; that does not close the drift.");
    expect(said).toEqual(expect.arrayContaining([expect.stringMatching(/^The latest read at 2026-10-2\d \d\d:\d\d could not read: configDigest \(node unreachable\)\.$/)]));
    // The observed read's attribution is shown under the open drift too (#423 C5).
    expect(await $('[data-testid="observed"]').getText()).toMatch(new RegExp(`^read at 2026-10-2\\d \\d\\d:\\d\\d \\(${short(m1.id)}\\): attributed to ${short(rel("b"))}$`));
    expect(said.at(-1)).toBe(HEALTH);
    // Pending convergence is not stated while the drift is open (§2.4: they are different).
    expect(said.join(" ")).not.toContain("Pending convergence");
    expect(await texts('[data-testid="observations"] tbody tr td:last-child')).toEqual(["configDigest (node unreachable)", "nothing", "nothing"]);
    // The principal is the identity Bronzeward recorded for h-author, shortened (G23).
    await expect($('[data-testid="timeline"]')).toHaveText(expect.stringMatching(/freeze idn_[a-z2-7]{4}…[a-z2-7]{4} author "synthetic incident"/), { wait: 20_000 });
    expect(await $("main").getText()).not.toMatch(/healthy|converged/i);
  });

  it("a machine never read: the absent fields said so, without a time; the timeline polled every 5 s or slower while shown", async function () {
    this.timeout(90_000);
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [id] = await machines(at, cl.id, 1);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await $(`a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(`Machine ${id}`);
    expect(await texts('[data-testid="sentences"] li')).toEqual(["No configuration read recorded.", "Not applied or adopted by Bronzeward.", "No release selected.", HEALTH]);
    await expect($("main")).toHaveText(expect.stringContaining("No observation recorded."));
    // A freeze made outside the page reaches the shown timeline by a poll.
    await write(at, `/machines/${id}/freezes`, { reason: "synthetic poll check" });
    await expect($('[data-testid="timeline"]')).toHaveText(expect.stringContaining('"synthetic poll check"'), { wait: 20_000 });
    const before = await timelineReads(id!);
    await browser.pause(11_000);
    const polled = (await timelineReads(id!)) - before;
    expect(polled).toBeGreaterThanOrEqual(1);
    expect(polled).toBeLessThanOrEqual(3);
    // Off screen, no more polls.
    await $('nav[aria-label="Main"]').$("a=Machines").click();
    await expect($("main h2")).toHaveText("Machines");
    await browser.pause(1000); // a poll sent just before leaving is still answered
    const left = await timelineReads(id!);
    await browser.pause(6_000);
    expect(await timelineReads(id!)).toBe(left);
  });

  it("#423 C4: Back/Forward to the same machine under another query while a timeline poll is in flight keeps the timeline polled", async function () {
    this.timeout(90_000);
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [id] = await machines(at, cl.id, 1);
    const path = `/machines/${id}/timeline`;
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await $(`a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(`Machine ${id}`);
    await expect($("main")).toHaveText(expect.stringContaining("No timeline entry recorded."));
    // The next poll is held; once the injector holds it, the view moves while it is in flight, so
    // the client discards its answer.
    await faults({ upstream: at, held: path });
    await browser.waitUntil(async () => (await holding()).includes(path), { timeout: 20_000, timeoutMsg: "no timeline poll was held" });
    await browser.execute((to: string) => {
      history.pushState(null, "", to);
      dispatchEvent(new PopStateEvent("popstate"));
    }, `/machines/${id}?from=c4`);
    await expect($("main h2")).toHaveText(`Machine ${id}`);
    await faults({ upstream: at });
    await release();
    // The timeline still polls under the new view.
    const after = await timelineReads(id!);
    await browser.pause(11_000);
    expect((await timelineReads(id!)) - after).toBeGreaterThanOrEqual(1);
  });

  it("#375: the timeline's newest page first, never the whole timeline; Load older appends the older page and is gone at its start", async function () {
    this.timeout(120_000);
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [id] = await machines(at, cl.id, 1);
    // More entries than the server's 50-entry page: freezes and unfreezes, each with its step.
    // h-all unfreezes: the deployment denies h-approver (AUTH-10).
    for (let i = 0; i < 30; i++) {
      await write(at, `/machines/${id}/freezes`, { reason: `synthetic step ${2 * i}` });
      await write(at, `/machines/${id}/unfreezes`, { reason: `synthetic step ${2 * i + 1}` }, "h-all");
    }
    const all = await fetch(`${at}/api/v1/machines/${id}/timeline?limit=500`, { headers: { Authorization: `Bearer ${mint("h-viewer")}` } });
    const total = ((await all.json()) as { items: unknown[] }).items.length;
    expect(total).toBeGreaterThan(50);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await $(`a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(`Machine ${id}`);
    const entries = () => $$('[data-testid="timeline"] tbody tr');
    await browser.waitUntil(async () => (await entries().length) === 50, { timeout: 20_000, timeoutMsg: "the timeline did not show its newest page" });
    const steps = async () => (await texts('[data-testid="timeline"] tbody tr td:last-child')).map((t) => /synthetic step (\d+)/.exec(t)?.[1]).filter((s) => s !== undefined).map(Number);
    const newest = await steps();
    expect(newest[0]).toBe(59);
    expect(newest).toEqual([...newest].sort((x, y) => y - x));
    // One request read that page, newest first; the polls read on from its tail, not from the start.
    const reads = (await answers()).filter((a) => a.startsWith(`/machines/${id}/timeline`));
    expect(reads[0]).toBe(`/machines/${id}/timeline?order=newest 200`);
    expect(reads.slice(1).every((a) => /\?cursor=\S+ 200$/.test(a))).toBe(true);
    await $("button=Load older").click();
    await browser.waitUntil(async () => (await entries().length) === total, { timeout: 20_000, timeoutMsg: "Load older did not append the older page" });
    const shown = await steps();
    expect(shown.slice(0, newest.length)).toEqual(newest);
    expect(shown).toEqual([...shown].sort((x, y) => y - x));
    expect(shown.at(-1)).toBe(0);
    expect((await answers()).filter((a) => a.startsWith(`/machines/${id}/timeline?order=newest&cursor=`))).toHaveLength(1);
    expect(await $("button=Load older").isExisting()).toBe(false);
  });

  it("#424 item 2: a poll with new timeline entries reads the machine and its observations again; the Frozen line and the Observed row follow", async function () {
    this.timeout(90_000);
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [id] = await machines(at, cl.id, 1);
    const obs = (c: string, basis: number, when: string): Observation => ({
      id: `obs_${c.repeat(26)}`, machine: id!, basis, revision: basis + 1, purpose: "scheduled", publicationGeneration: null, plan: null,
      operation: null, endpoint: "192.0.2.1:50000", controller: `ctl_${"c".repeat(26)}`, startedAt: when, at: when, access: null, smbiosUuid: null,
      talosNodeId: "synthetic-node-0", talosClusterId: null, assignmentEvidence: null, runningVersion: null, resourceVersion: null, health: null, unread: {},
    });
    const [m1, m2] = [obs("m", 2, "2026-10-26T08:46:00Z"), obs("n", 3, "2026-10-26T08:51:00Z")];
    const machine = (o: Observation, frozen: boolean): Machine => ({
      id: id!, cluster: cl.id, hardware: { smbiosUuid: null, talosNodeId: "synthetic-node-0", serial: null }, platform: "metal", talosEndpoint: "192.0.2.1:50000",
      desired: null, applied: null, pendingConvergence: false, observed: { observation: o.id, at: o.at, release: null }, frozen, scopeState: "normal", openDrift: null,
    });
    const bodies = (o: Observation[], frozen: boolean) => ({ [`/machines/${id}`]: machine(o[0]!, frozen), [`/machines/${id}/observations`]: { items: o } });
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at, bodies: bodies([m1], false) })).toMatch(SERVER);
    await $(`a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(`Machine ${id}`);
    expect(await texts('[data-testid="sentences"] li')).not.toContain(FROZEN);
    expect(await $('[data-testid="observed"]').getText()).toContain(short(m1.id));
    const reads = async () => (await answers()).filter((a) => a === `/machines/${id} 200`).length;
    const before = await reads();
    // The first timeline read must end before the freeze: a first read that already holds it is no advance.
    const timelineRead = async () => (await $('[data-testid="timeline"]').isExisting()) || (await $("main").getText()).includes("No timeline entry recorded.");
    await browser.waitUntil(timelineRead, { timeout: 20_000, timeoutMsg: "the first timeline read did not end" });
    // A freeze recorded outside the page, and a later read, reach the page by the timeline's poll.
    await faults({ upstream: at, bodies: bodies([m2, m1], true) });
    await write(at, `/machines/${id}/freezes`, { reason: "synthetic re-read check" });
    await expect($('[data-testid="timeline"]')).toHaveText(expect.stringContaining('"synthetic re-read check"'), { wait: 20_000 });
    await browser.waitUntil(async () => (await texts('[data-testid="sentences"] li')).includes(FROZEN), { timeout: 20_000, timeoutMsg: "the Frozen line did not follow the timeline" });
    expect(await $('[data-testid="observed"]').getText()).toContain(short(m2.id));
    // The observations are a read of their own: their answer can arrive after the machine's.
    const both = [short(m2.id), short(m1.id)];
    await browser.waitUntil(async () => JSON.stringify(await texts('[data-testid="observations"] tbody tr th')) === JSON.stringify(both), { timeout: 20_000, timeoutMsg: "the observations did not follow the machine" });
    expect((await reads()) - before).toBeGreaterThanOrEqual(1);
    // The timeline went on polling, not read anew.
    expect((await answers()).filter((a) => a.startsWith(`/machines/${id}/timeline?order=newest`))).toHaveLength(1);
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

// The read-only lists and items: the deployment's own empty lists and acts; sources, a release, a
// plan, an operation and a dependency as synthetic bodies (no publication runs on it), typed by the
// generated API types.
type S = components["schemas"];
const sid = (prefix: string, c: string) => `${prefix}_${c.repeat(26)}`;
const T0 = "2026-10-26T08:30:00Z";
function synthetic(cl: string, machine: string) {
  const author = sid("idn", "a");
  const frg: S["FragmentBody"] = { id: sid("frg", "f"), cluster: cl, scope: "cluster", name: "registries", layer: "cluster", revision: sid("frv", "r"), headRevision: 3, createdAt: T0 };
  const frv: S["FragmentRevisionBody"] = {
    id: frg.revision!, cluster: cl, name: "registries", layer: "cluster", author, createdAt: T0,
    document: `machine:\n  registries:\n    config:\n      registry.example.test:\n        auth: {username: "${"x".repeat(120)}"}\n`,
    declarations: { references: { "registry/example-pass": { kind: "string", version: 1 } }, embedded: [] },
  };
  const rel: S["ReleaseBody"] = {
    id: sid("rel", "h"), cluster: cl, draft: sid("drf", "d"), draftRevision: 2, operation: sid("op", "p"), publishedBy: { principal: author, role: "publisher" }, publishedAt: T0,
    renderer: { contract: "v1.13", machineryVersion: "v1.13.6", machineryChecksum: "sha256:synthetic", kubernetesVersion: "v1.34.1" },
    sources: [{ kind: "fragment", head: frg.id, revision: frv.id, headRevision: 3 }],
    machines: [{ machine, mode: "metal", review: "redacted", publicationObservation: sid("obs", "o") }],
  };
  const plan: S["PlanBody"] = {
    id: sid("pln", "q"), revision: 1, state: "proposed", committedOperation: null, approval: null, cluster: cl, machine, release: rel.id, operation: "apply-config",
    mode: "no-reboot", assignmentRevision: sid("asr", "s"), desiredRelease: rel.id, baselineRevision: 4, driftRecord: null, route: "192.0.2.1:50000",
    maxObservationAgeSeconds: 300, checkValiditySeconds: 120, transportDeadlineSeconds: 60, verificationDeadlineSeconds: 600, maxAttempts: 3, rolloutLimit: 1,
    approvalPolicy: "one-approver", createdBy: { principal: author, role: "publisher" }, epoch: "ep_synthetic", timelineRevision: 7, createdAt: T0, expiresAt: "2026-10-27T08:30:00Z",
    evidence: {
      diff: { from: sid("rel", "g"), to: rel.id, withheld: false, unified: "--- applied\n+++ release\n machine:\n   nodeLabels:\n+    team: storage\n" },
      validation: { result: "passed", at: "publication", contract: "v1.13", kubernetesVersion: "v1.34.1", machineryVersion: "v1.13.6", platformMode: "metal" },
      dryRun: { available: false, reason: "the PoC has none" },
    },
  };
  const op: S["OperationBody"] = {
    id: sid("op", "w"), kind: "apply-config", state: "unresolved", epoch: "ep_synthetic", lastEvent: 4, subject: { plan: plan.id, machine },
    createdBy: { principal: author, role: "approver" }, createdAt: T0, result: null, error: null,
    attempts: [{
      id: sid("att", "t"), number: 1, controller: "ctl-b", generation: 3, route: "192.0.2.1:50000", observation: sid("obs", "o"), transportDeadline: "2026-10-26T08:32:00Z",
      verificationDeadline: "2026-10-26T08:41:00Z", at: "2026-10-26T08:31:00Z", revision: 8, classification: null, response: null, accounting: null,
    }],
  };
  const dep: S["DependencyBody"] = {
    id: sid("dep", "e"), provider: "openbao", object: "registry/example-pass", version: 1, created: T0, class: "unknown", reason: "provider unreachable",
    firstRetainedAt: T0, unknownSince: "2026-10-26T08:40:00Z", observedFrom: "2026-10-26T08:40:00Z", recordedAt: "2026-10-26T08:41:00Z", stale: true,
  };
  const alert: S["AlertBody"] = {
    id: sid("dal", "l"), kind: "class-change", dependency: dep.id, provider: "openbao", object: dep.object, version: 1, created: T0, class: "unknown",
    reason: "provider unreachable", releases: [rel.id], deletion: null, observedFrom: dep.observedFrom, answerDate: null, epoch: "ep_synthetic", recordedAt: dep.recordedAt,
  };
  const page = <T,>(...items: T[]) => ({ items });
  const bodies: Record<string, unknown> = {
    "/fragments": page(frg), [`/fragments/${frg.id}`]: frg, [`/fragments/${frg.id}/revisions`]: page(frv), [`/fragment-revisions/${frv.id}`]: frv,
    "/releases": page(rel), [`/releases/${rel.id}`]: rel, "/plans": page(plan), [`/plans/${plan.id}`]: plan, "/operations": page(op), [`/operations/${op.id}`]: op,
    "/dependencies": { ...page(dep), lastPass: T0 } satisfies S["DependencyPage"], [`/dependencies/${dep.id}`]: dep, "/dependency-alerts": page(alert),
    [`/dependencies/${dep.id}/releases`]: page<S["DependencyReleaseBody"]>({ release: rel.id, cluster: cl, publishedAt: T0, records: [{ machine, kind: "effective" }] }),
    [`/dependencies/${dep.id}/alerts`]: page(alert),
  };
  return { frg, frv, rel, plan, op, dep, bodies };
}

const nav = async (title: string) => {
  await $('nav[aria-label="Main"]').$(`a=${title}`).click();
  await expect($("main h2")).toHaveText(title);
};
const loaded = () => browser.waitUntil(async () => !(await texts('main [data-testid="status"]')).some((t) => t.startsWith("Loading")), { timeout: 20_000, timeoutMsg: "a list was still loading" });

// The focused element after one Tab, or Shift+Tab, which must show its focus (WCAG 2.4.7).
async function tab(back = false) {
  await browser.keys(back ? ["Shift", "Tab"] : "Tab");
  const f = await browser.execute(() => {
    const e = document.activeElement as HTMLElement;
    const s = getComputedStyle(e);
    return { tag: e.tagName, text: e.textContent?.trim() ?? "", title: e.getAttribute("title"), outline: s.outlineStyle, width: parseFloat(s.outlineWidth) };
  });
  if (f.outline === "none" || f.width < 2) throw new Error(`focus on ${f.tag} "${f.text}" is not visible: outline ${f.outline} ${f.width}px`);
  return f;
}
// Tabs until the match, answering the text of every focus on the way.
async function tabTo(match: (f: Awaited<ReturnType<typeof tab>>) => boolean, what: string): Promise<string[]> {
  const passed: string[] = [];
  for (let i = 0; i < 40; i++) {
    const f = await tab();
    if (match(f)) return passed;
    passed.push(f.text);
  }
  throw new Error(`Tab never reached ${what}`);
}
const focused = () => browser.execute(() => document.activeElement?.id ?? "");
// A click on the header's title, which takes no focus, starts the next Tab from there.
const fromTitle = () => $("header h1").click();

// The lowest contrast of any shown text (WCAG 1.4.3) and the controls under 24 px (2.5.8), measured
// in the page.
const measure = () => browser.execute(() => {
  const lum = (c: string) => {
    const [r, g, b] = (c.match(/[\d.]+/g) ?? []).slice(0, 3).map((v) => {
      const s = Number(v) / 255;
      return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
  };
  const back = (e: Element | null): string => {
    for (; e !== null; e = e.parentElement) {
      const c = getComputedStyle(e).backgroundColor;
      if (!/rgba\(.*, 0\)|transparent/.test(c)) return c;
    }
    return "rgb(255, 255, 255)";
  };
  let low = Infinity;
  for (const e of document.querySelectorAll("body *")) {
    if (![...e.childNodes].some((n) => n.nodeType === 3 && n.textContent!.trim() !== "") || e.getClientRects().length === 0) continue;
    const [a, b] = [lum(getComputedStyle(e).color), lum(back(e))];
    low = Math.min(low, (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05));
  }
  const small = [...document.querySelectorAll("button, select, nav a")].map((e) => e.getBoundingClientRect()).filter((r) => r.width < 24 || r.height < 24).length;
  return { contrast: low, small };
});
const overflows = () => browser.execute(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

describe("the read-only lists and items", () => {
  it("empty lists are status messages; the audit lists the acts of real writes, principals shortened", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(SERVER);
    await nav("Sources");
    await loaded();
    expect(await texts('main [data-testid="status"]')).toEqual(expect.arrayContaining(["No fragments recorded yet.", "No profiles recorded yet.", "No assignments recorded yet.", "No dependency alerts raised."]));
    await expect($('[data-testid="last-pass"]')).toHaveText(/^(Last completed monitor pass: .+|No monitor pass has completed yet)\.$/);
    for (const [page, empty] of [["Changes", "No releases published yet."], ["Plans", "No plans created yet."], ["Operations", "No operations recorded yet."]] as const) {
      await nav(page);
      await expect($('main [data-testid="status"]')).toHaveText(empty);
    }
    await nav("Audit");
    const row = $(`//main//tbody/tr[contains(., "${short(cl.id)}")]`);
    await expect(row).toHaveText(expect.stringContaining("cluster.create"));
    expect(await row.$$("td").map((td) => td.getText())).toEqual(expect.arrayContaining([expect.stringMatching(/^idn_[a-z2-7]{4}…[a-z2-7]{4} \(\w+\)$/)]));
  });

  it("keyboard only: every focus visible, the skip link, a filter, and each identifier's link opens its item at its path", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [m] = await machines(at, cl.id, 1);
    const s = synthetic(cl.id, m!);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at, bodies: s.bodies })).toMatch(SERVER);
    await fromTitle();
    expect((await tab(true)).text).toBe("Skip to the page");
    await browser.keys("Enter");
    expect(await focused()).toBe("main");
    // T2: from the skipped-to list, the machine and its three values.
    await tabTo((f) => f.title === m, "the machine");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText(`Machine ${m}`);
    expect(await focused()).toBe("main");
    await expect($("main dl")).toHaveText(expect.stringContaining("Desired"));
    // T1: Sign out is on the way to the navigation.
    await fromTitle();
    expect(await tabTo((f) => f.text === "Sources", "the Sources link")).toContain("Sign out");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText("Sources");
    expect(await focused()).toBe("main");
    expect(await where()).toBe(`${FIXTURE_PAGE}/sources`);
    await tabTo((f) => f.title === s.frg.id, "the fragment");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText("Fragment registries");
    expect(await where()).toBe(`${FIXTURE_PAGE}/fragments/${s.frg.id}`);
    await tabTo((f) => f.title === s.frv.id && f.tag === "A", "the revision");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText(`Fragment revision ${short(s.frv.id)} of registries`);
    await expect($('[data-testid="document"]')).toHaveText(expect.stringContaining("registry.example.test"));
    await expect($("main")).toHaveText(expect.stringContaining("registry/example-pass"));
    // A filter set by keyboard lands in the query.
    await nav("Plans");
    await tabTo((f) => f.tag === "SELECT", "the state filter");
    await browser.keys("ArrowDown");
    await browser.waitUntil(async () => (await param("state")) === "proposed", { timeout: 10_000, timeoutMsg: "the state filter did not reach the query" });
    const seen = await measure();
    expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
    expect(seen.small).toBe(0);
  });

  it("a release, a plan, its operations and a dependency, each from its link, with their records", async () => {
    const at = await disposable();
    const cl = await cluster(at, "lab-a");
    const [m] = await machines(at, cl.id, 1);
    const s = synthetic(cl.id, m!);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at, bodies: s.bodies })).toMatch(SERVER);
    await nav("Changes");
    await $(`a[title="${s.rel.id}"]`).click();
    await expect($("main h2")).toHaveText(`Release ${short(s.rel.id)}`);
    await expect($("main")).toHaveText(expect.stringContaining("Source revisions this release was built from (these are not configuration)"));
    await expect($("main")).toHaveText(expect.stringContaining("contract v1.13 · machinery v1.13.6 · Kubernetes v1.34.1"));
    await expect($$(`main a[title="${s.frg.id}"]`)).toBeElementsArrayOfSize(1);
    await expect($$(`main a[title="${m}"]`)).toBeElementsArrayOfSize(1);
    await nav("Plans");
    await $(`a[title="${s.plan.id}"]`).click();
    await expect($("main h2")).toHaveText(`Plan ${short(s.plan.id)}: proposed`);
    await expect($('[data-testid="diff"]')).toHaveText(expect.stringContaining("+    team: storage"));
    await expect($("main")).toHaveText(expect.stringContaining("transport 1 min, verification 10 min, observation age 5 min, check validity 2 min, attempts 3, rollout limit 1"));
    await $("a=Operations of this plan").click();
    await expect($("main h2")).toHaveText("Operations");
    expect(await param("plan")).toBe(s.plan.id);
    await expect($("main")).toHaveText(expect.stringContaining(`Plan ${short(s.plan.id)} Show all`));
    // Show all removes its own button: focus moves on to the next filter, not to the page's start.
    await $("button=Show all").click();
    await browser.waitUntil(async () => (await param("plan")) === null, { timeoutMsg: "Show all kept the plan filter" });
    expect(await browser.execute(() => document.activeElement?.closest("label")?.textContent?.trim().split(/\s/)[0])).toBe("Kind:");
    await $(`a[title="${s.op.id}"]`).click();
    await expect($("main h2")).toHaveText(`Operation ${short(s.op.id)}: apply-config, unresolved`);
    await expect($$("main tbody tr")).toBeElementsArrayOfSize(1);
    expect((await answers()).some((a) => a.startsWith(`/operations?plan=${s.plan.id} `))).toBe(true);
    await nav("Sources");
    await expect($('[data-testid="last-pass"]')).toHaveText(/^Last completed monitor pass: .+\.$/);
    await expect($(`(//main//tbody/tr[contains(., "${short(s.dep.id)}")])[1]`)).toHaveText(expect.stringContaining("unknown (stale: not observed recently)"));
    // Linked from the dependencies table and from its alert's row: the first is the dependency's own.
    await $(`(//main//a[@title="${s.dep.id}"])[1]`).click();
    await expect($("main h2")).toHaveText("Dependency registry/example-pass, version 1");
    await expect($$(`main a[title="${s.rel.id}"]`)).toBeElementsArrayOfSize(2);
    await expect($("main")).toHaveText(expect.stringContaining("class-change"));
  });

  it("reflow at 320 CSS pixels: no page scrolls sideways; only a table scrolls on its own", async () => {
    const at = await disposable();
    // The longest valid name (128 bytes, no space to wrap at) is an option of the cluster filter.
    const cl = await cluster(at, `${"a".repeat(63)}.${"b".repeat(62)}.c`);
    const [m] = await machines(at, cl.id, 1);
    const s = synthetic(cl.id, m!);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at, bodies: s.bodies })).toMatch(SERVER);
    await browser.setViewport({ width: 320, height: 640 });
    try {
      expect(await browser.execute(() => innerWidth)).toBe(320);
      await expect($("main tbody tr")).toBeExisting();
      expect(["machines", await overflows()]).toEqual(["machines", 0]);
      for (const [page, item, title] of [["Plans", s.plan.id, "Plan"], ["Sources", s.frg.id, "Fragment"], ["Operations", s.op.id, "Operation"]] as const) {
        await nav(page);
        await loaded();
        expect([page, await overflows()]).toEqual([page, 0]);
        await $(`a[title="${item}"]`).click();
        await expect($("main h2")).toHaveText(expect.stringContaining(title));
        await loaded();
        expect([item, await overflows()]).toEqual([item, 0]);
      }
      await nav("Sources");
      await $(`a[title="${s.frg.id}"]`).click();
      await $(`main tbody a[title="${s.frv.id}"]`).click();
      await expect($('[data-testid="document"]')).toBeExisting();
      expect(["the fragment revision", await overflows()]).toEqual(["the fragment revision", 0]);
    } finally {
      await browser.setViewport({ width: 1280, height: 800 });
    }
  });
});

afterEach(storageEmpty);
