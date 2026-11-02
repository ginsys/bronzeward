// The recovery console's entry (ginsys/bronzeward#364): REC-02 and REC-04 (a) (conformance/cases/)
// against a disposable recovery start, through the injector. REC-04 (b) waits for the server's
// COMMIT fault hook (#263). Every value is synthetic.
import { $, browser, expect } from "@wdio/globals";
import { NORMAL_START, PRE_ENTRY } from "../../src/recovery.tsx";
import { SET_ASIDE } from "../../src/states.tsx";
import { CONTROL, FIXTURE_ISSUER } from "../auth.conf.ts";
import { answers, faults, fixtureSignIn, holding, mint, release, seen, storageEmpty } from "./helpers.ts";

const result = () => $('[data-testid="write-result"]');
const entered = () => $('[data-testid="entered"]');
// Every write the injector received since the sign-in, as [method, route, key, expected epoch].
const writes = async (): Promise<string[][]> => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).map((w) => w.split(" "));
const entries = async () => (await writes()).filter(([, route]) => route === "/recovery/entries");
const stamp = () => new Date(Date.now() - 60_000).toISOString();

// Opens the entry form on a start of the disposable deployment: the page reads the process's
// restriction (injected on a normal start) and shows the form.
async function open(start: "normal" | "recovery", human = "h-recovery"): Promise<string> {
  const started = await fetch(`${CONTROL}/disposable?start=${start}`, { method: "POST" });
  expect(started.status).toBe(200);
  const at = await started.text();
  const refuse = start === "normal" ? { processRefused: ["/server", "/identity", "/recovery"] } : {};
  // The form has a heading, so the page settles on its footer, not on the restriction's text.
  expect(await fixtureSignIn(human, {}, { upstream: at, ...refuse })).toBe("Server unknown");
  await expect($('[data-testid="status"]')).toHaveText(PRE_ENTRY);
  await expect($("main form[aria-label='Enter recovery']")).toBeExisting();
  return at;
}

// Fills the form with synthetic values and types the confirmation.
async function fill(reason: string) {
  const when = stamp();
  await $('input[name="database-backup"]').setValue("synthetic-backup-364");
  await $('input[name="database-taken"]').setValue(when);
  await $('input[name="stoppedAt"]').setValue(when);
  await $('input[name="reason"]').setValue(reason);
  expect(await $("main form").$("button=Enter recovery").isEnabled()).toBe(false);
  await $("main form").$("label*=Type enter").$("input").setValue("enter");
  expect(await $("main form").$("button=Enter recovery").isEnabled()).toBe(true);
}

