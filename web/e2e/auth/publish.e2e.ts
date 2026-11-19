// The publication page (ginsys/bronzeward#342) against the disposable deployment. The deployment has
// no Talos node, so no publication can succeed on it: the server's node evidence check refuses
// every machine (internal/api/publication_observation.go). The case runs the real path that exists
// here: the draft's page links to the publication page, Publish sends the write with the kept ETag,
// the operation is followed to its end, and its end says that nothing was sent to any machine. It then
// reads the API: no release exists, no apply-config or adopt operation exists, and the machine's
// Applied release is unchanged. A second case follows a succeeded publication through the injector: the
// POST's answer and the operation's reads are synthetic bodies (the deployment cannot produce a success),
// and the page must still send nothing but the one POST. The server's own proof that a publication sends
// nothing to a machine is TestPublication, subtest "no mutation", in internal/clitest/publication_test.go.
// The cases sign in as h-publisher.
import { randomBytes } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { CONTROL, syntheticDocument } from "../auth.conf.ts";
import { eventually, fixtureSignIn, focusIn, focusKept, focusSettles, fromTitle, measure, mint, mirrored, narrow, onMain, overflows, storageEmpty, tabTo } from "./helpers.ts";

const statusRegions = () => browser.execute(() => document.querySelectorAll('[role="status"]').length);

// Synthetic identifiers of a publication the deployment cannot produce.
const OP = `op_${"w".repeat(26)}`;
const REL = `rel_${"h".repeat(26)}`;

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

