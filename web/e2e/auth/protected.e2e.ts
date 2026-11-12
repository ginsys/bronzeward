// The protected import review (ginsys/bronzeward#341, client contracts §11 and the cases PROT-01,
// PROT-03, PROT-04, PROT-05 and PROT-06 cases 1 to 4) against the disposable deployment, whose
// provider is mise run dev-bao's OpenBao behind the proxy of bao.ts. PROT-06 case 4 stops the
// deployment's interrupt build at its draft commit (ginsys/bronzeward#366).
// Nothing here reads, prints or keeps a review: the page holds the review text in its memory, and a
// case asks the page only whether it holds one. A document never crosses WebDriver, whose log the
// artifact scan reads: the page fetches it from the synthetic provider (putDocument). The marks are
// paths without the canary. No case takes a screenshot or a trace (retention.test.ts checks this
// source). Opening a review asks the page, as a boolean, whether its review text holds the document's
// tier label, which carries the canary: the later checks then find absence of something that was there.
// After each case the storage check finds nothing in storage, IndexedDB or caches, and the HTTP cache
// check finds no answer of that case's review. The cases sign in as h-author.
import { randomBytes } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { CONTROL, FIXTURE_ISSUER } from "../auth.conf.ts";
import { cacheControl, cacheHolds, faults, fixtureSignIn, interrupt, mint, storageControl, storageEmpty } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
// The one value of the synthetic document a case marks by choosing it from the review's list.
const TIER = "doc[0]/machine/nodeLabels/example.test~1tier";
let at = "";
let cluster = "";
let machine = "";
// The case's import and the name of the document it staged, set by pausedImport and cleared after the case.
let importId = "";
let docName = "";

const hex = () => randomBytes(4).toString("hex");
const post = async (path: string) => expect((await fetch(`${CONTROL}${path}`, { method: "POST" })).ok).toBe(true);

// A request through the API as `human`, straight to the deployment (as imports.e2e.ts does).
async function api(human: string, method: string, path: string, body?: unknown) {
  const auth = { Authorization: `Bearer ${mint(human)}` };
  const server = await fetch(`${at}/api/v1/server`, { headers: auth });
  const epoch = server.headers.get("Bronzeward-Epoch") ?? "";
  const r = await fetch(`${at}/api/v1${path}`, {
    method,
    headers: { ...auth, "Content-Type": "application/json", "Idempotency-Key": randomBytes(16).toString("base64url"), "Bronzeward-Expected-Epoch": epoch },
    body: body === undefined ? null : JSON.stringify(body),
  });
  return { status: r.status, body: (await r.json().catch(() => undefined)) as Record<string, unknown> | undefined };
}

// A fresh deployment on a database of its own, with a cluster and a machine for the cases.
async function deployment(query: string) {
  const res = await fetch(`${CONTROL}/disposable?${query}`, { method: "POST" });
  expect(res.status).toBe(200);
  at = await res.text();
  const name = `synthetic-${hex()}`;
  const c = await api("h-author", "POST", "/clusters", { name, endpoint: `https://${name}.example.test:6443`, contract: "v1.14", talosClusterId: `${randomBytes(32).toString("base64url")}=` });
  expect(c.status).toBe(201);
  cluster = c.body!["id"] as string;
  const m = await api("h-author", "POST", "/machines", { cluster, talosNodeId: `synthetic-node-${hex()}`, platform: "metal", talosEndpoint: `192.0.2.${1 + (randomBytes(1)[0]! % 254)}` });
  expect(m.status).toBe(201);
  machine = m.body!["id"] as string;
}

// The same deployment again after it was killed, on the database it had.
const restart = async () => {
  const res = await fetch(`${CONTROL}/disposable?start=normal&keep`, { method: "POST" });
  expect(res.status).toBe(200);
};

const ended = () => $('[data-testid="ended"]');
const allowed = () => $('[data-testid="allowed"]');
const events = () => $('[data-testid="events"]');
const holds = (text: string) => expect.stringContaining(text);

