// Approval and revocation on the plan page (ginsys/bronzeward#358), against the disposable
// deployment. It has no release and no plan, so the plan, the release and the approval are bodies the
// injector serves, and what the page sends is asserted at the injector (one POST, its route, its
// answer). Real server bodies for a release and plans come with ginsys/bronzeward#822, and the cases
// that need a committed approval (AMB-05, AMB-07) wait for them. REC-03's first case is real: the
// deployment restarts for recovery on the same database, its entry is made outside the page, and the
// plan creation page, still open, sends its write. The refusal carries the entry's epoch, whose
// re-read remounts the page (client contracts section 8.5 item 1); the refusal must still be shown.
// Every value is synthetic.
import { $, browser, expect } from "@wdio/globals";
import { REVOKE_LIMIT, UNPREDICTED } from "../../src/approval.tsx";
import { short } from "../../src/list.tsx";
import { RECOVERY_START } from "../../src/recovery.tsx";
import { SCOPE_NO_MODE, SET_ASIDE } from "../../src/states.tsx";
import { FIXTURE_PAGE } from "../auth.conf.ts";
import { type Faults, INJECTED_SCOPE, type Step } from "../injector.ts";
import { answers, disposable, faults, fixtureSignIn, mint, storageEmpty, syntheticMachine, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
const PLAN = `pln_${"q".repeat(26)}`;
const REL = `rel_${"i".repeat(26)}`;
const DRAFT = `drf_${"d".repeat(26)}`;
const APR = `apr_${"a".repeat(26)}`;
const T0 = "2026-11-12T08:00:00Z";
const approvals = `/plans/${PLAN}/approvals`;
const revocations = `/approvals/${APR}/revocations`;

describe("approval and revocation (#358)", () => {
  let at = "";
  let machine = "";

  before(async () => {
    at = await disposable("start=normal");
    machine = await syntheticMachine(at);
  });

  const plan = (state: string, more: Record<string, unknown> = {}) => ({
    id: PLAN, revision: 1, state, committedOperation: null, approval: null, cluster: `cls_${"c".repeat(26)}`, machine, release: REL, operation: "adopt",
    mode: null, assignmentRevision: `asr_${"s".repeat(26)}`, desiredRelease: REL, baselineRevision: 4, driftRecord: null, route: "192.0.2.1:50000",
    maxObservationAgeSeconds: 300, checkValiditySeconds: 120, transportDeadlineSeconds: null, verificationDeadlineSeconds: null, maxAttempts: null, rolloutLimit: 1,
    approvalPolicy: "one-approver", createdBy: { principal: "prn_synthetic_creator", role: "publisher" }, epoch: "ep_synthetic", timelineRevision: 7, createdAt: T0, expiresAt: "2026-11-13T08:00:00Z",
    evidence: { diff: null, validation: { result: "passed", at: "publication", contract: "v1.14", kubernetesVersion: "v1.34.1", machineryVersion: "v1.14.0", platformMode: "metal" }, dryRun: { available: false, reason: "the PoC has none" }, baseline: null },
    ...more,
  });
  const release = { id: REL, cluster: `cls_${"c".repeat(26)}`, draft: DRAFT, draftRevision: 1, operation: "adopt", publishedBy: { principal: "prn_synthetic_publisher", role: "publisher" }, publishedAt: T0, renderer: {}, sources: [], machines: [] };
  const draft = { id: DRAFT, cluster: `cls_${"c".repeat(26)}`, title: "synthetic", state: "published", revision: 3, entries: [] };
  const approval = (reasons: string[] = [], more: Record<string, unknown> = {}) => ({ id: APR, plan: PLAN, approver: { principal: "prn_synthetic_approver", role: "approver" }, epoch: "ep_synthetic", selfApproval: { marked: reasons.length > 0, reasons }, unseenOverwrite: false, ...more });
  const revocation = { approval: APR, plan: PLAN, machine, revokedBy: "prn_synthetic_approver", role: "approver", reason: "synthetic reason", act: `act_${"v".repeat(26)}`, epoch: "ep_synthetic", at: T0 };

  // Signs in as `human` with these bodies and opens the plan page.
  async function open(human: string, bodies: Record<string, unknown>, state = "proposed", more: Faults = {}) {
    expect(await fixtureSignIn(human, {}, { upstream: at, bodies, ...more })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/${PLAN}`);
    await expect($("main h2")).toHaveText(`Plan ${short(PLAN)}: ${state}`, { wait: 20_000 });
  }
  const approve = () => $('main form[aria-label="Approve plan"]').$("button=Approve");
  const typed = () => $('main form[aria-label="Approve plan"] input[name="plan"]');
  const posts = async () => (await writes()).filter((w) => w.startsWith("POST "));

  it("a proposed plan shows its prediction; [Approve] waits for the typed plan ID, and a second click sends nothing new", async function () {
    this.timeout(120_000);
    await open("h-all", { [`/plans/${PLAN}`]: plan("proposed"), [`/releases/${REL}`]: release, [`/drafts/${DRAFT}`]: draft, [approvals]: approval(["created-plan"]) });
    await expect($('[data-testid="self-approval"]')).toHaveText(
      `Self-approval, predicted: you did not create this plan or publish this release, and you authored no fragment, profile or assignment revision in it. ${UNPREDICTED}`,
      { wait: 20_000 },
    );
    expect(await approve().isEnabled()).toBe(false);
    await typed().setValue(PLAN.slice(0, -1));
    expect(await approve().isEnabled()).toBe(false);
    await typed().setValue(PLAN);
    expect(await approve().isEnabled()).toBe(true);
    await approve().click();
    await browser.execute(() => document.querySelector<HTMLFormElement>('main form[aria-label="Approve plan"]')?.requestSubmit());
    // The reasons are the returned approval's, and the controller's next step is named.
    await expect($("main")).toHaveText(expect.stringContaining("Recorded self-approval reasons: created-plan."), { wait: 20_000 });
    await expect($("main")).toHaveText(expect.stringContaining(`Applied becomes ${short(REL)} (adoption) once it does.`));
    // The injected plan still reads as proposed after the reread, so the form stays: a forced submit
    // now, after the approval was recorded, must send nothing.
    await browser.execute(() => document.querySelector<HTMLFormElement>('main form[aria-label="Approve plan"]')?.requestSubmit());
    await browser.pause(500);
    expect(await posts()).toEqual([`POST ${approvals}`]);
  });

  it("a release that was not read leaves the prediction partial, never clear of self-approval", async function () {
    this.timeout(60_000);
    await open("h-all", { [`/plans/${PLAN}`]: plan("proposed") });
    await expect($('[data-testid="self-approval"]')).toHaveText(
      expect.stringContaining(`Self-approval, predicted: you did not create this plan. Not predicted, since this read failed: release ${short(REL)}.`),
      { wait: 20_000 },
    );
    expect(await posts()).toEqual([]);
  });

  it("an apply-config plan bound to a drift record names the overwrite no reviewer has seen", async function () {
    this.timeout(60_000);
    await open("h-all", { [`/plans/${PLAN}`]: plan("proposed", { operation: "apply-config", mode: "auto", driftRecord: 4, transportDeadlineSeconds: 60, verificationDeadlineSeconds: 120, maxAttempts: 3 }) });
    await expect($('[data-testid="unseen-overwrite"]')).toHaveText(expect.stringContaining("Overwrites the drifted configuration of drift record 4, which no reviewer has seen."), { wait: 20_000 });
  });

  it("h-viewer: [Approve] is disabled with its reason; a forced submit reaches the server, whose refusal is shown", async function () {
    this.timeout(60_000);
    await open("h-viewer", { [`/plans/${PLAN}`]: plan("proposed") });
    await typed().setValue(PLAN);
    expect(await approve().isEnabled()).toBe(false);
    await expect($('main form[aria-label="Approve plan"]')).toHaveText(expect.stringContaining("Needs approver"));
    await browser.execute(() => document.querySelector<HTMLFormElement>('main form[aria-label="Approve plan"]')!.requestSubmit());
    await expect($('[data-testid="write-result"]')).toHaveText(expect.stringMatching(/^Not saved: /), { wait: 20_000 });
    expect(await posts()).toEqual([`POST ${approvals}`]);
    expect((await answers()).filter((a) => a.startsWith(`${approvals} `))).toEqual([`${approvals} 403`]);
  });

  it("an approved plan: a reason is required; [Revoke approval] sends one revocation and states the limit on the control", async function () {
    this.timeout(120_000);
    const bodies = { [`/plans/${PLAN}`]: plan("approved", { approval: APR }), [`/approvals/${APR}`]: approval(), [revocations]: revocation };
    await open("h-all", bodies, "approved");
    await expect($('[data-testid="revoke-limit"]')).toHaveText(REVOKE_LIMIT);
    await $("main").$("button=Revoke approval").click();
    await expect($('[data-testid="revoke-input"]')).toHaveText("Give a reason.");
    expect(await posts()).toEqual([]);
    await $('main form[aria-label="Revoke approval"] input[name="reason"]').setValue("synthetic reason");
    await $("main").$("button=Revoke approval").click();
    await expect($("main")).toHaveText(expect.stringContaining(`recorded by ${short("prn_synthetic_approver")}`), { wait: 20_000 });
    expect(await posts()).toEqual([`POST ${revocations}`]);
  });

  it("a revoked plan shows the reason its revocation recorded on the machine's timeline", async function () {
    this.timeout(60_000);
    const entry = { revision: 9, epoch: "ep_synthetic", at: T0, kind: "approval-revocation", entry: { approval: APR, plan: PLAN, principal: "prn_synthetic_approver", role: "approver", reason: "synthetic reason" } };
    await open("h-all", { [`/plans/${PLAN}`]: plan("revoked", { approval: APR }), [`/approvals/${APR}`]: approval(), [`/machines/${machine}/timeline`]: { items: [entry] } }, "revoked");
    await expect($('[data-testid="revocation-reason"]')).toHaveText(`Revoked by ${short("prn_synthetic_approver")}: synthetic reason`, { wait: 20_000 });
  });

  // The first send of an approval is dropped, then the epoch changes: the repeat carries the epoch it
  // was made under and the server refuses it 412, so the write is set aside, unrepeated (AMB-12). The
  // dropped send never committed anything, so no approval record exists and the repeat is refused.
  async function approving(take: Step[]) {
    await open("h-all", { [`/plans/${PLAN}`]: plan("proposed"), [`/releases/${REL}`]: release, [`/drafts/${DRAFT}`]: draft }, "proposed", { steps: { path: approvals, take } });
    await typed().setValue(PLAN);
    await approve().click();
  }

  it("AMB-12 on the approval: no record of the first attempt, the repeat is refused 412 and set aside", async function () {
    this.timeout(90_000);
    await approving(["drop", "epoch"]);
    await expect($('[data-testid="write-notice"]')).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 30_000 });
    expect((await answers()).filter((a) => a.startsWith(`${approvals} `))).toEqual([`${approvals} 412`]);
    const sent = (await writes()).filter((w) => w === `POST ${approvals}`);
    expect(sent).toHaveLength(2);
  });

  it("REC-03 (scope): an approval refused 409 recovery-mode-active with a scope names that scope and says nothing about the start mode; sent once", async function () {
    this.timeout(60_000);
    await approving(["scope-refused"]);
    await expect($('[data-testid="write-result"]')).toHaveText(expect.stringContaining(`Scope ${INJECTED_SCOPE} refuses this request. ${SCOPE_NO_MODE}`), { wait: 20_000 });
    expect(await posts()).toEqual([`POST ${approvals}`]);
    expect(await $('[data-testid="recovery"]').isExisting()).toBe(false);
    expect(await $("body").getText()).not.toContain(RECOVERY_START);
  });

  it("REC-03, first case: plan creation on a recovery start after its entry is refused without a scope and read as a recovery start", async function () {
    this.timeout(120_000);
    at = await disposable("start=normal");
    machine = await syntheticMachine(at);
    expect(await fixtureSignIn("h-publisher", {}, { upstream: at })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/new?${new URLSearchParams({ release: REL, machine, operation: "adopt" })}`);
    await expect($("main h2")).toHaveText("Create a plan", { wait: 20_000 });
    // The deployment restarts for recovery on the same database, and its entry is made outside the
    // page while the page stays open.
    at = await disposable("start=recovery&keep");
    await faults({ upstream: at });
    const now = new Date(Date.now() - 60_000).toISOString();
    const entry = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-03-${Date.now()}-entry` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-rec-03", takenAt: now }], stoppedAt: now, reason: "REC-03 synthetic restore" }),
    });
    expect(entry.status).toBe(201);
    await $("main").$("button=Create plan").click();
    // The refusal carries the entry's epoch, so the shell reads again and remounts the page. The
    // refusal is still shown, in the set-aside notice outside the page: the write's outcome is not
    // lost with the page.
    await expect($("body")).toHaveText(expect.stringContaining("Not saved: the server answered 409. Recovery mode refuses the act on this scope"), { wait: 20_000 });
    expect((await answers()).filter((a) => a.startsWith("/plans "))).toEqual(["/plans 409"]);
    await expect($('[data-testid="recovery"]')).toHaveText(expect.stringContaining(RECOVERY_START), { wait: 20_000 });
  });
});

afterEach(storageEmpty);
