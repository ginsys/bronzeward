// The apply page (ginsys/bronzeward#1096, /releases/<id>/apply), against the disposable deployment. A plain one has no
// release and no plan, so the cases serve the release, its machines, their positions, the plans and the assignment
// revisions as bodies of the injector and assert what the page shows and what it sends at the injector (each POST, its
// route, its answer). Every value is synthetic.
import { $, $$, browser, expect } from "@wdio/globals";
import { FIXTURE_PAGE } from "../auth.conf.ts";
import { disposable, fixtureSignIn, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
// A well-formed identifier (128-bit lower-case base32, so the last character is one whose low two bits are zero): the server
// parses the identifiers in a request before it looks anything up, and answers the unknown ones from the injector's bodies.
const id = (p: string, c: string) => `${p}_${c.repeat(25)}a`;
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
    machines: [A, B].map((m) => ({ machine: m, mode: "no-reboot", review: id("rev", "r"), publicationObservation: id("obs", "o"), hostname: { source: "none" } })),
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

  it("lists the release's machines with role and profiles and none chosen (check 1) [step plan-and-approval-2.1]", async function () {
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

  it("lists the plans of exactly the machines chosen, each with its own change (check 2) [step plan-and-approval-2.2]", async function () {
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

  it("approves the plan of the machine chosen and no other (check 3) [step plan-and-approval-2.3]", async function () {
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

  it("makes a plan for a machine that has none and cancels it when the choice changes [step plan-and-approval-2.2] [step plan-and-approval-2.4]", async function () {
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

  it("a plan whose change the server withholds keeps Apply off, and the page names that plan and links to it [step plan-and-approval-2.4]", async function () {
    this.timeout(120_000);
    const p = plan(PA, A, "");
    const withheld = { ...p, evidence: { ...p.evidence, diff: { from: rel("a"), to: REL, withheld: true, unified: "" } } };
    await open("h-all", bodies({ [`/plans/${PA}`]: withheld }));
    await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000 });
    await choose(A).click();
    await expect($('[data-testid="apply-withheld"]')).toHaveText(expect.stringContaining("Open the plan"), { wait: 20_000 });
    expect(await $('[data-testid="apply-withheld"]').$("a=Open the plan").getAttribute("href")).toContain(`/plans/${PA}`);
    await expect($("button=Apply")).toBeDisabled();
    // once: the case presses nothing, and the page sends a write only from a press.
    expect(await posts()).toEqual([]);
  });

  // Criterion 3 of ginsys/bronzeward#1193 (one apply, one cluster; alternative C10): a release that moved a shared fragment lists it
  // with the machines of this cluster it reaches, and names the other cluster behind it as information. The page offers that
  // cluster no choice, plan or approval, and sends nothing for it.
  it("a release that moved a shared name names the other clusters as information and offers them no choice, plan or approval (criterion 3)", async function () {
    this.timeout(120_000);
    const FRG = id("frg", "f");
    const OTHER = id("cl", "o");
    const moved = { kind: "fragment", scope: "library", head: FRG, revision: id("frv", "n"), headRevision: null, previous: id("frv", "o"), adoptionRevision: 3 };
    const behind = (cluster: string) => ({ cluster, revision: id("frv", "o"), adoptionRevision: 2, behind: true, release: null, via: null, machines: [] });
    await open("h-all", bodies({
      [`/releases/${REL}`]: { ...release, sources: [...release.sources, moved] },
      [`/assignment-revisions/${id("asr", "a")}`]: { ...assignment(A, ["cp-base"]), fragments: { role: ["ntp"] } },
      [`/fragments/${FRG}`]: { id: FRG, cluster: null, scope: "library", name: "ntp", layer: "global", revision: moved.revision, headRevision: 4, createdAt: T0 },
      [`/fragments/${FRG}/adoptions`]: { items: [behind(CL), behind(OTHER)] },
      [`/clusters/${OTHER}`]: { id: OTHER, name: "lab-b" },
    }));
    const shared = () => $('[data-testid="apply-shared"]');
    await expect(shared()).toHaveText(expect.stringContaining("fragment ntp"), { wait: 20_000 });
    await expect($('[data-testid="apply-shared-reach"]')).toHaveText(expect.stringContaining("Reaches 1 machine here: alpha"), { wait: 20_000 });
    await expect($('[data-testid="apply-shared-others"]')).toHaveText(expect.stringContaining("lab-b"), { wait: 20_000 });
    // once: the block lists the other cluster and not this release's own, and the case presses nothing.
    expect(await shared().$$('[data-testid="apply-shared-other"]').length).toBe(1);
    expect(await shared().$$("input, button, select").length).toBe(0);
    await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000 });
    expect(await $$('[data-testid="apply-row"] input[type=checkbox]').length).toBe(2);
    // once: the page sends a write only from a press, and the case presses nothing.
    expect(await posts()).toEqual([]);
  });

  // Criterion 1 of ginsys/bronzeward#1193 (never unseen), read in the browser: the plan of the machine B shows the first revision
  // of a shared name while B's release holds no move of it, and the newer revision, with the block naming the move, once B's
  // release does. The bodies are recorded answers: that the server's plan holds nothing of a newer revision before B adopts it is
  // the client journey's and the server cases' check, and this case reads the plan diff as the person does.
  it("the plan of a cluster shows the first revision of a shared name until its release moves the cluster to the newer one (criterion 1)", async function () {
    this.timeout(120_000);
    const FRG = id("frg", "f");
    const moved = { kind: "fragment", scope: "library", head: FRG, revision: id("frv", "n"), headRevision: null, previous: id("frv", "o"), adoptionRevision: 3 };
    const read = async (unified: string, r: unknown, more: Record<string, unknown> = {}) => {
      await open("h-all", bodies({ [`/releases/${REL}`]: r, [`/plans/${PA}`]: plan(PA, A, unified), ...more }));
      await browser.waitUntil(async () => (await rows().length) === 2, { timeout: 20_000 });
      await choose(A).click();
      await expect($('[data-testid="apply-plan"]')).toBeExisting({ wait: 20_000 });
      await $('[data-testid="apply-plan"] summary').click();
      return $('[data-testid="diff"]');
    };
    await expect(await read("@@ machine.nodeLabels @@\n+  node.example.test/shared: one", release)).toHaveText(expect.stringContaining("node.example.test/shared: one"), { wait: 20_000 });
    // once: this release's sources hold no shared move, so the block is not drawn, and the diff was read above.
    expect(await $('[data-testid="apply-shared"]').isExisting()).toBe(false);
    await expect(await read("@@ machine.nodeLabels @@\n+  node.example.test/shared: two", { ...release, sources: [...release.sources, moved] }, {
      [`/fragments/${FRG}`]: { id: FRG, cluster: null, scope: "library", name: "ntp", layer: "global", revision: moved.revision, headRevision: 4, createdAt: T0 },
      [`/fragments/${FRG}/adoptions`]: { items: [] },
    })).toHaveText(expect.stringContaining("node.example.test/shared: two"), { wait: 20_000 });
    await expect($('[data-testid="apply-shared"]')).toHaveText(expect.stringContaining("fragment ntp"), { wait: 20_000 });
    // once: the page sends a write only from a press, and the case presses nothing.
    expect(await posts()).toEqual([]);
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