// Waits until the Claim section shows `text`, answered in the page as a boolean. The section holds the open
// review, so reading its text through WebDriver would carry the review text, and the canary in it, into
// the retained driver log while the review is open. The page leaves the review's field out of what it
// compares, and only true or false crosses.
async function claimShows(text: string, timeout: number) {
  await browser.waitUntil(
    () =>
      browser.execute((t: string) => {
        const section = document.querySelector('section[aria-label="Claim"]')?.cloneNode(true) as HTMLElement | undefined;
        if (section === undefined) return false;
        section.querySelectorAll('[data-testid="review-text"]').forEach((f) => f.remove());
        return (section.textContent ?? "").includes(t);
      }, text),
    { timeout, interval: 250, timeoutMsg: `the Claim section did not show: ${text}` },
  );
}

// The wizard from the Changes page with the case's machine and a new draft chosen and the document
// source selected, then the import started: encrypted staging with review is the default (B15, G5).
async function pausedImport(): Promise<string> {
  const d = await api("h-author", "POST", "/drafts", { cluster, title: `synthetic ${hex()}` });
  expect(d.status).toBe(201);
  const draft = d.body!["id"] as string;
  docName = `protected-${hex()}`;
  expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
  await $('nav[aria-label="Main"]').$("a=Changes").click();
  await $("main").$("a=Imports").click();
  await $("main").$("button=Start import").click();
  await expect($("main h2")).toHaveText("Start import");
  await $(`select[name="machine"] option[value="${machine}"]`).waitForExist({ timeout: 20_000 });
  await $('select[name="machine"]').selectByAttribute("value", machine);
  await $(`select[name="draft"] option[value="${draft}"]`).waitForExist({ timeout: 20_000 });
  await $('select[name="draft"]').selectByAttribute("value", draft);
  await $('input[name="source"][value="document"]').click();
  // The document is fetched by the page and put in the field as typing would.
  const ok = await browser.execute(async (issuer: string, n: string) => {
    const area = [...document.querySelectorAll("main label")].find((l) => l.firstChild?.textContent?.startsWith("Document"))?.querySelector("textarea");
    const r = await fetch(`${issuer}/synthetic-document?name=${n}`).catch(() => undefined);
    if (r?.ok !== true || area == null) return false;
    const text = await r.text();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, text);
    area.dispatchEvent(new Event("input", { bubbles: true }));
    return area.value === text;
  }, FIXTURE_ISSUER, docName);
  expect(ok).toBe(true);
  expect(await $('input[name="staging"][value="encrypted"]').isSelected()).toBe(true);
  expect(await $('input[name="review"]').isSelected()).toBe(true);
  await $("main form").$("button=Start import").click();
  await browser.waitUntil(async () => /^\/ingestions\/ing_/.test(await browser.execute(() => location.pathname)), { timeout: 20_000, timeoutMsg: "the wizard did not open the import page" });
  const id = (await browser.execute(() => location.pathname)).split("/").pop()!;
  importId = id;
  await expect(events()).toHaveText(holds("paused (generation 1)"), { wait: 60_000 });
  await expect(allowed()).toHaveText("Paused for review. Continue it, or abandon it.", { wait: 20_000 });
  return id;
}

// Opens the review. The page is asked only a boolean, never for the text: whether the review text holds the
// tier label of the document the case staged. The page reads that label from the synthetic provider, as it
// did when it put the document in the form, so neither the label nor the canary in it crosses WebDriver.
// The label is no secret, so the review shows it. This is what the later storage and cache checks, and the
// artifact scan, are measured against: they find absence of something that was on the page.
async function openReview() {
  await $('section[aria-label="Protected review"]').$("button=Review import").click();
  await $('[data-testid="review-text"]').waitForExist({ timeout: 20_000 });
  const held = await browser.execute(async (issuer: string, n: string) => {
    const text = document.querySelector<HTMLTextAreaElement>('[data-testid="review-text"]')?.value ?? "";
    const r = await fetch(`${issuer}/synthetic-document?name=${n}`).catch(() => undefined);
    const label = r?.ok === true ? /example\.test\/tier: (\S+)/.exec(await r.text())?.[1] : undefined;
    return label !== undefined && label.length > 0 && text.includes(label);
  }, FIXTURE_ISSUER, docName);
  expect(held).toBe(true);
}

