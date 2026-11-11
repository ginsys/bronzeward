// The draft editor (ginsys/bronzeward#339) against the disposable deployment, whose provider is
// mise run dev-bao's OpenBao (bao.ts): Create draft, a fragment saved and removed, Revert to base,
// Discard draft, the unsaved guard, ETAG-01, AMB-02, AMB-06, a 422 with [Go to field], and the
// fragment entry by keyboard only, the profile entry (pins moved by keyboard, ETAG-01, a 422
// at its path) and the assignment entry (a layer's list moved by keyboard, ETAG-01, the composition
// order, a 422 at the field for a fragment or a profile the draft removed). ETAG-01 on a fragment
// also reads the entry back through the API after the 412: it still holds the first browser's text.
// A document never crosses WebDriver, whose log the artifact scan
// reads: the page fetches it from the synthetic provider inside a script, or the case writes it
// through the API itself. Each document carries the run's canary (syntheticDocument), so the scan
// finds one in any retained file; no case takes a screenshot or reads a document's text back. The
// text a case types or adds is a synthetic edit that holds no value. The cases sign in as
// h-author. ETAG-01 runs with two browsers on one draft: a second session of the same browser,
// signed in as h-all (wdio.conf.ts secondSession); a deterministic variant moves the draft
// through the API as h-all instead.
import { randomBytes } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { NOT_RENDERED } from "../../src/drafts.tsx";
import { REPLAYED, unknown } from "../../src/states.tsx";
import { CONTROL, FIXTURE_ISSUER, FIXTURE_PAGE, syntheticDocument } from "../auth.conf.ts";
import type { Step } from "../injector.ts";
import { secondSession } from "../wdio.conf.ts";
import { faults, fixtureSignIn, mint, storageEmpty } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
let at = "";
let cluster = { id: "", name: "" };

const hex = () => randomBytes(4).toString("hex");
// Every write the injector received since the sign-in, as [method, route, key, expected epoch].
const writes = async (): Promise<string[][]> => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).map((w) => w.split(" "));
// Each helper acts in `b`, the spec's browser unless a case passes its second session (ETAG-01).
type B = WebdriverIO.Browser;
const pane = (b: B = browser) => b.$('main section[aria-label^="Fragment"]');
const result = (b: B = browser) => pane(b).$('[data-testid="write-result"]');
const notice = () => $('[data-testid="write-notice"]');
const profilePane = (b: B = browser) => b.$('main section[aria-label^="Profile"]');

// A write through the API as `human`, straight to the deployment: its answer's status, ETag and
// body, which no case prints.
async function api(human: string, method: string, path: string, body?: unknown, ifMatch?: string) {
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

// A draft in the case's cluster, with one fragment entry `name` holding its synthetic document.
async function seeded(name: string): Promise<{ id: string; etag: string; revision: string }> {
  const d = await api("h-author", "POST", "/drafts", { cluster: cluster.id, title: `synthetic ${name}` });
  expect(d.status).toBe(201);
  const id = d.body!["id"] as string;
  return { id, ...(await putFragment(id, name, d.etag)) };
}

// A write of fragment `name` through the API: the draft's new ETag and the revision it proposes.
// A `variant` writes another document, so the fragment has a second revision.
async function putFragment(id: string, name: string, etag: string, human = "h-author", variant = "") {
  const f = await api(human, "PUT", `/drafts/${id}/fragments/${name}`, { layer: "role", document: syntheticDocument(`${name}${variant}`), marks: null, declarations: null }, etag);
  expect(f.status).toBe(200);
  return { etag: f.etag, revision: (f.body!["entry"] as { revision: string }).revision };
}

// Signs in as h-author on the disposable deployment and opens the draft from the Changes list.
async function openDraft(id: string) {
  expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
  await $('nav[aria-label="Main"]').$("a=Changes").click();
  await $(`main a[title="${id}"]`).click();
  await expect($("main h2")).toHaveText(expect.stringContaining("synthetic"));
}

// Puts the synthetic document for `name` in the editor: fetched by the page, set as typing would.
// The page's CSP allows no eval, so each script finds the document's textarea by its label itself.
async function putDocument(name: string) {
  const ok = await browser.execute(async (issuer: string, n: string) => {
    const area = [...document.querySelectorAll("main section label")].find((l) => l.firstChild?.textContent?.startsWith("Document"))?.querySelector("textarea");
    if (area == null) return false;
    const r = await fetch(`${issuer}/synthetic-document?name=${n}`).catch(() => undefined);
    if (r?.ok !== true) return false;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, await r.text());
    area.dispatchEvent(new Event("input", { bubbles: true }));
    return true;
  }, FIXTURE_ISSUER, name);
  expect(ok).toBe(true);
}

// Whether the editor holds a document, read in the page; its text stays there. No case reads the
// text of an element that holds a textarea: React keeps a controlled textarea's text in its DOM.
const loaded = (b: B = browser) => b.waitUntil(() => b.execute(() => {
  const area = [...document.querySelectorAll("main section label")].find((l) => l.firstChild?.textContent?.startsWith("Document"))?.querySelector("textarea");
  return (area?.value.length ?? 0) > 0;
}), { timeout: 20_000, timeoutMsg: "the editor did not load the entry's revision" });

