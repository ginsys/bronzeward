// The publication page (ginsys/bronzeward#342) against the disposable deployment. The deployment has
// no Talos node, so no publication can succeed on it: the server's node evidence check refuses
// every machine (internal/api/publication_observation.go). The case runs the real path that exists
// here: the draft's page links to the publication page, Publish sends the write with the kept ETag,
// the operation is followed to its end, and its end says that nothing was sent to any machine. It then
// reads the API: no release exists, no apply-config or adopt operation exists, and the machine's
// Applied release is unchanged. A succeeded publication is the unit tests' (publish.test.ts) and the
// CLI's Go test, which has a Talos stand-in. The case signs in as h-publisher.
import { randomBytes } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { CONTROL, syntheticDocument } from "../auth.conf.ts";
import { fixtureSignIn, mint, storageEmpty } from "./helpers.ts";

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
    expect((await writes()).filter(([m]) => m === "POST").map(([, r]) => r)).toEqual([`/drafts/${id}/publications`]);
    // Nothing reached any machine: no release, no apply-config or adopt operation, no Applied release.
    const releases = await api("h-viewer", "GET", "/releases");
    expect(releases.status).toBe(200);
    expect(releases.body!["items"]).toEqual([]);
    const ops = await api("h-viewer", "GET", "/operations");
    expect((ops.body!["items"] as { kind: string }[]).filter((o) => o.kind === "apply-config" || o.kind === "adopt")).toEqual([]);
    const m = await api("h-viewer", "GET", `/machines/${machine}`);
    expect(m.body!["applied"] ?? null).toBeNull();
  });
});

afterEach(storageEmpty);
