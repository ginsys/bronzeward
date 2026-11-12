// The plan creation page (ginsys/bronzeward#357, B19) against the disposable deployment. The deployment has
// no Talos node, so no release and no plan can exist on it. The first case runs the real path that exists
// here: a plan for a release the server does not know is refused, the page shows the refusal, and the
// server holds no plan afterwards. The second case follows a created plan through the injector: the 201 and
// the plan's read are synthetic bodies, and the page must send the one POST, say the plan is not approved
// and open the plan. The server's own proof that a plan is created only for a Desired release with an
// Applied configuration is internal/api/plans.go; the page's next steps are unit tests in plan.test.ts.
// The cases sign in as h-publisher.
import { randomBytes } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { CONTROL } from "../auth.conf.ts";
import { fixtureSignIn, mint, storageEmpty } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
// Canonical identifiers (internal/id/id.go): 26 base32 characters whose last one has two zero low bits.
const PLAN = `pln_${"q".repeat(26)}`;
const REL = `rel_${"i".repeat(26)}`;
const T0 = "2026-11-12T08:00:00Z";

let at = "";
let machine = "";

const hex = () => randomBytes(4).toString("hex");
// A write is logged as "METHOD route key epoch": the method and the route are what a case compares.
const writes = async (): Promise<string[]> => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).map((w) => w.split(" ").slice(0, 2).join(" "));

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

describe("the plan creation page (#357)", () => {
  before(async () => {
    const res = await fetch(`${CONTROL}/disposable?start=normal`, { method: "POST" });
    expect(res.status).toBe(200);
    at = await res.text();
    const name = `synthetic-${hex()}`;
    const c = await api("h-author", "POST", "/clusters", { name, endpoint: `https://${name}.example.test:6443`, contract: "v1.14", talosClusterId: `${randomBytes(32).toString("base64url")}=` });
    expect(c.status).toBe(201);
    const m = await api("h-author", "POST", "/machines", { cluster: c.body!["id"], talosNodeId: `synthetic-node-${hex()}`, platform: "metal", talosEndpoint: `192.0.2.${1 + (randomBytes(1)[0]! % 254)}` });
    expect(m.status).toBe(201);
    machine = m.body!["id"] as string;
  });

  it("a plan for a release the server does not know is refused and saved nowhere", async function () {
    this.timeout(120_000);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at })).toMatch(SERVER);
    const unknown = `rel_${"y".repeat(26)}`;
    await browser.url(`/plans/new?${new URLSearchParams({ release: unknown, machine, operation: "apply-config" })}`);
    await expect($("main h2")).toHaveText("Create a plan", { wait: 20_000 });
    await $("main").$("button=Create plan").click();
    await expect($('[data-testid="write-result"]')).toHaveText(expect.stringMatching(/^Not saved: /), { wait: 60_000 });
    // The refusal is shown with the server's own words and the button stays available for a corrected plan.
    await expect($("main").$("button=Create plan")).toBeEnabled();
    // The refusal belongs to apply-config: choosing adopt hides it.
    await $("main").$("select").selectByAttribute("value", "adopt");
    await expect($('[data-testid="write-result"]')).not.toBeExisting();
    expect((await writes()).filter((w) => w.startsWith("POST "))).toEqual(["POST /plans"]);
    const plans = await api("h-viewer", "GET", "/plans");
    expect(plans.status).toBe(200);
    expect(plans.body!["items"]).toEqual([]);
  });

  it("a created plan, served by the injector, says it is not approved and opens the plan; one POST is sent", async function () {
    this.timeout(120_000);
    const plan = {
      id: PLAN, revision: 1, state: "proposed", committedOperation: null, approval: null, cluster: `cls_${"c".repeat(26)}`, machine, release: REL, operation: "adopt",
      mode: null, assignmentRevision: `asr_${"s".repeat(26)}`, desiredRelease: REL, baselineRevision: 4, driftRecord: null, route: "192.0.2.1:50000",
      maxObservationAgeSeconds: 300, checkValiditySeconds: 120, transportDeadlineSeconds: null, verificationDeadlineSeconds: null, maxAttempts: null, rolloutLimit: 1,
      approvalPolicy: "one-approver", createdBy: { principal: "h-publisher", role: "publisher" }, epoch: "ep_synthetic", timelineRevision: 7, createdAt: T0, expiresAt: "2026-11-13T08:00:00Z",
      evidence: { diff: null, validation: { result: "passed", at: "publication", contract: "v1.14", kubernetesVersion: "v1.34.1", machineryVersion: "v1.14.0", platformMode: "metal" }, dryRun: { available: false, reason: "the PoC has none" }, baseline: null },
    };
    const bodies = { "/plans": plan, [`/plans/${PLAN}`]: plan };
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at, bodies })).toMatch(SERVER);
    await browser.url(`/plans/new?${new URLSearchParams({ release: REL, machine, operation: "adopt" })}`);
    await expect($("main h2")).toHaveText("Create a plan", { wait: 20_000 });
    // An adopt plan is offered no deadline and no attempt limit.
    await expect($("main").$("label*=Transport deadline")).not.toBeExisting();
    await expect($("main").$("label*=Attempt limit")).not.toBeExisting();
    await $("main").$("button=Create plan").click();
    await expect($('[data-testid="plan-proposed"]')).toHaveText("Plan proposed. It is not approved, and nothing runs until an approver approves.", { wait: 60_000 });
    await expect($(`main a[title="${machine}"]`)).toBeExisting();
    expect((await writes()).filter((w) => w.startsWith("POST "))).toEqual(["POST /plans"]);
    await expect($("main h2")).toHaveText(expect.stringContaining(short(PLAN)));
  });
});

afterEach(storageEmpty);
