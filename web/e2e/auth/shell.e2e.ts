// COMP-01, COMP-02 (conformance/cases/) and the shell's header, navigation and states: the fixture
// page against the server that trusts the fixture issuer, through the injector.
import { $, $$, browser, expect } from "@wdio/globals";
import { NOT_ACCEPTED, REVOKED } from "../../src/app.tsx";
import { deprecated, incompatible } from "../../src/shell.tsx";
import { TRANSPORT_FAILURE } from "../../src/request.ts";
import { CONTROL, FIXTURE_API, FIXTURE_PAGE } from "../auth.conf.ts";
import type { Faults } from "../injector.ts";
import { faults, fixtureSignIn, mint, seen, settled, storageEmpty, where } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
const testid = (id: string) => $(`[data-testid="${id}"]`);
const nav = (title: string) => $('nav[aria-label="Main"]').$(`a=${title}`);
// The browser's own connectivity events, as the shell listens for them.
const event = (name: "offline" | "online") => browser.execute((n: string) => void dispatchEvent(new Event(n)), name);
const reads = (n: number) => browser.waitUntil(async () => (await seen()).length === n, { timeout: 10_000, timeoutMsg: `the page did not make ${n} reads` });

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
    // The server's version first (§7 item 3), the header's identity on each page.
    expect(await seen()).toEqual(["/server", "/identity", "/identity", "/identity"]);
  });

  it("a query-only move reads the identity again under the new view", async () => {
    await fixtureSignIn("h-viewer");
    await browser.execute(() => {
      history.pushState(null, "", "/?cluster=c1");
      dispatchEvent(new PopStateEvent("popstate"));
    });
    await reads(3);
    await browser.execute(() => history.back());
    await reads(4);
    expect(await seen()).toEqual(["/server", "/identity", "/identity", "/identity"]);
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
    expect(await seen()).toEqual(["/server", "/identity", "/identity", "/identity"]);
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
    expect(await seen()).toEqual(["/server", "/identity", "/identity", "/identity"]);
  });

  it("offline: a read an online event superseded is ignored when it completes, and the newer answer shown", async () => {
    await fixtureSignIn("h-viewer");
    await faults({ missing: "/identity" });
    await event("online");
    await announced("status", "Not read: not-found. Not found: injected Request req-injected.");
    await faults({ hold: { path: "/identity", ms: 4000 }, missing: "/identity" });
    await event("online");
    await reads(4);
    await faults({});
    await event("online");
    // The newer read's data replaces the refusal before the held read answers.
    await expect(testid("principal")).toHaveText("Signed in as h-viewer", { wait: 3000 });
    // The held read's refusal arrives after it and must not replace it.
    await browser.pause(4000);
    expect(await testid("principal").getText()).toBe("Signed in as h-viewer");
    expect(await testid("status").isExisting()).toBe(false);
  });

  it("offline: identity-revoked on a superseded read still ends the page with the revocation", async () => {
    await fixtureSignIn("h-viewer");
    await faults({ hold: { path: "/identity", ms: 3000 }, revoked: "/identity" });
    await event("online");
    await reads(3);
    await faults({ redirect: "/identity" });
    await event("online");
    await reads(4);
    // The newer read lost its answer; the old read's revocation still ends the page.
    await expect(testid("status")).toHaveText(REVOKED, { wait: 10_000 });
    expect(await testid("principal").isExisting()).toBe(false);
    expect(await $("footer").getText()).toMatch(/^Client .+\. Server unknown$/);
  });
});

afterEach(storageEmpty);
