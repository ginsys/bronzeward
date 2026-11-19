// What the AUTH specs share. Only the parts of a URL a case needs cross WebDriver, so the harness
// log never records a token (Zitadel's logout page carries one in its query).
import { execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { $, browser, expect } from "@wdio/globals";
import { CONTROL, FIXTURE_ISSUER, FIXTURE_PAGE, IDP_PAGE, idpEnv } from "../auth.conf.ts";
import type { Faults } from "../injector.ts";
import type { Counts } from "../provider.ts";

const root = fileURLToPath(new URL("../../..", import.meta.url));

export const where = () => browser.execute(() => location.origin + location.pathname);
export const param = (name: string) => browser.execute((n: string) => new URLSearchParams(location.search).get(n), name);
export const status = () => $('[data-testid="status"]').getText();
// The text of every message the page's one role="status" region holds: each status message is
// mirrored into it and carries no role of its own (ginsys/bronzeward#830).
export const mirrored = () => browser.execute(() => [...document.querySelectorAll('[role="status"] > p')].map((p) => p.textContent ?? ""));

// Waits until the page is back on `page`/ with its reads settled: the footer's server versions once
// the shell shows a page, which may hold a state of its own (an empty list), else the state text.
// Checked and read in one step in the page: an element a re-render replaced between two WebDriver
// calls would be stale for the second.
export async function settled(page: string): Promise<string> {
  let text: string | null = null;
  await browser.waitUntil(async () => {
    text = await browser.execute((want: string) => {
      if (location.origin + location.pathname !== want) return null;
      const s = document.querySelector<HTMLElement>('[data-testid="status"]');
      if ((s?.textContent ?? "").startsWith("Loading")) return null;
      if (document.querySelector("main h2") !== null) return document.querySelector<HTMLElement>('[data-testid="server"]')?.innerText.trim() ?? null;
      return s !== null ? s.innerText.trim() : null;
    }, `${page}/`);
    return text !== null;
  }, { timeout: 20_000, timeoutMsg: `the page did not settle on ${page}/` });
  return text as unknown as string;
}

// A token the fixture issuer mints, under the synthetic provider's issuer.
export function mint(human: string, flags: Record<string, string> = {}): string {
  const args = ["mint", "-key", `${root}.dev/browser-auth/fixture.key`, "-issuer", FIXTURE_ISSUER, "-human", human];
  for (const [k, v] of Object.entries(flags)) args.push(`-${k}`, v);
  return execFileSync(`${root}bin/oidc-fixture`, args).toString().trim();
}

// A case's start leaves the page an earlier case left, maybe with a write still open, without its
// leave dialog. Firefox's navigation stops at that dialog, even with unhandledPromptBehavior
// accepting it (wdio.conf.ts), so the next case would never start. A capturing listener on window
// runs before the page's own (forms.tsx useUnloadGuard) and stops it. The guard keeps its own cases.
async function leaveUnasked(): Promise<void> {
  await browser.execute(() => addEventListener("beforeunload", (e) => e.stopImmediatePropagation(), { capture: true })).catch(() => {});
}

// Signs the fixture page in: the synthetic provider hands out the token the issuer mints with flags,
// and the injector applies the faults.
export async function fixtureSignIn(human: string, flags: Record<string, string> = {}, f: Faults = {}): Promise<string> {
  const q = new URLSearchParams({ human, ...flags });
  expect((await fetch(`${CONTROL}/next?${q}`)).ok).toBe(true);
  await faults(f);
  await leaveUnasked();
  await browser.url(`${FIXTURE_PAGE}/`);
  return settled(FIXTURE_PAGE);
}

export const hex = () => randomBytes(4).toString("hex");

// Starts the disposable deployment as the control server's query says ("start=recovery&keep": for
// recovery, on the earlier one's database) and returns its URL.
export async function disposable(query: string): Promise<string> {
  const res = await fetch(`${CONTROL}/disposable?${query}`, { method: "POST" });
  expect(res.status).toBe(200);
  return res.text();
}

// The identifiers of the records a seeded deployment holds (internal/api/e2eseed_test.go): one
// release and two machines of one cluster, and one plan in each state a plan page shows. `machine`
// is the committed plan's, which has an operation with its first attempt; the other plans are on
// `machine2`.
export type Seed = {
  cluster: string; release: string; machine: string; machine2: string; operation: string;
  plans: { proposed: string; approved: string; committed: string; expired: string; cancelled: string; revoked: string };
};

// Starts a disposable deployment with the seeded records and returns its URL and their identifiers.
export async function seeded(): Promise<{ at: string; seed: Seed }> {
  const at = await disposable("start=normal&seed");
  const res = await fetch(`${CONTROL}/seeded`);
  expect(res.status).toBe(200);
  return { at, seed: (await res.json()) as Seed };
}

// One API call to the disposable deployment at `at`, as `human`, under the epoch it reports now.
export async function api(at: string, human: string, method: string, path: string, body?: unknown, ifMatch?: string) {
  const auth = { Authorization: `Bearer ${mint(human)}` };
  const server = await fetch(`${at}/api/v1/server`, { headers: auth });
  const epoch = server.headers.get("Bronzeward-Epoch") ?? "";
  const r = await fetch(`${at}/api/v1${path}`, {
    method,
    headers: { ...auth, "Content-Type": "application/json", "Idempotency-Key": randomBytes(16).toString("base64url"), "Bronzeward-Expected-Epoch": epoch, ...(ifMatch !== undefined ? { "If-Match": ifMatch } : {}) },
    body: body === undefined ? null : JSON.stringify(body),
  });
  return { status: r.status, etag: r.headers.get("ETag") ?? "", body: (await r.json().catch(() => undefined)) as Record<string, unknown> | undefined };
}

// A synthetic cluster and one machine on it, made as h-author; returns the machine's id.
export async function syntheticMachine(at: string): Promise<string> {
  const name = `synthetic-${hex()}`;
  const c = await api(at, "h-author", "POST", "/clusters", { name, endpoint: `https://${name}.example.test:6443`, contract: "v1.14", talosClusterId: `${randomBytes(32).toString("base64url")}=` });
  expect(c.status).toBe(201);
  const m = await api(at, "h-author", "POST", "/machines", { cluster: c.body!["id"], talosNodeId: `synthetic-node-${hex()}`, platform: "metal", talosEndpoint: `192.0.2.${1 + (randomBytes(1)[0]! % 254)}` });
  expect(m.status).toBe(201);
  return m.body!["id"] as string;
}

// Every write the injector received since the sign-in, as "<method> <route>"; the key and the epoch are
// left to the cases that compare them.
export const writes = async (): Promise<string[]> => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).map((w) => w.split(" ").slice(0, 2).join(" "));
export const counts = async (): Promise<Counts> => (await fetch(`${CONTROL}/counts`)).json() as never;
export const seen = async (): Promise<string[]> => (await fetch(`${CONTROL}/seen`)).json() as never;
// The server's answers the injector passed on, as "<route><query> <status>".
export const answers = async (): Promise<string[]> => (await fetch(`${CONTROL}/answered`)).json() as never;
export const faults = async (f: Faults) => expect((await fetch(`${CONTROL}/faults`, { method: "POST", body: JSON.stringify(f) })).ok).toBe(true);
// The paths whose answers the `held` fault holds now.
export const holding = async (): Promise<string[]> => (await fetch(`${CONTROL}/held`)).json() as never;
// Lets the answers the `held` fault holds go; returns once the injector has sent each.
export const release = async () => expect((await fetch(`${CONTROL}/release`, { method: "POST" })).ok).toBe(true);
// The point the disposable deployment's interrupt build stops at ("<step> <action>", ginsys/bronzeward#366);
// no point removes it, which ends a stall. Only after a sign-in with the faults' upstream on that deployment.
export const interrupt = async (point = "") => expect((await fetch(`${CONTROL}/interrupt?point=${encodeURIComponent(point)}`, { method: "POST" })).ok).toBe(true);
// Whether the page has received every answer to `path` it asked for, by the browser's resource
// timing: an entry appears once its response has ended.
export const answered = (path: string, n: number) =>
  browser.execute((p: string) => performance.getEntriesByType("resource").filter((e) => new URL(e.name).pathname === p).length, `/api/v1${path}`)
    .then((count) => count >= n);

