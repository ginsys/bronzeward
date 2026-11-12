// Approval and revocation on the plan page (ginsys/bronzeward#358), against the disposable
// deployment. REC-03's first case is real: the deployment restarts for recovery on the same
// database, its entry is made outside the page, and the plan creation page, still open, sends its
// write. The refusal carries the entry's epoch, whose re-read remounts the page (client contracts
// section 8.5 item 1), so the write's outcome must survive the remount (item 3). Every value is
// synthetic.
import { $, browser, expect } from "@wdio/globals";
import { RECOVERY_START } from "../../src/recovery.tsx";
import { FIXTURE_PAGE } from "../auth.conf.ts";
import { answers, disposable, faults, fixtureSignIn, mint, storageEmpty, syntheticMachine } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
const REL = `rel_${"i".repeat(26)}`;

describe("approval and revocation (#358)", () => {
  let at = "";
  let machine = "";

  before(async () => {
    at = await disposable("start=normal");
    machine = await syntheticMachine(at);
  });

  it("REC-03, first case: plan creation on a recovery start after its entry is refused without a scope and read as a recovery start", async function () {
    this.timeout(120_000);
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
    // refusal is still shown, after the epoch notice: the write's outcome is not lost with the page.
    await expect($("body")).toHaveText(expect.stringContaining("Not saved: recovery-mode-active."), { wait: 20_000 });
    expect((await answers()).filter((a) => a.startsWith("/plans "))).toEqual(["/plans 409"]);
    await expect($('[data-testid="recovery"]')).toHaveText(expect.stringContaining(RECOVERY_START), { wait: 20_000 });
  });
});

afterEach(storageEmpty);
