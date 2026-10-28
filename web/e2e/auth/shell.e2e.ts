// COMP-01, COMP-02, REC-01 (conformance/cases/) and the shell's header, navigation and states: the
// fixture page against the server that trusts the fixture issuer, or a recovery start of its
// deployment, through the injector.
import { $, $$, browser, expect } from "@wdio/globals";
import { NOT_ACCEPTED, REVOKED } from "../../src/app.tsx";
import { PRE_ENTRY, RECOVERY_START } from "../../src/recovery.tsx";
import { deprecated, incompatible } from "../../src/shell.tsx";
import { EPOCH_CHANGED, TRANSPORT_FAILURE } from "../../src/request.ts";
import { CONTROL, FIXTURE_API, FIXTURE_PAGE } from "../auth.conf.ts";
import type { Faults } from "../injector.ts";
import { answered, faults, fixtureSignIn, mint, release, seen, settled, storageEmpty, where } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
const testid = (id: string) => $(`[data-testid="${id}"]`);
const nav = (title: string) => $('nav[aria-label="Main"]').$(`a=${title}`);
// The browser's own connectivity events, as the shell listens for them.
const event = (name: "offline" | "online") => browser.execute((n: string) => void dispatchEvent(new Event(n)), name);
const reads = (n: number) => browser.waitUntil(async () => (await seen()).length === n, { timeout: 10_000, timeoutMsg: `the page did not make ${n} reads` });
// The machine list and its cluster filter are read at once, so they arrive in either order.
const ordered = async () => (await seen()).join(" ").replaceAll("/clusters /machines", "/machines /clusters").split(" ");

// Loads the page without waiting for it to settle, so a state on the way can be read.
async function load(f: Faults) {
  expect((await fetch(`${CONTROL}/next?human=h-viewer`)).ok).toBe(true);
  await faults(f);
  await browser.url(`${FIXTURE_PAGE}/`);
}

// Shown as a status message, which assistive technology announces without moving focus. Looked up
// again on each try: one state's message replaces the one before it.
async function announced(id: string, text: string | RegExp) {
  const shows = async () => {
    const el = await testid(id);
    if (!(await el.isExisting())) return false;
    const t = await el.getText().catch(() => "");
    return typeof text === "string" ? t === text : text.test(t);
  };
  await browser.waitUntil(shows, { timeout: 20_000, timeoutMsg: `${id} did not show ${String(text)}` });
  expect(await testid(id).getAttribute("role")).toBe("status");
}

