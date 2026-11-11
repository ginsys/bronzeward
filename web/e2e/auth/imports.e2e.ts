// The import pages (ginsys/bronzeward#340) against the disposable deployment, whose provider is
// mise run dev-bao's OpenBao (bao.ts): an import started from a pasted document and from a file,
// its progress followed to its end, ETAG-02 both ways, a paused import with review, and the import
// page after a reload. A document never crosses WebDriver, whose log the artifact scan reads: the
// page fetches it from the synthetic provider inside a script, and a file is made in the page from
// that answer. Each document carries the run's canary (syntheticDocument), so the scan finds one
// in any retained file; no case takes a screenshot or reads a field that holds a document. After
// each case the storage check finds nothing in storage, IndexedDB or caches. The cases sign in as
// h-author.
import { randomBytes } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { CONTROL, FIXTURE_ISSUER, syntheticDocument } from "../auth.conf.ts";
import { faults, fixtureSignIn, holding, mint, release, seen, storageEmpty } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
let at = "";
let cluster = "";
let machine = "";

const hex = () => randomBytes(4).toString("hex");
const writes = async (): Promise<string[][]> => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).map((w) => w.split(" "));

// A request through the API as `human`, straight to the deployment: its answer's status, ETag and
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

// An open draft in the case's cluster.
async function draft(): Promise<string> {
  const d = await api("h-author", "POST", "/drafts", { cluster, title: `synthetic ${hex()}` });
  expect(d.status).toBe(201);
  return d.body!["id"] as string;
}

// The wizard from the Changes page, with the case's machine and the draft `id` chosen and the
// document source selected.
async function wizard(id: string) {
  await $('nav[aria-label="Main"]').$("a=Changes").click();
  await $("main").$("a=Imports").click();
  await expect($("main h2")).toHaveText("Imports");
  await $("main").$("button=Start import").click();
  await expect($("main h2")).toHaveText("Start import");
  await $(`select[name="machine"] option[value="${machine}"]`).waitForExist({ timeout: 20_000 });
  await $('select[name="machine"]').selectByAttribute("value", machine);
  await $(`select[name="draft"] option[value="${id}"]`).waitForExist({ timeout: 20_000 });
  await $('select[name="draft"]').selectByAttribute("value", id);
  await $('input[name="source"][value="document"]').click();
}

// Puts the synthetic document for `name` in the wizard, fetched by the page: pasted, set as typing
// would, or as a file chosen in the file field. The page's CSP allows no eval, so each script finds
// its field itself; only whether the document field then holds text leaves the page.
async function putDocument(name: string, file: boolean) {
  const ok = await browser.execute(async (issuer: string, n: string, asFile: boolean) => {
    const area = () => [...document.querySelectorAll("main label")].find((l) => l.firstChild?.textContent?.startsWith("Document"))?.querySelector("textarea");
    const r = await fetch(`${issuer}/synthetic-document?name=${n}`).catch(() => undefined);
    if (r?.ok !== true || area() == null) return false;
    const text = await r.text();
    if (asFile) {
      const input = [...document.querySelectorAll("main label")].find((l) => l.firstChild?.textContent?.startsWith("Or read it from a file"))?.querySelector("input");
      if (input == null) return false;
      const files = new DataTransfer();
      files.items.add(new File([text], `${n}.yaml`, { type: "application/yaml" }));
      input.files = files.files;
      input.dispatchEvent(new Event("change", { bubbles: true }));
      for (let i = 0; i < 100 && area()!.value !== text; i++) await new Promise((go) => setTimeout(go, 100));
    } else {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area(), text);
      area()!.dispatchEvent(new Event("input", { bubbles: true }));
    }
    return area()!.value === text;
  }, FIXTURE_ISSUER, name, file);
  expect(ok).toBe(true);
}

const staging = (mode: "encrypted" | "transient") => $(`input[name="staging"][value="${mode}"]`).click();
const start = () => $("main form").$("button=Start import").click();
const ended = () => $('[data-testid="ended"]');
const allowed = () => $('[data-testid="allowed"]');
const events = () => $('[data-testid="events"]');
// The import page's ingestion, once the wizard went there.
async function ingestion(): Promise<string> {
  await browser.waitUntil(async () => /^\/ingestions\/ing_/.test(await browser.execute(() => location.pathname)), { timeout: 20_000, timeoutMsg: "the wizard did not open the import page" });
  return (await browser.execute(() => location.pathname)).split("/").pop()!;
}
const revision = async (id: string) => (await api("h-author", "GET", `/drafts/${id}`)).body!["revision"] as number;

