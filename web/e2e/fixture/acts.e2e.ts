// One act of the recovery-admin on a running fixture server, made in the browser as a person (ginsys/bronzeward#365), run by
// fixtures/scenarios/s6 and s7 mode web through fixture.conf.ts. BW_FIXTURE_ACT picks the case; the scenario judges the state the
// server ends in. Every printed line is `ACT <name>: ...` and holds page text, never a credential.
// On an operation's recovery page (BW_FIXTURE_OP names the operation):
//   account:  the accounting of the operation's attempt, the start and end observations chosen by id (BW_FIXTURE_START,
//             BW_FIXTURE_END) and the explanation in BW_FIXTURE_TEXT.
//   resolve:  the operation resolved as cancelled on the reason in BW_FIXTURE_TEXT.
//   takeover: the operation taken over on the reason in BW_FIXTURE_TEXT.
// On the console (BW_FIXTURE_MACHINE names the scope's row):
//   enter:    the entry form of a recovery start, from BW_FIXTURE_DB_BACKUP and BW_FIXTURE_DB_TAKEN, BW_FIXTURE_PROVIDER_BACKUP and
//             BW_FIXTURE_PROVIDER_TAKEN, BW_FIXTURE_STOPPED and the reason in BW_FIXTURE_TEXT (no machine).
//   scope-account: the scope's accounting on its row, with BW_FIXTURE_START, BW_FIXTURE_END and BW_FIXTURE_TEXT as above.
//   mark, release: the row's one press, its answer printed.
//   exit:     the exit form (no machine).
import { $, browser, expect } from "@wdio/globals";
import { FIXTURE_PAGE } from "../ports.ts";
import { button, choice, control, field, region, shown } from "./control.ts";

const need = (name: string) => {
  const v = process.env[name];
  if (v === undefined || v === "") throw new Error(`${name} must be set by the scenario`);
  return v;
};
const act = need("BW_FIXTURE_ACT");
const say = (line: string) => console.log(`ACT ${act}: ${line.replace(/\s+/g, " ")}`);
const OPERATION = ["account", "resolve", "takeover"];

// Signs the page in through the provider, then opens the act's page: the operation's recovery page, the console, or (the entry, whose
// form the shell shows on any page of a recovery start before its entry) the page the sign-in returns to.
async function open() {
  await browser.url(`${FIXTURE_PAGE}/`);
  await browser.waitUntil(async () => (await $('[data-testid="principal"]').isExisting()) || (await $("button*=Sign in").isExisting()), { timeout: 30_000, timeoutMsg: "the page showed neither a signed-in principal nor a Sign in button" });
  const signin = await $("button*=Sign in");
  if (await signin.isExisting()) await signin.click();
  await expect($('[data-testid="principal"]')).toBeExisting({ wait: 30_000 });
  say(`${await $('[data-testid="principal"]').getText()}; ${await $('[data-testid="roles"]').getText()}`);
  if (act === "enter") return;
  if (OPERATION.includes(act)) {
    await browser.url(`${FIXTURE_PAGE}/recovery/operations/${need("BW_FIXTURE_OP")}`);
    const state = await $('[data-testid="recovery-operation-state"]');
    await expect(state).toBeExisting({ wait: 30_000 });
    say(`the page: ${await state.getText()}`);
  } else {
    await browser.url(`${FIXTURE_PAGE}/recovery`);
    await expect($('[data-testid="scopes"]')).toBeExisting({ wait: 30_000 });
  }
}

// A control that is off while the page reads what it sends is waited for, then pressed.
async function press(form: WebdriverIO.Element, name: string) {
  const b = await button(form, name);
  await b.waitForEnabled({ timeout: 30_000, timeoutMsg: `the ${name} button stayed off for 30 s` });
  await b.click();
}
// The accounting form of an attempt or a scope: the chosen start and end, the explanation, one press.
async function account(form: WebdriverIO.Element) {
  // The form reads the machine's observations after the page is up and draws no radio until they arrive. There are many radios, so the
  // wait counts them: a single-element wait fails on a strict-selector error as soon as there is more than one.
  await browser.waitUntil(async () => (await form.$$('input[type="radio"]')).length > 0, { timeout: 30_000, interval: 500, timeoutMsg: "the accounting form listed no observation within 30 s" });
  say(`the scenario's start ${process.env["BW_FIXTURE_START"] || "<empty>"}, end ${process.env["BW_FIXTURE_END"] || "<empty>"}`);
  for (const line of await offer(form)) say(`offered: ${line}`);
  const start = await pick(form, "start", need("BW_FIXTURE_START"));
  say(`start: ${(await (await start.parentElement()).getText())}`);
  await start.click();
  const end = await pick(form, "end", need("BW_FIXTURE_END"));
  say(`end: ${(await (await end.parentElement()).getText())}`);
  await end.click();
  await (await field(form, "explanation")).setValue(need("BW_FIXTURE_TEXT"));
  say(`judgment shown: ${await (await shown(form, "settle-judgment")).getText()}`);
  await press(form, "Record accounting");
  const answer = await shown(form, "settle-answer");
  await expect(answer).toHaveText(expect.stringContaining("Recorded."), { wait: 60_000 });
  say(`answer: ${await answer.getText()}`);
}
const row = () => control($("main"), `[data-testid="scope-${need("BW_FIXTURE_MACHINE")}"]`, "the scope's row");
// A row's one-press decision: its answer is the row's own result text.
async function decide(label: string, answered: string) {
  const r = await row();
  await press(r, label);
  const answer = await shown(r, "write-result");
  await expect(answer).toHaveText(expect.stringContaining(answered), { wait: 60_000 });
  say(`answer: ${await answer.getText()}`);
}

