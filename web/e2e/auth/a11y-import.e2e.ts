// The keyboard-only and 320 CSS pixel runs of journeys §13 (WCAG 2.2 AA, ginsys/bronzeward#894) over
// the screens of tasks T3 and T5 that had none: the import wizard and its import page, the protected
// import review (mark, continue, abandon) and the conflict view of the draft editor. They are measured
// on the same items as the inventory views (inventory.e2e.ts), and conformance/accessibility-web.md
// records each by the case's name. The cases sign in as h-author against the disposable deployment.
// An automated check is evidence for what it measures, not conformance.
//
// Nothing protected crosses WebDriver, whose log the artifact scan reads. A case that needs a document
// with the run's canary makes the import through the API in this process, which knows the document
// (syntheticDocument), so the page only receives it from the server; a document a keyboard types holds
// no canary. A focus is described by tag, name and label, never by a field's text (helpers.ts tab), and
// the page is asked only booleans about a review. No case takes a screenshot or a trace
// (retention.test.ts reads this source). After each case the storage check finds nothing in storage,
// IndexedDB or caches, and the protected cases find no cached answer of their review.
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { FIXTURE_ISSUER, FIXTURE_PAGE, syntheticDocument } from "../auth.conf.ts";
import { absent, api, cacheControl, cacheHolds, disposable, eventually, fixtureSignIn, focusIn, focusKept, focusSettles, fromTitle, hex, itNarrow, measure, narrow, onMain, overflows, pickByKeys, storageControl, storageEmpty, syntheticMachine, tab, tabTo } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
// The one value of the synthetic document a case marks by choosing it from the review's list.
const TIER = "doc[0]/machine/nodeLabels/example.test~1tier";
// A document a keyboard types: no canary, the shape of the synthetic one.
const TYPED = "machine:\n  nodeLabels:\n    example.test/kbd: synthetic\n";
let at = "";
let cluster = "";
let machine = "";
// The review's import of the running protected case, for the cache check after it.
let importId = "";
let docName = "";

const ended = () => $('[data-testid="ended"]');
const allowed = () => $('[data-testid="allowed"]');
const holds = (text: string) => expect.stringContaining(text);

// A draft in the case's cluster; its id and ETag.
async function draft(title = `synthetic ${hex()}`): Promise<{ id: string; etag: string }> {
  const d = await api(at, "h-author", "POST", "/drafts", { cluster, title });
  expect(d.status).toBe(201);
  return { id: d.body!["id"] as string, etag: d.etag };
}

// An import the API starts, as the wizard does: its id once the import is `state`.
async function startImport(staging: "encrypted" | "transient", review: boolean, state: string): Promise<string> {
  const d = await draft();
  docName = `a11y-${hex()}`;
  const i = await api(at, "h-author", "POST", "/ingestions", { kind: "import", machine, draft: d.id, source: "document", staging, review, document: syntheticDocument(docName) }, d.etag);
  expect(i.status).toBeLessThan(300);
  const id = i.body!["ingestion"] as string;
  // Held before the wait, so the cleanup finds an accepted import whose state never arrives.
  importId = id;
  await browser.waitUntil(async () => (await api(at, "h-author", "GET", `/ingestions/${id}`)).body?.["state"] === state, { timeout: 60_000, interval: 500, timeoutMsg: `the import did not reach ${state}` });
  return id;
}

// Signs in and opens the import page of `id`, as a reload does.
async function openImport(id: string, heading: string) {
  expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
  await browser.url(`${FIXTURE_PAGE}/ingestions/${id}`);
  await expect($("main h2")).toHaveText(`Import ${short(id)}: ${heading}`, { wait: 30_000 });
}

// What every protected case leaves behind: nothing in storage, IndexedDB or caches, and no cached
// answer of the case's review.
async function leavesNothing() {
  await storageEmpty();
  if (importId !== "") expect(await cacheHolds(`/api/v1/ingestions/${importId}/review`)).toBe(false);
  importId = "";
}


// Whether the review text holds the tier label of the document the case staged, answered in the page
// (it reads the label from the synthetic provider, as the case that staged it did): neither the label
// nor the canary in it crosses WebDriver.
const reviewHolds = () =>
  browser.execute(async (issuer: string, n: string) => {
    const text = document.querySelector<HTMLTextAreaElement>('[data-testid="review-text"]')?.value ?? "";
    const r = await fetch(`${issuer}/synthetic-document?name=${n}`).catch(() => undefined);
    const label = r?.ok === true ? /example\.test\/tier: (\S+)/.exec(await r.text())?.[1] : undefined;
    return label !== undefined && label.length > 0 && text.includes(label);
  }, FIXTURE_ISSUER, docName);

