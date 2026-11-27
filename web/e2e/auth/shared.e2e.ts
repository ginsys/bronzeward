// Shared fragments in the web (ginsys/bronzeward#1193, criteria 1 and 2; the operator page shared-fragments), against the
// disposable deployment. A plain one has no library and no second cluster, so the cases serve the draft, the shared fragment,
// the clusters using it and the reach of a draft's entries as bodies of the injector, and assert what the page shows and that
// it sends nothing. The cluster A and cluster B of the page are CL and OTHER here. Every value is synthetic.
import { $, $$, browser, expect } from "@wdio/globals";
import { FIXTURE_PAGE } from "../auth.conf.ts";
import { disposable, fixtureSignIn, writes } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;
// A well-formed identifier (128-bit lower-case base32, so the last character is one whose low two bits are zero): the server
// parses the identifiers in a request before it looks anything up, and answers the unknown ones from the injector's bodies.
const id = (p: string, c: string) => `${p}_${c.repeat(25)}a`;
const CL = id("cl", "c");
const OTHER = id("cl", "o");
const DRF = id("drf", "d");
const FRG = id("frg", "f");
const FRV_NEW = id("frv", "n");
const FRV_OLD = id("frv", "o");
const M = id("mch", "m");
const T0 = "2026-11-22T08:00:00Z";

describe("shared fragments on the draft page and the shared fragment page (#1193)", () => {
  let at = "";
  before(async () => {
    at = await disposable("start=normal");
  });

  const head = { id: FRG, cluster: null, scope: "library", name: "ntp", layer: "global", revision: FRV_NEW, headRevision: 4, createdAt: T0 };
  // This cluster follows the name behind the head; the other cluster follows it at the head.
  const row = (cluster: string, revision: string, behind: boolean) => ({ cluster, revision, adoptionRevision: 2, behind, release: null, via: null, machines: [] });
  const draft = (entries: unknown[]) => ({ id: DRF, cluster: CL, title: "shared ntp", state: "open", revision: 2, entries });
  const adoption = { kind: "adoption", of: "fragment", name: "ntp", head: FRG, base: 2, revision: FRV_NEW };
  const machine = { id: M, cluster: CL, name: "alpha", role: "worker", hardware: { smbiosUuid: null, talosNodeId: "synthetic-alpha", serial: null }, platform: "metal", talosEndpoint: "192.0.2.1:50000", desired: null, applied: null, pendingConvergence: false, formOnly: false, observed: null, frozen: false, scopeState: "normal", openDrift: null };
  const bodies = (entries: unknown[]) => ({
    [`/drafts/${DRF}`]: draft(entries),
    [`/drafts/${DRF}/impact`]: {
      items: [{ kind: "fragment", name: "ntp", scope: "library", machines: [{ machine: M, reason: "assignment", change: "gains" }], otherClusters: [{ cluster: OTHER, revision: FRV_OLD, via: null, unchanged: true }] }],
    },
    "/fragments": { items: [head] },
    [`/fragments/${FRG}`]: head,
    [`/fragments/${FRG}/adoptions`]: { items: [row(CL, FRV_OLD, true), row(OTHER, FRV_NEW, false)] },
    "/profiles": { items: [] },
    [`/clusters/${CL}`]: { id: CL, name: "lab-a" },
    [`/clusters/${OTHER}`]: { id: OTHER, name: "lab-b" },
    [`/machines/${M}`]: machine,
  });
  async function open(path: string, b: Record<string, unknown>) {
    expect(await fixtureSignIn("h-all", {}, { upstream: at, bodies: b })).toMatch(SERVER);
    await browser.url(`${FIXTURE_PAGE}${path}`);
  }
  // writes() lists only the writes the injector received since the sign-in: reads are not in it.
  const posts = writes;

  it("the Shared names section shows, once opened, what this cluster follows of each shared fragment, and the controls that apply [step shared-fragments.1]", async function () {
    this.timeout(120_000);
    await open(`/drafts/${DRF}`, bodies([]));
    await expect($("button=Show shared names")).toBeExisting({ wait: 20_000 });
    await $("button=Show shared names").click();
    const list = () => $('ul[aria-label="Shared fragments"]');
    await expect(list()).toHaveText(expect.stringContaining("fragment ntp"), { wait: 20_000 });
    await expect(list()).toHaveText(expect.stringContaining("behind the shared head"), { wait: 20_000 });
    // The press of a control changes this draft only; this case presses none, and the page sends a write only from a press.
    await expect(list().$("button=Adopt ntp")).toBeExisting();
    // once: the page sends a write only from a press, and the case pressed none.
    expect(await posts()).toEqual([]);
  });

  it("What the shared entries reach lists the machines of this cluster and the other clusters that stay as they are [step shared-fragments.2]", async function () {
    this.timeout(120_000);
    await open(`/drafts/${DRF}`, bodies([adoption]));
    const reach = () => $('section[aria-label="Reach of shared entries"]');
    await expect(reach()).toHaveText(expect.stringContaining("Gains it here: 1 machine"), { wait: 20_000 });
    await expect(reach()).toHaveText(expect.stringContaining("Stays unchanged: lab-b"), { wait: 20_000 });
    await expect(reach()).toHaveText(expect.stringContaining("alpha"));
    // once: the reach block holds no control, and a control that came late would have come with its list.
    expect(await reach().$$("button, input, select").length).toBe(0);
    // once: the page sends a write only from a press, and the case pressed none.
    expect(await posts()).toEqual([]);
  });

  it("a shared fragment's page lists every cluster using it and whether it is behind, and changes no cluster [step shared-fragments.3]", async function () {
    this.timeout(120_000);
    await open(`/fragments/${FRG}`, bodies([]));
    const clusters = () => $('[aria-label="Clusters using this name"]');
    await expect(clusters()).toHaveText(expect.stringContaining("lab-a"), { wait: 20_000 });
    await expect(clusters()).toHaveText(expect.stringContaining("lab-b"));
    await expect(clusters()).toHaveText(expect.stringContaining("behind the shared head"));
    await expect(clusters()).toHaveText(expect.stringContaining("on the shared head"));
    expect(await $$("main table tbody tr").length).toBe(2);
    // once: the clusters' list holds no control, and the case pressed nothing.
    expect(await clusters().$$("button, input, select").length).toBe(0);
    // once: the page sends a write only from a press, and the case pressed none.
    expect(await posts()).toEqual([]);
  });
});