// Signs the test provider's page in as human: the page sends the browser to the provider, which has
// no login UI here; the case completes the authorization request through the Session API, as the
// login UI would, and hands the callback back to the browser.
export async function idpSignIn(human: string): Promise<string> {
  const env = idpEnv();
  const issuer = env["BW_TEST_IDP_ISSUER"]!;
  const users = Object.fromEntries(env["BW_TEST_IDP_USERS"]!.split(",").map((p) => p.split("=")));
  const login = { Authorization: `Bearer ${readFileSync(env["BW_TEST_IDP_LOGIN_TOKEN_FILE"]!, "utf8").trim()}`, "Content-Type": "application/json" };
  await browser.url(`${IDP_PAGE}/`);
  await browser.waitUntil(async () => (await where()) === `${issuer}/ui/v2/login/login`, { timeout: 20_000, timeoutMsg: "the page did not go to the provider" });
  const request = await param("authRequest");
  const s = await fetch(`${issuer}/v2/sessions`, {
    method: "POST", headers: login,
    body: JSON.stringify({ checks: { user: { userId: users[human] }, password: { password: env["BW_TEST_IDP_PASSWORD"] } } }),
  });
  const { sessionId, sessionToken } = (await s.json()) as { sessionId: string; sessionToken: string };
  const cb = await fetch(`${issuer}/v2/oidc/auth_requests/${request}`, { method: "POST", headers: login, body: JSON.stringify({ session: { sessionId, sessionToken } }) });
  expect(cb.status).toBe(200);
  // The code is JWT-shaped: it reaches the browser through the control redirect, never WebDriver.
  await fetch(`${CONTROL}/go`, { method: "POST", body: ((await cb.json()) as { callbackUrl: string }).callbackUrl });
  await browser.url(`${CONTROL}/go`);
  return settled(IDP_PAGE);
}