describe("the shell", () => {
  it("shows the identity, roles and epoch GET /identity serves, and navigates by path", async () => {
    expect(await fixtureSignIn("h-viewer")).toMatch(SERVER);
    const res = await fetch(`${FIXTURE_API}/api/v1/identity`, { headers: { Authorization: `Bearer ${mint("h-viewer")}` } });
    const served = (await res.json()) as { roles: string[]; epoch: string };
    expect(await testid("principal").getText()).toBe("Signed in as h-viewer");
    expect(await testid("roles").getText()).toBe(`Roles: ${served.roles.join(", ")}`);
    expect(await testid("epoch").getText()).toBe(`Epoch ${served.epoch}`);
    expect(await $("footer").getText()).toMatch(/^Client .+\. Server .+, API revision \d+$/);
    expect(await nav("Machines").getAttribute("aria-current")).toBe("page");
    await nav("Plans").click();
    await expect($("main h2")).toHaveText("Plans");
    expect(await where()).toBe(`${FIXTURE_PAGE}/plans`);
    expect(await nav("Plans").getAttribute("aria-current")).toBe("page");
    await browser.execute(() => history.back());
    await expect($("main h2")).toHaveText("Machines");
    expect(await where()).toBe(`${FIXTURE_PAGE}/`);
    // The server's version first (§7 item 3), the header's identity on each page, the machine list
    // and its cluster filter once the identity is shown.
    expect(await ordered()).toEqual(["/server", "/identity", "/machines", "/clusters", "/identity", "/machines", "/clusters", "/identity"]);
  });

  it("a query-only move reads the identity again under the new view", async () => {
    await fixtureSignIn("h-viewer");
    await browser.execute(() => {
      history.pushState(null, "", "/?cluster=c1");
      dispatchEvent(new PopStateEvent("popstate"));
    });
    // Each move reads the identity, the machine list filtered by the query, and the cluster filter's
    // list: the client discards an answer read under the old view, so every page read starts again.
    await reads(7);
    await browser.execute(() => history.back());
    await reads(10);
    const s = await seen();
    expect(s.filter((p) => p === "/identity")).toHaveLength(3);
    expect(s.filter((p) => p === "/machines")).toHaveLength(3);
    expect(s.filter((p) => p === "/clusters")).toHaveLength(3);
  });

  it("COMP-01: a server below the client's minimum API revision is refused before any other read, both revisions named", async () => {
    expect(await fixtureSignIn("h-viewer", {}, { revision: 1 })).toBe(incompatible(1));
    expect(await testid("principal").isExisting()).toBe(false);
    expect(await $("nav").isExisting()).toBe(false);
    expect(await testid("server").getText()).toMatch(/^Server .+, API revision 1$/);
    expect(await seen()).toEqual(["/server"]);
  });

  it("COMP-02: a Deprecation header is shown once per page load, naming the route", async () => {
    await fixtureSignIn("h-viewer", {}, { deprecate: "/identity" });
    await announced("deprecation", deprecated("/api/v1/identity"));
    await nav("Audit").click();
    await expect($("main h2")).toHaveText("Audit");
    await nav("Sources").click();
    await expect($("main h2")).toHaveText("Sources");
    expect(await ordered()).toEqual(["/server", "/identity", "/machines", "/clusters", "/identity", "/identity"]);
    expect(await $$('[data-testid="deprecation"]').length).toBe(1);
  });

  it("COMP-02: a Deprecation header on the answer that refuses the sign-in is still shown", async () => {
    expect(await fixtureSignIn("h-viewer", { defect: "expired" }, { deprecate: "/server" })).toBe(NOT_ACCEPTED);
    await announced("deprecation", deprecated("/api/v1/server"));
    expect(await $$('[data-testid="deprecation"]').length).toBe(1);
    // §7 item 5: the view the refused GET /server ended the shell with still names both versions.
    expect(await $("footer").getText()).toMatch(/^Client .+\. Server unknown$/);
  });
});