// Whether the editor's document holds the synthetic node label `example.test/<label>`, read in
// the page; `their` reads the conflict view's "Their text" instead.
const holds = (label: string, b: B = browser, their = false) => b.execute((l: string, t: boolean) => {
  const area = [...document.querySelectorAll("main section label")].find((x) => x.firstChild?.textContent?.startsWith(t ? "Their text" : "Document"))?.querySelector("textarea");
  return area?.value.includes(`example.test/${l}:`) ?? false;
}, label, their);

// Whether the document stored for fragment entry `name` of the draft holds the synthetic node label
// `example.test/<label>: synthetic`, read through the API. It is compared here, so a failure prints
// a boolean and never the document.
async function storedHolds(draft: string, name: string, label: string): Promise<boolean> {
  const entries = (await api("h-author", "GET", `/drafts/${draft}`)).body!["entries"] as { kind: string; name?: string; revision: string | null }[];
  const revision = entries.find((e) => e.kind === "fragment" && e.name === name)?.revision;
  expect(typeof revision).toBe("string");
  return String((await api("h-author", "GET", `/fragment-revisions/${revision}`)).body!["document"]).includes(`example.test/${label}: synthetic`);
}

// Adds the synthetic node label `example.test/<label>: synthetic` next to the document's tier
// label, at its indentation, as typing would: an edit that holds no value, made in the page so the
// document stays there, whatever indentation the server's stored form uses.
async function edit(label: string, b: B = browser) {
  const ok = await b.execute((l: string) => {
    const area = [...document.querySelectorAll("main section label")].find((x) => x.firstChild?.textContent?.startsWith("Document"))?.querySelector("textarea");
    const m = area == null ? null : /^( *)example\.test\/tier:.*$/m.exec(area.value);
    if (area == null || m == null) return false;
    const at = m.index + m[0].length;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, `${area.value.slice(0, at)}\n${m[1]}example.test/${l}: synthetic${area.value.slice(at)}`);
    area.dispatchEvent(new Event("input", { bubbles: true }));
    return true;
  }, label);
  expect(ok).toBe(true);
}

// Selects the fragment entry `name` and waits for its revision.
async function select(name: string, b: B = browser) {
  await b.$('main ul[aria-label="Entries"]').$(`button=fragment ${name}`).click();
  await loaded(b);
}

// Sets the faults for the fragment's sends (unless `keep`, as a second session's save does) and
// saves it.
async function save(draft: string, name: string, take: Step[] = [], b: B = browser, keep = false) {
  if (!keep) await faults({ upstream: at, steps: { path: `/drafts/${draft}/fragments/${name}`, take } });
  await pane(b).$("button=Save").click();
}

// One key and one reference epoch over every send of the fragment, n sends in all (at least n
// when `atLeast`).
async function oneWrite(n: number, atLeast = false) {
  const w = (await writes()).filter(([m]) => m === "PUT");
  if (atLeast) expect(w.length).toBeGreaterThanOrEqual(n);
  else expect(w).toHaveLength(n);
  expect(w[0]![2]).toMatch(/^[A-Za-z0-9_-]{22}$/);
  for (const x of w) expect(x).toEqual(w[0]);
}

// Each dialog's message, answered with `accept`, while `act` runs.
async function dialogs(accept: boolean, act: () => Promise<void>): Promise<string[]> {
  const asked: string[] = [];
  const answer = (d: { message: () => string; accept: () => Promise<void>; dismiss: () => Promise<void> }) => void (asked.push(d.message()), accept ? d.accept() : d.dismiss());
  browser.on("dialog", answer);
  try {
    await act();
    await browser.waitUntil(async () => asked.length > 0, { timeout: 10_000, timeoutMsg: "no dialog" });
  } finally {
    browser.off("dialog", answer);
  }
  return asked;
}

const guarded = () => browser.execute(() => !dispatchEvent(new Event("beforeunload", { cancelable: true })));

// At 320 CSS pixels (journeys §13, WCAG 1.4.10): how far the page scrolls sideways, and whether
// the region `sel` scrolls on its own, so the wide row is really there. The window is restored.
async function narrow(sel: string): Promise<{ page: number; region: boolean }> {
  const size = await browser.getWindowSize();
  await browser.setWindowSize(320 + size.width - (await browser.execute(() => document.documentElement.clientWidth)), size.height);
  try {
    return await browser.execute((s: string) => {
      const r = document.querySelector(s);
      return { page: document.documentElement.scrollWidth - document.documentElement.clientWidth, region: r !== null && r.scrollWidth > r.clientWidth };
    }, sel);
  } finally {
    await browser.setWindowSize(size.width, size.height);
  }
}