export const userId = (human: string) => Object.fromEntries(idpEnv()["BW_TEST_IDP_USERS"]!.split(",").map((p) => p.split("=")))[human] as string;

// What the page's storage holds, read the way client contracts §11.5 item 3 lists it. The page answers
// counts and database and cache names only, so a failing read never exports a stored value (a stored
// document carries the run's canary, which must not reach the WebDriver log).
const storageState = () =>
  browser.execute(async () => ({
    local: localStorage.length,
    session: sessionStorage.length,
    indexedDB: "databases" in indexedDB ? (await indexedDB.databases()).map((d) => d.name ?? "") : ["unreadable"],
    caches: "caches" in self ? await caches.keys() : ["unreadable"],
  }));

// Client contracts §11.5 item 3: no browser storage holds a value outside a redirect.
export async function storageEmpty() {
  expect(await storageState()).toEqual({ local: 0, session: 0, indexedDB: [], caches: [] });
}

// The positive control of storageEmpty (ginsys/bronzeward#341, client contracts §11.5): plants a
// harmless marker in each of the four stores and expects the same read to see every one, then removes
// them. The marker is not the run's canary, which never crosses WebDriver.
export async function storageControl() {
  try {
    await browser.execute(async () => {
      localStorage.setItem("bw-control", "marker");
      sessionStorage.setItem("bw-control", "marker");
      await new Promise<void>((ok, bad) => {
        const r = indexedDB.open("bw-control");
        r.onsuccess = () => (r.result.close(), ok());
        r.onerror = () => bad(r.error);
      });
      await caches.open("bw-control");
    });
    const s = await storageState();
    expect(s.local).toBe(1);
    expect(s.session).toBe(1);
    expect(s.indexedDB).toContain("bw-control");
    expect(s.caches).toContain("bw-control");
  } finally {
    await browser.execute(async () => {
      localStorage.removeItem("bw-control");
      sessionStorage.removeItem("bw-control");
      await new Promise<void>((ok) => {
        const r = indexedDB.deleteDatabase("bw-control");
        r.onsuccess = r.onerror = r.onblocked = () => ok();
      });
      await caches.delete("bw-control");
    });
  }
  await storageEmpty();
}

// Whether the browser's HTTP cache answers a same-origin GET of `path` with no request: a hit is an
// ok answer, a miss is a refused or failed one (a network error in Chrome and Edge, 504 in Firefox).
export const cacheHolds = (path: string) =>
  browser.execute(async (p: string) => {
    try {
      return (await fetch(p, { cache: "only-if-cached", mode: "same-origin" })).ok;
    } catch {
      return false;
    }
  }, path);

// The positive control of cacheHolds (ginsys/bronzeward#341): the injector answers one path with a
// cacheable response (every real answer says no-store), the page reads it once, and cacheHolds must
// then see it. Without this the check would pass in a browser whose probe never finds anything.
export async function cacheControl(upstream: string) {
  const path = "/api/v1/__cache-probe";
  await faults({ upstream, cacheable: "/__cache-probe" });
  try {
    expect(await browser.execute(async (p: string) => (await fetch(p)).ok, path)).toBe(true);
    expect(await cacheHolds(path)).toBe(true);
  } finally {
    await faults({ upstream });
  }
}