describe("the state catalogue", () => {
  it("loading", async () => {
    await load({ hold: { path: "/identity", ms: 4000 } });
    await announced("status", "Loading your identity");
    expect(await settled(FIXTURE_PAGE)).toMatch(SERVER);
  });

  it("disconnected: no answer the page can read, the server's version unknown", async () => {
    expect(await fixtureSignIn("h-viewer", {}, { redirect: "/server" })).toBe(TRANSPORT_FAILURE);
    expect(await testid("status").getAttribute("role")).toBe("status");
    expect(await testid("server").getText()).toBe("Server unknown");
  });

  it("disconnected: a first identity read with no answer is shown as such, not as loading or offline", async () => {
    expect(await fixtureSignIn("h-viewer", {}, { redirect: "/identity" })).toBe(TRANSPORT_FAILURE);
    expect(await testid("offline").isExisting()).toBe(false);
    expect(await testid("server").getText()).toMatch(SERVER);
  });

  it("error: the problem's code, detail and instance", async () => {
    expect(await fixtureSignIn("h-viewer", {}, { missing: "/identity" })).toBe("Not read: not-found. Not found: injected Request req-injected.");
    expect(await testid("status").getAttribute("role")).toBe("status");
    expect(await testid("server").getText()).toMatch(SERVER);
  });

  it("offline: the last data stays under the banner until a read answers again", async () => {
    await fixtureSignIn("h-viewer");
    await faults({ redirect: "/identity" });
    await nav("Operations").click();
    await announced("offline", /^Offline since \d\d:\d\d\. Showing data read at \d\d:\d\d\. Actions are disabled\.$/);
    expect(await testid("principal").getText()).toBe("Signed in as h-viewer");
    await faults({});
    await nav("Changes").click();
    await expect($("main h2")).toHaveText("Changes");
    await expect(testid("offline")).not.toBeExisting({ wait: 10_000 });
    expect(await testid("status").isExisting()).toBe(false);
  });

  it("offline: a refusal the server answers ends the banner too, and is shown", async () => {
    await fixtureSignIn("h-viewer");
    await faults({ redirect: "/identity" });
    await nav("Operations").click();
    await announced("offline", /^Offline since /);
    await faults({ missing: "/identity" });
    await nav("Changes").click();
    await announced("status", "Not read: not-found. Not found: injected Request req-injected.");
    expect(await testid("offline").isExisting()).toBe(false);
  });

  it("offline: after a refusal replaced the data, a read without answer shows as disconnected", async () => {
    await fixtureSignIn("h-viewer");
    await faults({ missing: "/identity" });
    await nav("Changes").click();
    await announced("status", "Not read: not-found. Not found: injected Request req-injected.");
    await faults({ redirect: "/identity" });
    await event("online");
    await announced("status", TRANSPORT_FAILURE);
    expect(await testid("offline").isExisting()).toBe(false);
  });

  it("offline: the browser's offline event holds the banner over answered reads until its online event, which reads again", async () => {
    await fixtureSignIn("h-viewer");
    await event("offline");
    await announced("offline", /^Offline since /);
    await faults({ missing: "/identity" });
    await nav("Changes").click();
    await announced("status", "Not read: not-found. Not found: injected Request req-injected.");
    expect(await testid("offline").isExisting()).toBe(true);
    await faults({});
    await event("online");
    await expect(testid("principal")).toHaveText("Signed in as h-viewer");
    expect(await testid("offline").isExisting()).toBe(false);
    expect(await ordered()).toEqual(["/server", "/identity", "/machines", "/clusters", "/identity", "/identity"]);
  });

  it("offline: a read an online event superseded is ignored when it completes, and the newer answer shown", async () => {
    await fixtureSignIn("h-viewer");
    await faults({ missing: "/identity" });
    await event("online");
    await announced("status", "Not read: not-found. Not found: injected Request req-injected.");
    // The first online event read the machine list and its cluster filter again with the identity;
    // the refusal then replaced the list, so this one reads the identity alone.
    await faults({ held: "/identity", missing: "/identity" });
    await event("online");
    await reads(8);
    await faults({});
    await event("online");
    // The newer read's data replaces the refusal while the older read is still held.
    await expect(testid("principal")).toHaveText("Signed in as h-viewer");
    // The held read's refusal arrives after it and must not replace it.
    await release();
    await browser.waitUntil(() => answered("/identity", 4), { timeout: 10_000, timeoutMsg: "the held read's answer did not reach the page" });
    await browser.pause(500);
    expect(await testid("principal").getText()).toBe("Signed in as h-viewer");
    // The machine list's own state may be shown; the refusal is not.
    expect(await $("main").getText()).not.toContain("Not read");
  });

  it("offline: identity-revoked on a superseded read still ends the page with the revocation", async () => {
    await fixtureSignIn("h-viewer");
    // On a page that reads only the identity: any other read that answers would end the banner.
    await nav("Changes").click();
    await reads(5);
    await faults({ held: "/identity", revoked: "/identity" });
    await event("online");
    await reads(6);
    await faults({ redirect: "/identity" });
    await event("online");
    await reads(7);
    // The newer read lost its answer; the old read's revocation, released after it, still ends the page.
    await announced("offline", /^Offline since /);
    await release();
    await expect(testid("status")).toHaveText(REVOKED, { wait: 10_000 });
    expect(await testid("principal").isExisting()).toBe(false);
    expect(await $("footer").getText()).toMatch(/^Client .+\. Server unknown$/);
  });
});

const literal = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const INJECTED_REFUSAL = "Not read: Recovery mode active (injected). Request req-injected.";