// What every protected journey leaves behind: nothing in storage, IndexedDB or caches, and no answer of
// the case's review in the HTTP cache. The cache probe of cacheControl is its positive control.
async function leavesNothing() {
  await storageEmpty();
  if (importId !== "") expect(await cacheHolds(`/api/v1/ingestions/${importId}/review`)).toBe(false);
  importId = "";
}

// Puts paths in the marks field as typing would. They are paths of the synthetic document and hold
// no canary.
async function typeMarks(paths: string[]) {
  const ok = await browser.execute((text: string) => {
    const area = document.querySelector<HTMLTextAreaElement>('textarea[data-testid="marks"]');
    if (area === null) return false;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, text);
    area.dispatchEvent(new Event("input", { bubbles: true }));
    return area.value === text;
  }, paths.join("\n"));
  expect(ok).toBe(true);
  // The positive control of the later absence checks: while the paths sit in the field, pageShows sees them.
  for (const p of paths) expect(await pageShows(p)).toBe(true);
}

// Chooses a value from the review's list, which puts its path in the marks field.
async function choose(path: string) {
  const button = $(`//ul[@data-testid="candidates"]//li[code[text()="${path}"]]//button`);
  await button.waitForExist({ timeout: 20_000 });
  await button.click();
}

const sendMarks = () => $("section[aria-label=Marks]").$("button=Send marks").click();

// Whether the page shows `text` anywhere, in its text or in the value of a field, answered in the page so
// that the text never leaves it. A field's typed value is not part of the body's text.
const pageShows = (text: string) =>
  browser.execute(
    (t: string) => document.body.innerText.includes(t) || [...document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("textarea, input")].some((f) => f.value.includes(t)),
    text,
  );

// The read of the import after the serving process was killed and started again: the watch ends once
// the lease passed, with the outcome unknown and never as not applied (PROT-06).
// `takeover` is the generation of a takeover that came before: the page then knows that it discarded the
// earlier mark, and says so with exactly that sentence, once. No other "not applied" text may show, so the
// run of this step stays unknown. With no takeover before it, the page has no "not applied" text at all.
async function unknownOutcome(what: string, takeover?: number) {
  await expect(ended()).toHaveText(holds("the outcome is unknown"), { wait: 90_000 });
  // The page's hidden status region mirrors each status message, so its copy is not counted (ginsys/bronzeward#830).
  const notApplied = await browser.execute(() => {
    const count = (text: string) => (text.match(/not applied/g) ?? []).length;
    return count(document.body.innerText) - count(document.querySelector('[data-testid="page-status"]')?.textContent ?? "");
  });
  if (takeover === undefined) {
    expect(notApplied).toBe(0);
  } else {
    expect(await pageShows(`The earlier mark was not applied: a takeover at generation ${takeover} resumed the import without it.`)).toBe(true);
    expect(notApplied).toBe(1);
  }
  await expect(allowed()).toHaveText("Its run stopped renewing the lease. Take it over to resume, or abandon it.", { wait: 20_000 });
  // `what` names the step in the failing line of the log.
  if (!(await $("button=Abandon import").isExisting())) throw new Error(`${what}: abandon is not offered`);
}