// The keyboard and measured checks of journeys §13 (WCAG 2.2 AA), shared by the specs that run them.
// What a focus reports never carries a field's text: a controlled textarea keeps its text in the DOM
// (drafts.e2e.ts), and a document or a review has the run's canary, which must not reach the
// WebDriver log the artifact scan reads. So `text` is the text of an element that is no field and holds
// none, cut at 120 characters, and "" otherwise: a focus lost to the body must not read the page.
export type Focus = { tag: string; text: string; title: string | null; name: string | null; testid: string | null; outline: string; width: number; same: boolean };

// The focused element after one Tab, or Shift+Tab, which must show its focus (WCAG 2.4.7). `same`:
// it is the element that held focus after the last call, which two Tabs in a row make a trap (2.1.2).
export async function tab(back = false): Promise<Focus> {
  await browser.keys(back ? ["Shift", "Tab"] : "Tab");
  const f = await browser.execute(() => {
    const e = document.activeElement as HTMLElement;
    const s = getComputedStyle(e);
    const held = globalThis as unknown as { bwLastFocus?: Element };
    const same = held.bwLastFocus === e;
    held.bwLastFocus = e;
    // An element that is a field or holds one (the body, when focus is lost) gives no text.
    const field = e.tagName === "TEXTAREA" || e.tagName === "INPUT" || e.querySelector("textarea, input") !== null;
    return { tag: e.tagName, text: field ? "" : (e.textContent?.trim() ?? "").slice(0, 120), title: e.getAttribute("title"), name: e.getAttribute("name"), testid: e.getAttribute("data-testid"), outline: s.outlineStyle, width: parseFloat(s.outlineWidth), same };
  });
  if (f.outline === "none" || f.width < 2) throw new Error(`focus on ${f.tag} "${f.text}" is not visible: outline ${f.outline} ${f.width}px`);
  return f;
}
// Tabs until the match, answering the text of every focus on the way. Two Tabs that stay on one
// element are a keyboard trap.
export async function tabTo(match: (f: Focus) => boolean, what: string): Promise<string[]> {
  const passed: string[] = [];
  for (let i = 0; i < 60; i++) {
    const f = await tab().catch((e: unknown) => {
      throw new Error(`Tab on the way to ${what}, after ${passed.length} stops (${passed.slice(-12).join(" | ")}):${e instanceof Error ? e.message : String(e)}`);
    });
    if (match(f)) return passed;
    if (f.same) throw new Error(`Tab did not leave ${f.tag} "${f.text}" on the way to ${what}`);
    passed.push(f.text);
  }
  throw new Error(`Tab never reached ${what}`);
}
export const focused = () => browser.execute(() => document.activeElement?.id ?? "");
// Whether focus is on an element of the page, not lost to the body after a change (2.4.3).
export const focusKept = () => browser.execute(() => document.activeElement !== null && document.activeElement !== document.body && document.activeElement !== document.documentElement);
// Whether focus is on an element of `selector` or inside it: the new content took it after an action (2.4.3).
// Only a boolean leaves the page, so a focused field's text never reaches the WebDriver log.
export const focusIn = (selector: string) => browser.execute((s: string) => document.activeElement !== document.body && document.activeElement?.closest(s) != null, selector);
// A click on the header's title, which takes no focus, starts the next Tab from there.
export const fromTitle = () => $("header h1").click();

// The lowest contrast of any shown text (WCAG 1.4.3) and the controls under 24 px (2.5.8), measured
// in the page. Only numbers leave it.
export const measure = () => browser.execute(() => {
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

// How far the page scrolls sideways (WCAG 1.4.10): 0 when nothing reflows out of the viewport.
export const overflows = () => browser.execute(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
// Whether the element `sel` scrolls inside itself, so a wide row is really there.
export const scrollsOnItsOwn = (sel: string) => browser.execute((s: string) => {
  const r = document.querySelector(s);
  return r !== null && r.scrollWidth > r.clientWidth;
}, sel);
// Runs `run` at 320 CSS pixels, and restores the viewport it found.
export async function narrow<T>(run: () => Promise<T>): Promise<T> {
  const before = await browser.execute(() => ({ width: innerWidth, height: innerHeight }));
  try {
    await browser.setViewport({ width: 320, height: 640 });
    expect(await browser.execute(() => innerWidth)).toBe(320);
    return await run();
  } finally {
    await browser.setViewport(before);
  }
}
