// The plan creation page and the plan page's state lines (ginsys/bronzeward#357, B19, B20, B22) against the disposable deployment. The deployment has
// no Talos node, so no release and no plan can exist on it. The first case runs the real path that exists
// here: a plan for a release the server does not know is refused, the page shows the refusal, and the
// server holds no plan afterwards. The second case follows a created plan through the injector: the answer to
// the creation and the plan's read are synthetic bodies, and the page must send the one POST, say the plan
// is not approved and open the plan. The injector answers 200, never 201: the server's own proof of the 201,
// and that a plan is created only for a Desired release with an Applied configuration, is
// internal/api/plans_test.go; the page's next steps are unit tests in plan.test.ts.
// The cases sign in as h-publisher.
import { $, browser, expect } from "@wdio/globals";
import { short } from "../../src/list.tsx";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { api, fixtureSignIn, storageEmpty, syntheticMachine, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
// Canonical identifiers (internal/id/id.go): 26 base32 characters whose last one has two zero low bits.
const PLAN = `pln_${"q".repeat(26)}`;
const REL = `rel_${"i".repeat(26)}`;
const T0 = "2026-11-12T08:00:00Z";

let at = "";
let machine = "";

describe("the plan creation page (#357)", () => {
  before(async () => {
    const res = await fetch(`${CONTROL}/disposable?start=normal`, { method: "POST" });
    expect(res.status).toBe(200);
    at = await res.text();
    machine = await syntheticMachine(at);
  });

  it("a plan for a release the server does not know is refused and saved nowhere", async function () {
    this.timeout(120_000);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at })).toMatch(SERVER);
    const unknown = `rel_${"y".repeat(26)}`;
    await browser.url(`${FIXTURE_PAGE}/plans/new?${new URLSearchParams({ release: unknown, machine, operation: "apply-config" })}`);
    await expect($("main h2")).toHaveText("Create a plan", { wait: 20_000 });
    await $("main").$("button=Create plan").click();
    await expect($('[data-testid="write-result"]')).toHaveText(expect.stringMatching(/^Not saved: /), { wait: 60_000 });
    // The refusal is shown with the server's own words and the button stays available for a corrected plan.
    await expect($("main").$("button=Create plan")).toBeEnabled();
    // The refusal belongs to apply-config: choosing adopt hides it.
    await $("main").$("select").selectByAttribute("value", "adopt");
    await expect($('[data-testid="write-result"]')).not.toBeExisting();
    expect((await writes()).filter((w) => w.startsWith("POST "))).toEqual(["POST /plans"]);
    const plans = await api(at, "h-viewer", "GET", "/plans");
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
    await browser.url(`${FIXTURE_PAGE}/plans/new?${new URLSearchParams({ release: REL, machine, operation: "adopt" })}`);
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

  // The plan page's state lines (B20, B22), served by the injector: no release or plan exists here.
  const synthetic = (state: string, more: Record<string, unknown> = {}) => ({
    id: PLAN, revision: 1, state, committedOperation: null, approval: null, cluster: `cls_${"c".repeat(26)}`, machine, release: REL, operation: "apply-config",
    mode: "auto", assignmentRevision: `asr_${"s".repeat(26)}`, desiredRelease: REL, baselineRevision: null, driftRecord: null, route: "192.0.2.1:50000",
    maxObservationAgeSeconds: 300, checkValiditySeconds: 120, transportDeadlineSeconds: 60, verificationDeadlineSeconds: 120, maxAttempts: 3, rolloutLimit: 1,
    approvalPolicy: "one-approver", createdBy: { principal: "h-publisher", role: "publisher" }, epoch: "ep_synthetic", timelineRevision: 7, createdAt: T0, expiresAt: "2026-11-13T08:00:00Z",
    evidence: { diff: { from: `rel_${"j".repeat(26)}`, to: REL, withheld: true, unified: "" }, validation: { result: "passed", at: "publication", contract: "v1.14", kubernetesVersion: "v1.34.1", machineryVersion: "v1.14.0", platformMode: "metal" }, dryRun: { available: false, reason: "the PoC has none" }, baseline: null },
    ...more,
  });

  it("an expired plan shows its state and a link to a new plan, and the page sends nothing", async function () {
    this.timeout(120_000);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at, bodies: { [`/plans/${PLAN}`]: synthetic("expired") } })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/${PLAN}`);
    await expect($('[data-testid="plan-terminal"]')).toHaveText(expect.stringContaining("expired"), { wait: 20_000 });
    await expect($('[data-testid="plan-terminal"] a')).toHaveAttribute("href", `/plans/new?${new URLSearchParams({ release: REL, machine, operation: "apply-config" })}`);
    await expect($("main").$("p*=The change cannot be shown.")).toBeExisting();
    await expect($("main").$("button=Cancel plan")).not.toBeExisting();
    expect(await writes()).toEqual([]);
  });

  it("an approved plan whose approval is from an earlier epoch says so, and [Cancel plan] sends one cancellation", async function () {
    this.timeout(120_000);
    const approval = `apr_${"a".repeat(26)}`;
    const bodies = {
      [`/plans/${PLAN}`]: synthetic("approved", { approval }),
      [`/approvals/${approval}`]: { id: approval, plan: PLAN, approver: { principal: "h-approver", role: "approver" }, epoch: "ep_before", selfApproval: { marked: false, reasons: [] }, unseenOverwrite: false },
      [`/plans/${PLAN}/cancellations`]: { plan: PLAN, machine, cancelledBy: "h-publisher", role: "publisher", reason: "synthetic reason", act: "cancel", epoch: "ep_synthetic", at: T0 },
    };
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at, bodies })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/${PLAN}`);
    await expect($('[data-testid="plan-old-approval"]')).toHaveText("This approval is from before the last recovery entry and authorizes nothing. An approver may approve again.", { wait: 20_000 });
    await expect($("main").$("p*=Cancelling after the controller commits ends the operation only while no attempt is recorded.")).toBeExisting();
    // A reason is required: an empty one sends nothing.
    await $("main").$("button=Cancel plan").click();
    await expect($('[data-testid="cancel-input"]')).toHaveText("Give a reason.");
    expect(await writes()).toEqual([]);
    await $("main").$('input[name="reason"]').setValue("synthetic reason");
    await $("main").$("button=Cancel plan").click();
    await expect($('[data-testid="write-result"]')).toHaveText("Cancellation recorded.", { wait: 60_000 });
    expect(await writes()).toEqual([`POST /plans/${PLAN}/cancellations`]);
  });
});

afterEach(storageEmpty);
