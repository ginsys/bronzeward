// The accounting forms of the recovery decisions (ginsys/bronzeward#365): a scope's on the console's row and an attempt's on the
// operation's recovery page, against a disposable recovery start with the seeded records and their recovery observations
// (?seed=observations, internal/api/e2eseed_test.go). The entry is made through the API with a stop an hour back, so every
// observation is after it. Each form reads the sentence it records and sends once per press; every press goes to the real routes.
// Every value is synthetic.
import { $, $$, browser, expect } from "@wdio/globals";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { absent, answers, api, disposable, faults, fixtureSignIn, fromTitle, measure, mint, storageEmpty, tab, type Seed } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;

describe("the accounting forms of the recovery decisions (#365)", () => {
  let at = "";
  let seed: Seed;
  let obs: NonNullable<Seed["observations"]>;

  const start = async () => {
    at = await disposable("start=recovery&seed=observations");
    seed = (await (await fetch(`${CONTROL}/seeded`)).json()) as Seed;
    obs = seed.observations!;
    expect(obs).toBeDefined();
    // The scopes' deadline is the stop plus the maximum transport deadline, and the first read of a machine after it is the only start
    // the server accepts. The setup's own reads of the machines are older than the seeded ones, so the stop is now: the deadline then
    // falls after them and before the seeded Start (3 or 5 minutes ahead).
    const when = new Date(Date.now() - 3_600_000).toISOString();
    const stopped = new Date(Date.now() - 5_000).toISOString();
    const made = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-365acc-${Date.now()}-${Math.random().toString(36).slice(2)}` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-365", takenAt: when }], stoppedAt: stopped, reason: "accounting synthetic restore" }),
    });
    expect(made.status).toBe(201);
  };
  const row = (machine: string) => $(`[data-testid="scope-${machine}"]`);
  const openConsole = async (human = "h-recovery") => {
    expect(await fixtureSignIn(human, {}, { upstream: at })).toMatch(SERVER);
    await $('nav[aria-label="Main"]').$("a=Recovery").click();
    await expect($('[data-testid="scopes"]')).toBeExisting({ wait: 30_000 });
  };
  const scopeForm = (machine: string) => row(machine).$('form[aria-label="Record accounting"]');
  const choose = async (form: ChainablePromiseElement, which: "start" | "end", id: string) => {
    const radio = form.$(`input[name="${which}"][value="${id}"]`);
    await expect(radio).toBeExisting({ wait: 30_000 });
    await radio.click();
  };
  // The injector's own lines, "<method> <route> <key> <epoch>".
  const posts = async (route: string) => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).filter((w) => w.startsWith(`POST ${route} `));
  const scopeState = async (machine: string) => {
    const r = await api(at, "h-viewer", "GET", `/machines/${machine}`);
    expect(r.status).toBe(200);
    return (r.body as { scopeState?: string })["scopeState"];
  };

  it("a scope's accounting shows the sentence it records and sends nothing until the two observations are chosen and any moved version is explained", async function () {
    this.timeout(180_000);
    await start();
    await openConsole();
    const m = seed.machine2;
    const form = scopeForm(m);
    await expect(form).toBeExisting({ wait: 30_000 });
    // The judgment, the limits and the observation choices are on the page before anything is pressed.
    await expect(form.$('[data-testid="settle-judgment"]')).toHaveText(expect.stringContaining("This records your judgment that no request sent before the restore"), { wait: 30_000 });
    await expect(form.$('[data-testid="settle-judgment"]')).toHaveText(expect.stringContaining("nothing here shows whether it did"));
    await expect(form.$('[data-testid="settle-limits"]')).toHaveText(expect.stringMatching(/^Settle floor .+, maximum transport deadline .+, as the server serves them\.$/), { wait: 30_000 });
    // The form's own text: the console's revocation form, on the same page, does ask for the identity to be typed.
    await absent(async () => /type (the|back)/i.test(await form.getText()), "an instruction to type anything back in the accounting form");
    await form.$("button=Record accounting").click();
    await expect(form.$('[data-testid="settle-input"]')).toHaveText("Choose the start observation.", { wait: 20_000 });
    await choose(form, "start", obs.machine2.start);
    await form.$("button=Record accounting").click();
    await expect(form.$('[data-testid="settle-input"]')).toHaveText("Choose the end observation.", { wait: 20_000 });
    // The end that reads another resource version needs an explanation; the form says so and sends nothing without one.
    await choose(form, "end", obs.machine2.end);
    await expect(form).toHaveText(expect.stringContaining("required: the resource version moved"), { wait: 20_000 });
    await form.$("button=Record accounting").click();
    await expect(form.$('[data-testid="settle-input"]')).toHaveText(expect.stringContaining("say why"), { wait: 20_000 });
    await absent(async () => (await posts("/recovery/accountings")).length > 0, "an accounting sent with a moved version and no explanation");
    // The start cannot be an end, and an end that began before the start was recorded is off with its reason.
    await expect(form.$(`input[name="end"][value="${obs.machine2.start}"]`)).toBeDisabled();
    expect(await scopeState(m)).toBe("pre-restore-unaccounted");
  });

  it("a scope's accounting records one act and says what the scope is now, and the scope no longer offers it", async function () {
    this.timeout(180_000);
    await start();
    await openConsole();
    const m = seed.machine2;
    const form = scopeForm(m);
    await choose(form, "start", obs.machine2.start);
    await choose(form, "end", obs.machine2.end);
    await form.$('input[name="explanation"]').setValue("the operator re-applied the restored configuration");
    await form.$("button=Record accounting").click();
    const answer = form.$('[data-testid="settle-answer"]');
    await expect(answer).toHaveText(expect.stringMatching(/^Recorded\. Scope \S+ is now blocked until it is marked\./), { wait: 60_000 });
    await expect(answer).toHaveText(expect.stringContaining("Recorded by"));
    await expect(answer).toHaveText(expect.stringContaining("Next: mark the scope."));
    expect(await posts("/recovery/accountings")).toHaveLength(1);
    expect(await scopeState(m)).toBe("blocked");
    // The console rereads the scopes while the page is open, and the scope is no longer owed: the answer stays, and the form is off.
    await absent(async () => !(await form.$('[data-testid="settle-answer"]').getText()).startsWith("Recorded."), "the answer dropped by the console's reread");
    // once: off in the same render as the recorded answer awaited above (`busy` in web/src/accounting.tsx), so it cannot be read early.
    expect(await form.$("button=Record accounting").isEnabled()).toBe(false);
    await storageEmpty();
  });

  it("the server's refusal of an end inside the settle floor is shown in place with its next step, and nothing is recorded", async function () {
    this.timeout(180_000);
    await start();
    await openConsole();
    const m = seed.machine2;
    const form = scopeForm(m);
    await choose(form, "start", obs.machine2.start);
    await choose(form, "end", obs.machine2.soon);
    await form.$('input[name="explanation"]').setValue("the version moved with the restore");
    await form.$("button=Record accounting").click();
    const answer = form.$('[data-testid="settle-answer"]');
    await expect(answer).toHaveText(expect.stringContaining("began before the settle floor"), { wait: 60_000 });
    await expect(form.$('[data-testid="write-next"]')).toHaveText(expect.stringContaining("Wait until the floor has passed"), { wait: 20_000 });
    expect((await answers()).filter((a) => a.startsWith("/recovery/accountings "))).toEqual(["/recovery/accountings 409"]);
    expect(await scopeState(m)).toBe("pre-restore-unaccounted");
  });

  it("an accounting whose answer is dropped after the commit is repeated under its key and is one act, and a reload sends nothing", async function () {
    this.timeout(180_000);
    await start();
    await openConsole();
    const m = seed.machine2;
    const form = scopeForm(m);
    await faults({ upstream: at, steps: { path: "/recovery/accountings", take: ["commit-drop"] } });
    await choose(form, "start", obs.machine2.start);
    await choose(form, "end", obs.machine2.end);
    await form.$('input[name="explanation"]').setValue("the operator re-applied the restored configuration");
    await form.$("button=Record accounting").click();
    // The repeat meets a scope that is no longer unaccounted: its refusal would show, so it is the key that carries the first answer.
    await expect(form.$('[data-testid="settle-answer"]')).toHaveText(expect.stringContaining("Recorded. Scope"), { wait: 60_000 });
    const sent = await posts("/recovery/accountings");
    expect(sent).toHaveLength(2);
    // "<method> <route> <key> <epoch>": the key is the third field.
    expect(sent[0]!.split(" ")[2]).not.toBe("");
    expect(sent[1]!.split(" ")[2]).toBe(sent[0]!.split(" ")[2]);
    expect(await scopeState(m)).toBe("blocked");
    await browser.refresh();
    await expect($('[data-testid="scopes"]')).toBeExisting({ wait: 30_000 });
    await absent(async () => (await posts("/recovery/accountings")).length > 2, "an accounting sent again after the reload");
  });

  it("a signed-in operator without recovery-admin sees the accounting off with the reason and sends nothing", async function () {
    this.timeout(180_000);
    await start();
    await openConsole("h-viewer");
    const form = scopeForm(seed.machine2);
    await expect(form).toBeExisting({ wait: 30_000 });
    // once: disabled by role in the same Action element as its reason (web/src/shell.tsx)
    expect(await form.$("button=Record accounting").isEnabled()).toBe(false);
    expect(await form.getText()).toContain("Needs recovery-admin");
    await absent(async () => (await posts("/recovery/accountings")).length > 0, "an accounting by a role that cannot decide");
  });

  // The operation's own page: the committed plan's operation is unresolved after the entry and holds one attempt.
  const attempt = async () => {
    const r = await api(at, "h-viewer", "GET", `/operations/${seed.operation}`);
    expect(r.status).toBe(200);
    return (r.body!["attempts"] as { id: string; accounting: unknown }[])[0]!;
  };
  const openOperation = async () => {
    expect(await fixtureSignIn("h-recovery", {}, { upstream: at })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/recovery/operations/${seed.operation}`);
    await expect($('[data-testid="recovery-operation-state"]')).toBeExisting({ wait: 30_000 });
  };
  const attemptForm = () => $('section[aria-label="Account for an attempt"]').$('form[aria-label="Record accounting"]');

  // The seeded attempt's deadline is a minute ahead: First is the first read after it, Later is a read after that.
  it("an attempt's accounting marks the first read after the deadline, keeps a later read off with its reason, and is recorded once for the marked one", async function () {
    this.timeout(240_000);
    await start();
    const att = await attempt();
    expect(att.accounting).toBeNull();
    await openOperation();
    const form = attemptForm();
    await expect(form).toBeExisting({ wait: 30_000 });
    await expect(form.$('[data-testid="settle-judgment"]')).toHaveText(expect.stringContaining("no copy of the request this attempt sent, whose response was lost"), { wait: 30_000 });
    const route = `/operations/${seed.operation}/attempts/${att.id}/accountings`;
    // The page names the start the server accepts: the first read after the attempt's own deadline is marked, a later read is off.
    const later = form.$(`input[name="start"][value="${obs.machine.later}"]`);
    await expect(later).toBeDisabled({ wait: 30_000 });
    await expect(later.$("..")).toHaveText(expect.stringContaining("it is not the first read after the transport deadline"));
    await expect(form.$(`input[name="start"][value="${obs.machine.first}"]`).$("..")).toHaveText(expect.stringContaining("(the first read after the deadline)"));
    await choose(form, "start", obs.machine.first);
    await choose(form, "end", obs.machine.end);
    await form.$("button=Record accounting").click();
    await expect(form.$('[data-testid="settle-answer"]')).toHaveText(expect.stringMatching(/^Recorded\. Attempt \S+ is accounted for; the operation's state is unchanged\./), { wait: 60_000 });
    expect(await posts(route)).toHaveLength(1);
    expect((await attempt()).accounting).not.toBeNull();
    // The reread that follows the answer shows the attempt accounted: the answer stays, and the form is off, not offered again.
    await absent(async () => !(await form.$('[data-testid="settle-answer"]').getText()).startsWith("Recorded."), "the answer dropped by the reread");
    // once: off in the same render as the recorded answer awaited above (`busy` in web/src/accounting.tsx), so it cannot be read early.
    expect(await form.$("button=Record accounting").isEnabled()).toBe(false);
    await browser.refresh();
    await expect($('[data-testid="recovery-operation-state"]')).toBeExisting({ wait: 30_000 });
    await absent(async () => (await posts(route)).length > 1, "an accounting sent again after the reload");
    expect((await $$('form[aria-label="Record accounting"]')).length).toBe(0);
  });

  // T11 (journeys §13), WCAG 2.2 AA on the console's scope row: every control by Tab alone, in reading order, each focus visible
  // (2.1.1, 2.1.2, 2.4.7), then the task itself, "account a pre-restore-unaccounted scope, then mark and release it", with the keyboard
  // alone. The keys are Tab, Shift+Tab, the arrow keys inside a radio group, Space and Enter, and typing in a field. A radio group is
  // one Tab stop, so it counts as one control, and the arrow keys skip a disabled radio. The mark's result depends on the machine, so
  // the scope is readied by the harness before the release (as recovery-scopes does); the walk of mark and release on real machines is
  // fixtures/scenarios/s7 web. The screen-reader pass is owed by ginsys/bronzeward#447.
  it("keyboard only (T11): Tab reaches every control of a scope's row, and the accounting, mark and release are made with the keyboard", async function () {
    this.timeout(300_000);
    await start();
    await openConsole();
    const m = seed.machine2;
    await expect(scopeForm(m)).toBeExisting({ wait: 30_000 });
    // Every control of the row, a radio group as one (the controls of a group share the number).
    const total = await browser.execute((machine: string) => {
      const groups = new Map<string, number>();
      let n = 0;
      for (const e of document.querySelectorAll(`[data-testid="scope-${machine}"] a[href], [data-testid="scope-${machine}"] button, [data-testid="scope-${machine}"] input, [data-testid="scope-${machine}"] select, [data-testid="scope-${machine}"] textarea`)) {
        const radio = e instanceof HTMLInputElement && e.type === "radio";
        const key = radio ? `radio:${(e as HTMLInputElement).name}` : "";
        if (radio && groups.has(key)) {
          e.setAttribute("data-t11", String(groups.get(key)));
          continue;
        }
        if (radio) groups.set(key, n);
        e.setAttribute("data-t11", String(n++));
      }
      return n;
    }, m);
    expect(total).toBeGreaterThan(0);
    await fromTitle();
    const reached: number[] = [];
    for (let i = 0; i < 120 && reached.length < total; i++) {
      const f = await tab();
      if (f.same) throw new Error(`Tab did not leave ${f.tag} "${f.text}" on the way through the console`);
      const k = await browser.execute(() => document.activeElement?.closest("[data-t11]")?.getAttribute("data-t11") ?? null);
      if (k !== null && !reached.includes(Number(k))) reached.push(Number(k));
    }
    expect(reached).toEqual([...reached].sort((a, b) => a - b));
    expect(reached.length).toBe(total);
    // Only a boolean and a value leave the page: where the focus is, never a field's text.
    const on = (machine: string, name: string) =>
      browser.execute((mm: string, n: string) => {
        const e = document.activeElement as HTMLInputElement | null;
        return e !== null && e.closest(`[data-testid="scope-${mm}"]`) !== null && (e.getAttribute("name") === n || e.textContent?.trim() === n);
      }, machine, name);
    const tabTo = async (name: string) => {
      for (let i = 0; i < 120; i++) {
        await tab();
        if (await on(m, name)) return;
      }
      throw new Error(`Tab never reached "${name}" in the row of the scope`);
    };
    const held = () => browser.execute(() => ((document.activeElement as HTMLInputElement | null)?.value ?? ""));
    const checked = (which: "start" | "end") => browser.execute((mm: string, w: string) => (document.querySelector(`[data-testid="scope-${mm}"] input[name="${w}"]:checked`) as HTMLInputElement | null)?.value ?? "", m, which);
    // Start: the group takes one Tab, which lands on the marked start (the first enabled radio), and Space chooses it.
    await fromTitle();
    await tabTo("start");
    expect(await held()).toBe(obs.machine2.start);
    await browser.keys("Space");
    expect(await checked("start")).toBe(obs.machine2.start);
    // End: the observation that is the start is off and comes first, so Tab lands past it, and the arrow key moves on to the next one.
    await tabTo("end");
    expect(await held()).toBe(obs.machine2.soon);
    await browser.keys("ArrowDown");
    expect(await held()).toBe(obs.machine2.end);
    expect(await checked("end")).toBe(obs.machine2.end);
    await tabTo("explanation");
    await browser.keys("the operator re-applied the restored configuration".split(""));
    await tabTo("Record accounting");
    await browser.keys("Enter");
    const answer = scopeForm(m).$('[data-testid="settle-answer"]');
    await expect(answer).toHaveText(expect.stringMatching(/^Recorded\. Scope \S+ is now blocked until it is marked\./), { wait: 60_000 });
    expect(await posts("/recovery/accountings")).toHaveLength(1);
    expect(await scopeState(m)).toBe("blocked");
    // Mark: the console rereads the scopes, the row offers Mark, and Enter on it records the act.
    await expect(row(m).$("button=Mark")).toBeExisting({ wait: 30_000 });
    await fromTitle();
    await tabTo("Mark");
    await browser.keys("Enter");
    await expect(row(m).$('[data-testid="write-result"]')).toHaveText(expect.stringMatching(/^Scope marked (blocked|ready|unresolved), was blocked/), { wait: 60_000 });
    // Release: a ready scope offers Release; Enter on it releases the scope with the sentence that it is not an approval.
    const ready = await fetch(`${CONTROL}/scope?machine=${m}&state=ready`, { method: "POST" });
    expect([ready.status, await ready.text()]).toEqual([200, ""]);
    await expect(row(m).$("button=Release")).toBeExisting({ wait: 30_000 });
    await fromTitle();
    await tabTo("Release");
    await browser.keys("Enter");
    const released = row(m).$('[data-testid="write-result"]');
    await expect(released).toHaveText(expect.stringContaining("Scope released, was ready"), { wait: 60_000 });
    await expect(released).toHaveText(expect.stringContaining("A release is not an approval"));
    const seen = await measure();
    expect(seen.contrast).toBeGreaterThanOrEqual(4.5);
    expect(seen.small).toBe(0);
    await storageEmpty();
  });
});