describe("the protected import review (ginsys/bronzeward#341)", () => {
  before(() => deployment("start=normal"));
  afterEach(async () => {
    // A fault or a hold a case left must not reach the next case.
    await post("/bao/fault?mode=none").catch(() => {});
    await post("/bao/decrypt-hold?ms=0").catch(() => {});
    await interrupt().catch(() => {});
    await leavesNothing();
  });

  it("controls: the storage check and the HTTP cache check each find what is planted", async function () {
    this.timeout(60_000);
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    await storageControl();
    await cacheControl(at);
  });

  it("PROT-01: a protected import is paused, reviewed, marked and continued; the review leaves no storage and no cached answer", async function () {
    this.timeout(180_000);
    const id = await pausedImport();
    await openReview();
    await expect($('[data-testid="review-notice"]')).toHaveText(holds("PROTECTED REVIEW"));
    await choose(TIER);
    await sendMarks();
    await claimShows("The marks were accepted in operation", 30_000);
    await expect(ended()).toHaveText(holds("The marks were applied: re-staged and paused again"), { wait: 60_000 });
    // The marked value's path is not shown anywhere once it was sent.
    expect(await pageShows(TIER)).toBe(false);
    // The review ends with the paused state it was read in; the page holds no text of it.
    expect(await browser.execute(() => document.querySelector('[data-testid="review-text"]') !== null)).toBe(false);
    await $("button=Continue import").click();
    await expect(ended()).toHaveText(holds(`Import ${short(id)} released into draft`), { wait: 60_000 });
    // The storage and cache checks of this journey run in afterEach, with the probe of the controls case as
    // their positive control. The probe is run again here, in the page the journey ended in.
    await cacheControl(at);
  });

  it("PROT-03, a mark that is not a path: refused with 422 at its position, no path shown, the review stays open", async function () {
    this.timeout(120_000);
    await pausedImport();
    await openReview();
    await typeMarks(["not a path"]);
    await sendMarks();
    await claimShows("The mark at position 0 (the first is 0) is refused.", 30_000);
    expect(await pageShows("not a path")).toBe(false);
    await expect(allowed()).toHaveText("Paused for review. Continue it, or abandon it.");
    await abandon();
  });

  it("PROT-03, a mark that compilation refuses: accepted, then refused at its position, and the import is paused again", async function () {
    this.timeout(120_000);
    await pausedImport();
    await openReview();
    // A mapping that holds a mapping has no kind: compilation refuses it after the 202 (mark-kind).
    await typeMarks(["doc[0]/machine"]);
    await sendMarks();
    await claimShows("The marks were accepted in operation", 30_000);
    await expect(ended()).toHaveText(holds("The mark was not applied: it was refused ("), { wait: 60_000 });
    await expect(ended()).toHaveText(holds("the mark at position 0 (the first is 0)"));
    await expect(ended()).toHaveText(holds("The import is paused again"));
    expect(await pageShows("doc[0]/machine")).toBe(false);
    await abandon();
  });

  it("PROT-03, a compilation that fails on a dependency: refused with 503, shown by its code as not applied", async function () {
    this.timeout(120_000);
    await pausedImport();
    await openReview();
    await choose(TIER);
    await post("/bao/fault?mode=kv");
    await sendMarks();
    await claimShows("The marks were accepted in operation", 30_000);
    await expect(ended()).toHaveText(holds("The mark was not applied: it was refused (dependency-unavailable"), { wait: 60_000 });
    await expect(ended()).toHaveText(holds("The import is paused again"));
    expect(await pageShows(TIER)).toBe(false);
    await post("/bao/fault?mode=none");
    await abandon();
  });

  it("PROT-04: the review read fails on the way; nothing is kept, and the review is read again on request", async function () {
    this.timeout(120_000);
    const id = await pausedImport();
    // A redirect the page refuses is a transport failure as the page sees it (client contracts §6 item 7).
    await faults({ upstream: at, redirect: `/ingestions/${id}/review` });
    await $('section[aria-label="Protected review"]').$("button=Review import").click();
    // The failed read puts the shell offline: its banner shows and actions are disabled until a read answers.
    await $('[data-testid="offline"]').waitForExist({ timeout: 20_000, timeoutMsg: "the failed read did not end in the offline state" });
    expect(await browser.execute(() => document.querySelector('[data-testid="review-text"]') !== null)).toBe(false);
    await storageEmpty();
    expect(await cacheHolds(`/api/v1/ingestions/${id}/review`)).toBe(false);
    await faults({ upstream: at });
    // A read that answers ends the offline state; the review is then read again on request.
    await browser.refresh();
    await expect(allowed()).toHaveText("Paused for review. Continue it, or abandon it.", { wait: 30_000 });
    expect(await $('[data-testid="offline"]').isExisting()).toBe(false);
    await openReview();
    await abandon();
  });

  it("PROT-05: the staged envelope cannot be decrypted after the mark is accepted; the mark is not applied, takeover is offered once the lease lapses, and abandon at once", async function () {
    this.timeout(180_000);
    await pausedImport();
    await openReview();
    await choose(TIER);
    await post("/bao/fault?mode=decrypt");
    await sendMarks();
    await claimShows("The marks were accepted in operation", 30_000);
    await expect(events()).toHaveText(holds("resume-failed: dependency-unavailable"), { wait: 60_000 });
    // The mark is reported as not applied and the claim stays held (client contracts, row PROT-05).
    await expect(ended()).toHaveText(holds("was not applied: its staged input could not be decrypted (resume-failed)"), { wait: 20_000 });
    await expect(ended()).toHaveText(holds("The claim stays held until its lease lapses at"));
    expect(await $("button=Abandon import").isExisting()).toBe(true);
    await post("/bao/fault?mode=none");
    // No reload: the page itself reads the import again after the lease lapsed, and then offers the takeover.
    // The lease is 15 s and the page reads again 10 s after it (LEASE_MARGIN), so 60 s is a margin of more than
    // twice the longest wait. A page that never reads again keeps its text and fails here.
    await expect(allowed()).toHaveText("Its run stopped renewing the lease. Take it over to resume, or abandon it.", { wait: 60_000 });
    expect(await $("button=Take over import").isExisting()).toBe(true);
    await abandon();
  });

  it("PROT-06, cases 1 and 3: the serving process is killed after a mark is accepted, then after a takeover is accepted; each outcome is unknown, never not applied", async function () {
    this.timeout(300_000);
    await pausedImport();
    await openReview();
    await choose(TIER);
    // The run waits for the staged envelope's decrypt, so the process is killed before it writes an event.
    await post("/bao/decrypt-hold?ms=120000");
    await sendMarks();
    await claimShows("The marks were accepted in operation", 30_000);
    // The generation the mark's run took the claim at, read in the page: only the number crosses WebDriver.
    const read = await browser.execute(() => /The marks were accepted in operation \S+ at generation (\d+)\./.exec(document.querySelector('section[aria-label="Claim"]')?.textContent ?? "")?.[1] ?? null);
    const markGeneration = Number(read);
    if (!Number.isInteger(markGeneration) || markGeneration < 2) throw new Error(`the accepted mark's generation read as ${String(read)}, not an integer of at least 2`);
    await post("/disposable/kill");
    await post("/bao/decrypt-hold?ms=0");
    await restart();
    await unknownOutcome("after the mark");
    // The takeover, killed the same way.
    await post("/bao/decrypt-hold?ms=120000");
    await $("button=Take over import").click();
    await claimShows("The import was taken over and resumes in operation", 30_000);
    await post("/disposable/kill");
    await post("/bao/decrypt-hold?ms=0");
    await restart();
    // The mark's run took the claim at markGeneration, so the one takeover after it is the next generation.
    await unknownOutcome("after the takeover", markGeneration + 1);
    await abandon();
  });

  it("PROT-06, case 2: the serving process is killed after a continuation is accepted; the outcome is unknown, never not applied", async function () {
    this.timeout(200_000);
    await pausedImport();
    await post("/bao/decrypt-hold?ms=120000");
    await $("button=Continue import").click();
    await claimShows("The import continues in operation", 30_000);
    await post("/disposable/kill");
    await post("/bao/decrypt-hold?ms=0");
    await restart();
    await unknownOutcome("after the continuation");
    await abandon();
  });

  it("PROT-06, case 4: the run releases its claim and its draft commit stalls, so the lease lapses; the read answers at once, the outcome is unknown, and the import is released once the commit goes", async function () {
    this.timeout(240_000);
    const id = await pausedImport();
    // The interrupt build stalls inside the draft transaction, after the claim's release (ginsys/bronzeward#366).
    await interrupt("draft-commit stall");
    await $("button=Continue import").click();
    const began = Date.now();
    await claimShows("The import continues in operation", 30_000);
    await unknownOutcome("during the stall");
    // The read of the operation answers while the commit stalls (ginsys/bronzeward#803). The lease lapses about
    // 17 s in, and the page reads again at LEASE_MARGIN, 10 s later (web/src/imports.tsx:30). The watch then ends
    // within one 15 s keep-alive beat of that read (client contracts §11.3, internal/api/operations.go:215), so
    // 42 s, and 45 s with the time of the reads. A read that blocks again waits for the page's 30 s bound on
    // response headers (AMB-14) inside the check at the 30 s beat, so it stops at about 60 s at the earliest.
    // 45 s separates the two. The CLI decides in one call and stops near 27 s (cmd/bronzewardctl/imports.go:455-482).
    const stopped = Date.now() - began;
    console.log(`PROT-06 case 4: the watch stopped ${stopped} ms after the click`);
    expect(stopped).toBeLessThan(45_000);
    expect(await $('[data-testid="offline"]').isExisting()).toBe(false);
    // The commit goes: the claim is released and the import is in the draft. The page's watch ended on the
    // unknown outcome, so the state is read by reload.
    await interrupt();
    await browser.waitUntil(async () => {
      await browser.refresh();
      const heading = await $("main h2").getText();
      return heading === `Import ${short(id)}: released`;
    }, { timeout: 60_000, interval: 3_000, timeoutMsg: "the import does not show its claim released after the commit went" });
    expect(await $("button=Abandon import").isExisting()).toBe(false);
  });
});

