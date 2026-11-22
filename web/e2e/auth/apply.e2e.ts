// The apply page (ginsys/bronzeward#1096, /releases/<id>/apply), against the disposable deployment. A plain one has no
// release and no plan, so the cases serve the release, its machines, their positions, the plans and the assignment
// revisions as bodies of the injector and assert what the page shows and what it sends at the injector (each POST, its
// route, its answer). Every value is synthetic.
import { $, $$, browser, expect } from "@wdio/globals";
import { FIXTURE_PAGE } from "../auth.conf.ts";
import { disposable, fixtureSignIn, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
const id = (p: string, c: string) => `${p}_${c.repeat(26)}`;
const REL = id("rel", "r");
const CL = id("cl", "c");
const A = id("mch", "a");
const B = id("mch", "b");
const PA = id("pln", "a");
const PB = id("pln", "b");
const T0 = "2026-11-22T08:00:00Z";
const rel = (c: string) => id("rel", c);

describe("the apply page (#1096)", () => {
  let at = "";
  before(async () => {
    at = await disposable("start=normal");
  });

  const machine = (m: string, name: string, role: string | null, over: Record<string, unknown> = {}) => ({
    id: m, cluster: CL, name, role, hardware: { smbiosUuid: null, talosNodeId: `synthetic-${name}`, serial: null }, platform: "metal", talosEndpoint: "192.0.2.1:50000",
    desired: REL, applied: { release: rel("a"), source: "operation", importBase: null }, pendingConvergence: true, formOnly: false, observed: null, frozen: false, scopeState: "normal", openDrift: null, ...over,
  });
  const position = (m: Record<string, unknown>, step: string, plan: string | null) => ({
    machine: m, ingestion: null, draft: null, operation: null, rolloutHeldBy: null, step,
    plan: plan === null ? null : { id: plan, state: "proposed", operation: "apply-config", release: REL, approvalCurrent: null, createdAt: T0, expiresAt: "2026-11-23T08:00:00Z" },
  });
  const plan = (p: string, m: string, unified: string) => ({
    id: p, revision: 1, state: "proposed", committedOperation: null, approval: null, cluster: CL, machine: m, release: REL, operation: "apply-config", mode: "no-reboot", assignmentRevision: id("asr", "s"),
    desiredRelease: REL, baselineRevision: 1, driftRecord: null, route: "192.0.2.1:50000", maxObservationAgeSeconds: 300, checkValiditySeconds: 120, transportDeadlineSeconds: 60, verificationDeadlineSeconds: 120,
    maxAttempts: 3, rolloutLimit: 1, approvalPolicy: "one-approver", createdBy: { principal: "prn_synthetic_creator", role: "publisher" }, epoch: "ep_synthetic", timelineRevision: 1, createdAt: T0,
    expiresAt: "2026-11-23T08:00:00Z",
    evidence: { diff: { from: rel("a"), to: REL, withheld: false, unified }, validation: { result: "passed", at: "publication", contract: "v1.14", kubernetesVersion: "v1.34.1", machineryVersion: "v1.14.0", platformMode: "metal" }, dryRun: { available: false, reason: "none" }, baseline: null },
  });
  const assignment = (m: string, profiles: string[]) => ({ id: id("asr", m === A ? "a" : "b"), cluster: CL, machine: m, profiles, fragments: {}, author: "prn_synthetic_author", createdAt: T0 });
  const release = {
    id: REL, cluster: CL, draft: id("drf", "d"), draftRevision: 1, operation: id("op", "o"), publishedBy: { principal: "prn_synthetic_publisher", role: "publisher" }, publishedAt: T0,
    renderer: { contract: "v1.14", machineryVersion: "v1.14.0", machineryChecksum: "x", kubernetesVersion: "v1.34.1" },
    sources: [{ kind: "assignment", head: id("asg", "a"), revision: id("asr", "a"), headRevision: 1 }, { kind: "assignment", head: id("asg", "b"), revision: id("asr", "b"), headRevision: 1 }],
    machines: [A, B].map((m) => ({ machine: m, mode: "no-reboot", review: id("rev", "r"), publicationObservation: id("obs", "o") })),
  };
  // Machine A is a control plane with a plan a publisher prepared; B is a worker with another prepared plan: their two changes differ.
  const mA = machine(A, "alpha", "controlplane");
  const mB = machine(B, "beta", "worker");
  const bodies = (more: Record<string, unknown> = {}) => ({
    [`/releases/${REL}`]: release,
    [`/machines/${A}`]: mA, [`/machines/${B}`]: mB,
    [`/machines/${A}/position`]: position(mA, "awaiting-approval", PA), [`/machines/${B}/position`]: position(mB, "awaiting-approval", PB),
    [`/assignment-revisions/${id("asr", "a")}`]: assignment(A, ["cp-base"]), [`/assignment-revisions/${id("asr", "b")}`]: assignment(B, ["worker-base", "storage"]),
    [`/plans/${PA}`]: plan(PA, A, "@@ machine.network @@\n+  hostname: alpha"), [`/plans/${PB}`]: plan(PB, B, "@@ machine.install @@\n+  disk: /dev/sdb"),
    [`/plans/${PA}/approvals`]: { id: id("apr", "a"), plan: PA }, [`/plans/${PB}/approvals`]: { id: id("apr", "b"), plan: PB },
    ...more,
  });
  async function open(human: string, b: Record<string, unknown>) {
    expect(await fixtureSignIn(human, {}, { upstream: at, bodies: b })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/releases/${REL}/apply`);
    await expect($("main h2")).toHaveText(expect.stringContaining("Apply release"), { wait: 20_000 });
  }
  const rows = () => $$('[data-testid="apply-row"]');
  const choose = (m: string) => $(`[data-testid="apply-row"][data-machine="${m}"] input[type=checkbox]`);
  const posts = async () => (await writes()).filter((w) => w.startsWith("POST "));

  it("lists the release's machines with role and profiles and none chosen (check 1)", async function () {
    this.timeout(120_000);
    await open("h-all", bodies());
    await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000, timeoutMsg: "two machine rows did not appear" });
    // once: nothing chooses a row but a click, and the case clicks nothing before this read.
    for (const m of [A, B]) expect(await choose(m).isSelected()).toBe(false);
    await expect($(`[data-testid="apply-row"][data-machine="${A}"] [data-testid="apply-role"]`)).toHaveText("control plane", { wait: 20_000 });
    await expect($(`[data-testid="apply-row"][data-machine="${B}"] [data-testid="apply-role"]`)).toHaveText("worker");
    await expect($(`[data-testid="apply-row"][data-machine="${B}"]`)).toHaveText(expect.stringContaining("worker-base"), { wait: 20_000 });
    await expect($(`[data-testid="apply-row"][data-machine="${B}"]`)).toHaveText(expect.stringContaining("storage"));
    // once: the section shows only for a machine chosen, and the case chose none. Nothing is chosen, so no plan is listed
    // and nothing was sent.
    await expect($('section[aria-label="Plans to approve"]')).not.toBeExisting();
    // once: the page sends a write only from a press, and the case presses nothing.
    expect(await posts()).toEqual([]);
  });

  it("lists the plans of exactly the machines chosen, each with its own change (check 2)", async function () {
    this.timeout(120_000);
    await open("h-all", bodies());
    await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000 });
    await choose(A).click();
    await $("button=Show apply").click();
    // The press disables the button until it has finished, so an enabled button after the click means the press is done.
    await expect($("button=Show apply")).toBeEnabled({ wait: 20_000 });
    await expect($('[data-testid="apply-plan"]')).toBeExisting({ wait: 20_000 });
    // once: only A is chosen and the press is done, so one plan is listed; a second would have needed B chosen.
    expect(await $$('[data-testid="apply-plan"]').length).toBe(1);
    await choose(B).click();
    await $("button=Show apply").click();
    await expect($("button=Show apply")).toBeEnabled({ wait: 20_000 });
    await browser.waitUntil(async () => (await $$('[data-testid="apply-plan"]').length) === 2, { timeout: 20_000, timeoutMsg: "the second plan did not appear" });
    // Each plan's change is inside a closed details, whose text a browser does not return: open each one first.
    for (const s of await $$('[data-testid="apply-plan"] summary')) await s.click();
    const diffs = await $$('[data-testid="diff"]').map((d) => d.getText());
    expect(diffs.sort()).toEqual(["@@ machine.install @@\n+  disk: /dev/sdb", "@@ machine.network @@\n+  hostname: alpha"]);
    // once: Show apply was pressed twice and has already answered; a plan it made would have been sent before the second list
    // shows. A prepared plan is reused, so no plan was made, and the page says a control plane is among those chosen.
    expect(await posts()).toEqual([]);
    await expect($('[data-testid="apply-control-plane"]')).toBeExisting();
  });

  it("approves the plan of the machine chosen and no other (check 3)", async function () {
    this.timeout(120_000);
    await open("h-all", bodies());
    await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000 });
    await choose(B).click();
    await expect($('[data-testid="apply-plan"]')).toBeExisting({ wait: 20_000 });
    // A plan another person prepared says who prepared it and when, and the plan shown is the plan Apply approves.
    await browser.waitUntil(async () => ((await $('[data-testid="apply-plan-by"]').getProperty("textContent")) as string).startsWith("Prepared by"), { timeout: 20_000, timeoutMsg: "the plan did not say who prepared it" });
    expect(await $('[data-testid="apply-plan"]').getAttribute("data-plan")).toBe(PB);
    await expect($('[data-testid="apply-sentence"]')).toHaveText(expect.stringContaining("Pressing Apply records your approval of each plan shown above."));
    await $("button=Apply").click();
    await browser.waitUntil(async () => (await posts()).length > 0, { timeout: 20_000, timeoutMsg: "no approval was sent" });
    // The press disables Apply until every approval has been sent: an enabled button means the press is done.
    await expect($("button=Apply")).toBeEnabled({ wait: 20_000 });
    // once: only B is chosen, and the press is done, so the approval of A, which was never chosen, was not sent.
    expect(await posts()).toEqual([`POST /plans/${PB}/approvals`]);
  });

  it("makes a plan for a machine that has none and cancels it when the choice changes", async function () {
    this.timeout(120_000);
    const none = { [`/machines/${A}/position`]: position(mA, "needs-plan", null), "/plans": plan(PA, A, ""), [`/plans/${PA}/cancellations`]: { id: PA } };
    await open("h-all", bodies(none));
    await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000 });
    await choose(A).click();
    await $("button=Show apply").click();
    await browser.waitUntil(async () => (await posts()).includes("POST /plans"), { timeout: 20_000, timeoutMsg: "no plan was made" });
    // The choice is locked while the press runs, so the machine is unchosen once the press is done.
    await expect($("button=Show apply")).toBeEnabled({ wait: 20_000 });
    await choose(A).click();
    await browser.waitUntil(async () => (await posts()).includes(`POST /plans/${PA}/cancellations`), { timeout: 20_000, timeoutMsg: "the plan the page made was not cancelled" });
    // once: the plan was made once and cancelled once, and the case sends nothing else.
    expect(await posts()).toEqual(["POST /plans", `POST /plans/${PA}/cancellations`]);
  });

  it("a viewer sees the controls disabled, each with its reason (check 7)", async function () {
    this.timeout(120_000);
    await open("h-viewer", bodies());
    await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000 });
    await choose(A).click();
    await expect($("button=Show apply")).toBeDisabled({ wait: 20_000 });
    await expect($("main")).toHaveText(expect.stringContaining("Needs publisher"));
    await expect($("button=Apply")).toBeDisabled();
    await expect($("main")).toHaveText(expect.stringContaining("Needs approver"));
    // once: both buttons are disabled, so the case presses nothing and the page sends a write only from a press.
    expect(await posts()).toEqual([]);
  });
});
