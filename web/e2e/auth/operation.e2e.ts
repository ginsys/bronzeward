// The operation page's reconnect, renewal and reset behaviour (ginsys/bronzeward#359): EVT-01 to EVT-05
// and the stream half of AUTH-06 (conformance/cases/) against the disposable deployment on a recovery
// start with the seed that has no attempt (internal/api/e2eseed_test.go): a cancel is refused once an
// attempt is recorded, so the operation of the ordinary seed cannot be resolved. Each case starts a
// fresh deployment, makes the entry as h-recovery (which takes the committed operation to unresolved
// under a new epoch, with its events), reads the operation's state and events first, then opens the
// operation page as h-viewer. The injector's `cut` fault closes the stream (injector.ts) and the
// resolution (cancelled, by h-recovery) is the event that follows. Every value is synthetic.
import { $, browser, expect } from "@wdio/globals";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import type { Faults } from "../injector.ts";
import { absent, answers, api, counts, disposable, faults, fixtureSignIn, mint, type Seed, storageEmpty } from "./helpers.ts";

const LIVE = "Live: connected (resumes after a drop)";
const ENDED = "Live: ended.";

interface Event { id: string; type: string }
// The deployment of a case: its URL, the operation, and the ids of the events the page will meet first.
interface Started { at: string; op: string; ids: string[] }

let current: Started | undefined;

// A fresh recovery start with the no-attempt seed and its entry made, so the operation is unresolved.
// State read first: the case does not run on any other.
async function start(): Promise<Started> {
  const at = await disposable("start=recovery&seed=noattempt");
  const seeded = await fetch(`${CONTROL}/seeded`);
  expect(seeded.status).toBe(200);
  const { operation: op } = (await seeded.json()) as Seed;
  const when = new Date(Date.now() - 60_000).toISOString();
  const entry = await fetch(`${at}/api/v1/recovery/entries`, {
    method: "POST",
    headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `evt-359-${Date.now()}-${Math.random().toString(36).slice(2)}` },
    body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-359", takenAt: when }], stoppedAt: when, reason: "synthetic restore, operation page cases" }),
  });
  expect(entry.status).toBe(201);
  const read = await api(at, "h-viewer", "GET", `/operations/${op}`);
  expect([read.status, read.body!["state"]]).toEqual([200, "unresolved"]);
  const events = await api(at, "h-viewer", "GET", `/operations/${op}/events`);
  const ids = (events.body!["items"] as Event[]).map((e) => e.id);
  expect(ids.length).toBeGreaterThanOrEqual(2);
  current = { at, op, ids };
  return current;
}

// Signs in as h-viewer with the faults and opens the operation page, whose stream is the first one
// opened after the faults were set.
async function open(s: Started, f: Faults = {}, flags: Record<string, string> = {}) {
  await fixtureSignIn("h-viewer", flags, { upstream: s.at, ...f });
  await browser.url(`${FIXTURE_PAGE}/operations/${s.op}`);
  await $('[data-testid="events"]').waitForExist({ timeout: 30_000 });
}

const rows = () => browser.execute(() => [...document.querySelectorAll('[data-testid="events"] li')].map((li) => li.textContent ?? ""));
const reach = (n: number) => browser.waitUntil(async () => (await rows()).length === n, { timeout: 30_000, timeoutMsg: `the page did not show ${n} events` });
const timeline = () => $('section[aria-label="Timeline"]').getText();
const streams = async () => (await (await fetch(`${CONTROL}/streams`)).json()) as string[];
const cuts = async () => (await (await fetch(`${CONTROL}/cuts`)).json()) as number;
const eventsOf = (s: Started) => `/operations/${s.op}/events`;
// On a new epoch the shell reads the server and the identity again and mounts the page anew (web/src/shell.tsx onEpochChange),
// whichever answer it learned the epoch from, so the reader's own notice of the clear (RESTORED, RESTORED_WHILE_READ) never
// reaches the page in these cases: the cases read the display's result and the shell's notice (§8.5 item 3). The reader's rule
// itself is read in web/src/progress.test.ts.
// The resolution that cancels the unresolved operation: one more event, the last.
const resolve = async (s: Started) => expect((await api(s.at, "h-recovery", "POST", `/operations/${s.op}/resolutions`, { to: "cancelled", reason: "synthetic resolution" })).status).toBe(201);
const epochOf = (id: string) => id.split(":", 1)[0]!;