// The path the focused "Mark as secret" button belongs to; a path holds no canary.
const focusedPath = () => browser.execute(() => document.activeElement?.closest("li")?.querySelector("code")?.textContent ?? "");
// The label a focused field belongs to.
const focusedLabel = () => browser.execute(() => document.activeElement?.closest("label")?.firstChild?.textContent?.trim() ?? "");
// A step after which focus was lost to the page: recorded, and every step's result is asserted at the end,
// so one run names each of them. Focus may be on the body for a few ms before the shell moves it, so a step
// is lost only when focus never arrives within 2 s or is lost again after it did (ginsys/bronzeward#911).
const watch = () => {
  const lost: string[] = [];
  return { lost, after: async (step: string) => void (await focusSettles(focusKept, step).catch(() => lost.push(step))) };
};
// Whether a status or alert shows `text`: announced without moving focus (4.1.3).
const announced = (text: string) => browser.execute((t: string) => [...document.querySelectorAll('[role="alert"], [role="status"] > p')].some((e) => e.textContent?.includes(t)), text);
const statusRegions = () => browser.execute(() => document.querySelectorAll('[role="status"]').length);
const CLAIM = 'section[aria-label="Claim"]';
const REVIEW = 'section[aria-label="Protected review"]';
// The editor pane: Save is disabled while it runs, and a browser that keeps focus on a disabled control leaves it on Save.
const PANE = 'section[aria-label^="Fragment"]';