describe("the import pages (#340)", () => {
  before(async () => {
    const res = await fetch(`${CONTROL}/disposable?start=normal`, { method: "POST" });
    expect(res.status).toBe(200);
    at = await res.text();
    const name = `synthetic-${hex()}`;
    const c = await api("h-author", "POST", "/clusters", { name, endpoint: `https://${name}.example.test:6443`, contract: "v1.14", talosClusterId: `${randomBytes(32).toString("base64url")}=` });
    expect(c.status).toBe(201);
    cluster = c.body!["id"] as string;
    const m = await api("h-author", "POST", "/machines", { cluster, talosNodeId: `synthetic-node-${hex()}`, platform: "metal", talosEndpoint: `192.0.2.${1 + randomBytes(1)[0]! % 254}` });
    expect(m.status).toBe(201);
    machine = m.body!["id"] as string;
  });

  it("ETAG-02, adopted: a pasted document imported transiently is followed to released; the draft's ETag at its revision is kept, and an import from a file sends it with no read between", async function () {
    this.timeout(120_000);
    const id = await draft();
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    await wizard(id);
    await putDocument(`paste-${hex()}`, false);
    await staging("transient");
    await start();
    const first = await ingestion();
    await expect(ended()).toHaveText(expect.stringMatching(new RegExp(`^Import ${short(first)} released into draft ${short(id)} \\(revision (\\d+)\\), import base ibr_\\S+\\.$`)), { wait: 60_000 });
    await expect(events()).toHaveText(expect.stringMatching(/^started\n(.*\n)*succeeded$/));
    await expect(allowed()).toHaveText(`Released into draft ${short(id)}. Nothing is left to do.`, { wait: 20_000 });
    expect(await revision(id)).toBe(Number(/\(revision (\d+)\)/.exec(await ended().getText())![1]));
    // The second import, from a file: its start reads no draft, so its If-Match is the ETag kept
    // after the first; the server accepting it shows that ETag is the draft's current one.
    await wizard(id);
    await putDocument(`file-${hex()}`, true);
    await staging("transient");
    const from = (await seen()).length;
    await start();
    const second = await ingestion();
    await expect(ended()).toHaveText(expect.stringContaining(`Import ${short(second)} released into draft ${short(id)}`), { wait: 60_000 });
    const after = (await seen()).slice(from);
    expect(after.indexOf("/ingestions")).toBeGreaterThanOrEqual(0);
    expect(after.indexOf(`/drafts/${id}`)).toBeGreaterThan(after.indexOf("/ingestions"));
    expect((await writes()).filter(([m]) => m === "POST").map(([, r]) => r)).toEqual(["/ingestions", "/ingestions"]);
    // A reload: the page signs in again and shows the import's state and what it allows.
    await browser.refresh();
    await expect($("main h2")).toHaveText(`Import ${short(second)}: released`, { wait: 20_000 });
    await expect(allowed()).toHaveText(`Released into draft ${short(id)}. Nothing is left to do.`);
  });

  it("ETAG-02, refused: another write moves the draft before the page re-reads it after released; the ETag is not kept, and Review the change shows the newer revision", async function () {
    this.timeout(120_000);
    const id = await draft();
    const route = `/drafts/${id}`;
    expect(await fixtureSignIn("h-author", {}, { upstream: at, held: route })).toMatch(SERVER);
    await wizard(id);
    await putDocument(`moved-${hex()}`, false);
    await staging("transient");
    await start();
    // The start's read for the If-Match is held first, then the re-read after the import released.
    await browser.waitUntil(async () => (await holding()).includes(route), { timeout: 20_000, timeoutMsg: "the start's draft read was not held" });
    await release();
    await ingestion();
    await browser.waitUntil(async () => (await holding()).includes(route), { timeout: 60_000, timeoutMsg: "the re-read after the import was not held" });
    const d = await api("h-author", "GET", route);
    const imported = d.body!["revision"] as number;
    const other = await api("h-author", "PUT", `${route}/fragments/other-${hex()}`, { layer: "role", document: syntheticDocument("other"), marks: null, declarations: null }, d.etag);
    expect(other.status).toBe(200);
    await faults({ upstream: at });
    await release();
    await expect(ended()).toHaveText(expect.stringContaining(`Draft ${short(id)} is now revision ${imported + 1}: another write followed this import's revision ${imported}. Its ETag is not kept. Review the change before writing again.`), { wait: 20_000 });
    await ended().$("a=Review the change").click();
    await expect($("main h2")).toHaveText(expect.stringContaining(`revision ${imported + 1}`), { wait: 20_000 });
  });

  it("encrypted staging with review: the import pauses, the page says so, and after a reload shows paused with continue or abandon; the paused filter lists it", async function () {
    this.timeout(120_000);
    const id = await draft();
    expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
    await wizard(id);
    await putDocument(`review-${hex()}`, false);
    // Encrypted staging with review is the default (B15, G5).
    expect(await $('input[name="staging"][value="encrypted"]').isSelected()).toBe(true);
    expect(await $('input[name="review"]').isSelected()).toBe(true);
    await start();
    const ing = await ingestion();
    await expect(events()).toHaveText(expect.stringContaining("paused (generation 1)"), { wait: 60_000 });
    await expect(ended()).toHaveText(expect.stringMatching(/^Paused for review \(expires .+\)\.$/), { wait: 20_000 });
    await expect(allowed()).toHaveText("Paused for review. Continue it, or abandon it.", { wait: 20_000 });
    await browser.refresh();
    await expect($("main h2")).toHaveText(`Import ${short(ing)}: paused`, { wait: 20_000 });
    await expect(allowed()).toHaveText("Paused for review. Continue it, or abandon it.");
    await $('nav[aria-label="Main"]').$("a=Changes").click();
    await $("main").$("a=Imports").click();
    await $("label*=State").$("select").selectByAttribute("value", "paused");
    await expect($(`main a[title="${ing}"]`)).toBeExisting({ wait: 20_000 });
    // The claim stays until its expiry; abandoning it is #341's control, so the case abandons it
    // through the API, keeping the run's claims few.
    expect((await api("h-author", "POST", `/ingestions/${ing}/abandonments`, {})).status).toBe(200);
  });
});

afterEach(storageEmpty);
