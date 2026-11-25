// The plan creation page and the plan page's state lines (ginsys/bronzeward#357, B19, B20, B22) against the disposable deployment. A plain
// deployment has no Talos node, so no release and no plan exists on it. The first case runs the real path
// that needs none: a plan for a release the server does not know is refused, the page shows the refusal,
// and the server holds no plan afterwards. The cases of the seeded describe read the records a seeded
// deployment holds (ginsys/bronzeward#822: a real release and one plan in each state, made by the API
// tests' own helpers): the creation of a plan, an expired plan and a cancellation. One case stays
// synthetic: an approved plan whose approval is from an earlier epoch needs a recovery entry after the
// approval, which the seed does not make, so the injector serves its bodies. The server's own proof
// of the 201, and that a plan is created only for a Desired release with an Applied configuration, is
// internal/api/plans_test.go; the page's next steps are unit tests in plan.test.ts.
// The cases sign in as h-publisher.
import { $, browser, expect } from "@wdio/globals";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { absent, api, approvalReadsAnswered, approvalRoute, fixtureSignIn, type Seed, seeded, storageEmpty, syntheticMachine, writes } from "./helpers.ts";

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

  it("a plan for a release the server does not know is refused and saved nowhere [step plan-and-approval-1.1]", async function () {
    this.timeout(120_000);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at })).toMatch(SERVER);
    const unknown = `rel_${"y".repeat(26)}`;
    await browser.url(`${FIXTURE_PAGE}/plans/new?${new URLSearchParams({ release: unknown, machine, operation: "apply-config" })}`);
    await expect($("main h2")).toHaveText("Create a plan", { wait: 20_000 });
    await $("main").$("button=Create plan").click();
    await expect($('[data-testid="write-result"]')).toHaveText(expect.stringMatching(/^Not saved[:.] /), { wait: 60_000 });
    // The refusal is shown with the server's own words and the button stays available for a corrected plan.
    await expect($("main").$("button=Create plan")).toBeEnabled();
    // The refusal belongs to apply-config: choosing adopt hides it.
    await $("main").$("select").selectByAttribute("value", "adopt");
    // once: the refusal was shown and awaited above; the matcher waits for its removal on the change of the operation, and no write is in flight to draw it again.
    await expect($('[data-testid="write-result"]')).not.toBeExisting();
    expect((await writes()).filter((w) => w.startsWith("POST "))).toEqual(["POST /plans"]);
    const plans = await api(at, "h-viewer", "GET", "/plans");
    expect(plans.status).toBe(200);
    // once: an API body read after the one refused write was answered (awaited above), and nothing else sends.
    expect(plans.body!["items"]).toEqual([]);
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
    // The approval is from an earlier epoch, so the page offers a new approval.
    await expect($('main form[aria-label="Approve plan"]')).toBeExisting();
    await expect($("main").$("p*=Cancelling after the controller commits ends the operation only while no attempt is recorded.")).toBeExisting();
    // A reason is required: an empty one sends nothing.
    await $("main").$("button=Cancel plan").click();
    await expect($('[data-testid="cancel-input"]')).toHaveText("Give a reason.");
    await absent(async () => (await writes()).length > 0, "a cancellation sent without a reason");
    // The page also carries the revocation's reason field, so the cancellation's own form is named.
    await $('main form[aria-label="Cancel plan"] input[name="reason"]').setValue("synthetic reason");
    await $("main").$("button=Cancel plan").click();
    await expect($('[data-testid="write-result"]')).toHaveText("Cancellation recorded.", { wait: 60_000 });
    expect(await writes()).toEqual([`POST /plans/${PLAN}/cancellations`]);
  });
});

