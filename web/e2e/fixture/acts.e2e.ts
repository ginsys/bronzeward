// One act of the recovery-admin on a running fixture server, made in the browser as a person (ginsys/bronzeward#365), run by
// fixtures/scenarios/s6 mode web through fixture.conf.ts. BW_FIXTURE_ACT picks the case; BW_FIXTURE_OP names the operation page;
// the scenario judges the state the server ends in. Every printed line is `ACT <name>: ...` and holds page text, never a credential.
//   account: the accounting of the operation's attempt, the start and end observations chosen by id (BW_FIXTURE_START,
//            BW_FIXTURE_END) and the explanation in BW_FIXTURE_TEXT.
//   resolve: the operation resolved as cancelled on the reason in BW_FIXTURE_TEXT.
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

// Signs the page in through the provider, then opens the operation's recovery page.
async function open() {
  await browser.url(`${FIXTURE_PAGE}/`);
  await browser.waitUntil(async () => (await $('[data-testid="principal"]').isExisting()) || (await $("button*=Sign in").isExisting()), { timeout: 30_000, timeoutMsg: "the page showed neither a signed-in principal nor a Sign in button" });
  const signin = await $("button*=Sign in");
  if (await signin.isExisting()) await signin.click();
  await expect($('[data-testid="principal"]')).toBeExisting({ wait: 30_000 });
  say(`${await $('[data-testid="principal"]').getText()}; ${await $('[data-testid="roles"]').getText()}`);
  await browser.url(`${FIXTURE_PAGE}/recovery/operations/${need("BW_FIXTURE_OP")}`);
  const state = await $('[data-testid="recovery-operation-state"]');
  await expect(state).toBeExisting({ wait: 30_000 });
  say(`the page: ${await state.getText()}`);
}

describe(`fixture act ${act}`, () => {
  before(open);

  if (act === "account") {
    it("accounts for the attempt by decision on the chosen start and end", async function () {
      this.timeout(120_000);
      const form = await control(await region($("main"), "Account for an attempt"), 'form[aria-label="Record accounting"]', "the accounting form");
      const start = await choice(form, "start", need("BW_FIXTURE_START"));
      say(`start: ${(await (await start.parentElement()).getText())}`);
      await start.click();
      const end = await choice(form, "end", need("BW_FIXTURE_END"));
      say(`end: ${(await (await end.parentElement()).getText())}`);
      await end.click();
      await (await field(form, "explanation")).setValue(need("BW_FIXTURE_TEXT"));
      say(`judgment shown: ${await (await shown(form, "settle-judgment")).getText()}`);
      await (await button(form, "Record accounting")).click();
      const answer = await shown(form, "settle-answer");
      await expect(answer).toHaveText(expect.stringContaining("Recorded."), { wait: 60_000 });
      say(`answer: ${await answer.getText()}`);
    });
  } else if (act === "resolve") {
    it("resolves the operation as cancelled on a reason", async function () {
      this.timeout(120_000);
      const form = await control(await region($("main"), "Resolve as cancelled"), 'form[aria-label="Resolve as cancelled"]', "the resolve form");
      await (await field(form, "reason")).setValue(need("BW_FIXTURE_TEXT"));
      await (await button(form, "Resolve as cancelled")).click();
      const answer = await shown($("main"), "resolutions-answer");
      await expect(answer).toHaveText(expect.stringContaining("Resolved:"), { wait: 60_000 });
      say(`answer: ${await answer.getText()}`);
    });
  } else {
    throw new Error(`BW_FIXTURE_ACT must be account or resolve, not ${act}`);
  }
});
