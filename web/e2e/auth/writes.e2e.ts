// The first write views (ginsys/bronzeward#338) against a disposable deployment through the
// injector: AMB-01, AMB-03, AMB-05, AMB-07, AMB-08, AMB-09, AMB-12, TLS-03's write and AUTH-07's
// pending write (conformance/cases/), a second submit, the leave guards and each form sent by
// keyboard only. Each faulted write is "Add cluster" as h-author, with a synthetic name and a
// random cluster ID; machines get a synthetic node ID and a documentation-range address. The
// injector's `steps` fault answers each send of the write in turn and lets the rest through; its
// epoch commits a new epoch in the deployment's database, as a recovery entry does.
import { randomBytes } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { PRINCIPAL_CHANGED } from "../../src/app.tsx";
import { REPLAYED, SET_ASIDE, UNKNOWN, unknown } from "../../src/states.tsx";
import { CONTROL } from "../auth.conf.ts";
import type { Faults, Step } from "../injector.ts";
import { answers, faults, fixtureSignIn, holding, release, storageEmpty } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
let at = "";

// Every write the injector received since the sign-in, as [method, route, key, expected epoch].
const writes = async (): Promise<string[][]> => ((await (await fetch(`${CONTROL}/writes`)).json()) as string[]).map((w) => w.split(" "));
// The server's answers to the write the injector passed on: the list's reads answer 200, a write never.
const writeAnswers = async () => (await answers()).filter((a) => a.startsWith("/clusters ") && a !== "/clusters 200");
const result = () => $('[data-testid="write-result"]');
const notice = () => $('[data-testid="write-notice"]');
const sends = (n: number) => browser.waitUntil(async () => (await writes()).length >= n, { timeout: 30_000, timeoutMsg: `the page did not send the write ${n} times` });

// Signs in as h-author on the disposable deployment and opens Add cluster from the machine list.
async function open(flags: Record<string, string> = {}): Promise<string> {
  expect(await fixtureSignIn("h-author", flags, { upstream: at })).toMatch(SERVER);
  // The sign-in cleared the write log, and no write of an earlier case reached it since.
  expect(await writes()).toEqual([]);
  await $('nav[aria-label="Main"]').$("a=Machines").click();
  await $("main").$("button=Add cluster").click();
  await expect($("main h2")).toHaveText("Add cluster");
  return `synthetic-${randomBytes(4).toString("hex")}`;
}

async function fill(name: string) {
  await $('input[name="name"]').setValue(name);
  await $('input[name="endpoint"]').setValue(`https://${name}.example.test:6443`);
  await $('input[name="contract"]').setValue("v1.13");
  await $('input[name="talosClusterId"]').setValue(`${randomBytes(32).toString("base64url")}=`);
}

// Opens the form, fills it, sets the faults for its sends, and submits it.
async function add(take: Step[], f: Faults = {}, flags: Record<string, string> = {}): Promise<string> {
  const name = await open(flags);
  await fill(name);
  await faults({ upstream: at, steps: { path: "/clusters", take }, ...f });
  await $("main form").$("button=Add cluster").click();
  return name;
}

// One key and one reference epoch over every send, n sends in all.
async function oneWrite(n: number): Promise<{ key: string; epoch: string }> {
  const w = await writes();
  expect(w).toHaveLength(n);
  const [method, route, key, epoch] = w[0]!;
  expect([method, route]).toEqual(["POST", "/clusters"]);
  expect(key).toMatch(/^[A-Za-z0-9_-]{22}$/);
  for (const x of w) expect(x).toEqual(w[0]);
  return { key: key!, epoch: epoch! };
}

// Moves the focus to each named field with Tab only, types its value (replacing the field's own
// when `replace`), and sends the form with Enter.
async function type(values: [string, string][], replace = false) {
  const focused = () => browser.execute(() => document.activeElement?.getAttribute("name"));
  for (const [field, value] of values) {
    for (let i = 0; i < 40 && (await focused()) !== field; i++) await browser.keys("Tab");
    expect(await focused()).toBe(field);
    if (replace) await browser.keys(["Control", "a"]);
    await browser.keys(value.split(""));
  }
  await browser.keys("Enter");
}