describe("the draft editor (#339)", () => {
  before(async () => {
    const res = await fetch(`${CONTROL}/disposable?start=normal`, { method: "POST" });
    expect(res.status).toBe(200);
    at = await res.text();
    const name = `synthetic-${hex()}`;
    const c = await api("h-author", "POST", "/clusters", { name, endpoint: `https://${name}.example.test:6443`, contract: "v1.14", talosClusterId: `${randomBytes(32).toString("base64url")}=` });
    expect(c.status).toBe(201);
    cluster = { id: c.body!["id"] as string, name };
  });

  it("the harness holds KV writes and retires the digest key only for the disposable deployment", async () => {
    // The page is open, on the fixture deployment, so the storage check after each case reads it.
    expect(await fixtureSignIn("h-viewer")).toMatch(SERVER);
    await faults({});
    for (const route of ["/bao/hold?ms=1000", "/bao/retire"]) {
      const refused = await fetch(`${CONTROL}${route}`, { method: "POST" });
      expect([route, refused.status, await refused.text()]).toEqual([route, 500, expect.stringContaining("only in this run's disposable deployment")]);
    }
  });

  it("Create draft, then a fragment saved: the entry is new, the footer says the draft is not rendered, and unsaved text is guarded", async () => {
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    await $('nav[aria-label="Main"]').$("a=Changes").click();
    await $("main").$("button=Create draft").click();
    await expect($("main h2")).toHaveText("Create draft");
    await $('select[name="cluster"]').selectByVisibleText(cluster.name);
    const title = `synthetic ${hex()}`;
    await $('input[name="title"]').setValue(title);
    await $("main form").$("button=Create draft").click();
    await expect($('[data-testid="write-result"]')).toHaveText(expect.stringContaining(`"${title}" created in cluster`), { wait: 20_000 });
    await $('[data-testid="write-result"]').$("a").click();
    await expect($("main h2")).toHaveText(expect.stringContaining(`"${title}" open, revision`));
    await expect($('[data-testid="not-rendered"]')).toHaveText(NOT_RENDERED);
    await $("main").$("button=Add fragment").click();
    const name = `save-${hex()}`;
    await pane().$("label*=name").$("input").setValue(name);
    await putDocument(name);
    await expect(pane().$("p*=Layer:")).toHaveText(expect.stringContaining("new -> unsaved"));
    // The guard asks before the page leaves unsaved text, and the text stays.
    expect(await guarded()).toBe(true);
    const asked = await dialogs(false, () => $('nav[aria-label="Main"]').$("a=Changes").click());
    expect(asked[0]).toContain(`Unsaved text in fragment ${name} of draft`);
    await expect($("main h2")).toHaveText(expect.stringContaining(title));
    const draft = (await browser.execute(() => location.pathname)).split("/").pop()!;
    await save(draft, name);
    await expect(result()).toHaveText(expect.stringContaining(`Fragment ${name} saved in draft`), { wait: 30_000 });
    await expect($('main ul[aria-label="Entries"]')).toHaveText(expect.stringContaining(`fragment ${name} new`));
    expect(await guarded()).toBe(false);
    await oneWrite(1);
    // ginsys/bronzeward#517 H1: the pane now edits the entry it created. H4: Add fragment with that
    // name again sends nothing.
    await expect(pane().$("label*=name")).not.toBeExisting();
    await expect(pane().$("button=Remove")).toBeExisting();
    await $("main").$("button=Add fragment").click();
    await pane().$("label*=name").$("input").setValue(name);
    await pane().$("button=Save").click();
    await expect($('[data-testid="editor-note"]')).toHaveText(`Not sent: this draft already holds fragment ${name}. Open it from Entries to change it.`);
    await oneWrite(1);
  });

  it("ETAG-01: two browsers on one draft: the second to save gets 412, sends nothing more, sees the first's text, and re-applies its own", async function () {
    this.timeout(120_000);
    const name = `etag2-${hex()}`;
    const { id } = await seeded(name);
    // The other browser: a second session of this browser, signed in as h-all first, since the
    // next sign-in's /next clears the injector's log.
    const other = await secondSession(browser.requestedCapabilities as WebdriverIO.Capabilities);
    try {
      expect((await fetch(`${CONTROL}/next?human=h-all`)).ok).toBe(true);
      await faults({ upstream: at });
      await other.url(`${FIXTURE_PAGE}/`);
      await other.$('nav[aria-label="Main"]').$("a=Changes").waitForClickable({ timeout: 20_000 });
      await other.$('nav[aria-label="Main"]').$("a=Changes").click();
      await other.$(`main a[title="${id}"]`).click();
      await select(name, other);
      await openDraft(id);
      await select(name);
      // Both pages hold the same ETag. The other saves first.
      await edit("theirs", other);
      await save(id, name, [], other, true);
      await expect(result(other)).toHaveText(expect.stringContaining(`Fragment ${name} saved in draft`), { wait: 30_000 });
      await edit("mine");
      await save(id, name, [], browser, true);
      await expect(pane().$('[data-testid="status"]')).toHaveText(expect.stringContaining("Someone changed this draft"), { wait: 20_000 });
      // Nothing more is sent after the 412, once the repeats' backoff would have run.
      await browser.pause(3_000);
      const puts = (await writes()).filter(([m]) => m === "PUT");
      expect(puts).toHaveLength(2);
      expect(puts[0]![2]).not.toBe(puts[1]![2]);
      // The 412 lost nothing: the entry still holds the other browser's text, and not this one's.
      expect(await storedHolds(id, name, "theirs")).toBe(true);
      expect(await storedHolds(id, name, "mine")).toBe(false);
      const now =(await api("h-author", "GET", `/drafts/${id}`)).body!["revision"] as number;
      await pane().$("button=Show their change").click();
      await expect($("main h2")).toHaveText(expect.stringContaining(`revision ${now}`), { wait: 20_000 });
      await browser.waitUntil(() => holds("theirs", browser, true), { timeout: 20_000, timeoutMsg: "the conflict view does not show the other browser's text" });
      await pane().$("button=Copy my text into the new revision").click();
      expect(await holds("mine")).toBe(true);
      await save(id, name, [], browser, true);
      await expect(result()).toHaveText(expect.stringContaining(`Fragment ${name} saved in draft`), { wait: 30_000 });
      // The saved revision, read back through the API, holds this browser's text: the editor
      // already shows it, so the editor alone proves nothing.
      expect(await storedHolds(id, name, "mine")).toBe(true);
      await browser.waitUntil(() => holds("mine"), { timeout: 20_000, timeoutMsg: "the saved revision lost this browser's text" });
      await other.execute(() => [localStorage.length, sessionStorage.length]).then((n) => expect(n).toEqual([0, 0]));
    } finally {
      await other.deleteSession();
    }
  });

  it("ETAG-01, deterministic: another author's API write moves the draft: 412, no resend, the newer state shown, then my text saved", async () => {
    const name = `etag-${hex()}`;
    const { id, etag } = await seeded(name);
    await openDraft(id);
    await select(name);
    await edit("mine");
    const other = await api("h-all", "PUT", `/drafts/${id}/fragments/other-${hex()}`, { layer: "role", document: syntheticDocument("other"), marks: null, declarations: null }, etag);
    expect(other.status).toBe(200);
    const now = (await api("h-author", "GET", `/drafts/${id}`)).body!["revision"] as number;
    await save(id, name);
    await expect(pane().$('[data-testid="status"]')).toHaveText(expect.stringContaining("Someone changed this draft"), { wait: 20_000 });
    // No resend: the 412 is the only answer, after the repeats' backoff would have run.
    await browser.pause(3_000);
    await oneWrite(1);
    await pane().$("button=Show their change").click();
    await expect($("main h2")).toHaveText(expect.stringContaining(`revision ${now}`), { wait: 20_000 });
    await expect($('main ul[aria-label="Entries"]')).toHaveText(expect.stringContaining("fragment other-"));
    await pane().$("button=Copy my text into the new revision").click();
    await save(id, name);
    await expect(result()).toHaveText(expect.stringContaining(`Fragment ${name} saved in draft`), { wait: 30_000 });
  });

  it("a 422 names the refused reference, and Go to field selects it in the document", async () => {
    const name = `ref-${hex()}`;
    const { id } = await seeded(`base-${hex()}`);
    await openDraft(id);
    await $("main").$("button=Add fragment").click();
    await pane().$("label*=name").$("input").setValue(name);
    await pane().$("label*=Document").$("textarea").setValue("machine:\n  nodeLabels:\n    example.test/ref: !bwref ghost\n");
    await pane().$("button=Declare").click();
    await save(id, name);
    await expect(result()).toHaveText(expect.stringContaining("validation-failed (rule unknown-reference)"), { wait: 30_000 });
    await pane().$("button=Go to field").click();
    const chosen = await browser.execute(() => {
      const a = document.activeElement as HTMLTextAreaElement | null;
      return a?.tagName === "TEXTAREA" ? a.value.slice(a.selectionStart, a.selectionEnd) : null;
    });
    expect(chosen).toBe("!bwref ghost");
  });

  it("AMB-02: a fragment write dropped while it ingests is repeated with its key, waits on the in-progress 409, then shows the replay", async function () {
    this.timeout(120_000);
    const name = `amb02-${hex()}`;
    const { id } = await seeded(name);
    await openDraft(id);
    await select(name);
    await edit("amb-02");
    // The mark makes the ingestion write the new label's value as a generation: with no extracted
    // value it writes nothing to the provider, and the hold holds nothing.
    await pane().$("label*=Marks").$("textarea").setValue("doc[0]/machine/nodeLabels/example.test~1amb-02");
    // The first request's ingestion holds its claim while the provider holds its KV write.
    expect((await fetch(`${CONTROL}/bao/hold?ms=6000`, { method: "POST" })).status).toBe(200);
    await save(id, name, ["pass-drop"]);
    await expect(result()).toHaveText(expect.stringContaining("the first request with this key is still ingesting (ingestion ing_"), { wait: 30_000 });
    await expect(result()).toHaveText(expect.stringContaining(`${REPLAYED} Fragment ${name} saved in draft`), { wait: 60_000 });
    await oneWrite(3, true);
    // The drop showed the unknown notice, so it shows the replayed result until dismissed (B11).
    expect(await notice().getText()).toContain(`${REPLAYED} Saved (the server answered 200).`);
    await notice().parentElement().$("button=Dismiss").click();
    expect(await notice().isExisting()).toBe(false);
  });

  it("Revert to base on a new entry says it has no base; Remove proposes the removal; Discard asks, then discards", async () => {
    const name = `rm-${hex()}`;
    const { id } = await seeded(name);
    await openDraft(id);
    await select(name);
    await pane().$("button=Revert to base").click();
    await expect($('[data-testid="editor-note"]')).toHaveText("No base: this fragment is new in the draft.");
    await faults({ upstream: at });
    await pane().$("button=Remove").click();
    await expect(result()).toHaveText(expect.stringContaining(`Fragment ${name} removed from draft`), { wait: 20_000 });
    await expect($('main ul[aria-label="Entries"]')).toHaveText(expect.stringContaining(`fragment ${name} removed`));
    // ginsys/bronzeward#517 H8: the draft stores the removal, so the text still shown is unsaved.
    await expect(pane()).toHaveText(expect.stringContaining("-> unsaved"));
    // Dismissed, nothing is sent; accepted, the draft is discarded.
    const before = (await writes()).length;
    expect((await dialogs(false, () => $("main").$("button=Discard draft").click()))[0]).toContain(`Discard draft ${id}`);
    await browser.pause(500);
    expect(await writes()).toHaveLength(before);
    await dialogs(true, () => $("main").$("button=Discard draft").click());
    await expect($("main h2")).toHaveText(expect.stringContaining("discarded"), { wait: 20_000 });
    expect((await writes()).map(([m, r]) => `${m} ${r}`).slice(before)).toEqual([`POST /drafts/${id}/discard`]);
  });

  it("keyboard only: a fragment is named, typed and saved without the pointer", async () => {
    const { id } = await seeded(`base-${hex()}`);
    await openDraft(id);
    const focused = () => browser.execute(() => {
      const a = document.activeElement;
      return a?.tagName === "BUTTON" ? `button ${a.textContent}` : `field ${a?.closest("label")?.firstChild?.textContent?.trim() ?? ""}`;
    });
    const tabTo = async (want: string) => {
      for (let i = 0; i < 60 && (await focused()) !== want; i++) await browser.keys("Tab");
      expect(await focused()).toBe(want);
    };
    await tabTo("button Add fragment");
    await browser.keys("Enter");
    const name = `kbd-${hex()}`;
    await tabTo("field name");
    await browser.keys(name.split(""));
    await tabTo("field Document");
    await browser.keys("machine:\n  nodeLabels:\n    example.test/kbd: synthetic\n".split(""));
    await faults({ upstream: at });
    await tabTo("button Save");
    await browser.keys("Enter");
    await expect(result()).toHaveText(expect.stringContaining(`Fragment ${name} saved in draft`), { wait: 30_000 });
    await oneWrite(1);
  });

  // The profile entry's cases, registered here so that AMB-06 stays the last case (mocha runs a
  // suite's own cases before its nested suites).
  const profileCases = () => {
    // The names of the pinned rows, in order: each row starts with its fragment's name.
    const order = async () => {
      const items = await profilePane().$$('ol[aria-label="Pinned fragment revisions"] li');
      const names: string[] = [];
      for (const li of items) names.push((await li.getText()).split(" ")[0]!);
      return names;
    };
    // Focuses an element of the profile pane, as Tab would.
    const focusOn = async (selector: string) => {
      const el = await profilePane().$(selector);
      await browser.execute((e: HTMLElement) => e.focus(), el as unknown as HTMLElement);
    };
    const active = () => browser.execute(() => document.activeElement?.getAttribute("aria-label") ?? document.activeElement?.textContent ?? "");
    const profileRevisions = async (draft: string, name: string) => {
      const entries = (await api("h-author", "GET", `/drafts/${draft}`)).body!["entries"] as { kind: string; name?: string; revision: string | null }[];
      const revision = entries.find((e) => e.kind === "profile" && e.name === name)?.revision;
      expect(typeof revision).toBe("string");
      return (await api("h-author", "GET", `/profile-revisions/${revision}`)).body!["fragments"] as string[];
    };
    const openProfile = async (draft: string, name: string, pins: number) => {
      await openDraft(draft);
      await $('main ul[aria-label="Entries"]').$(`button=profile ${name}`).click();
      await browser.waitUntil(async () => (await order()).length === pins, { timeout: 20_000, timeoutMsg: "the editor did not load the profile's pins" });
    };

    it("a profile pins two revisions in order, one is moved and saved by keyboard, and the order reads back", async () => {
      const a = `pa-${hex()}`;
      const b = `pb-${hex()}`;
      const p = `prof-${hex()}`;
      const first = await seeded(a);
      const second = await putFragment(first.id, b, first.etag);
      await openDraft(first.id);
      await $("main").$("button=Add profile").click();
      await profilePane().$("label*=name").$("input").setValue(p);
      for (const r of [first.revision, second.revision]) {
        await profilePane().$("select").selectByAttribute("value", r);
        await profilePane().$("button=Add to profile").click();
      }
      expect(await order()).toEqual([a, b]);
      await expect(profilePane()).toHaveText(expect.stringContaining("A profile pins these fragment revisions."));
      await faults({ upstream: at });
      await focusOn(`button[aria-label="Move up ${b}"]`);
      await browser.keys("Enter");
      expect(await order()).toEqual([b, a]);
      // The row is first now, so Move up is off and the focus stays on the row's other button.
      expect(await active()).toBe(`Move down ${b}`);
      await focusOn("button=Save");
      await browser.keys("Enter");
      await expect(profilePane().$('[data-testid="write-result"]')).toHaveText(expect.stringContaining(`Profile ${p} saved in draft`), { wait: 30_000 });
      await oneWrite(1);
      expect(await profileRevisions(first.id, p)).toEqual([second.revision, first.revision]);
      // Signed in again, the page reads the order from the server.
      await openProfile(first.id, p, 2);
      expect(await order()).toEqual([b, a]);
    });

    it("ETAG-01 on a profile: another author's API write moves the draft: 412, no resend, the pins kept, then saved", async () => {
      const a = `pe-${hex()}`;
      const b = `pf-${hex()}`;
      const p = `profe-${hex()}`;
      const first = await seeded(a);
      const second = await putFragment(first.id, b, first.etag);
      const saved = await api("h-author", "PUT", `/drafts/${first.id}/profiles/${p}`, { fragments: [first.revision, second.revision] }, second.etag);
      expect(saved.status).toBe(200);
      await openProfile(first.id, p, 2);
      await profilePane().$(`button[aria-label="Move up ${b}"]`).click();
      expect(await order()).toEqual([b, a]);
      // Another author pins one revision only, in the same profile: their list is not mine.
      const other = await api("h-all", "PUT", `/drafts/${first.id}/profiles/${p}`, { fragments: [second.revision] }, saved.etag);
      expect(other.status).toBe(200);
      expect(other.etag).not.toBe(saved.etag);
      const now = (await api("h-author", "GET", `/drafts/${first.id}`)).body!["revision"] as number;
      await faults({ upstream: at });
      await profilePane().$("button=Save").click();
      await expect(profilePane().$('[data-testid="status"]')).toHaveText(expect.stringContaining("Someone changed this draft"), { wait: 20_000 });
      // No resend: the 412 is the only answer, after the repeats' backoff would have run.
      await browser.pause(3_000);
      await oneWrite(1);
      expect(await order()).toEqual([b, a]);
      await profilePane().$("button=Show their change").click();
      await expect($("main h2")).toHaveText(expect.stringContaining(`revision ${now}`), { wait: 20_000 });
      // Their list shows, and mine waits aside until Copy puts it back.
      await browser.waitUntil(async () => (await order()).join(",") === b, { timeout: 20_000, timeoutMsg: "Show their change did not show the other author's pins" });
      await profilePane().$("button=Copy my text into the new revision").click();
      expect(await order()).toEqual([b, a]);
      await profilePane().$("button=Save").click();
      await expect(profilePane().$('[data-testid="write-result"]')).toHaveText(expect.stringContaining(`Profile ${p} saved in draft`), { wait: 30_000 });
      expect(await profileRevisions(first.id, p)).toEqual([second.revision, first.revision]);
    });

    it("at 320 CSS pixels a long unbroken profile name wraps and the page does not scroll sideways", async () => {
      const a = `pw-${hex()}`;
      const long = `p${"x".repeat(60)}`;
      const seed = await seeded(a);
      const saved = await api("h-author", "PUT", `/drafts/${seed.id}/profiles/${long}`, { fragments: [seed.revision] }, seed.etag);
      expect(saved.status).toBe(200);
      await openProfile(seed.id, long, 1);
      expect(await narrow('main section[aria-label^="Profile"]')).toEqual({ page: 0, region: false });
    });

    it("a pin that is not the fragment's current revision is marked, and its 422 names the path and no revision", async () => {
      const a = `ps-${hex()}`;
      const p = `profs-${hex()}`;
      const seed = await seeded(a);
      const saved = await api("h-author", "PUT", `/drafts/${seed.id}/profiles/${p}`, { fragments: [seed.revision] }, seed.etag);
      expect(saved.status).toBe(200);
      // A second revision of the fragment in the draft: the profile's pin is the first.
      await putFragment(seed.id, a, saved.etag, "h-author", "-2");
      await openProfile(seed.id, p, 1);
      await expect(profilePane().$('ol[aria-label="Pinned fragment revisions"]')).toHaveText(expect.stringContaining("pin is not current"), { wait: 20_000 });
      await faults({ upstream: at });
      await profilePane().$("button=Save").click();
      const refused = profilePane().$('[data-testid="write-result"]');
      await expect(refused).toHaveText(expect.stringContaining("validation-failed"), { wait: 30_000 });
      await expect(profilePane().$('[data-testid="write-where"]')).toHaveText("At fragments[0].");
      expect(await refused.getText()).not.toContain(seed.revision);
    });
  };
  profileCases();

  // The assignment entry's cases, registered here for the same reason as the profile cases.
  const assignmentCases = () => {
    const asg = (b: B = browser) => b.$('main section[aria-label^="Assignment"]');
    // The names in a section of the assignment (Profiles or Layer role), in order: each row starts with its name.
    const names = async (title: string) => {
      const out: string[] = [];
      for (const li of await asg().$$(`ol[aria-label="${title}"] li`)) out.push((await li.getText()).split(" ")[0]!);
      return out;
    };
    // A machine of the case's cluster, recorded through the API.
    const machine = async () => {
      const m = await api("h-author", "POST", "/machines", { cluster: cluster.id, talosNodeId: `synthetic-node-${hex()}`, platform: "metal", talosEndpoint: `192.0.2.${1 + Math.floor(Math.random() * 250)}:50000` });
      expect(m.status).toBe(201);
      return m.body!["id"] as string;
    };
    const add = async (title: string, name: string, button: string) => {
      const select = asg().$(`select[aria-label="Add to ${title}"]`);
      await select.$(`option[value="${name}"]`).waitForExist({ timeout: 20_000 });
      await select.selectByAttribute("value", name);
      await asg().$(`button=${button}`).click();
    };
    // The assignment of `m` as the draft stores it: its revision id and its whole body.
    const storedAssignment = async (draft: string, m: string) => {
      const entries = (await api("h-author", "GET", `/drafts/${draft}`)).body!["entries"] as { kind: string; machine?: string; revision: string | null }[];
      const revision = entries.find((e) => e.kind === "assignment" && e.machine === m)?.revision;
      expect(typeof revision).toBe("string");
      return { revision: revision as string, body: (await api("h-author", "GET", `/assignment-revisions/${revision}`)).body! };
    };
    const storedFragments = async (draft: string, m: string) => (await storedAssignment(draft, m)).body["fragments"] as Record<string, string[]>;
    const openAssignment = async (draft: string, count: number) => {
      await openDraft(draft);
      await $('main ul[aria-label="Entries"]').$("button*=assignment").click();
      await browser.waitUntil(async () => (await names("Layer role")).length === count, { timeout: 20_000, timeoutMsg: "the editor did not load the assignment's list" });
    };

    it("an assignment lists two fragments of a layer, one is moved and saved by keyboard, and the order reads back", async () => {
      const a = `aa-${hex()}`;
      const b = `ab-${hex()}`;
      const m = await machine();
      const first = await seeded(a);
      await putFragment(first.id, b, first.etag);
      await openDraft(first.id);
      await $("main").$("button=Add assignment").click();
      await asg().$("label*=machine").$("select").$(`option[value="${m}"]`).waitForExist({ timeout: 20_000 });
      await asg().$("label*=machine").$("select").selectByAttribute("value", m);
      await add("Layer role", a, "Add to layer role");
      await add("Layer role", b, "Add to layer role");
      expect(await names("Layer role")).toEqual([a, b]);
      await faults({ upstream: at });
      const up = await asg().$(`button[aria-label="Move up role ${b}"]`);
      await browser.execute((e: HTMLElement) => e.focus(), up as unknown as HTMLElement);
      await browser.keys("Enter");
      expect(await names("Layer role")).toEqual([b, a]);
      await asg().$("button=Save").click();
      await expect(asg().$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("saved in draft"), { wait: 30_000 });
      await oneWrite(1);
      expect(await storedFragments(first.id, m)).toEqual({ role: [b, a] });
      await openAssignment(first.id, 2);
      expect(await names("Layer role")).toEqual([b, a]);
    });

    it("ETAG-01 on an assignment: another author's API write moves the draft: 412, no resend, the list kept, then saved", async () => {
      const a = `ae-${hex()}`;
      const b = `af-${hex()}`;
      const m = await machine();
      const first = await seeded(a);
      const second = await putFragment(first.id, b, first.etag);
      const saved = await api("h-author", "PUT", `/drafts/${first.id}/assignments/${m}`, { profiles: [], fragments: { role: [a, b] } }, second.etag);
      expect(saved.status).toBe(200);
      await openAssignment(first.id, 2);
      await asg().$(`button[aria-label="Move up role ${b}"]`).click();
      expect(await names("Layer role")).toEqual([b, a]);
      const other = await api("h-all", "PUT", `/drafts/${first.id}/assignments/${m}`, { profiles: [], fragments: { role: [b] } }, saved.etag);
      expect(other.status).toBe(200);
      const now = (await api("h-author", "GET", `/drafts/${first.id}`)).body!["revision"] as number;
      await faults({ upstream: at });
      await asg().$("button=Save").click();
      await expect(asg().$('[data-testid="status"]')).toHaveText(expect.stringContaining("Someone changed this draft"), { wait: 20_000 });
      await browser.pause(3_000);
      await oneWrite(1);
      expect(await names("Layer role")).toEqual([b, a]);
      await asg().$("button=Show their change").click();
      await expect($("main h2")).toHaveText(expect.stringContaining(`revision ${now}`), { wait: 20_000 });
      await browser.waitUntil(async () => (await names("Layer role")).join(",") === b, { timeout: 20_000, timeoutMsg: "Show their change did not show the other author's list" });
      await asg().$("button=Copy my text into the new revision").click();
      expect(await names("Layer role")).toEqual([b, a]);
      await asg().$("button=Save").click();
      await expect(asg().$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("saved in draft"), { wait: 30_000 });
      expect(await storedFragments(first.id, m)).toEqual({ role: [b, a] });
    });

    // An id as the page shows it: the prefix and the first and last four characters (short in list.tsx).
    const shortId = (id: string) => {
      const i = id.indexOf("_") + 1;
      return id.length - i > 10 ? `${id.slice(0, i + 4)}…${id.slice(-4)}` : id;
    };
    // The assignment of `draft` for machine `m` names something the draft removed. Save is refused
    // 422 at `where`: the text holds the rule and the path and none of `revisions` (nor the stored
    // assignment's own), whole or shortened, and neither the draft nor the stored assignment moved.
    const refusedAt = async (draft: string, m: string, where: string, revisions: string[]) => {
      const draftRevision = async () => (await api("h-author", "GET", `/drafts/${draft}`)).body!["revision"] as number;
      const before = await draftRevision();
      const stored = await storedAssignment(draft, m);
      await openAssignment(draft, 1);
      await faults({ upstream: at });
      await asg().$("button=Save").click();
      const refused = asg().$('[data-testid="write-result"]');
      await expect(refused).toHaveText(expect.stringContaining("validation-failed"), { wait: 30_000 });
      // The rule the server states (updateAssignment), beside the path.
      await expect(refused).toHaveText(expect.stringContaining("a name must have a head or be proposed by this draft, not removed by it"));
      await expect(asg().$('[data-testid="write-where"]')).toHaveText(`At ${where}.`);
      const shown = await refused.getText();
      for (const id of [...revisions, stored.revision]) {
        expect(shown).not.toContain(id);
        expect(shown).not.toContain(shortId(id));
      }
      expect(await draftRevision()).toBe(before);
      // The same revision id and the same body: the profiles and the fragments both stay.
      expect(await storedAssignment(draft, m)).toEqual(stored);
    };

    it("an assignment that names a fragment the draft removed is refused 422 at the field, and nothing stored moves", async () => {
      const a = `ag-${hex()}`;
      const m = await machine();
      const seed = await seeded(a);
      const saved = await api("h-author", "PUT", `/drafts/${seed.id}/assignments/${m}`, { profiles: [], fragments: { role: [a] } }, seed.etag);
      expect(saved.status).toBe(200);
      expect((await api("h-author", "DELETE", `/drafts/${seed.id}/fragments/${a}`, undefined, saved.etag)).status).toBe(200);
      await refusedAt(seed.id, m, "fragments.role[0]", [seed.revision]);
    });

    it("an assignment that names a profile the draft removed is refused 422 at the field, and nothing stored moves", async () => {
      const a = `ah-${hex()}`;
      const p = `asgrm-${hex()}`;
      const m = await machine();
      const seed = await seeded(a);
      const profile = await api("h-author", "PUT", `/drafts/${seed.id}/profiles/${p}`, { fragments: [seed.revision] }, seed.etag);
      expect(profile.status).toBe(200);
      const saved = await api("h-author", "PUT", `/drafts/${seed.id}/assignments/${m}`, { profiles: [p], fragments: { role: [a] } }, profile.etag);
      expect(saved.status).toBe(200);
      expect((await api("h-author", "DELETE", `/drafts/${seed.id}/profiles/${p}`, undefined, saved.etag)).status).toBe(200);
      await refusedAt(seed.id, m, "profiles[0]", [seed.revision, (profile.body!["entry"] as { revision: string }).revision]);
    });

    it("Show composition and references lists a profile's pin before the assignment's fragment of its layer, and no document", async () => {
      const a = `ac-${hex()}`;
      const b = `ad-${hex()}`;
      const p = `asgprof-${hex()}`;
      const m = await machine();
      const first = await seeded(a);
      const profile = await api("h-author", "PUT", `/drafts/${first.id}/profiles/${p}`, { fragments: [first.revision] }, first.etag);
      expect(profile.status).toBe(200);
      const second = await putFragment(first.id, b, profile.etag);
      const saved = await api("h-author", "PUT", `/drafts/${first.id}/assignments/${m}`, { profiles: [p], fragments: { role: [b] } }, second.etag);
      expect(saved.status).toBe(200);
      await openAssignment(first.id, 1);
      const show = asg().$("button=Show composition and references");
      await show.waitForEnabled({ timeout: 20_000, timeoutMsg: "the lists did not load" });
      await show.click();
      const rows = () => asg().$$('ol[aria-label="Composition order"] li');
      await browser.waitUntil(async () => (await rows()).length === 2, { timeout: 30_000, timeoutMsg: "no composition order" });
      const texts: string[] = [];
      for (const li of await rows()) texts.push(await li.getText());
      const [pin, own] = texts;
      expect(pin!.startsWith(`role ${a} `)).toBe(true);
      expect(pin).toContain(`pinned by profile ${p}`);
      expect(own!.startsWith(`role ${b} `)).toBe(true);
      expect(own).toContain("from this assignment");
      // The documents are read for their references and never shown: the page holds no canary.
      expect(await browser.execute(() => document.body.innerText.includes("-doc-"))).toBe(false);
    });
  };
  assignmentCases();

  it("at 320 CSS pixels a wide row scrolls inside the Drafts and References tables, and the page does not", async () => {
    const wide = `w${"x".repeat(119)}`;
    const d = await api("h-author", "POST", "/drafts", { cluster: cluster.id, title: `synthetic ${wide}` });
    expect(d.status).toBe(201);
    const id = d.body!["id"] as string;
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    await $('nav[aria-label="Main"]').$("a=Changes").click();
    await $(`main a[title="${id}"]`).waitForExist({ timeout: 20_000 });
    expect(await narrow('main [role="region"][aria-label="Drafts"]')).toEqual({ page: 0, region: true });
    await $(`main a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(expect.stringContaining("synthetic"));
    await $("main").$("button=Add fragment").click();
    await pane().$("label*=Document").$("textarea").setValue(`machine:\n  nodeLabels:\n    example.test/ref: !bwref ${wide}\n`);
    await expect(pane().$('[role="region"][aria-label="References in this document"]')).toBeExisting();
    expect(await narrow('main [role="region"][aria-label="References in this document"]')).toEqual({ page: 0, region: true });
  });

  // Last: the digest key's earlier versions stay retired for the rest of the run.
  it("AMB-06: a repeat after the digest key's recorded version became uncomputable is 422, shown as unknown; no new key", async () => {
    const name = `amb06-${hex()}`;
    const { id } = await seeded(name);
    await openDraft(id);
    await select(name);
    await edit("amb-06");
    await save(id, name, ["commit-drop-retire"]);
    await expect(notice()).toHaveText(expect.stringContaining(unknown("this repeat could not be matched to the earlier request under its key, so it did nothing")), { wait: 30_000 });
    await oneWrite(2);
    expect(await notice().getText()).toContain(`To learn the outcome, read /drafts/${id}.`);
  });
});

afterEach(storageEmpty);
