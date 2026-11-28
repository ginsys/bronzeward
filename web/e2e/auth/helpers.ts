// What the AUTH specs share. Only the parts of a URL a case needs cross WebDriver, so the harness
// log never records a token (Zitadel's logout page carries one in its query).
import { execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { CONTROL, FIXTURE_ISSUER, FIXTURE_PAGE, IDP_PAGE, idpEnv } from "../auth.conf.ts";
import { binPath } from "../children.ts";
import type { Faults } from "../injector.ts";
import type { Counts } from "../provider.ts";

const root = fileURLToPath(new URL("../../..", import.meta.url));

// The heading of a plan page in `state` (ginsys/bronzeward#987): the derived title with the whole identifier
// as its title attribute, or, where a read the title needs failed, the shortened identifier of the old heading.
export async function planHeading(id: string, state: string, wait = 20_000) {
  const h2 = $("main h2");
  await expect(h2).toHaveText(expect.stringMatching(new RegExp(`^Plan(: .+ \\(${state}\\)| ${short(id)}: ${state})$`)), { wait });
  // The fallback names the identifier in its text; the derived title carries it as the span's title attribute.
  if ((await h2.getText()).startsWith("Plan:")) await expect($(`main h2 [title="${id}"]`)).toBeExisting();
}

// The heading of a release page: the derived title with the whole identifier as its title attribute.
export async function releaseHeading(id: string, wait = 20_000) {
  await expect($("main h2")).toHaveText(expect.stringMatching(/^Release: .+, published \d{1,2} [A-Za-z]+\.? \d{4}, \d{2}:\d{2}$/), { wait });
  await expect($(`main h2 [title="${id}"]`)).toBeExisting();
}

export const where = () => browser.execute(() => location.origin + location.pathname);
export const param = (name: string) => browser.execute((n: string) => new URLSearchParams(location.search).get(n), name);
export const status = () => $('[data-testid="status"]').getText();
// The text of every reference block on the page (the code, rule, scope and request number of a refusal). The
// block is a closed details element, so its text is read as text content, never as visible text.
export const references = () => browser.execute(() => [...document.querySelectorAll('[data-testid="request-ref"]')].map((d) => d.textContent ?? ""));
// The text of every message the page's one role="status" region holds: each status message is
// mirrored into it and carries no role of its own (ginsys/bronzeward#830).
export const mirrored = () => browser.execute(() => [...document.querySelectorAll('[role="status"] > p')].map((p) => p.textContent ?? ""));
// A read of the page taken right after a wait on another element can be early: the mirror is filled by an effect
// after the message shows, and an end text of an earlier run stays until the watch restarts (ginsys/bronzeward#916).
// This waits for `read` to hold, so a read that never holds fails with what it waited for.
export const eventually = (read: () => Promise<boolean>, what: string, timeout = 5_000) =>
  browser.waitUntil(read, { timeout, interval: 25, timeoutMsg: `${what} was not shown within ${timeout} ms` });
// The draft page's Entries is a table (the layout design, ginsys/bronzeward#1255): a row is read by the name in its first cell, and
// its Open button is named by its entry. `entryRow` is the row's cells joined by " | " ("ntp | fragment | this cluster | new |
// Open"), or "" while no row has that name; `entryNames` is the first cell of every row.
export const ENTRIES = 'main [data-testid="entries"]';
export const entryOpen = (kind: string, name: string) => `${ENTRIES} button[aria-label="Open ${kind} ${name}"]`;
export const entryRow = (name: string) =>
  browser.execute((table: string, n: string) => {
    const row = [...document.querySelectorAll(`${table} tbody tr`)].find((r) => r.firstElementChild?.textContent === n);
    return row === undefined ? "" : [...row.children].map((c) => c.textContent ?? "").join(" | ");
  }, ENTRIES, name);
export const entryNames = () =>
  browser.execute((table: string) => [...document.querySelectorAll(`${table} tbody th`)].map((t) => t.textContent ?? ""), ENTRIES);
// The other side of the same early read: a check that `read` is false passes when the page has not yet rendered the
// thing it must not show. Called once the page is on the state the check is about, it reads, pauses and reads again,
// so a thing that renders a moment later still fails it (ginsys/bronzeward#921).
export async function absent(read: () => Promise<boolean>, what: string) {
  expect([`${what} is absent`, await read()]).toEqual([`${what} is absent`, false]);
  await browser.pause(300);
  expect([`${what} is absent after a pause`, await read()]).toEqual([`${what} is absent after a pause`, false]);
}

// A new epoch is committed in the disposable deployment the page reads (the case's `upstream`), and the shell reads again as it does
// when the connection is back: its read meets the new epoch.
export async function newEpoch() {
  expect((await fetch(`${CONTROL}/epoch`, { method: "POST" })).status).toBe(200);
  await browser.execute(() => void dispatchEvent(new Event("online")));
}

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
  return execFileSync(binPath(root, "oidc-fixture"), args).toString().trim();
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
  // Only for ?seed=observations: the recovery observations of the accounting cases (internal/api/e2eseed_test.go).
  observations?: { machine: { first: string; later: string; end: string }; machine2: { start: string; soon: string; end: string } };
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

// The route of a plan's approval, for `held` and `approvalReadsAnswered`.
export async function approvalRoute(at: string, planId: string): Promise<string> {
  const plan = await api(at, "h-all", "GET", `/plans/${planId}`);
  expect(plan.status).toBe(200);
  return `/approvals/${plan.body!["approval"] as string}`;
}

// For a page signed in with `held: route` (route = approvalRoute) that opened an approved plan: returns once every read
// that decides the Approve form (EarlierEpoch, web/src/approval.tsx:158) has been answered, so the absence of that form
// is read after them and not after a pause. The page asks for the approval three times (web/src/plan.tsx:199,
// web/src/approval.tsx:160 and :204) and for /identity three times (web/src/shell.tsx:454, web/src/plan.tsx:200,
// web/src/approval.tsx:161). While the approval's answers are held the plan's poll, which restarts only after the
// reads at plan.tsx:199-201 settle, cannot add a read, so a count of three is exact. A fourth reader of either route
// would not fail this wait: it would only weaken it, so a change to those reads changes the numbers here. /identity is
// never held: the shell mounts no page until it answers (web/src/shell.tsx:121).
export async function approvalReadsAnswered(route: string, upstream: string) {
  await browser.waitUntil(async () => (await holding()).filter((p) => p === route).length >= 3, { timeout: 20_000, timeoutMsg: `fewer than three answers to ${route} were held` });
  await browser.waitUntil(() => answered("/identity", 3), { timeout: 20_000, timeoutMsg: "the page did not take three answers to /identity" });
  await faults({ upstream });
  await release();
  await browser.waitUntil(() => answered(route, 3), { timeout: 20_000, timeoutMsg: `the page did not take three answers to ${route}` });
}

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

// Client contracts §11.5 item 3: no browser storage holds a value outside a redirect. One read: a write that lands a
// moment after the case's last step is missed, so the protected specs use storageEmptyAfterPause instead (the
// guard, absence-guard.test.ts, refuses this name there).
export async function storageEmpty() {
  expect(await storageState()).toEqual({ local: 0, session: 0, indexedDB: [], caches: [] });
}

// storageEmpty read again after a pause, as `absent` does for a page read: for the specs that handle a protected
// document, where a late write would be a leak that one read passes before it exists.
export async function storageEmptyAfterPause() {
  await storageEmpty();
  await browser.pause(300);
  await storageEmpty();
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
  throw new Error(`Tab never reached ${what} (stops: ${passed.slice(0, 30).map((s) => s.slice(0, 30)).join(" | ")})`);
}
export const focused = () => browser.execute(() => document.activeElement?.id ?? "");
// Whether focus is on an element of the page, not lost to the body after a change (2.4.3).
export const focusKept = () => browser.execute(() => document.activeElement !== null && document.activeElement !== document.body && document.activeElement !== document.documentElement);
// Whether focus is on an element of `selector` or inside it: the new content took it after an action (2.4.3).
// Only a boolean leaves the page, so a focused field's text never reaches the WebDriver log.
export const focusIn = (selector: string) => browser.execute((s: string) => document.activeElement !== document.body && document.activeElement?.closest(s) != null, selector);
// Whether the page's `main` holds focus: a navigation moved it to the new page.
export const onMain = async () => (await focused()) === "main";
// The shell moves focus in an effect, a few milliseconds after the content it follows is on screen, so one
// read right after that content can be early (ginsys/bronzeward#911). This waits up to 2 s for `read` to hold,
// then reads again after 300 ms, so a focus lost again later still fails.
export async function focusSettles(read: () => Promise<boolean>, what: string) {
  await browser.waitUntil(read, { timeout: 2_000, interval: 25, timeoutMsg: `focus did not reach ${what} within 2 s` });
  await browser.pause(300);
  expect([`focus stays on ${what}`, await read()]).toEqual([`focus stays on ${what}`, true]);
}
// A click on the header's title, which takes no focus, starts the next Tab from there.
export const fromTitle = () => $("header h1").click();

// The lowest contrast of any shown text (WCAG 1.4.3) and the controls under 24 px (2.5.8), measured
// in the page. Only numbers leave it.
export const measure = () => browser.execute(() => {
  // Only an opaque rgb() reads as three bytes: color(), oklch(), or a translucent colour would read as
  // garbage or as near black, so it throws instead.
  const lum = (c: string) => {
    const m = /^rgba?\((\d+(?:\.\d+)?), (\d+(?:\.\d+)?), (\d+(?:\.\d+)?)(?:, (\d+(?:\.\d+)?))?\)$/.exec(c);
    if (m === null || (m[4] !== undefined && Number(m[4]) !== 1)) throw new Error(`computed colour "${c}" is not an opaque rgb()`);
    const [r, g, b] = [m[1], m[2], m[3]].map((v) => {
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
  // Named by tag, position among its kind and size only, never by text: a select's text is its options'.
  const controls = [...document.querySelectorAll("button, select, nav a")];
  const smalls = controls.map((e, i) => ({ e, i, r: e.getBoundingClientRect() })).filter(({ r }) => r.width < 24 || r.height < 24)
    .map(({ e, i, r }) => `${e.tagName.toLowerCase()} ${i} of ${controls.length}: ${r.width}x${r.height}, display ${getComputedStyle(e).display}`);
  if (smalls.length > 0) throw new Error(`controls under 24 px: ${smalls.join("; ")}`);
  return { contrast: low, small: smalls.length };
});

// How far the page scrolls sideways (WCAG 1.4.10): 0 when nothing reflows out of the viewport.
export const overflows = () => browser.execute(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
// Whether the element `sel` scrolls inside itself, so a wide row is really there.
export const scrollsOnItsOwn = (sel: string) => browser.execute((s: string) => {
  const r = document.querySelector(s);
  return r !== null && r.scrollWidth > r.clientWidth;
}, sel);
// The Web Locks of the page's origin cannot be read from about:blank. A file the host serves as itself is a
// same-origin page that runs no application: the first asset the served index.html names. A failed read is not kept.
let lockPage: Promise<string> | undefined;
const lockPageUrl = () =>
  (lockPage ??= fetch(`${FIXTURE_PAGE}/`)
    .then(async (r) => {
      const asset = /\/assets\/[\w.-]+\.(?:css|js)/.exec(await r.text())?.[0];
      if (asset === undefined) throw new Error("noAppLocks: the served page names no asset to read the locks from");
      return `${FIXTURE_PAGE}${asset}`;
    })
    .catch((e: unknown) => {
      lockPage = undefined;
      throw e;
    }));
const heldLocks = () =>
  browser.execute(async () => {
    const names = ((await navigator.locks.query()).held ?? []).map((l) => l.name ?? "");
    return { renewer: names.filter((n) => n === "bronzeward-renewer").length, tab: names.filter((n) => n.startsWith("bronzeward-tab:")).length };
  });
// Waits, on the current window, until no page of the app holds the renewer lock or a tab lock: the next case's
// first tab would join a holder that is still there and sign in with its pair, with no authorize
// (ginsys/bronzeward#964). It returns only on two zero reads in a row, 250 ms apart (an absence is read twice,
// ginsys/bronzeward#944), and fails after `timeout`, naming the counts of its last read. A wait that saw a holder
// prints one line, counts only, so a run's log shows whether the trigger happened.
export async function noAppLocks(timeout = 15_000) {
  const t0 = Date.now();
  const url = await lockPageUrl();
  let first: { renewer: number; tab: number } | undefined;
  let last = { renewer: 0, tab: 0 };
  let zeros = 0;
  try {
    await browser.url(url);
    for (;;) {
      last = await heldLocks();
      first ??= last;
      zeros = last.renewer + last.tab === 0 ? zeros + 1 : 0;
      if (zeros === 2 || Date.now() - t0 > timeout) break;
      await browser.pause(250);
    }
  } finally {
    // A failed return is not allowed to hide the error that got us here; the next navigation fails on it anyway.
    await browser.url("about:blank").catch(() => undefined);
  }
  if (first !== undefined && first.renewer + first.tab > 0) console.log(`noAppLocks: waited ${Date.now() - t0} ms for renewer ${first.renewer}, tab ${first.tab}`);
  if (zeros < 2) throw new Error(`noAppLocks: no two zero reads in a row within ${timeout} ms of the case; the last read had renewer ${last.renewer}, tab ${last.tab}: the next case would join a page's pair`);
}
// Whether this session is Safari's, whose driver has no BiDi session (setViewport, dialog events) and
// shows no leave dialog; the cases that need them take the route written beside them.
export const inSafari = () => (browser.capabilities as { browserName?: string }).browserName?.toLowerCase() === "safari";
// Another window on `url`, switched to; its handle. Over WebDriver BiDi newWindow does not switch to
// the tab it opens. Safari's window.open can leave the new window with no address, or on about:blank,
// never loading the one asked for: it is given a few seconds, then loaded by address.
export async function newTab(url: string): Promise<string> {
  const before = await browser.getWindowHandles();
  const w = await browser.newWindow(url);
  const asked = "handle" in w ? w.handle : w.contextId;
  // The handle newWindow answers is not the new window's in Safari (measured on the runner: the case
  // went on in the window it opened from), so the new window is the one that was not there before.
  const opened = (await browser.getWindowHandles()).filter((h) => !before.includes(h));
  const handle = opened.length === 1 ? opened[0]! : asked;
  await browser.switchToWindow(handle);
  const now = await browser.getWindowHandle();
  if (now !== handle) throw new Error(`newTab: switched to ${handle} (newWindow said ${asked}), and the current window is ${now}; windows before: ${before.join(", ")}`);
  if (inSafari() && url !== "about:blank") {
    const loaded = await browser.waitUntil(async () => !["", "about:blank"].includes(await browser.getUrl()), { timeout: 5000 }).catch(() => false);
    if (!loaded) await browser.url(url);
  }
  return handle;
}
// Moves the focused select to the option of `value` by keyboard. Other browsers take ArrowDown;
// Safari on macOS opens its own popup for the arrow keys, which safaridriver's key events do not
// operate (measured on the runner: seven key sequences left the value as it was), and it does change
// the value for type-ahead, so the option's label is typed.
export async function pickByKeys(value: string) {
  const read = () => browser.execute(() => (document.activeElement as HTMLSelectElement).value);
  if (inSafari()) {
    const label = await browser.execute((v: string) => [...(document.activeElement as HTMLSelectElement).options].find((o) => o.value === v)?.text ?? "", value);
    expect(label).not.toBe("");
    await browser.pause(1100); // type-ahead forgets what was typed after a second (wdio.conf.ts)
    await browser.keys(label.split(""));
  } else {
    for (let i = 0; i < 20 && (await read()) !== value; i++) await browser.keys("ArrowDown");
  }
  expect(await read()).toBe(value);
}
// Each dialog the page raises while `act` runs, answered with `accept`, as the messages. Other
// browsers answer it from a BiDi event; Safari has no BiDi session, so the dialog is read and
// answered through the classic alert commands once `act` has returned or reported it.
type Dialog = { message: () => string; accept: () => Promise<void>; dismiss: () => Promise<void> };
export async function dialogsDuring(accept: boolean, act: () => Promise<unknown>, timeoutMsg: string): Promise<string[]> {
  const asked: string[] = [];
  if (inSafari()) {
    const failed = await act().then(() => undefined, (e: unknown) => e);
    await browser.waitUntil(async () => {
      try {
        // The message is kept only once the alert is answered: a failed answer is tried again, and the
        // alert is then read again, so one dialog is not counted twice.
        const message = await browser.getAlertText();
        await (accept ? browser.acceptAlert() : browser.dismissAlert());
        asked.push(message);
        return true;
      } catch {
        return false;
      }
    }, { timeout: 10_000, timeoutMsg: failed === undefined ? timeoutMsg : `${timeoutMsg}; the action failed: ${String(failed)}` });
    return asked;
  }
  const answer = (d: Dialog) => void (asked.push(d.message()), accept ? d.accept() : d.dismiss());
  browser.on("dialog", answer);
  try {
    await act();
    await browser.waitUntil(async () => asked.length > 0, { timeout: 10_000, timeoutMsg });
  } finally {
    browser.off("dialog", answer);
  }
  return asked;
}
// A case at 320 CSS pixels (WCAG 1.4.10). Safari cannot be narrowed that far: its driver has no BiDi
// session for setViewport, and a window it resizes stops at 336 CSS pixels (measured on the runner).
// The case is skipped there, and its record says so (conformance/cases).
export const itNarrow: Mocha.TestFunction | Mocha.PendingTestFunction = inSafari() ? it.skip : it;
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
