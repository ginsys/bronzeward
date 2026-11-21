// Onboard cluster (ginsys/bronzeward#1001) against the disposable deployment, whose provider is
// mise run dev-bao's OpenBao (bao.ts) and whose Talos nodes are two stand-ins on loopback that
// POST /talos starts (internal/api e2enode_test.go). ONBOARD-01: a talosconfig chosen in the page onboards
// the cluster end to end, its first key stored through OpenBao and each machine imported to a release.
// ONBOARD-02: a step that fails stops the run, and the page's own [Run again] finishes the rest with
// nothing recorded twice. The file never crosses WebDriver, whose log the artifact scan reads: the page
// fetches it from the synthetic provider inside a script and makes the file from that answer, and the
// scan finds the run's canary (a comment in the file, which the cut to one context drops) in any retained
// file that kept a copy of the whole text. The os:reader the nodes' answers imply is proven against the
// stand-ins only: no case here reaches a real Talos node, and the deployment's address allow-list
// (ginsys/bronzeward#1009) may refuse loopback members once it lands.
import { $, $$, browser, expect } from "@wdio/globals";
import { CONTROL, FIXTURE_ISSUER } from "../auth.conf.ts";
import { absent, api, disposable, faults, fixtureSignIn, hex, storageEmptyAfterPause } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
let at = "";

// Two stand-in nodes, a cluster of their own for each case (the control server replaces the earlier pair).
async function nodes() {
  const r = await fetch(`${CONTROL}/talos`, { method: "POST" });
  expect(r.status).toBe(200);
}

// Signs in as h-author and opens the page from the machine list.
async function open() {
  expect(await fixtureSignIn("h-author", {}, { upstream: at })).toMatch(SERVER);
  await $('nav[aria-label="Main"]').$("a=Machines").click();
  await $("main").$("button=Onboard cluster").click();
  await expect($("main h2")).toHaveText("Onboard cluster");
}

// Chooses the synthetic talosconfig in the file field, fetched by the page: only whether a file was chosen leaves it.
async function chooseFile() {
  const ok = await browser.execute(async (issuer: string) => {
    const input = document.querySelector<HTMLInputElement>('main input[name="file"]');
    const r = await fetch(`${issuer}/synthetic-document?name=talosconfig`).catch(() => undefined);
    if (input === null || r?.ok !== true) return false;
    const files = new DataTransfer();
    files.items.add(new File([await r.text()], "talosconfig", { type: "application/yaml" }));
    input.files = files.files;
    return input.files.length === 1;
  }, FIXTURE_ISSUER);
  expect(ok).toBe(true);
}

// Through the file, the read and the confirmation, to the run's start.
async function start(name: string) {
  await chooseFile();
  // Before the button, the page says which endpoint it will read the cluster at: the file's, an IP address.
  await expect($('[data-testid="onboard-endpoint"]')).toHaveText(expect.stringMatching(/^The page will read the cluster at \d+\.\d+\.\d+\.\d+(:\d+)?\.$/));
  await $("main form").$("button=Read cluster").click();
  await expect($('[data-testid="onboard-found"]')).toHaveText(expect.stringContaining("2 members"), { wait: 60_000 });
  await $('input[name="name"]').setValue(name);
}

async function states(): Promise<string[]> {
  const out: string[] = [];
  for (const l of await $$('[data-testid="onboard-line"]')) out.push(`${(await l.getText()).split(":")[0]}=${await l.getAttribute("data-state")}`);
  return out;
}
const result = () => $('[data-testid="onboard-result"]');

// What the deployment holds for the cluster of `name`: its id, machines and the state of each machine's imports.
async function recorded(name: string) {
  const clusters = ((await api(at, "h-author", "GET", "/clusters")).body!["items"] as { id: string; name: string }[]).filter((c) => c.name === name);
  const id = clusters[0]?.id ?? "";
  const machines = id === "" ? [] : ((await api(at, "h-author", "GET", `/machines?cluster=${id}`)).body!["items"] as { id: string }[]);
  const imports: string[] = [];
  for (const m of machines) for (const i of (await api(at, "h-author", "GET", `/ingestions?machine=${m.id}`)).body!["items"] as { state: string }[]) imports.push(i.state);
  const drafts = ((await api(at, "h-author", "GET", "/drafts")).body!["items"] as { cluster: string; title: string }[]).filter((d) => d.cluster === id && d.title === `onboard ${name}`);
  return { clusters: clusters.length, machines: machines.length, imports, drafts: drafts.length };
}

describe("Onboard cluster (#1001)", () => {
  // A deployment of its own for each case: the stand-in nodes carry the same cluster ID and machine
  // identities each time, so a second case on one deployment would find the first's records.
  beforeEach(async () => {
    at = await disposable("start=normal");
  });

  it("ONBOARD-01: a talosconfig chosen in the page records the cluster, supplies its first key and imports each machine to a release, and the page keeps no copy of the file", async function () {
    this.timeout(240_000);
    await nodes();
    await open();
    const name = `synthetic-${hex()}`;
    await start(name);
    await $("main form").$("button=Onboard cluster").click();
    await expect(result()).toHaveText(expect.stringMatching(/^Onboarded\./), { wait: 180_000 });
    expect(await states()).toEqual([
      `Cluster ${name}=done`, "First key=done", "Machine cp-1=done", "Machine w-1=done", "Draft onboard " + name + "=done", "Import cp-1=done", "Import w-1=done",
    ]);
    expect(await recorded(name)).toEqual({ clusters: 1, machines: 2, imports: ["released", "released"], drafts: 1 });
    // The file's key is in no part of the page, and nothing is kept in storage, IndexedDB or caches.
    // A file with no key to look for reads as shown, so the check cannot pass by finding nothing to compare.
    await absent(() => browser.execute(async (issuer: string) => {
      const text = await (await fetch(`${issuer}/synthetic-document?name=talosconfig`)).text();
      const key = /^\s*key: (\S+)/m.exec(text)?.[1];
      return key === undefined || key.length < 20 ? true : document.documentElement.outerHTML.includes(key);
    }, FIXTURE_ISSUER), "the file's key in the page");
    await storageEmptyAfterPause();
  });

  it("ONBOARD-02: a refused draft stops the run after the cluster, the first key and the machines, and [Run again] finishes it with nothing recorded twice", async function () {
    this.timeout(240_000);
    await nodes();
    await open();
    const name = `synthetic-${hex()}`;
    await start(name);
    await faults({ upstream: at, steps: { path: "/drafts", take: ["scope-refused"] } });
    await $("main form").$("button=Onboard cluster").click();
    await expect(result()).toHaveText(expect.stringContaining(`Stopped at Draft onboard ${name}`), { wait: 120_000 });
    expect(await states()).toEqual([`Cluster ${name}=done`, "First key=done", "Machine cp-1=done", "Machine w-1=done", `Draft onboard ${name}=failed`]);
    await $("main").$("button=Run again").click();
    await expect(result()).toHaveText(expect.stringMatching(/^Onboarded\./), { wait: 180_000 });
    expect(await recorded(name)).toEqual({ clusters: 1, machines: 2, imports: ["released", "released"], drafts: 1 });
    await storageEmptyAfterPause();
  });
});