describe("the publication page (#342)", () => {
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

  it("Publish sends the draft's kept ETag, follows the operation to its end, and sends nothing to any machine", async function () {
    this.timeout(180_000);
    const d = await api("h-author", "POST", "/drafts", { cluster, title: `synthetic ${hex()}` });
    expect(d.status).toBe(201);
    const id = d.body!["id"] as string;
    const name = `pub-${hex()}`;
    const f = await api("h-author", "PUT", `/drafts/${id}/fragments/${name}`, { layer: "role", document: syntheticDocument(name), marks: null, declarations: null }, d.etag);
    expect(f.status).toBe(200);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at })).toMatch(SERVER);
    await $('nav[aria-label="Main"]').$("a=Changes").click();
    await $(`main a[title="${id}"]`).click();
    await expect($("main h2")).toHaveText(expect.stringContaining("synthetic"));
    await $("main").$("a=Publish this draft").click();
    await expect($("main h2")).toHaveText(expect.stringContaining(`Publish draft ${short(id)}`), { wait: 20_000 });
    await $("main").$("button=Publish").click();
    // The server accepts the write (202, which needs the kept ETag as If-Match) and the operation fails
    // at its node evidence check: the page names the operation, then says nothing was sent.
    await expect($('[data-testid="write-result"]')).toHaveText(expect.stringMatching(/^Publishing draft \S+ revision \d+ \(operation \S+\)\.$/), { wait: 60_000 });
    await expect($('[data-testid="ended"]')).toHaveText(expect.stringMatching(/^Publication operation \S+ (failed|rejected)(: .+)?\. Nothing has been sent to any machine\.$/), { wait: 120_000 });
    // The operation failed and left the draft open, so Publish is available again.
    await expect($("main").$("button=Publish")).toBeEnabled({ wait: 20_000 });
    expect((await writes()).filter(([m]) => m === "POST").map(([, r]) => r)).toEqual([`/drafts/${id}/publications`]);
    // Nothing reached any machine: no release, no apply-config or adopt operation, no Applied release.
    const releases = await api("h-viewer", "GET", "/releases");
    expect(releases.status).toBe(200);
    expect(releases.body!["items"]).toEqual([]);
    const ops = await api("h-viewer", "GET", "/operations");
    expect(ops.status).toBe(200);
    expect((ops.body!["items"] as { kind: string }[]).filter((o) => o.kind === "apply-config" || o.kind === "adopt")).toEqual([]);
    const m = await api("h-viewer", "GET", `/machines/${machine}`);
    expect(m.status).toBe(200);
    expect(m.body!["applied"]).toBeNull();
  });

  // A draft with one fragment, so its revision is 2, and the synthetic bodies of its succeeded publication.
  async function staged(title: string) {
    const d = await api("h-author", "POST", "/drafts", { cluster, title });
    expect(d.status).toBe(201);
    const id = d.body!["id"] as string;
    const name = `pub-${hex()}`;
    const f = await api("h-author", "PUT", `/drafts/${id}/fragments/${name}`, { layer: "role", document: syntheticDocument(name), marks: null, declarations: null }, d.etag);
    expect(f.status).toBe(200);
    return id;
  }
  const succeeded = (id: string) => {
    const at0 = "2026-11-11T22:00:00Z";
    const accepted = { id: OP, kind: "publish", state: "queued", epoch: "ep_synthetic", lastEvent: 0, subject: { draft: id, draftRevision: 2 }, createdBy: null, createdAt: at0, result: null, error: null };
    const release = {
      id: REL, cluster, draft: id, draftRevision: 2, operation: OP, publishedBy: { principal: "h-publisher", role: "publisher" }, publishedAt: at0,
      renderer: { contract: "v1.14", machineryVersion: "synthetic", machineryChecksum: "synthetic", kubernetesVersion: "synthetic" }, sources: [], machines: [],
    };
    return {
      [`/drafts/${id}/publications`]: accepted,
      [`/operations/${OP}`]: { ...accepted, state: "succeeded", lastEvent: 1, result: { release: REL } },
      [`/operations/${OP}/events`]: { items: [] },
      [`/releases/${REL}`]: release,
    };
  };

  it("a succeeded publication, served by the injector, names its release and sends nothing but the one POST", async function () {
    this.timeout(180_000);
    const id = await staged(`synthetic ${hex()}`);
    const bodies = succeeded(id);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at, bodies })).toMatch(SERVER);
    await $('nav[aria-label="Main"]').$("a=Changes").click();
    await $(`main a[title="${id}"]`).click();
    await $("main").$("a=Publish this draft").click();
    await expect($("main h2")).toHaveText(expect.stringContaining(`Publish draft ${short(id)}`), { wait: 20_000 });
    await $("main").$("button=Publish").click();
    await expect($('[data-testid="write-result"]')).toHaveText(`Publishing draft ${short(id)} revision 2 (operation ${short(OP)}).`, { wait: 60_000 });
    await expect($('[data-testid="ended"]')).toHaveText(`Publication succeeded: release ${short(REL)} was published. Nothing has been sent to any machine.`, { wait: 120_000 });
    await expect($(`main a[title="${REL}"]`)).toBeExisting();
    // A published draft takes no second write: Publish stays disabled.
    await expect($("main").$("button=Publish")).toBeDisabled();
    // The page sent the one POST and reads: no apply-config, adopt or other write came from the client.
    expect((await writes()).map(([m, r]) => `${m} ${r}`)).toEqual([`POST /drafts/${id}/publications`]);
  });

  // The page's answer to Publish: the accepted line and what follows it. Publish stays disabled once the
  // write is accepted, so a keyboard user's focus must move here and not fall to the body (2.4.3).
  const ANSWER = '[data-testid="publish-answer"]';

  it("keyboard only (T3): the draft's Publish this draft link, Publish and the release link are reached and operated, focus follows the new content", async function () {
    this.timeout(180_000);
    const id = await staged(`synthetic ${hex()}`);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at, bodies: succeeded(id) })).toMatch(SERVER);
    await fromTitle();
    await tabTo((f) => f.text === "Changes", "the Changes link");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText("Changes");
    await focusSettles(onMain, "the new page");
    await tabTo((f) => f.title === id, "the draft's link");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText(expect.stringContaining("synthetic"));
    await focusSettles(onMain, "the new page");
    await tabTo((f) => f.tag === "A" && f.text === "Publish this draft", "the Publish this draft link");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText(expect.stringContaining(`Publish draft ${short(id)}`), { wait: 20_000 });
    await focusSettles(onMain, "the new page");
    await tabTo((f) => f.tag === "BUTTON" && f.text === "Publish", "Publish");
    await browser.keys("Enter");
    await expect($('[data-testid="write-result"]')).toHaveText(`Publishing draft ${short(id)} revision 2 (operation ${short(OP)}).`, { wait: 60_000 });
    // The accepted line is a status, announced without moving focus; the button that had focus is now disabled.
    await eventually(async () => (await mirrored()).some((t) => t.includes("Publishing draft")), "the accepted line mirrored");
    // The busy state and the accepted line come from one operation in one render (web/src/publish.tsx:145,163), so the button is already disabled here.
    expect(await $("main").$("button=Publish").isEnabled()).toBe(false);
    await focusSettles(focusKept, "the page, not the body");
    await focusSettles(() => focusIn(ANSWER), "the answer");
    await expect($('[data-testid="ended"]')).toHaveText(`Publication succeeded: release ${short(REL)} was published. Nothing has been sent to any machine.`, { wait: 120_000 });
    await focusSettles(() => focusIn(ANSWER), "the answer after the end");
    expect(await statusRegions()).toBe(1);
    const seen = await measure();
    expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
    expect(seen.small).toBe(0);
    // On to the release, by keyboard: the next Tab leaves the answer, and the link opens the release's page.
    await tabTo((f) => f.title === REL, "the release link");
    await browser.keys("Enter");
    await expect($("main h2")).toHaveText(`Release ${short(REL)}`, { wait: 20_000 });
    await expect($("main").$("h3=Machines covered (0)")).toBeExisting({ wait: 20_000 });
    await focusSettles(onMain, "the new page");
    expect((await writes()).map(([m, r]) => `${m} ${r}`)).toEqual([`POST /drafts/${id}/publications`]);
  });

  it("320 CSS pixels: the publication page with a long draft title, its accepted line, its end and the release page do not scroll the page sideways", async function () {
    this.timeout(180_000);
    const id = await staged(`synthetic w${"x".repeat(119)}`);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at, bodies: succeeded(id) })).toMatch(SERVER);
    await narrow(async () => {
      await $('nav[aria-label="Main"]').$("a=Changes").click();
      await $(`main a[title="${id}"]`).click();
      await $("main").$("a=Publish this draft").click();
      await expect($("main h2")).toHaveText(expect.stringContaining(`Publish draft ${short(id)}`), { wait: 20_000 });
      expect(["the publication page", await overflows()]).toEqual(["the publication page", 0]);
      await $("main").$("button=Publish").click();
      await expect($('[data-testid="write-result"]')).toHaveText(`Publishing draft ${short(id)} revision 2 (operation ${short(OP)}).`, { wait: 60_000 });
      expect(["the accepted line", await overflows()]).toEqual(["the accepted line", 0]);
      await expect($('[data-testid="ended"]')).toHaveText(`Publication succeeded: release ${short(REL)} was published. Nothing has been sent to any machine.`, { wait: 120_000 });
      expect(["the end", await overflows()]).toEqual(["the end", 0]);
      await $(`main a[title="${REL}"]`).click();
      await expect($("main h2")).toHaveText(`Release ${short(REL)}`, { wait: 20_000 });
      // The heading shows while the release loads: wait for its tables, then measure.
      await expect($("main").$("h3=Machines covered (0)")).toBeExisting({ wait: 20_000 });
      expect(["the release", await overflows()]).toEqual(["the release", 0]);
    });
  });
});

afterEach(storageEmpty);