describe("the recovery entry (#364)", () => {
  it("REC-02: an entry sent to a normal start is read as one: 409 conflict without an epoch", async () => {
    const at = await open("normal");
    await fill("REC-02 synthetic restore");
    // The injected refusal stood for the restriction; the entry itself reaches the normal start.
    await faults({ upstream: at });
    await $("main form").$("button=Enter recovery").click();
    // The refusal says the start is normal, so the restriction the page showed is stale: the shell
    // reads again, finds the server and leaves the state before the entry, and the header keeps why.
    await expect(entered()).toHaveText(expect.stringContaining(NORMAL_START), { wait: 20_000 });
    // With the refusal's own problem (§8.2), kept after the form went.
    await expect(entered()).toHaveText(expect.stringContaining("Request "));
    await expect($("main h2")).toHaveText("Machines", { wait: 20_000 });
    expect(await $("body").getText()).not.toContain(PRE_ENTRY);
    const sent = await entries();
    expect(sent).toHaveLength(1);
    // The entry is sent without the epoch header (§8.4 item 3), the first send included.
    expect(sent[0]![3]).toBe("undefined");
  });

  it("REC-02: an entry sent to a recovery start whose entry committed under another key is read as one: 409 conflict with an epoch", async () => {
    const at = await open("recovery");
    const when = stamp();
    const first = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-02-${Date.now()}-other` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-rec-02", takenAt: when }], stoppedAt: when, reason: "REC-02 synthetic restore" }),
    });
    expect(first.status).toBe(201);
    const { epoch } = (await first.json()) as { epoch: string };
    // The page still shows the form it read before: it sends under its own key.
    await fill("REC-02 second");
    await $("main form").$("button=Enter recovery").click();
    // The refusal carries the epoch, so §8.5 moves the page off the entry form to the recovery
    // state at that epoch: read as a recovery start, never as a normal one.
    await expect($('[data-testid="epoch"]')).toHaveText(`Epoch ${epoch}`, { wait: 20_000 });
    await expect($('[data-testid="recovery"]')).toHaveText(expect.stringContaining(`epoch ${epoch}`), { wait: 20_000 });
    expect(await $("body").getText()).not.toContain(NORMAL_START);
    // The form is gone with the move, so the header keeps the refusal: its reading and its problem.
    await expect(entered()).toHaveText(expect.stringContaining(`An entry is already recorded on this server, in epoch ${epoch}`));
    await expect(entered()).toHaveText(expect.stringContaining("Request "));
    const sent = await entries();
    expect(sent).toHaveLength(1);
    expect(sent[0]![3]).toBe("undefined");
  });

  it("REC-04 (a): the 201 dropped after the commit: repeated under the same key, no epoch check, the minted epoch shown", async function () {
    this.timeout(60_000);
    const at = await open("recovery");
    await fill("REC-04 synthetic restore");
    await faults({ upstream: at, steps: { path: "/recovery/entries", take: ["commit-drop"] } });
    await $("main form").$("button=Enter recovery").click();
    // The result is kept in the header, since the epoch change ended the form's own page.
    await expect(entered()).toHaveText(expect.stringContaining("Recovery mode entered (act"), { wait: 30_000 });
    const sent = await entries();
    expect(sent).toHaveLength(2);
    // One key over both sends, and no epoch header on either (§8.4 item 3).
    expect(sent[1]).toEqual(sent[0]);
    expect(sent[0]![2]).toMatch(/^[A-Za-z0-9_-]{22}$/);
    expect(sent[0]![3]).toBe("undefined");
    // The first send committed before its answer was dropped; the repeat was replayed.
    expect((await answers()).filter((a) => a.startsWith("/recovery/entries "))).toEqual(["/recovery/entries 201", "/recovery/entries 201"]);
    expect(await entered().getText()).toMatch(/Epoch \S+, was \S+\./);
    // Not set aside, though the epoch changed under the write.
    expect(await $("body").getText()).not.toContain(SET_ASIDE);
  });

  it("an entry submitted without the typed confirmation sends nothing, the disabled button bypassed", async () => {
    await open("recovery");
    const when = stamp();
    await $('input[name="database-backup"]').setValue("synthetic-backup-364");
    await $('input[name="database-taken"]').setValue(when);
    await $('input[name="stoppedAt"]').setValue(when);
    await $('input[name="reason"]').setValue("unconfirmed synthetic restore");
    expect(await $("main form").$("button=Enter recovery").isEnabled()).toBe(false);
    await browser.execute(() => document.querySelector<HTMLFormElement>("main form[aria-label='Enter recovery']")!.requestSubmit());
    await browser.pause(1_000);
    expect(await entries()).toEqual([]);
    expect(await $("body").getText()).not.toContain("Recovery mode entered");
  });

  it("the exit with no scope in recovery is sent once, and the console reads again once, not in a loop", async () => {
    const at = await open("recovery");
    const when = stamp();
    const made = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-364-${Date.now()}-exit` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-exit", takenAt: when }], stoppedAt: when, reason: "exit synthetic restore" }),
    });
    expect(made.status).toBe(201);
    expect(await fixtureSignIn("h-recovery", {}, { upstream: at })).toMatch(/^Server .+, API revision \d+$/);
    await $('nav[aria-label="Main"]').$("a=Recovery").click();
    const exit = $("main form[aria-label='Exit recovery']");
    await expect(exit).toBeExisting({ wait: 20_000 });
    expect(await exit.$("button=Exit recovery").isEnabled()).toBe(true);
    const reads = async () => (await seen()).filter((p) => p === "/recovery").length;
    // Hold the console's next poll, so one read is in flight when the exit completes.
    await faults({ upstream: at, held: "/recovery" });
    await browser.waitUntil(async () => (await holding()).includes("/recovery"), { timeout: 15_000, timeoutMsg: "the poll was not held" });
    const inflight = await reads();
    await exit.$("button=Exit recovery").click();
    await expect(exit.$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("Recovery mode left"), { wait: 20_000 });
    // The exit's refresh waits for the read in flight: no second read overlaps it.
    await browser.pause(1_500);
    expect(await reads()).toBe(inflight);
    await faults({ upstream: at });
    await release();
    // Once it settles the refresh reads at once, well before a poll is due, and only once: no loop.
    await browser.waitUntil(async () => (await reads()) === inflight + 1, { timeout: 3_000, timeoutMsg: "the console did not read again after the exit" });
    await browser.pause(3_000);
    expect(await reads()).toBe(inflight + 1);
    expect((await writes()).filter(([, route]) => route === "/recovery/exits")).toHaveLength(1);
  });

  it("the exit and the revocation stay off for a role without recovery-admin, and the page says why", async () => {
    const at = await open("recovery");
    const when = stamp();
    const made = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-364-${Date.now()}-roles` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-roles", takenAt: when }], stoppedAt: when, reason: "roles synthetic restore" }),
    });
    expect(made.status).toBe(201);
    // Another sign-in, as a viewer: recovery mode is on, so the page is in the navigation.
    expect(await fixtureSignIn("h-viewer", {}, { upstream: at })).toMatch(/^Server .+, API revision \d+$/);
    await $('nav[aria-label="Main"]').$("a=Recovery").click();
    await expect($("main h2")).toHaveText("Recovery");
    const exit = $("main form[aria-label='Exit recovery']");
    const revoke = $("main form[aria-label='Revoke identity']");
    await expect(exit).toBeExisting({ wait: 20_000 });
    for (const [form, label] of [[exit, "Exit recovery"], [revoke, "Revoke identity"]] as const) {
      expect(await form.$(`button=${label}`).isEnabled()).toBe(false);
      expect(await form.getText()).toContain("Needs recovery-admin");
    }
    // Nothing was sent by the page.
    expect((await writes()).filter(([, route]) => route !== "/recovery/entries")).toEqual([]);
    // The role gate also stops a submit that skips the disabled button, the confirmation typed.
    await fillRevocation({ iss: FIXTURE_ISSUER, sub: "h-viewer-revokee", reason: "viewer synthetic revocation" }, "h-viewer-revokee");
    await browser.execute(() => document.querySelector<HTMLFormElement>("main form[aria-label='Revoke identity']")!.requestSubmit());
    await browser.pause(1_000);
    expect((await writes()).filter(([, route]) => route !== "/recovery/entries")).toEqual([]);
    expect(await revoke.$('[data-testid="write-result"]').isExisting()).toBe(false);
  });

  it("a revocation submitted without the typed confirmation sends nothing, the disabled button bypassed", async () => {
    await openRevocation("revocation-unconfirmed");
    await fillRevocation({ iss: FIXTURE_ISSUER, sub: "h-revokee-unconfirmed", reason: "unconfirmed synthetic revocation" }, "");
    expect(await $("main form[aria-label='Revoke identity']").$("button=Revoke identity").isEnabled()).toBe(false);
    await browser.execute(() => document.querySelector<HTMLFormElement>("main form[aria-label='Revoke identity']")!.requestSubmit());
    await browser.pause(1_000);
    expect(await revocations()).toEqual([]);
    expect(await $("body").getText()).not.toContain("Revoked ");
  });

  it("a revocation by issuer and subject is sent once and revokes that subject; one by its identity reaches that identity, not the signed-in human", async function () {
    this.timeout(60_000);
    const at = await openRevocation("revocation-targets");
    const sub = "h-approver";
    await fillRevocation({ iss: FIXTURE_ISSUER, sub, reason: "synthetic revocation by subject" }, sub);
    const form = $("main form[aria-label='Revoke identity']");
    await form.$("button=Revoke identity").click();
    await expect(form.$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("Revoked idn"), { wait: 20_000 });
    await expect(form.$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("auth.deniedSubjects lists it: no"));
    expect(await revocations()).toHaveLength(1);
    // The subject is the one revoked: its next request is refused as revoked, the caller's is not.
    const identity = (human: string) => fetch(`${at}/api/v1/identity`, { headers: { Authorization: `Bearer ${mint(human)}` } });
    expect((await identity(sub)).status).toBe(403);
    expect((await identity("h-recovery")).status).toBe(200);
    // By identity: the identity the result named, not the caller's. The server resolves that one,
    // finds it revoked and refuses with 409; a send that named the caller would revoke the caller.
    const named = /Revoked (idn\S*) \(act/.exec(await form.$('[data-testid="write-result"]').getText())?.[1];
    expect(named).toBeDefined();
    await form.$("label*=Identity:").$("input").setValue(named!);
    await clearRevocation(["Issuer", "Subject"]);
    await form.$("label*=Reason").$("input").setValue("synthetic revocation by identity");
    await form.$("label*=Type the identity").$("input").setValue(named!);
    await form.$("button=Revoke identity").click();
    await browser.waitUntil(async () => (await answers()).filter((a) => a.startsWith("/identity-revocations ")).length === 2, { timeout: 20_000, timeoutMsg: "the revocation by identity was not answered" });
    expect((await answers()).filter((a) => a.startsWith("/identity-revocations "))).toEqual(["/identity-revocations 201", "/identity-revocations 409"]);
    expect((await identity("h-recovery")).status).toBe(200);
  });
});

const revocations = async () => (await writes()).filter(([, route]) => route === "/identity-revocations");

// A recovery start with its entry made outside the page, signed in as h-recovery on the Recovery
// page. `tag` keeps the entry's key apart from the other tests'.
async function openRevocation(tag: string): Promise<string> {
  const at = await open("recovery");
  const when = stamp();
  const made = await fetch(`${at}/api/v1/recovery/entries`, {
    method: "POST",
    headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-364-${Date.now()}-${tag}` },
    body: JSON.stringify({ restored: [{ family: "database", backup: `synthetic-backup-${tag}`, takenAt: when }], stoppedAt: when, reason: `${tag} synthetic restore` }),
  });
  expect(made.status).toBe(201);
  expect(await fixtureSignIn("h-recovery", {}, { upstream: at })).toMatch(/^Server .+, API revision \d+$/);
  await $('nav[aria-label="Main"]').$("a=Recovery").click();
  await expect($("main form[aria-label='Revoke identity']")).toBeExisting({ wait: 20_000 });
  return at;
}

// Fills the revocation form by issuer and subject, and types `typed` back as the confirmation.
async function fillRevocation(v: { iss: string; sub: string; reason: string }, typed: string) {
  const form = $("main form[aria-label='Revoke identity']");
  await form.$("label*=Issuer").$("input").setValue(v.iss);
  await form.$("label*=Subject").$("input").setValue(v.sub);
  await form.$("label*=Reason").$("input").setValue(v.reason);
  if (typed !== "") await form.$("label*=Type the identity").$("input").setValue(typed);
}

async function clearRevocation(labels: string[]) {
  for (const label of labels) await $("main form[aria-label='Revoke identity']").$(`label*=${label}`).$("input").setValue("");
}

afterEach(storageEmpty);
