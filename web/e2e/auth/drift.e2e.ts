// The drift page of a machine (ginsys/bronzeward#363; wireframe W9, journeys §5.8; acceptance plan S5), against the
// disposable deployment's seeded records. Every case reads real records and sends to the real routes: the deployment's
// detector runs every 5 minutes and its Talos stand-ins' configuration is fixed, so no read differs from Applied, and the
// cases that need an open drift record ask the harness to open one (POST /drift, TestE2EDrift in internal/api, the helper
// the API tests use). A plan, an approval, a freeze and an act are the server's own. The adoption itself (the wizard's
// start and its review, against a node) is the fixture walk of the same issue; here [Start adoption] is followed as far
// as the wizard opening as a drift adoption. Every value is synthetic.
import { $, browser, expect } from "@wdio/globals";
import { PLAN_FROZEN } from "../../src/plan.tsx";
import { SCOPE_NO_MODE } from "../../src/states.tsx";
import { DRIFT_NONE } from "../../src/words.ts";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { INJECTED_SCOPE, type Faults } from "../injector.ts";
import { absent, answers, api, fixtureSignIn, fromTitle, itNarrow, measure, narrow, overflows, references, seeded, storageEmpty, tab, type Seed, where, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
const CONTROLS = 'main a[href], main button, main input, main select, main textarea, main [tabindex="0"]';

describe("the drift page (#363)", () => {
  let at = "";
  let seed: Seed;
  let m = "";

  beforeEach(async () => {
    ({ at, seed } = await seeded());
    m = seed.machine2;
  });

  const signIn = async (human: string, f: Faults = {}) => expect(await fixtureSignIn(human, {}, { upstream: at, ...f })).toMatch(SERVER);
  const openDrift = async () => expect((await fetch(`${CONTROL}/drift?machine=${m}`, { method: "POST" })).status).toBe(200);
  const read = async (path: string) => {
    const r = await api(at, "h-viewer", "GET", path);
    expect(r.status).toBe(200);
    return r.body!;
  };
  const driftPage = async () => {
    await browser.url(`${FIXTURE_PAGE}/machines/${m}/drift`);
    await expect($("main h2")).toHaveText(expect.stringMatching(/^Change on the machine: /), { wait: 20_000 });
  };
  const form = (label: string) => $(`main form[aria-label="${label}"]`);
  const reason = (label: string, text: string) => form(label).$('input[name="reason"]').setValue(text);
  const press = (label: string) => form(label).$(`button=${label}`);
  const answer = (kind: string) => $(`[data-testid="${kind}-answer"]`);
  const posts = async () => (await writes()).filter((w) => w.startsWith("POST "));

  it("S5 in the browser: a change on the machine, freeze, the adoption link, a revert plan bound to the record, approval, unfreeze", async function () {
    this.timeout(180_000);
    const since = new Date().toISOString();
    await signIn("h-all");
    await openDrift();
    // The machine page says the change and its one link leads to the page.
    await browser.url(`${FIXTURE_PAGE}/machines/${m}`);
    await expect($('[data-testid="sentences"]')).toHaveText(expect.stringContaining("Changed on the machine since"), { wait: 20_000 });
    await $('[data-testid="drift-link"] a').click();
    await expect($("main h2")).toHaveText(expect.stringMatching(/^Change on the machine: /), { wait: 20_000 });
    expect(await where()).toBe(`${FIXTURE_PAGE}/machines/${m}/drift`);
    // The record in words, from the server's own record and the observation that opened it.
    await expect($('[data-testid="drift-record"]')).toHaveText(expect.stringMatching(/Opened by the read at 20\d\d-\d\d-\d\d \d\d:\d\d, last seen in the read at 20\d\d-\d\d-\d\d \d\d:\d\d\.$/), { wait: 20_000 });
    await expect($('[data-testid="drift-late"]')).toHaveText("Late landing: no.");
    await expect($('[data-testid="drift-not-shown"]')).toBeExisting();
    const opened = (await read(`/machines/${m}`))["openDrift"] as { revision: number };
    expect(opened.revision).toBeGreaterThan(0);
    // Freeze: one send to the machine's route, the freeze recorded, and the answer names who and the act.
    await reason("Freeze", "synthetic incident");
    await press("Freeze").click();
    await expect(answer("freeze")).toHaveText(expect.stringMatching(/^Frozen\. Recorded by .+ \(\w+(-\w+)?\), act /), { wait: 20_000 });
    expect(await posts()).toEqual([`POST /machines/${m}/freezes`]);
    expect((await read(`/machines/${m}`))["frozen"]).toBe(true);
    await expect($('[data-testid="drift-frozen"]')).toBeExisting();
    await expect($("main").$("button=Unfreeze")).toBeExisting();
    // [Start adoption] opens the wizard as a drift adoption, which the page does not start.
    await $("main").$("a=Start adoption").click();
    await expect($("main h2")).toHaveText("Start drift adoption", { wait: 20_000 });
    await absent(() => $('[data-testid="start-refused"]').isExisting(), "a refusal of the start parameters");
    expect(await where()).toBe(`${FIXTURE_PAGE}/ingestions/new`);
    // [Plan revert]: the create-plan page, then a proposed plan the server bound to the open record.
    await driftPage();
    await $("main").$("a=Plan revert").click();
    await expect($("main h2")).toHaveText("Create a plan", { wait: 20_000 });
    await $("main").$("button=Create plan").click();
    await browser.waitUntil(async () => /\/plans\/pln_[a-z2-7]+$/.test(await where()), { timeout: 30_000, timeoutMsg: "the plan page did not open" });
    const plan = (await where()).split("/").at(-1)!;
    await expect($("main h2")).toHaveText(expect.stringMatching(/\(proposed\)$/), { wait: 20_000 });
    const body = await read(`/plans/${plan}`);
    expect([body["operation"], body["driftRecord"], body["machine"]]).toEqual(["apply-config", opened.revision, m]);
    // The plan page says what the plan overwrites, and that the freeze holds it, with the step that releases it.
    await expect($('[data-testid="unseen-overwrite"]')).toHaveText(expect.stringContaining("which no reviewer has seen"), { wait: 20_000 });
    await expect($('[data-testid="plan-frozen"]')).toHaveText(expect.stringContaining(PLAN_FROZEN), { wait: 20_000 });
    // Approval records that the approver overwrote a configuration nobody reviewed.
    await $('main form[aria-label="Approve plan"] input[name="plan"]').setValue(plan);
    await $("main").$("button=Approve").click();
    await expect($("main h2")).toHaveText(expect.stringMatching(/\(approved\)$/), { wait: 30_000 });
    const approved = await read(`/plans/${plan}`);
    expect((await read(`/approvals/${approved["approval"] as string}`))["unseenOverwrite"]).toBe(true);
    // Unfreeze from the plan page's own link.
    await $('[data-testid="plan-frozen"] a').click();
    await expect($("main h2")).toHaveText(expect.stringMatching(/^Change on the machine: /), { wait: 20_000 });
    await reason("Unfreeze", "synthetic decision made");
    await press("Unfreeze").click();
    await expect(answer("unfreeze")).toHaveText(expect.stringMatching(/^Unfrozen\. Recorded by /), { wait: 20_000 });
    expect((await read(`/machines/${m}`))["frozen"]).toBe(false);  // once: the answer to the unfreeze was awaited on the line above, so the machine's recorded state is read after the write, never before it
    // The acts: each step was one recorded act, in this order.
    // The deployment's own seeding recorded plan acts before the freeze, so the list starts at the freeze this case made.
    const acts = ((await read("/acts?limit=500"))["items"] as { action: string; at: string }[]).filter((a) => a.at >= since).sort((a, b) => a.at.localeCompare(b.at)).map((a) => a.action);
    const mine = acts.slice(acts.indexOf("machine.freeze")).filter((a) => ["machine.freeze", "plan.create", "plan.approve", "machine.unfreeze"].includes(a));
    expect(mine).toEqual(["machine.freeze", "plan.create", "plan.approve", "machine.unfreeze"]);
  });

  it("a machine neither changed nor frozen: the machine page links to the page, which says so, offers Freeze and no adoption or revert; a frozen one reaches Unfreeze", async function () {
    this.timeout(120_000);
    await signIn("h-all");
    await browser.url(`${FIXTURE_PAGE}/machines/${m}`);
    await expect($('[data-testid="drift-link"] a')).toHaveText("Freeze this machine", { wait: 20_000 });
    await $('[data-testid="drift-link"] a').click();
    await expect($('[data-testid="drift-none"]')).toHaveText(DRIFT_NONE, { wait: 20_000 });
    await absent(() => $('main section[aria-label="Adopt"]').isExisting(), "the adoption choice with no open drift");
    await absent(() => $('main section[aria-label="Revert"]').isExisting(), "the revert choice with no open drift");
    await reason("Freeze", "synthetic freeze, nothing changed");
    await press("Freeze").click();
    await expect(answer("freeze")).toHaveText(expect.stringMatching(/^Frozen\. /), { wait: 20_000 });
    await expect($('[data-testid="drift-frozen"]')).toBeExisting();
    await expect($("main").$("button=Unfreeze")).toBeExisting();
    // The machine page's one link now says what the machine needs, and the page reaches Unfreeze with no open drift.
    await browser.url(`${FIXTURE_PAGE}/machines/${m}`);
    await expect($('[data-testid="drift-link"] a')).toHaveText("Unfreeze the machine", { wait: 20_000 });
    await $('[data-testid="drift-link"] a').click();
    await reason("Unfreeze", "synthetic unfreeze, nothing changed");
    await press("Unfreeze").click();
    await expect(answer("unfreeze")).toHaveText(expect.stringMatching(/^Unfrozen\. /), { wait: 20_000 });
    expect((await read(`/machines/${m}`))["frozen"]).toBe(false);  // once: the answer to the unfreeze was awaited on the line above, so the machine's recorded state is read after the write, never before it
    expect(await posts()).toEqual([`POST /machines/${m}/freezes`, `POST /machines/${m}/unfreezes`]);
  });

  it("without the role a control is disabled with its reason; a forced send shows the server's 403 and what the route accepts", async function () {
    this.timeout(120_000);
    await signIn("h-viewer");
    await openDrift();
    expect((await api(at, "h-author", "POST", `/machines/${m}/freezes`, { reason: "synthetic incident" })).status).toBe(201);
    await signIn("h-publisher");
    await driftPage();
    const unfreeze = $("main").$("button=Unfreeze");
    expect(await unfreeze.isEnabled()).toBe(false);  // once: the button is disabled with its reason in one element, and the reason is read at the next line, so an early read cannot pass here
    await expect(form("Unfreeze")).toHaveText(expect.stringContaining("Needs approver"));
    // The page's two links are controls too: a publisher cannot start an adoption (an author's step) and can plan a revert.
    expect(await $("main").$("button=Start adoption").isEnabled()).toBe(false);  // once: without the role the control is a disabled button from its first render, never a link, so an early read finds the same state
    await expect($("main").$("a=Plan revert")).toBeExisting();
    await reason("Unfreeze", "synthetic forced send");
    await browser.execute(() => document.querySelector<HTMLFormElement>('main form[aria-label="Unfreeze"]')!.requestSubmit());
    await expect(answer("unfreeze")).toHaveText(expect.stringContaining("Needs approver"), { wait: 20_000 });
    expect(await posts()).toEqual([`POST /machines/${m}/unfreezes`]);
    expect((await answers()).filter((a) => a.startsWith(`/machines/${m}/unfreezes `))).toEqual([`/machines/${m}/unfreezes 403`]);
    expect((await read(`/machines/${m}`))["frozen"]).toBe(true);
  });

  it("an unfreeze refused for a scope names that scope and says nothing about the start mode", async function () {
    this.timeout(120_000);
    expect((await api(at, "h-author", "POST", `/machines/${m}/freezes`, { reason: "synthetic incident" })).status).toBe(201);
    await signIn("h-all", { steps: { path: `/machines/${m}/unfreezes`, take: ["scope-refused"] } });
    await driftPage();
    await reason("Unfreeze", "synthetic scope refusal");
    await press("Unfreeze").click();
    await expect(answer("unfreeze")).toHaveText(expect.stringContaining(`This applies to one part of the installation only. ${SCOPE_NO_MODE}`), { wait: 20_000 });
    expect((await references()).join("\n")).toContain(`Scope: ${INJECTED_SCOPE}`);
    await absent(() => $('[data-testid="recovery"]').isExisting(), "the recovery banner after a scope's refusal");
  });

  // T10 (journeys §13), WCAG 2.2 AA on the drift page of a machine with an open change: every control by Tab alone, in
  // reading order, each focus visible (2.1.1, 2.1.2, 2.4.7), the contrast and target size the other views measure
  // (1.4.3, 2.5.8), and the page at 320 CSS pixels without scrolling sideways (1.4.10). The screen-reader pass is owed by
  // ginsys/bronzeward#447.
  it("keyboard only (T10): Tab reaches every control of the drift page, and the page measures", async function () {
    this.timeout(120_000);
    await signIn("h-all");
    await openDrift();
    await driftPage();
    await expect($('[data-testid="drift-record"]')).toBeExisting();
    const total = await browser.execute((sel: string) => {
      const all = [...document.querySelectorAll(sel)];
      all.forEach((e, i) => e.setAttribute("data-t10", String(i)));
      return all.length;
    }, CONTROLS);
    expect(total).toBeGreaterThan(0);
    await fromTitle();
    const reached: number[] = [];
    for (let i = 0; i < 80 && reached.length < total; i++) {
      const f = await tab();
      if (f.same) throw new Error(`Tab did not leave ${f.tag} "${f.text}" on the way through the page`);
      const k = await browser.execute(() => document.activeElement?.closest("[data-t10]")?.getAttribute("data-t10") ?? null);
      if (k !== null && !reached.includes(Number(k))) reached.push(Number(k));
    }
    expect(reached).toEqual([...reached].sort((a, b) => a - b));
    expect(reached.length).toBe(total);
    const seen = await measure();
    expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
    expect(seen.small).toBe(0);
    await storageEmpty();
  });

  itNarrow("reflow at 320 CSS pixels (T10): the drift page does not scroll sideways, and no control is clipped", async function () {
    this.timeout(120_000);
    await signIn("h-all");
    await openDrift();
    await driftPage();
    await expect($('[data-testid="drift-record"]')).toBeExisting();
    await narrow(async () => {
      expect(["the drift page", await overflows()]).toEqual(["the drift page", 0]);
      const clipped = await browser.execute((sel: string) => [...document.querySelectorAll(sel)].filter((e) => {
        const r = e.getBoundingClientRect();
        return r.left < -0.5 || r.right > innerWidth + 0.5;
      }).length, CONTROLS);
      expect(clipped).toBe(0);
    });
    await storageEmpty();
  });
});