describe("recovery", () => {
  it("REC-01: a recovery start before its entry names the process's restriction; after it, the banner, the scopes and the epoch change", async () => {
    const started = await fetch(`${CONTROL}/disposable?start=recovery`, { method: "POST" });
    expect(started.status).toBe(200);
    const at = await started.text();
    // Before the entry: GET /server refused as every request but the entry, so nothing else is read.
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toBe(PRE_ENTRY);
    expect(await testid("status").getAttribute("role")).toBe("status");
    // §8.2: the refusal's own title, detail and request ID stay shown under the restriction.
    expect(await testid("problem").getText()).toMatch(/^Not read: .+\. Request \S+\.$/);
    expect(await testid("principal").isExisting()).toBe(false);
    expect(await testid("server").getText()).toBe("Server unknown");
    expect(await seen()).toEqual(["/server"]);
    // The entry, as a recovery administrator would make it (synthetic values only).
    const now = new Date(Date.now() - 60_000).toISOString();
    const entry = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-01-${Date.now()}-entry` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-rec-01", takenAt: now }], stoppedAt: now, reason: "REC-01 synthetic restore" }),
    });
    expect(entry.status).toBe(201);
    const { epoch } = (await entry.json()) as { epoch: string };
    // The banner's first read of GET /recovery finds no answer: the banner says what it knows, and
    // the read's own state is shown under it.
    await faults({ upstream: at, redirect: "/recovery" });
    await $("button=Read again").click();
    await expect($("main h2")).toHaveText("Machines");
    await announced("epoch-changed", `${EPOCH_CHANGED}.`);
    await announced("recovery", `Recovery mode. Drafts can be edited. ${RECOVERY_START}`);
    await announced("recovery-read", TRANSPORT_FAILURE);
    // The next page reads it again.
    await faults({ upstream: at });
    await nav("Changes").click();
    await expect(testid("recovery-read")).not.toBeExisting({ wait: 10_000 });
    await announced("recovery", new RegExp(`^Recovery mode since \\d\\d:\\d\\d, epoch ${literal(epoch)}\\. No machine scopes\\. Drafts can be edited\\. ${literal(RECOVERY_START)}$`));
    expect(await testid("epoch").getText()).toBe(`Epoch ${epoch}`);
    expect(await testid("server").getText()).toMatch(SERVER);
    // The process restarts for recovery again, under the same epoch: whichever of the page's next
    // reads meets its refusal first, nothing read before stays shown, the other read's late answer
    // included, and the page reads again on request.
    const since = new RegExp(`^Recovery mode since \\d\\d:\\d\\d, epoch ${literal(epoch)}\\.`);
    for (const [refused, late, page] of [["/recovery", "/identity", "Machines"], ["/identity", "/recovery", "Changes"]] as const) {
      await faults({ upstream: at, processRefused: [refused, "/server"], held: late });
      await nav(page).click();
      await announced("status", PRE_ENTRY);
      expect(await testid("problem").getText()).toBe(INJECTED_REFUSAL);
      await announced("recovery", `Recovery mode. Drafts can be edited. ${RECOVERY_START}`);
      expect(await testid("principal").isExisting()).toBe(false);
      await release();
      await browser.pause(500);
      expect(await testid("principal").isExisting()).toBe(false);
      expect(await testid("recovery").getText()).toBe(`Recovery mode. Drafts can be edited. ${RECOVERY_START}`);
      await faults({ upstream: at });
      await $("button=Read again").click();
      await expect(testid("principal")).toHaveText("Signed in as h-viewer");
      await announced("recovery", since);
    }
  });

  it("REC-01: an open page whose next read meets the process's refusal, under the same epoch, shows the state before the entry and reads again on request", async () => {
    await fixtureSignIn("h-viewer");
    // A restart for recovery keeps the epoch until the entry: the injected refusal carries none.
    await faults({ processRefused: ["/identity", "/server"] });
    await nav("Changes").click();
    await announced("status", PRE_ENTRY);
    expect(await testid("problem").getText()).toBe(INJECTED_REFUSAL);
    expect(await testid("principal").isExisting()).toBe(false);
    expect(await testid("server").getText()).toBe("Server unknown");
    expect((await seen()).slice(-2)).toEqual(["/identity", "/server"]);
    await faults({});
    await $("button=Read again").click();
    await expect(testid("principal")).toHaveText("Signed in as h-viewer");
    expect(await testid("server").getText()).toMatch(SERVER);
  });
});

afterEach(storageEmpty);
