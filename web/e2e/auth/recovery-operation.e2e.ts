// The recovery page of one operation (ginsys/bronzeward#365): take over and resolve as cancelled, at /recovery/operations/<id>,
// against a disposable recovery start whose entry was made through the API (as operation.e2e.ts does), so the committed
// operation is unresolved. `seed=noattempt` has no attempt (a resolution is accepted); `seed` has one (a resolution is refused).
// The first press of each case goes to the real routes. Every value is synthetic.
import { $, browser, expect } from "@wdio/globals";
import { CONTROL, FIXTURE_PAGE } from "../auth.conf.ts";
import { absent, answers, api, disposable, faults, fixtureSignIn, mint, storageEmpty, type Seed } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;

describe("the recovery page of one operation (#365)", () => {
  let at = "";
  let op = "";

  // A recovery start with the entry made through the API: the committed operation is unresolved.
  const start = async (seed: "seed" | "seed=noattempt") => {
    at = await disposable(`start=recovery&${seed}`);
    op = ((await (await fetch(`${CONTROL}/seeded`)).json()) as Seed).operation;
    const when = new Date(Date.now() - 60_000).toISOString();
    const made = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-365op-${Date.now()}-${Math.random().toString(36).slice(2)}` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-365", takenAt: when }], stoppedAt: when, reason: "operation recovery synthetic restore" }),
    });
    expect(made.status).toBe(201);
    const read = await api(at, "h-viewer", "GET", `/operations/${op}`);
    expect([read.status, read.body!["state"]]).toEqual([200, "unresolved"]);
  };
  const open = async (human = "h-recovery") => {
    expect(await fixtureSignIn(human, {}, { upstream: at })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/recovery/operations/${op}`);
    await expect($('[data-testid="recovery-operation-state"]')).toBeExisting({ wait: 30_000 });
  };
  // The injector's own lines, "<method> <route> <key> <epoch>".
  const posts = async (route: string) => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).filter((w) => w.startsWith(`POST /operations/${op}/${route} `));
  const events = async () => ((await api(at, "h-viewer", "GET", `/operations/${op}/events`)).body!["items"] as { type: string; data: { generation?: number } }[]);
  const generation = async () => Math.max(1, ...(await events()).map((e) => e.data.generation ?? 0));
  const reason = (act: string, text: string) => $(`form[aria-label="${act}"] input[name="reason"]`).setValue(text);

  it("take over sends the generation read from the events and one reason, and the answer names the new generation", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    const before = await generation();
    await open();
    await expect($('[data-testid="recovery-operation-state"]')).toHaveText(expect.stringContaining(`Owner generation read: ${before}`));
    await reason("Take over", "the owner was stopped");
    await $('form[aria-label="Take over"] button').click();
    await expect($('[data-testid="takeovers-answer"]')).toHaveText(expect.stringContaining(`Taken over: generation ${before + 1}`), { wait: 30_000 });
    expect(await posts("takeovers")).toHaveLength(1);
    expect(await generation()).toBe(before + 1);
    // The events are not read again, so the page's own generation is the answer's: a second press would send it, never the old one.
    await expect($('[data-testid="recovery-operation-state"]')).toHaveText(expect.stringContaining(`Owner generation read: ${before + 1}`), { wait: 30_000 });
  });

  it("resolve as cancelled ends an operation with no attempt, says it records no remote outcome, and leaves both controls off", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    await open();
    await reason("Resolve as cancelled", "never sent");
    await $('form[aria-label="Resolve as cancelled"] button').click();
    await expect($('[data-testid="resolutions-answer"]')).toHaveText(expect.stringContaining("This records no remote outcome"), { wait: 30_000 });
    expect(await posts("resolutions")).toHaveLength(1);
    expect((await api(at, "h-viewer", "GET", `/operations/${op}`)).body!["state"]).toBe("cancelled");
    await expect($('[data-testid="takeovers-off"]')).toHaveText(expect.stringContaining("is cancelled"), { wait: 20_000 });
    await expect($('[data-testid="resolutions-off"]')).toHaveText(expect.stringContaining("is cancelled"), { wait: 20_000 });
  });

  it("resolve is off with its reason where an attempt is recorded, and the page sends nothing", async function () {
    this.timeout(120_000);
    await start("seed");
    await open();
    await expect($('[data-testid="resolutions-off"]')).toHaveText(expect.stringContaining("the request may have landed"), { wait: 20_000 });
    // once: read after the reason text of the same render, which is what turns the control off (web/src/recovery-operation.tsx Act)
    expect(await $('form[aria-label="Resolve as cancelled"] button').isEnabled()).toBe(false);
    await absent(async () => (await posts("resolutions")).length > 0, "a resolution sent for an operation with an attempt");
    expect((await api(at, "h-viewer", "GET", `/operations/${op}`)).body!["state"]).toBe("unresolved");
  });

  it("a take over whose answer is dropped after the commit is repeated under its key and is one act, and a reload sends nothing", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    const before = await generation();
    await open();
    await faults({ upstream: at, steps: { path: `/operations/${op}/takeovers`, take: ["commit-drop"] } });
    await reason("Take over", "the owner was stopped");
    await $('form[aria-label="Take over"] button').click();
    await expect($('[data-testid="takeovers-answer"]')).toHaveText(expect.stringContaining("Taken over"), { wait: 60_000 });
    const sent = await posts("takeovers");
    expect(sent).toHaveLength(2);
    expect(sent[1]!.split(" ")[2]).toBe(sent[0]!.split(" ")[2]);
    expect(await generation()).toBe(before + 1);
    await browser.refresh();
    await expect($('[data-testid="recovery-operation-state"]')).toBeExisting({ wait: 30_000 });
    await absent(async () => (await posts("takeovers")).length > 2, "a take over sent again after the reload");
  });

  it("a resolution whose answer is dropped after the commit is repeated under its key and is one act, and a reload sends nothing", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    const before = (await events()).filter((e) => e.type === "operation-state").length;
    await open();
    await faults({ upstream: at, steps: { path: `/operations/${op}/resolutions`, take: ["commit-drop"] } });
    await reason("Resolve as cancelled", "never sent");
    await $('form[aria-label="Resolve as cancelled"] button').click();
    // The repeat is sent with the operation already cancelled: the client's own checks would refuse it, so it is the key that carries it.
    await expect($('[data-testid="resolutions-answer"]')).toHaveText(expect.stringContaining("This records no remote outcome"), { wait: 60_000 });
    const sent = await posts("resolutions");
    expect(sent).toHaveLength(2);
    expect(sent[1]!.split(" ")[2]).toBe(sent[0]!.split(" ")[2]);
    expect((await api(at, "h-viewer", "GET", `/operations/${op}`)).body!["state"]).toBe("cancelled");
    expect((await events()).filter((e) => e.type === "operation-state").length).toBe(before + 1);
    await browser.refresh();
    await expect($('[data-testid="recovery-operation-state"]')).toBeExisting({ wait: 30_000 });
    await absent(async () => (await posts("resolutions")).length > 2, "a resolution sent again after the reload");
  });

  it("an operation another operator resolved shows the server's refusal of a second resolution, and records no second act", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    await open();
    // The page reads the operation once and again only after its own act, so it still shows the operation unresolved here.
    expect((await api(at, "h-recovery", "POST", `/operations/${op}/resolutions`, { to: "cancelled", reason: "the other operator" })).status).toBe(201);
    const before = (await events()).filter((e) => e.type === "operation-state").length;
    await reason("Resolve as cancelled", "never sent");
    await $('form[aria-label="Resolve as cancelled"] button').click();
    await expect($('[data-testid="resolutions-answer"]')).toHaveText(expect.stringContaining("the operation is cancelled"), { wait: 30_000 });
    expect((await answers()).filter((a) => a.startsWith(`/operations/${op}/resolutions `))).toEqual([`/operations/${op}/resolutions 409`]);
    // once: read after the server's 409 was answered and shown, so the refused write is settled and the event list cannot still grow from it
    expect((await events()).filter((e) => e.type === "operation-state").length).toBe(before);
  });

  it("a reason is required, and a blank one sends nothing", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    await open();
    await $('form[aria-label="Take over"] button').click();
    await expect($('[data-testid="takeovers-input"]')).toHaveText("Give a reason.", { wait: 20_000 });
    // 4.1.3: the refused input is an alert on its own element.
    await expect($('[data-testid="takeovers-input"]')).toHaveAttribute("role", "alert");
    await absent(async () => (await posts("takeovers")).length > 0, "a take over sent with no reason");
  });

  it("a signed-in operator without recovery-admin sees both controls off with the reason, and the page note says who can act", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    await open("h-viewer");
    // once: disabled by role in the same Action element as its reason (web/src/shell.tsx:64-73), so it cannot pass early
    expect(await $('form[aria-label="Take over"] button').isEnabled()).toBe(false);
    // once: the same Action element as the line above
    expect(await $('form[aria-label="Resolve as cancelled"] button').isEnabled()).toBe(false);
    // The whole note, the role sentence included, is behind "What is this page?" (ginsys/bronzeward#1268): open it first.
    await $("details.about > summary").click();
    await expect($('[data-testid="page-note"]')).toHaveText(expect.stringContaining("recovery-admin"));
    expect(await $("main").getText()).toContain("Needs recovery-admin");
    await absent(async () => (await posts("takeovers")).length + (await posts("resolutions")).length > 0, "a write by a role that cannot decide");
  });

  it("the operation's own page links to this page, and the heading names the operation rather than an identifier", async function () {
    this.timeout(120_000);
    await start("seed=noattempt");
    expect(await fixtureSignIn("h-recovery", {}, { upstream: at })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}/operations/${op}`);
    await $("a=Open in Recovery").click();
    await browser.waitUntil(async () => (await browser.getUrl()) === `${FIXTURE_PAGE}/recovery/operations/${op}`, { timeout: 20_000, timeoutMsg: "the link did not open the recovery page" });
    await expect($('[data-testid="recovery-operation-state"]')).toBeExisting({ wait: 30_000 });
    // The titled form is "Operation: <title> (<kind>, <state>)", and the fallback while the title's reads run is "Operation <short id>: <kind>, <state>".
    await expect($("main h2")).toHaveText(expect.stringMatching(/^Recovery of the operation: Operation: .+ \(apply-config, unresolved\)$/), { wait: 30_000 });
    await storageEmpty();
  });
});