describe("the import wizard, the protected review and the conflict view, by keyboard and at 320 CSS pixels (ginsys/bronzeward#894)", () => {
  before(async () => {
    at = await disposable("start=normal");
    machine = await syntheticMachine(at);
    cluster = (await api(at, "h-author", "GET", `/machines/${machine}`)).body!["cluster"] as string;
  });
  afterEach(leavesNothing);

  it("controls: the storage check and the HTTP cache check each find what is planted", async function () {
    this.timeout(60_000);
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    await storageControl();
    await cacheControl(at);
  });

  it("wizard, keyboard only (T3): every control is reached and operated, a start with no document is refused in text, and the import is released", async function () {
    this.timeout(180_000);
    const d = await draft();
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    const w = watch();
    await fromTitle();
    await tabTo((f) => f.text === "Changes", "the Changes link");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText("Changes");
    await focusSettles(onMain, "the new page");
    await tabTo((f) => f.tag === "A" && f.text === "Imports", "the Imports link");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText("Imports");
    await focusSettles(onMain, "the new page");
    await tabTo((f) => f.tag === "BUTTON" && f.text === "Start import", "Start import");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText("Start import");
    await focusSettles(onMain, "the new page");
    // The machine, then the draft the machine's cluster offers: a select takes the arrow keys.
    await $(`select[name="machine"] option[value="${machine}"]`).waitForExist({ timeout: 20_000 });
    await tabTo((f) => f.tag === "SELECT" && f.name === "machine", "the machine select");
    await pickByKeys(machine);
    await $(`select[name="draft"] option[value="${d.id}"]`).waitForExist({ timeout: 20_000 });
    await tabTo((f) => f.tag === "SELECT" && f.name === "draft", "the draft select");
    await pickByKeys(d.id);
    expect(await $('select[name="machine"]').getValue()).toBe(machine);
    expect(await $('select[name="draft"]').getValue()).toBe(d.id);
    // The source radios: Tab enters the group, an arrow key picks the document.
    await tabTo((f) => f.tag === "INPUT" && f.name === "source", "the source radios");
    await browser.keys("ArrowDown");
    expect(await $('input[name="source"][value="document"]').isSelected()).toBe(true);
    await $("main form").$("label*=Document").$("textarea").waitForExist({ timeout: 10_000 });
    await w.after("the source chosen");
    // On to Start import with no document: refused in text, announced, and focus stays on the button.
    await tabTo((f) => f.tag === "BUTTON" && f.text === "Start import", "Start import");
    await browser.keys("Enter");
    await expect($('[data-testid="start-note"]')).toHaveText("Not sent: paste the document or choose its file.");
    await eventually(() => announced("Not sent: paste the document or choose its file."), "the refusal announced");
    await w.after("a start refused");
    // Back to the document field, which a typed document fills; the file field is reached and left on the way.
    for (let i = 0; i < 8 && !(await focusedLabel()).startsWith("Document"); i++) await tab(true);
    expect(await focusedLabel()).toMatch(/^Document/);
    await browser.keys(TYPED.split(""));
    // The file field is the browser's own chooser: Tab reaches it and leaves it, and the case does not open it.
    const passed = await tabTo((f) => f.tag === "INPUT" && f.name === "staging", "the staging radios");
    expect(passed.length).toBeGreaterThanOrEqual(1);
    await browser.keys("ArrowDown");
    expect(await $('input[name="staging"][value="transient"]').isSelected()).toBe(true);
    // A review needs encrypted staging, so the checkbox is disabled and Tab passes it.
    // The staging choice disables it in the same render (web/src/imports.tsx:702), so the read after the key press cannot pass early.
    expect(await $('input[name="review"]').isEnabled()).toBe(false);
    // The filled form is on show: its contrast and the size of its selects and buttons (1.4.3, 2.5.8).
    const form = await measure();
    expect(form.contrast).toBeGreaterThanOrEqual(4.5);
    expect(form.small).toBe(0);
    await tabTo((f) => f.tag === "TEXTAREA", "the marks field");
    await tabTo((f) => f.tag === "BUTTON" && f.text === "Start import", "Start import");
    const sentAt = Date.now();
    await browser.keys("Enter");
    await browser.waitUntil(async () => /^\/ingestions\/ing_/.test(await browser.execute(() => location.pathname)), { timeout: 20_000, timeoutMsg: "the wizard did not open the import page" });
    await focusSettles(onMain, "the new page");
    await expect(ended()).toHaveText(holds("released into draft"), { wait: 60_000 });
    expect(Date.now() - sentAt).toBeLessThan(90_000);
    await expect(allowed()).toHaveText(holds("Released into draft"), { wait: 20_000 });
    await eventually(() => announced("Released into draft"), "the release announced");
    expect(await statusRegions()).toBe(1);
    const seen = await measure();
    expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
    expect(seen.small).toBe(0);
    expect(w.lost).toEqual([]);
  });

  itNarrow("wizard, 320 CSS pixels: the form with a long draft title and the import page do not scroll the page sideways", async function () {
    this.timeout(180_000);
    const wide = `w${"x".repeat(119)}`;
    const d = await draft(`synthetic ${wide}`);
    const released = await startImport("transient", false, "released");
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    await narrow(async () => {
      await $('nav[aria-label="Main"]').$("a=Changes").click();
      await $("main").$("a=Imports").click();
      await expect($("main h2")).toHaveText("Imports");
      expect(["imports", await overflows()]).toEqual(["imports", 0]);
      await $("main").$("button=Start import").click();
      await expect($("main h2")).toHaveText("Start import");
      await $(`select[name="machine"] option[value="${machine}"]`).waitForExist({ timeout: 20_000 });
      await $('select[name="machine"]').selectByAttribute("value", machine);
      await $(`select[name="draft"] option[value="${d.id}"]`).waitForExist({ timeout: 20_000 });
      await $('select[name="draft"]').selectByAttribute("value", d.id);
      await $('input[name="source"][value="document"]').click();
      await $("main form").$("label*=Document").$("textarea").waitForExist({ timeout: 10_000 });
      expect(["the wizard", await overflows()]).toEqual(["the wizard", 0]);
      await browser.url(`${FIXTURE_PAGE}/ingestions/${released}`);
      await expect($("main h2")).toHaveText(`Import ${short(released)}: released`, { wait: 30_000 });
      expect(["the import page", await overflows()]).toEqual(["the import page", 0]);
    });
  });

  describe("the protected review", () => {
    afterEach(async () => {
      // A case that did not finish leaves its claim; abandoning it through the API keeps the run's claims few.
      if (importId !== "") await api(at, "h-author", "POST", `/ingestions/${importId}/abandonments`, {});
    });

    it("keyboard only (T3): the review is opened, a value marked, the marks sent and the import continued; nothing is kept", async function () {
      this.timeout(240_000);
      const id = await startImport("encrypted", true, "paused");
      await openImport(id, "paused");
      await expect(allowed()).toHaveText("Paused for review. Continue it, or abandon it.", { wait: 20_000 });
      await fromTitle();
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Review import", "Review import");
      await browser.keys("Enter");
      await $('[data-testid="review-text"]').waitForExist({ timeout: 20_000 });
      // The button that held focus is replaced by the review: focus is on its text.
      await focusSettles(() => focusIn('[data-testid="review-text"]'), "the review's text");
      expect(await reviewHolds()).toBe(true);
      await eventually(() => announced("PROTECTED REVIEW"), "the review announced");
      const seen = await measure();
      expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
      expect(seen.small).toBe(0);
      // The values to choose from are buttons in a list: Tab reaches the one for the tier label, Enter marks it.
      for (let i = 0; i < 8 && (await focusedPath()) !== TIER; i++) await tabTo((f) => f.tag === "BUTTON" && f.text === "Mark as secret", "the Mark as secret button of the tier label");
      expect(await focusedPath()).toBe(TIER);
      await browser.keys("Enter");
      expect(await browser.execute(() => (document.querySelector<HTMLTextAreaElement>('textarea[data-testid="marks"]')?.value ?? "").length > 0)).toBe(true);
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Send marks", "Send marks");
      await browser.keys("Enter");
      await expect(ended()).toHaveText(holds("The marks were applied: re-staged and paused again"), { wait: 60_000 });
      await absent(() => browser.execute(() => document.querySelector('[data-testid="review-text"]') !== null), "the review text after the marks were applied");
      await focusSettles(() => focusIn(CLAIM), "the claim section");
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Continue import", "Continue import");
      await browser.keys("Enter");
      await expect(ended()).toHaveText(holds(`Import ${short(id)} released into draft`), { wait: 60_000 });
      await focusSettles(() => focusIn(CLAIM), "the claim section");
    });

    it("keyboard only (T3): a closed review returns focus to its section, and a paused import is abandoned", async function () {
      this.timeout(180_000);
      const id = await startImport("encrypted", true, "paused");
      await openImport(id, "paused");
      await fromTitle();
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Review import", "Review import");
      await browser.keys("Enter");
      await $('[data-testid="review-text"]').waitForExist({ timeout: 20_000 });
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Close review", "Close review");
      await browser.keys("Enter");
      await expect($('[data-testid="review-text"]')).not.toBeExisting();
      await focusSettles(() => focusIn(REVIEW), "the review's section");
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Abandon import", "Abandon import");
      await browser.keys("Enter");
      await expect($(CLAIM)).toHaveText(holds("The import was abandoned. Its staged input is discarded."), { wait: 30_000 });
      // The case ended the import: the cleanup after it posts no abandon for it (it would answer 409).
      importId = "";
      await focusSettles(() => focusIn(CLAIM), "the claim section");
    });

    itNarrow("320 CSS pixels: the paused import, its open review and the marks panel do not scroll the page sideways", async function () {
      this.timeout(180_000);
      const id = await startImport("encrypted", true, "paused");
      await openImport(id, "paused");
      await narrow(async () => {
        expect(["the paused import", await overflows()]).toEqual(["the paused import", 0]);
        await $('section[aria-label="Protected review"]').$("button=Review import").click();
        await $('[data-testid="review-text"]').waitForExist({ timeout: 20_000 });
        expect(await reviewHolds()).toBe(true);
        expect(["the open review", await overflows()]).toEqual(["the open review", 0]);
        const button = $(`//ul[@data-testid="candidates"]//li[code[text()="${TIER}"]]//button`);
        await button.waitForExist({ timeout: 20_000 });
        await button.click();
        expect(["the marks", await overflows()]).toEqual(["the marks", 0]);
      });
    });
  });

  describe("the conflict view", () => {
    // A draft with one fragment, opened in the editor with that entry selected and an unsaved edit, after another
    // author's write moved the draft: the editor is ready for a Save that gets the 412.
    async function toStaleSave() {
      const name = `kbd-${hex()}`;
      const d = await draft();
      const f = await api(at, "h-author", "PUT", `/drafts/${d.id}/fragments/${name}`, { layer: "role", document: syntheticDocument(name), marks: null, declarations: null }, d.etag);
      expect(f.status).toBe(200);
      expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
      await $('nav[aria-label="Main"]').$("a=Changes").click();
      await $(`main a[title="${d.id}"]`).click();
      await expect($("main h2")).toHaveText(holds("synthetic"));
      return { id: d.id, name, etag: f.etag };
    }
    const pane = () => $('main section[aria-label^="Fragment"]');
    const loaded = () => browser.waitUntil(() => browser.execute(() => {
      const area = [...document.querySelectorAll("main section label")].find((l) => l.firstChild?.textContent?.startsWith("Document"))?.querySelector("textarea");
      return (area?.value.length ?? 0) > 0;
    }), { timeout: 20_000, timeoutMsg: "the editor did not load the entry's revision" });
    // An edit with no value in it, made in the page so the document stays there: a node label next to the tier label.
    const edit = async () => {
      const ok = await browser.execute(() => {
        const area = [...document.querySelectorAll("main section label")].find((x) => x.firstChild?.textContent?.startsWith("Document"))?.querySelector("textarea");
        const m = area == null ? null : /^( *)example\.test\/tier:.*$/m.exec(area.value);
        if (area == null || m == null) return false;
        const end = m.index + m[0].length;
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, `${area.value.slice(0, end)}\n${m[1]}example.test/mine: synthetic${area.value.slice(end)}`);
        area.dispatchEvent(new Event("input", { bubbles: true }));
        return true;
      });
      expect(ok).toBe(true);
    };
    // Another author's write moves the draft, so the page's ETag is stale.
    async function moveDraft(id: string, etag: string): Promise<number> {
      const other = await api(at, "h-all", "PUT", `/drafts/${id}/fragments/other-${hex()}`, { layer: "role", document: syntheticDocument("other"), marks: null, declarations: null }, etag);
      expect(other.status).toBe(200);
      return (await api(at, "h-author", "GET", `/drafts/${id}`)).body!["revision"] as number;
    }

    it("keyboard only (T5): a refused save, Show their change, Copy my text into the new revision and Save are operated without the pointer", async function () {
      this.timeout(180_000);
      const { id, name, etag } = await toStaleSave();
      const w = watch();
      await fromTitle();
      await tabTo((f) => f.tag === "BUTTON" && f.text === `fragment ${name}`, "the fragment entry");
      await browser.keys("Enter");
      await loaded();
      await w.after("the entry selected");
      await edit();
      await w.after("the text edited");
      const now = await moveDraft(id, etag);
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Save", "Save");
      await browser.keys("Enter");
      await expect(pane().$('[data-testid="status"]')).toHaveText(holds("Someone changed this draft"), { wait: 20_000 });
      await eventually(() => announced("Someone changed this draft"), "the refusal announced");
      // Save was disabled while it ran: focus is in the pane, on the refusal or on Save, and Show their change is next.
      await focusSettles(() => focusIn(PANE), "the editor pane");
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Show their change", "Show their change");
      await browser.keys("Enter");
      await expect($("main h2")).toHaveText(holds(`revision ${now}`), { wait: 20_000 });
      await expect($("label*=Their text")).toBeExisting();
      await w.after("their change shown");
      const seen = await measure();
      expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
      expect(seen.small).toBe(0);
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Copy my text into the new revision", "Copy my text into the new revision");
      await browser.keys("Enter");
      // Copy my text is disabled once it ran: focus is on the text now in the editor.
      await focusSettles(async () => (await focusedLabel()).startsWith("Document"), "the Document text");
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Save", "Save");
      await browser.keys("Enter");
      await expect(pane().$('[data-testid="write-result"]')).toHaveText(holds(`Fragment ${name} saved in draft`), { wait: 30_000 });
      await focusSettles(() => focusIn(PANE), "the editor pane");
      expect(w.lost).toEqual([]);
    });

    itNarrow("320 CSS pixels: the conflict view with their text and mine does not scroll the page sideways", async function () {
      this.timeout(180_000);
      const { id, name, etag } = await toStaleSave();
      await $('main ul[aria-label="Entries"]').$(`button=fragment ${name}`).click();
      await loaded();
      await edit();
      const now = await moveDraft(id, etag);
      await pane().$("button=Save").click();
      await expect(pane().$('[data-testid="status"]')).toHaveText(holds("Someone changed this draft"), { wait: 20_000 });
      await pane().$("button=Show their change").click();
      await expect($("main h2")).toHaveText(holds(`revision ${now}`), { wait: 20_000 });
      await expect($("label*=Their text")).toBeExisting();
      await narrow(async () => {
        expect(["the conflict view", await overflows()]).toEqual(["the conflict view", 0]);
        // Each comparison label holds its own read-only textarea; the page answers a boolean, never a text.
        const both = await browser.execute(() => ["Their text", "Your text"].every((t) => [...document.querySelectorAll("main label")].some((l) => l.firstChild?.textContent?.trim() === t && l.querySelector("textarea") !== null)));
        expect(both).toBe(true);
      });
    });
  });
});
