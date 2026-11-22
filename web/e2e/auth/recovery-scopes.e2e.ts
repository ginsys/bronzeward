// The scope decisions of the recovery console (ginsys/bronzeward#365): mark and release on a scope's own row, against a
// disposable recovery start with the seeded records. The entry is made through the API, the scope states a decision acts on
// are set by the harness (POST /scope, web/e2e/auth.conf.ts), and every press goes to the real routes. The accounting of a
// scope (pre-restore-unaccounted) is not on this page's rows yet. Every value is synthetic.
import { $, $$, browser, expect } from "@wdio/globals";
import { CONTROL } from "../auth.conf.ts";
import { absent, answers, api, disposable, eventually, faults, fixtureSignIn, holding, mint, storageEmpty, type Seed, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;

describe("the scope decisions of the recovery console (#365)", () => {
  let at = "";
  let seed: Seed;
  let m = "";

  // A recovery start with the seed and its entry made through the API: every machine's scope is pre-restore-unaccounted.
  beforeEach(async () => {
    at = await disposable("start=recovery&seed");
    seed = (await (await fetch(`${CONTROL}/seeded`)).json()) as Seed;
    m = seed.machine2;
    const when = new Date(Date.now() - 60_000).toISOString();
    const made = await fetch(`${at}/api/v1/recovery/entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Content-Type": "application/json", "Idempotency-Key": `rec-365-${Date.now()}-${Math.random().toString(36).slice(2)}` },
      body: JSON.stringify({ restored: [{ family: "database", backup: "synthetic-backup-365", takenAt: when }], stoppedAt: when, reason: "scope decisions synthetic restore" }),
    });
    expect(made.status).toBe(201);
  });

  const scope = async (machine: string, state: string) => {
    const r = await fetch(`${CONTROL}/scope?machine=${machine}&state=${state}`, { method: "POST" });
    expect([r.status, await r.text()]).toEqual([200, ""]);
  };
  // Signs in, which points the harness's faults at the deployment (its state seam acts on nothing else), sets the scopes,
  // and opens the console, whose first read finds them.
  const open = async (states: [string, string][], human = "h-recovery") => {
    expect(await fixtureSignIn(human, {}, { upstream: at })).toMatch(SERVER);
    for (const [machine, state] of states) await scope(machine, state);
    await $('nav[aria-label="Main"]').$("a=Recovery").click();
    await expect($('[data-testid="scopes"]')).toBeExisting({ wait: 20_000 });
  };
  const row = (machine: string) => $(`[data-testid="scope-${machine}"]`);
  // The injector's own lines, "<method> <route> <key> <epoch>": the shared writes() helper drops the key.
  const posts = async (route: string) =>
    ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).filter((w) => w.startsWith(`POST /recovery/scopes/${m}/${route} `));
  // The scope-state entries of `cause` a machine's timeline holds: the acts that happened, read from the API.
  const acts = async (machine: string, cause: string) => {
    const r = await api(at, "h-viewer", "GET", `/machines/${machine}/timeline?order=newest&limit=100`);
    expect(r.status).toBe(200);
    return (r.body!["items"] as { kind: string; entry: { cause?: string } }[]).filter((e) => e.kind === "scope-state" && e.entry.cause === cause).length;
  };

  it("a blocked scope can be marked, and the answer names its state and each missing item with its meaning; the others carry no control", async function () {
    this.timeout(120_000);
    await open([[m, "blocked"]]);
    await expect(row(m).$("button=Mark")).toBeExisting({ wait: 20_000 });
    expect(await row(m).$("button=Mark").isEnabled()).toBe(true);
    await absent(() => row(m).$("button=Release").isExisting(), "a release control on a blocked scope");
    // The scope of the other machine is still pre-restore-unaccounted: its accounting is not a button of this row.
    await expect(row(seed.machine)).toBeExisting();
    await absent(() => row(seed.machine).$("button=Mark").isExisting(), "a mark control on a pre-restore-unaccounted scope");
    await absent(() => row(seed.machine).$("button=Release").isExisting(), "a release control on a pre-restore-unaccounted scope");
    await row(m).$("button=Mark").click();
    const result = row(m).$('[data-testid="write-result"]');
    await expect(result).toHaveText(expect.stringMatching(/^Scope marked (blocked|ready|unresolved), was blocked \(act \S+\)\./), { wait: 60_000 });
    const text = await result.getText();
    // A scope that is not ready is missing something, and each missing item is named with its meaning; a ready one names none.
    const items = [...text.matchAll(/Missing (observation|identity|access|artifact): (\S.*)/g)];
    expect(items.length > 0).toBe(!/^Scope marked ready/.test(text));
    for (const item of items) expect(item[2]!.length).toBeGreaterThan(20);
    expect(await posts("marks")).toHaveLength(1);
    expect(await acts(m, "restoration-mark")).toBe(1);
  });

  it("a ready scope is released by one press that says it is not an approval, and the released scope has no control", async function () {
    this.timeout(120_000);
    await open([[m, "ready"]]);
    expect(await row(m).$("button=Release").isEnabled()).toBe(true);
    await row(m).$("button=Release").click();
    const result = row(m).$('[data-testid="write-result"]');
    await expect(result).toHaveText(expect.stringContaining("Scope released, was ready"), { wait: 30_000 });
    await expect(result).toHaveText(expect.stringContaining("A release is not an approval"));
    await expect(row(m).$('[data-testid="scope-state"]')).toHaveText("released", { wait: 20_000 });
    await absent(() => row(m).$("button=Release").isExisting(), "a release control on a released scope");
    await absent(() => row(m).$("button=Mark").isExisting(), "a mark control on a released scope");
    expect(await posts("releases")).toHaveLength(1);
    expect(await acts(m, "release")).toBe(1);
  });

  it("a release of a scope someone else moved is refused with the server's reason and a next step, and changes nothing", async function () {
    this.timeout(120_000);
    await open([[m, "ready"]]);
    await expect(row(m).$("button=Release")).toBeExisting();
    // The console reads again every 5 s: hold its read, so the page still shows the scope ready when the other operator's mark blocks it.
    await faults({ upstream: at, held: "/recovery" });
    await browser.waitUntil(async () => (await holding()).includes("/recovery"), { timeout: 15_000, timeoutMsg: "the poll was not held" });
    await scope(m, "blocked");
    await row(m).$("button=Release").click();
    const result = row(m).$('[data-testid="write-result"]');
    await expect(result).toHaveText(expect.stringContaining("only a ready scope is released"), { wait: 30_000 });
    await expect(row(m).$('[data-testid="write-next"]')).toHaveText(expect.stringContaining("Mark the scope, then read its recorded cause"));
    expect((await answers()).filter((a) => a.startsWith(`/recovery/scopes/${m}/releases `))).toEqual([`/recovery/scopes/${m}/releases 409`]);
    // once: read after the server's 409 was answered and shown, so the refused write is settled and the act list cannot still grow from it
    expect(await acts(m, "release")).toBe(0);
  });

  it("a release whose answer is dropped after the commit is repeated under its key and is one act", async function () {
    this.timeout(120_000);
    await open([[m, "ready"]]);
    await faults({ upstream: at, steps: { path: `/recovery/scopes/${m}/releases`, take: ["commit-drop"] } });
    await row(m).$("button=Release").click();
    await expect(row(m).$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("Scope released, was ready"), { wait: 60_000 });
    const sent = await posts("releases");
    expect(sent).toHaveLength(2);
    // One key over both sends.
    expect(sent[1]!.split(" ")[2]).toBe(sent[0]!.split(" ")[2]);
    expect(await acts(m, "release")).toBe(1);
    // A reload shows the scope released and sends nothing.
    await browser.refresh();
    await expect($('[data-testid="scopes"]')).toBeExisting({ wait: 20_000 });
    await expect(row(m).$('[data-testid="scope-state"]')).toHaveText("released", { wait: 20_000 });
    await absent(async () => (await posts("releases")).length > 2, "a release sent again after the reload");
  });

  it("a scope another operator released shows the refusal of a second release, and no control sends for more than one machine", async function () {
    this.timeout(120_000);
    await open([[m, "ready"], [seed.machine, "ready"]]);
    await faults({ upstream: at, held: "/recovery" });
    await browser.waitUntil(async () => (await holding()).includes("/recovery"), { timeout: 15_000, timeoutMsg: "the poll was not held" });
    // The other operator's release, through the API.
    const other = await api(at, "h-recovery", "POST", `/recovery/scopes/${m}/releases`, {});
    expect(other.status).toBe(201);
    await row(m).$("button=Release").click();
    await expect(row(m).$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("only a ready scope is released"), { wait: 30_000 });
    // One release per press, one machine per press: the page has no control for all scopes.
    const labels = await $$("main button").map((b) => b.getText());
    // once: read after the rows and the refusal's answer rendered, and the list names every button of the page
    expect(labels.filter((l) => /all/i.test(l))).toEqual([]);
    expect(labels.filter((l) => l === "Release")).toHaveLength(2);
    // once: the other machine's release was never pressed, and the page was read after the first machine's refusal settled
    expect(await acts(seed.machine, "release")).toBe(0);
  });

  it("the row names the machine, the page says what its controls do, and 'approval' appears only in the sentence that a release is not one", async function () {
    this.timeout(120_000);
    await open([[m, "ready"]]);
    await eventually(async () => {
      const first = await (await row(m).$$("td"))[0]!.getText();
      return first !== "" && !first.startsWith("mch_");
    }, "the machine named in the first column", 20_000);
    await expect($('[data-testid="page-note"]')).toHaveText(expect.stringContaining("Release"));
    await row(m).$("button=Release").click();
    await expect(row(m).$('[data-testid="write-result"]')).toHaveText(expect.stringContaining("Scope released"), { wait: 30_000 });
    // The row only: the page's other sentences say "approval" of plans and of the start mode.
    const text = await row(m).getText();
    const hits = [...text.matchAll(/approv\w*/gi)].map((x) => x[0]);
    const sentences = [...text.matchAll(/[^.]*approv[^.]*\./gi)].map((x) => x[0].trim());
    expect(hits.length).toBeGreaterThan(0);
    for (const s of sentences) expect(s).toMatch(/not an approval/i);
  });

  it("a signed-in operator without recovery-admin sees the controls off with the reason, and the page sends nothing", async function () {
    this.timeout(120_000);
    await open([[m, "ready"]], "h-viewer");
    // once: disabled by role in the same Action element as its reason (web/src/shell.tsx:64-73), so it cannot pass early
    expect(await row(m).$("button=Release").isEnabled()).toBe(false);
    expect(await row(m).getText()).toContain("Needs recovery-admin");
    await absent(async () => (await writes()).some((w) => w.startsWith("POST /recovery/scopes/")), "a scope write by a role that cannot decide");
    await absent(async () => (await $("main").getText()).includes("Scope released"), "a release answer for a role that cannot release");
  });
});

afterEach(storageEmpty);