describe("the operation page's stream (#359)", () => {
  // What the page and the injector held when a case failed, for the run's console (synthetic values only).
  afterEach(async function () {
    if (this.currentTest?.state !== "failed" || current === undefined) return;
    const s = current;
    const events = ((await api(s.at, "h-viewer", "GET", eventsOf(s))).body?.["items"] ?? []) as Event[];
    console.log(JSON.stringify({ case: this.currentTest.title.slice(0, 8), rows: await rows(), timeline: await timeline(), streams: await streams(), cuts: await cuts(), server: events.map((e) => e.id) }));
  });

  it("EVT-01: a stream cut mid-operation is opened again with Last-Event-ID, and no event is shown twice", async function () {
    this.timeout(120_000);
    const s = await start();
    const n = s.ids.length;
    await open(s, { cut: { after: n } });
    await reach(n);
    // The cut happened (its injected count is the control), and the page opened the stream again with the last id shown.
    await browser.waitUntil(async () => (await streams()).length >= 2, { timeout: 30_000, timeoutMsg: "the page did not open the stream again" });
    expect(await cuts()).toBe(1);
    expect(await streams()).toEqual([`${eventsOf(s)} -`, `${eventsOf(s)} ${s.ids[n - 1]}`]);
    await absent(async () => (await rows()).length !== n, "a repeated or missing event after the reconnect");
    // Every row is a receipt until the watch ends, and the page says it is live.
    expect((await rows()).every((r) => r.startsWith("received "))).toBe(true);
    expect(await timeline()).toContain(LIVE);
    await resolve(s);
    await reach(n + 1);
    await expect($('[data-testid="outcome"]')).toHaveText(expect.stringContaining("Cancelled: resolution"), { wait: 30_000 });
    expect((await rows()).filter((r) => r.includes("operation-state: unresolved → cancelled")).length).toBe(1);
    await browser.waitUntil(async () => (await timeline()).includes(ENDED), { timeout: 30_000, timeoutMsg: "the watch did not end at cancelled" });
    expect(await streams()).toHaveLength(2);
    await storageEmpty();
  });

  it("EVT-03: the watch continues past unresolved and ends at cancelled by a resolution", async function () {
    this.timeout(120_000);
    const s = await start();
    const n = s.ids.length;
    // The stream is cut while the operation is unresolved: the page reads the operation, finds it unresolved, and must go on, which a
    // stream that stays open never asks of it (the watch reads the operation only when a stream ends).
    await open(s, { cut: { after: n } });
    await reach(n);
    await browser.waitUntil(async () => (await streams()).length >= 2, { timeout: 30_000, timeoutMsg: "the page did not open the stream again on the unresolved operation" });
    // The page is on an unresolved operation: it says the outcome is unknown, offers the way to Recovery, and keeps following.
    await expect($('[data-testid="outcome"]')).toHaveText(expect.stringContaining("Outcome unknown"));
    await absent(async () => (await $('[data-testid="ended"]').isExisting()) || (await timeline()).includes(ENDED), "an end of the watch on an unresolved operation");
    expect(await timeline()).toContain(LIVE);
    expect(await streams()).toEqual([`${eventsOf(s)} -`, `${eventsOf(s)} ${s.ids[n - 1]}`]);
    await resolve(s);
    await expect($('[data-testid="outcome"]')).toHaveText(expect.stringContaining("Cancelled: resolution"), { wait: 30_000 });
    await browser.waitUntil(async () => (await timeline()).includes(ENDED), { timeout: 30_000, timeoutMsg: "the watch did not end at cancelled" });
    expect(await streams()).toHaveLength(2);
    await storageEmpty();
  });

  it("EVT-04: a stream cut after the terminal state and before its last event leaves the page to read the operation, drain the pages and show the event once", async function () {
    this.timeout(120_000);
    const s = await start();
    const n = s.ids.length;
    // The injector passes the n events the operation has, drops the next one the server writes (the cancellation's) and closes the stream.
    await open(s, { cut: { after: n, withhold: 1 } });
    await reach(n);
    // The receipt phase: each row shows the browser's clock and says so.
    expect((await rows()).every((r) => r.startsWith("received "))).toBe(true);
    await resolve(s);
    await browser.waitUntil(async () => (await cuts()) === 1, { timeout: 30_000, timeoutMsg: "the cancellation was not cut" });
    await reach(n + 1);
    await browser.waitUntil(async () => (await timeline()).includes(ENDED), { timeout: 30_000, timeoutMsg: "the watch did not end" });
    // The server's times replace the receipts, with the event the stream never carried, once; the stream was not opened again.
    const shown = await rows();
    await absent(async () => (await rows()).some((r) => r.startsWith("received ")), "a receipt row after the watch ended");
    expect(shown.filter((r) => r.includes("operation-state: unresolved → cancelled")).length).toBe(1);
    expect(shown).toHaveLength((((await api(s.at, "h-viewer", "GET", `/operations/${s.op}/events`)).body!["items"]) as Event[]).length);
    await expect($('[data-testid="outcome"]')).toHaveText(expect.stringContaining("Cancelled: resolution"));
    expect(await streams()).toEqual([`${eventsOf(s)} -`]);
    // The event pages were read to drain what the stream never carried, and once more for the server's times (the page's final
    // read): two reads, where a watch that does not drain makes one and the final read alone shows the same rows.
    expect((await answers()).filter((a) => a.startsWith(`${eventsOf(s)}`) && a.endsWith(" 200")).length).toBeGreaterThanOrEqual(2);
    await storageEmpty();
  });

  it("EVT-02: a Last-Event-ID of an earlier epoch gets the server's reset, and the display is cleared and shown again", async function () {
    this.timeout(120_000);
    const s = await start();
    const n = s.ids.length;
    // The first stream passes the n events and is cut; the page's reopening is held, so the new epoch is committed between the
    // page's operation read (the old epoch) and its stream, which then carries the old Last-Event-ID.
    await open(s, { cut: { after: n }, held: eventsOf(s) });
    await browser.waitUntil(async () => (await (await fetch(`${CONTROL}/held`)).json() as string[]).includes(eventsOf(s)), { timeout: 30_000, timeoutMsg: "the first stream was not held" });
    // Let the first stream through without waiting for it to end: the release answers once the held answers are sent.
    void fetch(`${CONTROL}/release`, { method: "POST" }).catch(() => {});
    await reach(n);
    await browser.waitUntil(async () => (await cuts()) === 1 && (await (await fetch(`${CONTROL}/held`)).json() as string[]).includes(eventsOf(s)), { timeout: 30_000, timeoutMsg: "the reopening was not held" });
    expect((await fetch(`${CONTROL}/epoch`, { method: "POST" })).status).toBe(200);
    // The held reopening goes on: the epoch it meets is not the Last-Event-ID's.
    await faults({ upstream: s.at });
    void fetch(`${CONTROL}/release`, { method: "POST" }).catch(() => {});
    // The reopening carried the old Last-Event-ID; the new epoch is told (§8.5 item 3), the display is cleared, and a stream is opened
    // again from the start, so the n events are shown once more and no more.
    await expect($('[data-testid="epoch-changed"]')).toBeExisting({ wait: 30_000 });
    await browser.waitUntil(async () => (await streams()).length >= 3, { timeout: 30_000, timeoutMsg: "the page did not open the stream again from the start" });
    await reach(n);
    await absent(async () => (await rows()).length !== n, "a repeated or missing event after the replay");
    const opened = await streams();
    expect(opened.slice(0, 2)).toEqual([`${eventsOf(s)} -`, `${eventsOf(s)} ${s.ids[n - 1]}`]);
    expect(opened.slice(2).every((o) => o === `${eventsOf(s)} -`)).toBe(true);
    const now = (await api(s.at, "h-viewer", "GET", `/operations/${s.op}/events`)).body!["items"] as Event[];
    expect(epochOf(now[0]!.id)).not.toBe(epochOf(s.ids[0]!));
    await storageEmpty();
  });

  it("EVT-05: an event of another epoch on an open stream ends it, the display is cleared and the stream is opened again without Last-Event-ID", async function () {
    this.timeout(120_000);
    const s = await start();
    const n = s.ids.length;
    await open(s);
    await reach(n);
    expect(await streams()).toEqual([`${eventsOf(s)} -`]);
    // The harness commits a new epoch and the entry's ownership-transition event for the operation, which stays unresolved.
    expect((await fetch(`${CONTROL}/epoch?operation=${s.op}`, { method: "POST" })).status).toBe(200);
    // The new epoch is told (§8.5 item 3) and the stream is opened again without Last-Event-ID.
    await expect($('[data-testid="epoch-changed"]')).toBeExisting({ wait: 30_000 });
    await browser.waitUntil(async () => (await streams()).length >= 2, { timeout: 30_000, timeoutMsg: "the page did not open the stream again" });
    expect(await streams()).toEqual([`${eventsOf(s)} -`, `${eventsOf(s)} -`]);
    // The display shows every event once, with the event of the new epoch, and the operation is still being followed.
    await reach(n + 1);
    await absent(async () => (await rows()).length !== n + 1, "a repeated or missing event after the replay");
    expect(await timeline()).toContain(LIVE);
    await storageEmpty();
  });

  it("AUTH-06, the stream half: the stream the token's expiry ends is opened again with the renewed token and Last-Event-ID", async function () {
    this.timeout(180_000);
    const s = await start();
    const n = s.ids.length;
    // The token is issued 270 s ago, so it expires 30 s after the sign-in, and the server ends the stream at the expiry of the token
    // that opened it; expires_in 24 s brings the renewal 12 s after the sign-in (the page renews at max(left - 60 s, left / 2),
    // web/src/auth.ts:18-22), with a token minted then, which has its full lifetime.
    const begun = Date.now();
    await open(s, {}, { rotate: "1", lifetime: "24", age: "270" });
    await reach(n);
    await browser.waitUntil(async () => (await streams()).length >= 2, { timeout: 90_000, timeoutMsg: "the page did not open the stream again after the token's expiry" });
    // The renewal comes 12 s after the sign-in: a reopening before the original token's expiry (30 s after it) is not the one this case is about.
    expect(Date.now() - begun).toBeGreaterThanOrEqual(25_000);
    expect((await counts()).refresh).toBe(1);
    expect(await streams()).toEqual([`${eventsOf(s)} -`, `${eventsOf(s)} ${s.ids[n - 1]}`]);
    await absent(async () => (await rows()).length !== n, "a repeated or missing event after the renewal");
    await resolve(s);
    await reach(n + 1);
    await expect($('[data-testid="outcome"]')).toHaveText(expect.stringContaining("Cancelled: resolution"), { wait: 30_000 });
    await storageEmpty();
  });
});