describe("an expired claim (ginsys/bronzeward#341)", () => {
  before(() => deployment("start=normal&expiry=short"));
  afterEach(leavesNothing);

  it("a paused import passes its absolute expiry: the page ends in the state the server left, offering no review and no continuation", async function () {
    this.timeout(240_000);
    const id = await pausedImport();
    // No reload: the claim expires at most 45 s after it started and the page reads again within 5 s of that
    // (EXPIRY_TICK), so 100 s is a margin of about twice the longest wait. The read shows either "Review expired"
    // (the sweep, which runs every 15 s, has not ended the import yet) or the state the sweep left. A page that
    // never reads again keeps "Review expires" and fails here.
    await browser.waitUntil(
      () =>
        browser.execute(() => {
          const claim = document.querySelector('section[aria-label="Claim"]')?.textContent ?? "";
          const heading = document.querySelector("main h2")?.textContent ?? "";
          return claim.includes("Review expired") || (heading.length > 0 && !heading.endsWith(": paused"));
        }),
      { timeout: 100_000, interval: 1_000, timeoutMsg: "the page did not read the import again after its absolute expiry" },
    );
    // The page can offer a continuation until it reads the state the sweep left, so the state is read by reload.
    await browser.waitUntil(async () => {
      await browser.refresh();
      return (await $("main h2").getText()).startsWith(`Import ${short(id)}: `) && !(await $("main h2").getText()).endsWith(": paused");
    }, { timeout: 150_000, interval: 5_000, timeoutMsg: "the import still shows paused long after its absolute expiry" });
    await expect(allowed()).toHaveText(holds("Ended: "));
    expect(await $("button=Review import").isExisting()).toBe(false);
    expect(await $("button=Continue import").isExisting()).toBe(false);
  });
});

// Abandons through the page, which also shows its outcome.
async function abandon() {
  await $("button=Abandon import").click();
  await claimShows("The import was abandoned. Its staged input is discarded.", 30_000);
}