// The same pages against the records a seeded deployment holds (ginsys/bronzeward#822): the server's
// own bodies, so the injector serves nothing and the page sends its writes to the real routes.
describe("the plan pages on a seeded deployment (#822)", () => {
  let sat = "";
  let seed: Seed;

  before(async () => {
    ({ at: sat, seed } = await seeded());
  });

  it("a created plan says it is not approved and opens the plan; one POST is sent and the server holds it [step plan-and-approval-1.1] [step plan-and-approval-1.2]", async function () {
    this.timeout(120_000);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: sat })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/new?${new URLSearchParams({ release: seed.release, machine: seed.machine2, operation: "apply-config" })}`);
    await expect($("main h2")).toHaveText("Create a plan", { wait: 20_000 });
    // An apply-config plan is offered its deadlines and attempt limit.
    await expect($("main").$("label*=Transport deadline")).toBeExisting();
    await expect($("main").$("label*=Attempt limit")).toBeExisting();
    await $("main").$("button=Create plan").click();
    await expect($('[data-testid="plan-proposed"]')).toHaveText("Plan proposed. It is not approved, and nothing runs until an approver approves.", { wait: 60_000 });
    await expect($(`main a[title="${seed.machine2}"]`)).toBeExisting();
    expect((await writes()).filter((w) => w.startsWith("POST "))).toEqual(["POST /plans"]);
    await expect($("main h2")).toHaveText(expect.stringMatching(/^Plan(: .+ \(proposed\)| \S+: proposed)$/), { wait: 20_000 });
  });

  it("W3's timeline links the plan and the operation a commitment names, and the operation's link opens its page (#359) [step operation-tracking.1]", async function () {
    this.timeout(120_000);
    expect(await fixtureSignIn("h-viewer", {}, { upstream: sat })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/machines/${seed.machine}`);
    const timeline = $('[data-testid="timeline"]');
    // Several entries name the plan and the operation (approval, commitment, state changes): each one links.
    await browser.waitUntil(async () => (await timeline.$$(`a[href="/plans/${seed.plans.committed}"]`)).length >= 1, { timeout: 20_000, timeoutMsg: "no timeline row links the committed plan" });
    const operations = await timeline.$$(`a[href="/operations/${seed.operation}"]`);
    expect(operations.length).toBeGreaterThanOrEqual(1);
    await operations[0]!.click();
    await browser.waitUntil(async () => (await browser.getUrl()) === `${FIXTURE_PAGE}/operations/${seed.operation}`, { timeout: 20_000, timeoutMsg: "the operation's link did not open its page" });
    await expect($('[data-testid="events"]')).toBeExisting({ wait: 30_000 });
  });

  it("an expired plan shows its state and a link to a new plan, and the page sends nothing", async function () {
    this.timeout(120_000);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: sat })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/${seed.plans.expired}`);
    await expect($('[data-testid="plan-terminal"]')).toHaveText(expect.stringContaining("expired"), { wait: 20_000 });
    await expect($('[data-testid="plan-terminal"] a')).toHaveAttribute("href", `/plans/new?${new URLSearchParams({ release: seed.release, machine: seed.machine2, operation: "apply-config" })}`);
    // once: the control is drawn from the plan's state in the render that drew the terminal line awaited above (web/src/resources.tsx:383, web/src/plan.tsx:233).
    await expect($("main").$("button=Cancel plan")).not.toBeExisting();
    // once: the expired plan's page has no control that sends a write (the Cancel plan form is absent, above), and the case clicks nothing.
    expect(await writes()).toEqual([]);
  });

  it("an approved plan: a reason is required, and [Cancel plan] sends one cancellation the server records [step plan-and-approval-1.4]", async function () {
    this.timeout(120_000);
    const route = `/plans/${seed.plans.approved}/cancellations`;
    // The automation identity made the seeded plan, and a publisher cancels only its own plans.
    // The approval's answers are held until the page has asked for every one of them, so the wait below ends only after
    // the reads that decide the Approve form have been answered.
    const held = await approvalRoute(sat, seed.plans.approved);
    expect(await fixtureSignIn("h-all", {}, { upstream: sat, held })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/${seed.plans.approved}`);
    await approvalReadsAnswered(held, sat);
    await expect($("main h2")).toHaveText(expect.stringMatching(/^Plan(: .+ \(approved\)| \S+: approved)$/), { wait: 20_000 });
    // The approval is of the current epoch, so the page offers no new approval. The form is drawn only once the approval's
    // read and the identity read answer (EarlierEpoch, web/src/approval.tsx:158). The wait above returned after the page
    // took every answer to both, three each, so the form's absence is read after the reads that could draw it.
    await $('[data-testid="approval-record"]').waitForExist({ timeout: 20_000 });
    await $('[data-testid="principal"]').waitForExist({ timeout: 20_000 });
    await absent(() => $('main form[aria-label="Approve plan"]').isExisting(), "the Approve form on a plan approved in the current epoch");
    await $("main").$("button=Cancel plan").click();
    await expect($('[data-testid="cancel-input"]')).toHaveText("Give a reason.");
    await absent(async () => (await writes()).length > 0, "a cancellation sent without a reason");
    await $('main form[aria-label="Cancel plan"] input[name="reason"]').setValue("seeded reason");
    await $("main").$("button=Cancel plan").click();
    // The page reads the plan again and shows it cancelled, which removes the write's own result.
    await expect($("main h2")).toHaveText(expect.stringMatching(/^Plan(: .+ \(cancelled\)| \S+: cancelled)$/), { wait: 60_000 });
    expect(await writes()).toEqual([`POST ${route}`]);
    expect((await api(sat, "h-viewer", "GET", `/plans/${seed.plans.approved}`)).body!["state"]).toBe("cancelled");
  });
});

afterEach(storageEmpty);