describe("the write views (#338)", () => {
  before(async () => {
    const res = await fetch(`${CONTROL}/disposable?start=normal`, { method: "POST" });
    expect(res.status).toBe(200);
    at = await res.text();
  });

  it("the harness commits a new epoch only in the disposable deployment", async () => {
    // The page is open, on the fixture deployment, so the storage check after each case reads it.
    expect(await fixtureSignIn("h-viewer")).toMatch(SERVER);
    await faults({});
    const refused = await fetch(`${CONTROL}/epoch`, { method: "POST" });
    expect([refused.status, await refused.text()]).toEqual([500, expect.stringContaining("only in this run's disposable deployment")]);
  });

  it("AMB-01: a connection dropped before the server saw the write is repeated with the same key, and done", async () => {
    const name = await add(["drop"]);
    await expect(result()).toHaveText(expect.stringContaining(`Cluster ${name} recorded as`), { wait: 20_000 });
    expect((await result().getText()).includes(REPLAYED)).toBe(false);
    await oneWrite(2);
    expect(await notice().isExisting()).toBe(false);
  });

  it("AMB-03: a 500 internal-error after the commit is repeated with the same key; the replayed result is shown", async () => {
    const name = await add(["commit-500"]);
    await expect(result()).toHaveText(expect.stringContaining(`${REPLAYED} Cluster ${name} recorded as`), { wait: 20_000 });
    await oneWrite(2);
  });

  it("AMB-08: a write answered 503 dependency-unavailable each time is repeated 3 times with the same key, then not done", async function () {
    this.timeout(60_000);
    await add(["503", "503", "503", "503"]);
    await expect(result()).toHaveText("Not saved: every attempt was answered 503 dependency-unavailable, which commits nothing.", { wait: 30_000 });
    await oneWrite(4);
    expect(await notice().isExisting()).toBe(false);
  });

  it("AMB-09: a write dropped after the commit, each repeat answered 503, is unknown with its key and resource, never not done", async function () {
    this.timeout(60_000);
    await add(["commit-drop", "503", "503", "503"]);
    await expect(notice()).toHaveText(expect.stringContaining(unknown("the server answered 503 dependency-unavailable")), { wait: 30_000 });
    const { key } = await oneWrite(4);
    expect(await notice().getText()).toContain(`Key ${key}, POST /api/v1/clusters. To learn the outcome, read /clusters.`);
    expect(await result().getText()).not.toContain("Not saved");
  });

  it("TLS-03: a write that meets a refused redirect each time is repeated with the same key, then unknown, never not done", async function () {
    this.timeout(60_000);
    await add(["redirect", "redirect", "redirect", "redirect"]);
    await expect(notice()).toHaveText(expect.stringContaining(UNKNOWN), { wait: 30_000 });
    const { key } = await oneWrite(4);
    expect(await notice().getText()).toContain(`Key ${key}`);
    expect(await notice().getText()).not.toContain("127.0.0.1:9");
  });

  it("AMB-05: a write dropped after the commit, then a new epoch, is set aside unrepeated; the resource read shows it", async () => {
    const name = await add(["commit-drop-epoch"]);
    await expect(notice()).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 20_000 });
    await oneWrite(1);
    // The notice's buttons follow its text, in the same block.
    expect(await notice().parentElement().$("button=Repeat exactly").isEnabled()).toBe(false);
    await notice().parentElement().$("button=Show current state").click();
    await expect($("main")).toHaveText(expect.stringContaining(name), { wait: 20_000 });
  });

  it("AMB-07: a new epoch after the confirming read meets the earlier epoch's record: 409, set aside, no new key", async () => {
    await add(["commit-drop", "epoch"]);
    await expect(notice()).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 20_000 });
    await oneWrite(2);
    expect(await writeAnswers()).toEqual(["/clusters 201", "/clusters 409"]);
  });

  it("AMB-12: as AMB-07 with no record of the first attempt: the repeat carries its epoch and is refused 412, set aside", async () => {
    await add(["drop", "epoch"]);
    await expect(notice()).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 20_000 });
    await oneWrite(2);
    expect(await writeAnswers()).toEqual(["/clusters 412"]);
  });

  it("AUTH-07: a renewal for another subject while the write is pending sets it aside", async function () {
    this.timeout(90_000);
    // expires_in 24 s: the renewal comes 12 s after the sign-in, while the write's answer is held.
    await add(["commit-drop"], { held: "/clusters" }, { rotate: "1", lifetime: "24", "renew-as": "h-publisher" });
    await browser.waitUntil(async () => (await holding()).includes("/clusters"), { timeout: 20_000, timeoutMsg: "the write was not held" });
    await expect($('[data-testid="status"]')).toHaveText(PRINCIPAL_CHANGED, { wait: 30_000 });
    await release();
    await expect(notice()).toHaveText(expect.stringContaining(SET_ASIDE), { wait: 20_000 });
    await oneWrite(1);
  });

  it("a second submit while the write is in flight sends nothing new", async () => {
    const name = await add([], { held: "/clusters" });
    await browser.waitUntil(async () => (await holding()).includes("/clusters"), { timeout: 20_000, timeoutMsg: "the write was not held" });
    expect(await $("main form").$("button=Add cluster").isEnabled()).toBe(false);
    // A second submit event of the same form, its button disabled or not: the form sends nothing new.
    await browser.execute(() => document.querySelector<HTMLFormElement>("main form")!.requestSubmit());
    await browser.pause(500);
    expect(await writes()).toHaveLength(1);
    await release();
    await expect(result()).toHaveText(expect.stringContaining(`Cluster ${name} recorded as`), { wait: 20_000 });
    await oneWrite(1);
  });

  it("the leave guard asks before the page moves on from a write with no answer, and the write stays", async () => {
    await add([], { held: "/clusters" });
    await browser.waitUntil(async () => (await holding()).includes("/clusters"), { timeout: 20_000, timeoutMsg: "the write was not held" });
    const { key } = await oneWrite(1);
    const asked: string[] = [];
    const answer = (d: { message: () => string; dismiss: () => Promise<void> }) => void (asked.push(d.message()), d.dismiss());
    browser.on("dialog", answer);
    try {
      await $('nav[aria-label="Main"]').$("a=Machines").click();
      await browser.waitUntil(async () => asked.length === 1, { timeout: 10_000, timeoutMsg: "no leave dialog" });
    } finally {
      browser.off("dialog", answer);
    }
    expect(asked[0]).toContain(`Sent, with no answer yet. Key ${key}, POST /api/v1/clusters.`);
    await expect($("main h2")).toHaveText("Add cluster");
    await release();
    await expect(result()).toHaveText(expect.stringContaining("recorded as"), { wait: 20_000 });
  });

  // WebDriver accepts the browser's own leave dialog of a reload, so the case reads the guard's
  // answer to an unload event and then reloads: the pending write is dropped, never repeated.
  it("a reload with a write pending is guarded, and drops the write", async () => {
    await add([], { held: "/clusters" });
    await browser.waitUntil(async () => (await holding()).includes("/clusters"), { timeout: 20_000, timeoutMsg: "the write was not held" });
    const guarded = () => browser.execute(() => !dispatchEvent(new Event("beforeunload", { cancelable: true })));
    expect(await guarded()).toBe(true);
    await browser.refresh();
    expect(await guarded()).toBe(false);
    await release();
    // Longer than the three repeats' 1 + 2 + 4 s.
    await browser.pause(8_000);
    await oneWrite(1);
    expect(await notice().isExisting()).toBe(false);
  });

  it("keyboard only: the form is filled and sent without the pointer", async () => {
    const name = await open();
    await faults({ upstream: at });
    await type([["name", name], ["endpoint", `https://${name}.example.test:6443`], ["contract", "v1.13"], ["talosClusterId", `${randomBytes(32).toString("base64url")}=`]]);
    await expect(result()).toHaveText(expect.stringContaining(`Cluster ${name} recorded as`), { wait: 20_000 });
    await oneWrite(1);
  });

  it("keyboard only: Record machine and Change endpoint are filled and sent without the pointer", async () => {
    const name = await add([]);
    await expect(result()).toHaveText(expect.stringContaining(`Cluster ${name} recorded as`), { wait: 20_000 });
    await $('nav[aria-label="Main"]').$("a=Machines").click();
    await $("main").$("button=Record machine").click();
    await expect($("main h2")).toHaveText("Record machine");
    // The cluster is chosen by typing its name into the list, the platform left at metal.
    const [ip, next] = [`192.0.2.${1 + randomBytes(1)[0]! % 254}`, `198.51.100.${1 + randomBytes(1)[0]! % 254}`];
    await type([["cluster", name], ["talosNodeId", `synthetic-node-${randomBytes(4).toString("hex")}`], ["talosEndpoint", ip]]);
    await expect(result()).toHaveText(expect.stringContaining(`Endpoint ${ip}:50000. Next: give Bronzeward the cluster's Talos access in the provider`), { wait: 20_000 });
    await result().$("a").click();
    await expect($("main h2")).toHaveText(expect.stringContaining("Machine "));
    await type([["talosEndpoint", next]], true);
    await expect(result()).toHaveText(expect.stringContaining(`Endpoint changed from ${ip}:50000 to ${next}:50000`), { wait: 20_000 });
    const w = await writes();
    expect(w.map(([m, r]) => `${m} ${r}`)).toEqual(["POST /clusters", "POST /machines", expect.stringMatching(/^POST \/machines\/[^/]+\/talos-endpoints$/)]);
    expect(new Set(w.map((x) => x[2])).size).toBe(3);
  });
});

afterEach(storageEmpty);