// What the accounting form offers, as page text: each radio's group, its label and whether it is off. It is printed before any choice,
// so a run that stops says what the page showed and needs no second run to find out.
async function offer(form: WebdriverIO.Element): Promise<string[]> {
  const lines: string[] = [];
  for (const r of await form.$$('input[type="radio"]')) {
    const value = await r.getAttribute("value");
    const wanted = value === process.env["BW_FIXTURE_START"] || value === process.env["BW_FIXTURE_END"] ? " (the scenario's)" : "";
    lines.push(`${await r.getAttribute("name")} ${value} ${(await (await r.parentElement()).getText()).replace(/\s+/g, " ").slice(0, 160)}${(await r.isEnabled()) ? "" : " (off)"}${wanted}`);
  }
  return lines;
}
// The radio of a group with the wanted value, chosen only when it is drawn and on. It is waited for up to 30 s, since the form draws the
// observations as they arrive and turns a radio on or off when the server's settings and the deadline arrive; a run that stops says which
// of the two it met: not drawn, or drawn but off (the form marks one start valid).
async function pick(form: WebdriverIO.Element, group: string, value: string): Promise<WebdriverIO.Element> {
  const css = `input[type="radio"][name="${group}"][value="${value}"]`;
  const ready = async () => {
    const found = await form.$$(css);
    return found.length === 1 && (await found[0]!.isEnabled());
  };
  await browser.waitUntil(ready, { timeout: 30_000, interval: 500 }).catch(() => undefined);
  const listing = (await offer(form)).join(" | ");
  if ((await form.$$(css)).length !== 1) throw new Error(`the ${group} radio ${value} was not drawn within 30 s; the form offers: ${listing}`);
  const radio = await choice(form, group, value);
  if (!(await radio.isEnabled())) throw new Error(`the ${group} radio ${value} is drawn but off after 30 s; the form offers: ${listing}`);
  return radio;
}

describe(`fixture act ${act}`, () => {
  before(open);

  if (act === "account") {
    it("accounts for the attempt by decision on the chosen start and end", async function () {
      this.timeout(120_000);
      await account(await control(await region($("main"), "Account for an attempt"), 'form[aria-label="Record accounting"]', "the accounting form"));
    });
  } else if (act === "resolve" || act === "takeover") {
    const label = act === "resolve" ? "Resolve as cancelled" : "Take over";
    it(`${act === "resolve" ? "resolves the operation as cancelled" : "takes the operation over"} on a reason`, async function () {
      this.timeout(120_000);
      const form = await control(await region($("main"), label), `form[aria-label="${label}"]`, `the ${label} form`);
      await (await field(form, "reason")).setValue(need("BW_FIXTURE_TEXT"));
      await press(form, label);
      const answer = await shown($("main"), act === "resolve" ? "resolutions-answer" : "takeovers-answer");
      await expect(answer).toHaveText(expect.stringContaining(act === "resolve" ? "Resolved:" : "Taken over:"), { wait: 60_000 });
      say(`answer: ${await answer.getText()}`);
    });
  } else if (act === "enter") {
    it("enters recovery on the restored backups, the stop and a reason", async function () {
      this.timeout(120_000);
      const form = await control($("main"), 'form[aria-label="Enter recovery"]', "the entry form");
      for (const [name, env] of [["database-backup", "BW_FIXTURE_DB_BACKUP"], ["database-taken", "BW_FIXTURE_DB_TAKEN"], ["provider-backup", "BW_FIXTURE_PROVIDER_BACKUP"], ["provider-taken", "BW_FIXTURE_PROVIDER_TAKEN"], ["stoppedAt", "BW_FIXTURE_STOPPED"], ["reason", "BW_FIXTURE_TEXT"]] as const) {
        await (await field(form, name)).setValue(need(env));
      }
      await press(form, "Enter recovery");
      // The result is kept in the header, since the epoch change ends the form's own page.
      const entered = await shown($("body"), "entered");
      await expect(entered).toHaveText(expect.stringContaining("Recovery mode entered (act"), { wait: 60_000 });
      say(`answer: ${await entered.getText()}`);
    });
  } else if (act === "scope-account") {
    it("accounts for the scope on its row", async function () {
      this.timeout(120_000);
      await account(await control(await row(), 'form[aria-label="Record accounting"]', "the scope's accounting form"));
    });
  } else if (act === "mark") {
    it("marks the scope", async function () {
      this.timeout(120_000);
      await decide("Mark", "Scope marked");
    });
  } else if (act === "release") {
    it("releases the scope", async function () {
      this.timeout(120_000);
      await decide("Release", "Scope released");
    });
  } else if (act === "exit") {
    it("exits recovery mode", async function () {
      this.timeout(120_000);
      const form = await control($("main"), 'form[aria-label="Exit recovery"]', "the exit form");
      await press(form, "Exit recovery");
      const answer = await shown(form, "write-result");
      await expect(answer).toHaveText(expect.stringContaining("Recovery mode left"), { wait: 60_000 });
      say(`answer: ${await answer.getText()}`);
    });
  } else {
    throw new Error(`BW_FIXTURE_ACT must be one of account, resolve, takeover, enter, scope-account, mark, release, exit, not ${act}`);
  }
});
