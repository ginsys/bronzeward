// Approval and revocation on the plan page (ginsys/bronzeward#358), against the disposable
// deployment. A plain one has no release and no plan, so the cases of the first describe serve the
// plan, the release and the approval as bodies of the injector, and assert what the page sends at the
// injector (one POST, its route, its answer): they need a read that fails, a drift record or a plan
// that stays proposed after its approval, none of which a seeded record is. The cases of the seeded
// describe read the records a seeded deployment holds (ginsys/bronzeward#822, made by the API tests'
// own helpers) and send to the real routes: the forced submit of a viewer, a revocation, the faulted
// approvals (AMB-05, AMB-07, AMB-12, REC-03's scope variant) and a revoked plan. REC-03's first case is real: the
// deployment restarts for recovery on the same database, its entry is made outside the page, and the
// plan creation page, still open, sends its write. The refusal carries the entry's epoch, whose
// re-read remounts the page (client contracts section 8.5 item 1); the refusal must still be shown.
// Every value is synthetic.
import { $, browser, expect } from "@wdio/globals";
import { REVOKE_LIMIT, UNPREDICTED } from "../../src/approval.tsx";
import { short } from "../../src/list.tsx";
import { RECOVERY_START } from "../../src/recovery.tsx";
import { SCOPE_NO_MODE, SET_ASIDE } from "../../src/states.tsx";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { type Faults, INJECTED_SCOPE, type Step } from "../injector.ts";
import { absent, answers, api, approvalReadsAnswered, approvalRoute, disposable, eventually, faults, fixtureSignIn, focusIn, focusKept, focusSettles, fromTitle, inSafari, measure, mint, mirrored, narrow, overflows, planHeading, type Seed, seeded, storageEmpty, syntheticMachine, tabTo, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
// The page's answer to Approve and to Revoke approval: each control's Result, in an element that takes
// focus once the control that sent it is disabled and the page changes under it (2.4.3).
const ANSWER = '[data-testid="approval-answer"]';
const REVOKE_ANSWER = '[data-testid="revoke-answer"]';
const statusRegions = () => browser.execute(() => document.querySelectorAll('[role="status"]').length);
const PLAN = `pln_${"q".repeat(26)}`;
const REL = `rel_${"i".repeat(26)}`;
const DRAFT = `drf_${"d".repeat(26)}`;
const APR = `apr_${"a".repeat(26)}`;
const T0 = "2026-11-12T08:00:00Z";
const approvals = `/plans/${PLAN}/approvals`;

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

  // The identity the server answers `human`, with a principal: a human with no row yet has none, and the
  // prediction then claims nothing. The roles and the epoch stay the server's own.
  async function identity(human: string): Promise<Record<string, unknown>> {
    const res = await fetch(`${at}/api/v1/identity`, { headers: { Authorization: `Bearer ${mint(human)}` } });
    expect(res.ok).toBe(true);
    return { ...((await res.json()) as Record<string, unknown>), principal: "prn_synthetic_me" };
  }

  // Signs in as `human` with these bodies (none: the server's own) and opens the plan page of `id`.
  async function open(human: string, bodies: Record<string, unknown> | undefined, state = "proposed", more: Faults = {}, id = PLAN) {
    expect(await fixtureSignIn(human, {}, { upstream: at, ...(bodies === undefined ? {} : { bodies }), ...more })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/plans/${id}`);
    await planHeading(id, state);
  }
  const approve = () => $('main form[aria-label="Approve plan"]').$("button=Approve");
  const typed = () => $('main form[aria-label="Approve plan"] input[name="plan"]');
  const posts = async () => (await writes()).filter((w) => w.startsWith("POST "));

  it("a proposed plan shows its prediction; [Approve] waits for the typed plan ID, and a second click sends nothing new", async function () {
    this.timeout(120_000);
    await open("h-all", { "/identity": await identity("h-all"), [`/plans/${PLAN}`]: plan("proposed"), [`/releases/${REL}`]: release, [`/drafts/${DRAFT}`]: draft, [approvals]: approval(["created-plan"]) });
    await expect($('[data-testid="self-approval"]')).toHaveText(
      `Self-approval, predicted: you did not create this plan or publish this release, and you authored no fragment, profile or assignment revision in it. ${UNPREDICTED}`,
      { wait: 20_000 },
    );
    // once: the gate is `busy || typed !== p.id` (web/src/approval.tsx:143), false from the first render until the full ID is typed.
    expect(await approve().isEnabled()).toBe(false);
    await typed().setValue(PLAN.slice(0, -1));
    // once: same gate: a short ID differs from the plan ID, and typing is synchronous.
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
    await open("h-all", { "/identity": await identity("h-all"), [`/plans/${PLAN}`]: plan("proposed") });
    await expect($('[data-testid="self-approval"]')).toHaveText(
      expect.stringContaining(`Self-approval, predicted: you did not create this plan. Not predicted, since this read failed: release ${short(REL)}.`),
      { wait: 20_000 },
    );
    // once: no step of the case submits, and the page sends a write only from a submit (web/src/approval.tsx:132-135).
    expect(await posts()).toEqual([]);
  });

  it("an apply-config plan bound to a drift record names the overwrite no reviewer has seen", async function () {
    this.timeout(60_000);
    await open("h-all", { [`/plans/${PLAN}`]: plan("proposed", { operation: "apply-config", mode: "auto", driftRecord: 4, transportDeadlineSeconds: 60, verificationDeadlineSeconds: 120, maxAttempts: 3 }) });
    await expect($('[data-testid="unseen-overwrite"]')).toHaveText(expect.stringContaining("Overwrites the drifted configuration of drift record 4, which no reviewer has seen."), { wait: 20_000 });
  });

  // T8 (journeys section 13), the order: an approver reaches the change before the approval's typed ID field (2.4.3),
  // every focus Tab reaches shows an outline (2.4.7), and the page measures as the other views do. The unified text is
  // synthetic; a long line makes the change scroll inside its own focusable region at 320 CSS pixels (1.4.10).
  it("keyboard only (T8): the change is reached before the typed plan ID, and the page measures", async function () {
    this.timeout(120_000);
    const unified = `@@ machine.nodeLabels @@\n+  team: storage\n+  note: ${"x".repeat(240)}`;
    const evidence = { ...plan("proposed").evidence, diff: { from: `rel_${"j".repeat(26)}`, to: REL, withheld: false, unified } };
    await open("h-all", { "/identity": await identity("h-all"), [`/plans/${PLAN}`]: plan("proposed", { operation: "apply-config", mode: "no-reboot", transportDeadlineSeconds: 60, verificationDeadlineSeconds: 120, maxAttempts: 3, evidence }), [`/releases/${REL}`]: release, [`/drafts/${DRAFT}`]: draft });
    await expect($('[data-testid="diff"]')).toBeExisting({ wait: 20_000 });
    await fromTitle();
    const before = await tabTo((f) => f.name === "plan", "the typed plan ID field");
    expect(before.some((t) => t.startsWith("@@ machine.nodeLabels @@"))).toBe(true);
    // once: nothing typed yet, so the gate `busy || typed !== p.id` (web/src/approval.tsx:143) holds from the first render.
    expect(await approve().isEnabled()).toBe(false);
    const seen = await measure();
    expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
    expect(seen.small).toBe(0);
    // Safari cannot be narrowed to 320 pixels (the reason is at itNarrow): the rest of the case runs there.
    if (!inSafari()) await narrow(async () => expect(["the plan page with its change", await overflows()]).toEqual(["the plan page with its change", 0]));
    // once: the case only tabs and measures, and the page sends a write only from a submit (web/src/approval.tsx:132-135).
    expect(await posts()).toEqual([]);
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

  // The same page against the records a seeded deployment holds (ginsys/bronzeward#822): the server's
  // own bodies, and the write goes to the real route. Each case gets its own deployment, because a
  // faulted write changes the epoch and an approval changes the plan.
  describe("on the seeded records (#822)", () => {
    let seed: Seed;

    beforeEach(async () => {
      ({ at, seed } = await seeded());
    });

    // A new proposed plan of the seeded release, made by the publisher, so that a human sign-in other
    // than its creator may approve it; opened as h-all (the deployment denies h-approver, AUTH-10) with its ID typed and [Approve] clicked,
    // the first send of the approval taking `take`. Returns the plan and its approval route.
    async function approving(take: Step[]) {
      const made = await api(at, "h-publisher", "POST", "/plans", { releaseId: seed.release, machine: seed.machine2, operation: "apply-config", mode: "no-reboot" });
      expect(made.status).toBe(201);
      const id = made.body!["id"] as string;
      const route = `/plans/${id}/approvals`;
      await open("h-all", undefined, "proposed", { steps: { path: route, take } }, id);
      await typed().setValue(id);
      await approve().click();
      return { id, route };
    }
    // The raw sends of the approval, each "<method> <route> <key> <epoch>".
    const sends = async (route: string) => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).filter((w) => w.startsWith(`POST ${route} `));
    const notice = () => $('[data-testid="write-notice"]');

    // Neither identity holds the approver role (fixtures/oidc/issuer: h-viewer is bw-viewers, h-publisher is bw-publishers). The
    // publisher is the one that can write elsewhere, which ginsys/bronzeward#358's comment of 2026-11-01 asks for.
    for (const human of ["h-viewer", "h-publisher"]) {
      it(`${human}: [Approve] is disabled with its reason; a forced submit reaches the server, whose refusal is shown`, async function () {
        this.timeout(60_000);
        const route = `/plans/${seed.plans.proposed}/approvals`;
        await open(human, undefined, "proposed", {}, seed.plans.proposed);
        await typed().setValue(seed.plans.proposed);
        // once: the button is disabled with its reason in one element, and the reason is read at the next line, so an early read cannot pass here.
        expect(await approve().isEnabled()).toBe(false);
        await expect($('main form[aria-label="Approve plan"]')).toHaveText(expect.stringContaining("Needs approver"));
        await browser.execute(() => document.querySelector<HTMLFormElement>('main form[aria-label="Approve plan"]')!.requestSubmit());
        await expect($('[data-testid="write-result"]')).toHaveText(expect.stringMatching(/^Not saved: /), { wait: 20_000 });
        expect(await posts()).toEqual([`POST ${route}`]);
        expect((await answers()).filter((a) => a.startsWith(`${route} `))).toEqual([`${route} 403`]);
      });
    }

    // T8, the operation: the approver types the plan ID and approves by keyboard. The approval makes the page read the
    // plan again, and the control that had focus is gone with its form; focus must be in the answer once the page reads
    // "approved", not only before it (2.4.3).
    it("keyboard only (T8): typing the plan ID and Approve by keyboard, focus is in the answer once the plan reads approved", async function () {
      this.timeout(120_000);
      const id = seed.plans.proposed;
      await open("h-all", undefined, "proposed", {}, id);
      await fromTitle();
      await tabTo((f) => f.name === "plan", "the typed plan ID field");
      await browser.keys(id);
      // The typed ID is read back before Tab: Approve stays disabled, and Tab skips it, until the field holds
      // the whole ID (ginsys/bronzeward#927 item 4). Only counts and an index leave the page.
      const read = await browser.execute((e: HTMLInputElement, want: string) => ({ typed: e.value.length, of: want.length, firstDifference: [...want].findIndex((c, i) => e.value[i] !== c) }), (await typed()) as unknown as HTMLInputElement, id);
      expect({ ...read, approveEnabled: await approve().isEnabled() }).toEqual({ typed: id.length, of: id.length, firstDifference: -1, approveEnabled: true });
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Approve", "Approve");
      await browser.keys("Enter");
      await planHeading(id, "approved");
      await focusSettles(focusKept, "the page, not the body");
      await focusSettles(() => focusIn(ANSWER), "the approval's answer");
      await eventually(async () => (await mirrored()).some((t) => t.includes("Recorded self-approval reasons")), "the self-approval answer mirrored");
      expect(await statusRegions()).toBe(1);
      expect(await posts()).toEqual([`POST /plans/${id}/approvals`]);
      expect((await api(at, "h-viewer", "GET", `/plans/${id}`)).body!["state"]).toBe("approved");
    });

    it("keyboard only (T8): Revoke approval by keyboard, focus is in the answer once the plan reads revoked", async function () {
      this.timeout(120_000);
      await open("h-all", undefined, "approved", {}, seed.plans.approved);
      await fromTitle();
      await tabTo((f) => f.name === "reason", "the reason field");
      await browser.keys("seeded reason");
      await tabTo((f) => f.tag === "BUTTON" && f.text === "Revoke approval", "Revoke approval");
      await browser.keys("Enter");
      await expect($("main h2")).toHaveText(expect.stringMatching(/^Plan(: .+ \(revoked\)| \S+: revoked)$/), { wait: 20_000 });
      await focusSettles(focusKept, "the page, not the body");
      await focusSettles(() => focusIn(REVOKE_ANSWER), "the revocation's answer");
      await expect($('[data-testid="revocation-reason"]')).toHaveText(expect.stringMatching(/^Revoked by h-\S+ at \S+: seeded reason$/), { wait: 20_000 });
      expect(await posts()).toEqual([expect.stringMatching(/^POST \/approvals\/apr_\w+\/revocations$/)]);
    });

    // A revocation after the controller committed is offered, with its limit on the control (ginsys/bronzeward#358, row 2).
    it("a committed plan offers [Revoke approval] with the limit that it does not stop an attempt already recorded", async function () {
      this.timeout(60_000);
      await open("h-all", undefined, "committed", {}, seed.plans.committed);
      await expect($('[data-testid="revoke-limit"]')).toHaveText(REVOKE_LIMIT);
      expect(await $("main").$("button=Revoke approval").isEnabled()).toBe(true);
      // once: the case clicks nothing, and the page sends a write only from a submit (web/src/approval.tsx:186).
      expect(await posts()).toEqual([]);
    });

    it("an approved plan: a reason is required; [Revoke approval] sends one revocation, states the limit on the control, and the server revokes the plan", async function () {
      this.timeout(120_000);
      // The approval's answers are held until the page has asked for every one of them, so the wait below ends only after
      // the reads that decide the Approve form have been answered.
      const route = await approvalRoute(at, seed.plans.approved);
      expect(await fixtureSignIn("h-all", {}, { upstream: at, held: route })).toMatch(SERVER);
      await browser.url(`${FIXTURE_PAGE}/plans/${seed.plans.approved}`);
      await approvalReadsAnswered(route, at);
      await planHeading(seed.plans.approved, "approved");
      await expect($('[data-testid="revoke-limit"]')).toHaveText(REVOKE_LIMIT);
      // The approval is of the current epoch, so the page offers no new one. The form is drawn only once the approval's
      // read and the identity read answer (EarlierEpoch, web/src/approval.tsx:158). The wait above returned after the page
      // took every answer to both, three each, so the form's absence is read after the reads that could draw it.
      await $('[data-testid="approval-record"]').waitForExist({ timeout: 20_000 });
      await $('[data-testid="principal"]').waitForExist({ timeout: 20_000 });
      await absent(() => $('main form[aria-label="Approve plan"]').isExisting(), "the Approve form on a plan approved in the current epoch");
      await $("main").$("button=Revoke approval").click();
      await expect($('[data-testid="revoke-input"]')).toHaveText("Give a reason.");
      await absent(async () => (await posts()).length > 0, "a revocation sent without a reason");
      await $('main form[aria-label="Revoke approval"] input[name="reason"]').setValue("seeded reason");
      await $("main").$("button=Revoke approval").click();
      // The page reads the plan again and shows it revoked, with the reason on the machine's timeline.
      await expect($("main h2")).toHaveText(expect.stringMatching(/^Plan(: .+ \(revoked\)| \S+: revoked)$/), { wait: 20_000 });
      await expect($('[data-testid="revocation-reason"]')).toHaveText(expect.stringMatching(/^Revoked by h-\S+ at \S+: seeded reason$/), { wait: 20_000 });
      expect(await posts()).toEqual([expect.stringMatching(/^POST \/approvals\/apr_\w+\/revocations$/)]);
      expect((await api(at, "h-viewer", "GET", `/plans/${seed.plans.approved}`)).body!["state"]).toBe("revoked");
    });

    it("a revoked plan shows the reason its revocation recorded on the machine's timeline", async function () {
      this.timeout(60_000);
      await open("h-all", undefined, "revoked", {}, seed.plans.revoked);
      await expect($('[data-testid="revocation-reason"]')).toHaveText(expect.stringMatching(/^Revoked by h-\S+ at \S+: seeded$/), { wait: 20_000 });
    });

    // AMB-05: the first send is committed and its answer dropped, then the epoch changes. The repeat
    // is never sent: the page reads the recovery state first, sees another epoch and sets the write aside.
    it("AMB-05 on the approval: committed, its reply lost, a new epoch: set aside unrepeated, and the server holds the approval", async function () {
      this.timeout(90_000);
      const { id, route } = await approving(["commit-drop-epoch"]);
      await expect(notice()).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 30_000 });
      expect(await posts()).toEqual([`POST ${route}`]);
      expect((await answers()).filter((a) => a.startsWith(`${route} `))).toEqual([`${route} 201`]);
      // once: "Repeat exactly" is drawn disabled in the same block as the notice text awaited above (web/src/states.tsx:167-179).
      expect(await notice().parentElement().$("button=Repeat exactly").isEnabled()).toBe(false);
      expect((await api(at, "h-viewer", "GET", `/plans/${id}`)).body!["state"]).toBe("approved");
    });

    // AMB-07: the first send is committed and its answer dropped, and the epoch changes after the
    // confirming read. The repeat carries the first attempt's key and meets its record: 409.
    it("AMB-07 on the approval: a repeat after the commit and a new epoch meets the earlier record: 409, set aside, no new key", async function () {
      this.timeout(90_000);
      const { route } = await approving(["commit-drop", "epoch"]);
      await expect(notice()).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 30_000 });
      await expect(notice()).toHaveText(expect.stringMatching(/The key's earlier record: request \S+, epoch \S+\./));
      expect((await answers()).filter((a) => a.startsWith(`${route} `))).toEqual([`${route} 201`, `${route} 409`]);
      const sent = await sends(route);
      expect(sent).toHaveLength(2);
      expect(sent[0]).toMatch(/^POST \S+ [A-Za-z0-9_-]{22} \S+$/);
      expect(sent[1]!.split(" ")[2]).toBe(sent[0]!.split(" ")[2]);
    });

    // AMB-12: the first send is dropped before the server read it, then the epoch changes: the repeat
    // carries the epoch it was made under and the server refuses it 412, so the write is set aside.
    it("AMB-12 on the approval: no record of the first attempt, the repeat is refused 412 and set aside", async function () {
      this.timeout(90_000);
      const { route } = await approving(["drop", "epoch"]);
      await expect(notice()).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 30_000 });
      expect((await answers()).filter((a) => a.startsWith(`${route} `))).toEqual([`${route} 412`]);
      // The repeat carries the first attempt's key and epoch.
      const sent = await sends(route);
      expect(sent).toHaveLength(2);
      expect(sent[0]).toMatch(/^POST \S+ [A-Za-z0-9_-]{22} \S+$/);
      expect(sent[1]).toBe(sent[0]);
    });

    it("REC-03 (scope): an approval refused 409 recovery-mode-active with a scope names that scope and says nothing about the start mode; sent once", async function () {
      this.timeout(60_000);
      const { route } = await approving(["scope-refused"]);
      await expect($('[data-testid="write-result"]')).toHaveText(expect.stringContaining(`Scope ${INJECTED_SCOPE} refuses this request. ${SCOPE_NO_MODE}`), { wait: 20_000 });
      expect(await posts()).toEqual([`POST ${route}`]);
      await absent(() => $('[data-testid="recovery"]').isExisting(), "the recovery banner after a scope's refusal");
      await absent(async () => (await $("body").getText()).includes(RECOVERY_START), "the recovery start text after a scope's refusal");
    });
  });
});

afterEach(storageEmpty);
