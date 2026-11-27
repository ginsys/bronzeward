// @vitest-environment happy-dom
// The apply page's shared block (ginsys/bronzeward#1193 contract 13, operator experience §5.4): which sources a release
// moved, the machines of this cluster each one reaches, and the other clusters named as information with no control.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { type Assignment, movedSources, reach, type Source } from "./apply-shared.ts";
import { ApplyForm } from "./apply.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const src = (over: Partial<Source>): Source => ({ kind: "fragment", scope: "library", head: "frg_1", revision: "frv_new", headRevision: null, previous: "frv_old", adoptionRevision: 3, ...over });
const asg = (over: Partial<Assignment> = {}): Assignment => ({ profiles: [], fragments: {}, ...over });

describe("the sources a release moved", () => {
  test("a shared source with an adoption whose previous revision differs; first adoptions and removals included", () => {
    const moved = src({});
    const first = src({ head: "frg_2", previous: null });
    const removed = src({ head: "frg_3", revision: null });
    expect(movedSources([moved, first, removed])).toEqual([moved, first, removed]);
  });
  test("an unmoved adoption, a cluster source and a fragment only a shared profile pins are not moved", () => {
    expect(movedSources([src({ previous: "frv_new" }), src({ scope: "cluster", adoptionRevision: null, previous: null }), src({ adoptionRevision: null, previous: null })])).toEqual([]);
  });
});

describe("the machines a moved source reaches", () => {
  const ms = ["mch_a", "mch_b", "mch_c"];
  test("a fragment reaches a machine that selects its name in any layer, and one whose profile pins the revision", () => {
    const got = reach(src({}), "ntp", ms, { mch_a: asg({ fragments: { role: ["ntp"] } }), mch_b: asg({ profiles: ["edge"] }), mch_c: asg({ profiles: ["other"] }) }, new Map([["edge", ["frv_new"]], ["other", ["frv_x"]]]));
    expect(got).toEqual({ machines: ["mch_a", "mch_b"], unknown: false });
  });
  test("a profile reaches a machine that selects its name; a profile named like an object property is a name", () => {
    const got = reach(src({ kind: "profile", head: "prf_1", revision: "prv_1" }), "constructor", ms, { mch_a: asg({ profiles: ["constructor"] }), mch_b: asg({ profiles: ["edge"] }), mch_c: asg() }, new Map());
    expect(got).toEqual({ machines: ["mch_a"], unknown: false });
  });
  test("a removed name reaches none", () => {
    expect(reach(src({ revision: null }), "ntp", ms, { mch_a: asg({ fragments: { role: ["ntp"] } }) }, new Map())).toEqual({ machines: [], unknown: false });
  });
  test("a machine whose assignment was not read, or profile pins that were not, make the list unknown, never complete", () => {
    expect(reach(src({}), "ntp", ms, { mch_a: asg({ fragments: { role: ["ntp"] } }) }, new Map())).toEqual({ machines: ["mch_a"], unknown: true });
    expect(reach(src({}), "ntp", ["mch_a"], { mch_a: asg() }, undefined)).toEqual({ machines: [], unknown: true });
  });
});

const REL = "rel_r";
const A = "mch_a";
const B = "mch_b";
const T0 = "2026-11-22T08:00:00Z";
const machine = (id: string) => ({ id, cluster: "cl_here", name: id === A ? "alpha" : "beta", role: "worker", platform: "metal", desired: REL, applied: null, pendingConvergence: true, formOnly: false, frozen: false, openDrift: null });
const needsPlan = (id: string) => ({ machine: machine(id), ingestion: null, draft: null, operation: null, rolloutHeldBy: null, step: "needs-plan", plan: null });
const row = (cluster: string, over: Record<string, unknown> = {}) => ({ cluster, revision: "frv_old", adoptionRevision: 2, behind: true, release: "rel_old", via: null, machines: ["mch_z"], ...over });
const sources = (...more: unknown[]) => [
  { kind: "assignment", scope: "cluster", head: "asg_a", revision: "asr_a", headRevision: 1, previous: null, adoptionRevision: null },
  { kind: "assignment", scope: "cluster", head: "asg_b", revision: "asr_b", headRevision: 1, previous: null, adoptionRevision: null },
  { kind: "profile", scope: "cluster", head: "prf_c", revision: "prv_edge", headRevision: 1, previous: null, adoptionRevision: null },
  ...more,
];
const served = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  [`/machines/${A}`]: machine(A), [`/machines/${B}`]: machine(B), [`/machines/${A}/position`]: needsPlan(A), [`/machines/${B}/position`]: needsPlan(B),
  "/assignment-revisions/asr_a": { id: "asr_a", cluster: "cl_here", machine: A, profiles: [], fragments: { role: ["ntp"] } },
  "/assignment-revisions/asr_b": { id: "asr_b", cluster: "cl_here", machine: B, profiles: ["edge"], fragments: {} },
  "/profile-revisions/prv_edge": { id: "prv_edge", cluster: "cl_here", name: "edge", fragments: ["frv_new"] },
  "/fragments/frg_1": { id: "frg_1", cluster: null, scope: "library", name: "ntp", layer: "global", revision: "frv_new", headRevision: 4, createdAt: T0 },
  "/fragments/frg_1/adoptions?behind=true": { items: [row("cl_here"), row("cl_other")] },
  "/clusters/cl_other": { id: "cl_other", name: "lab-b" },
  ...over,
});

describe("the apply page's shared block, mounted", () => {
  let box: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    box.remove();
  });
  async function mount(release: unknown, answers: Record<string, unknown>) {
    const reads = {
      client: { read: vi.fn(async (path: string) => (path in answers ? { kind: "ok", data: answers[path], etag: '"1"' } : { kind: "refused", status: 404, problem: null })) },
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
      undropped: () => () => true,
      view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
    } as unknown as Reads;
    const writes = new Writes(() => "p");
    const submit = vi.spyOn(writes, "submit");
    await act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(ApplyForm, { release: REL, r: release as never }))))),
    );
    await act(async () => void (await new Promise((r) => setTimeout(r, 40))));
    return { submit, reads };
  }
  const block = () => box.querySelector('[data-testid="apply-shared"]');
  const release = (...more: unknown[]) => ({ id: REL, cluster: "cl_here", sources: sources(...more), machines: [{ machine: A }, { machine: B }] });
  const moved = { kind: "fragment", scope: "library", head: "frg_1", revision: "frv_new", headRevision: null, previous: "frv_old", adoptionRevision: 3 };

  test("a moved fragment names the machines of this release it reaches and the other clusters behind it, with no control and no write", async () => {
    const { submit } = await mount(release(moved), served());
    const text = block()!.textContent!;
    expect(text).toContain("fragment ntp");
    expect(text).toContain("Reaches 2 machines here: alpha, beta");
    expect(text).toContain("Other clusters behind it, for information");
    // The release's own cluster is not "another cluster"; the other one is named (by its name once read), never as "This cluster".
    const others = block()!.querySelectorAll('[data-testid="apply-shared-other"]');
    expect(others).toHaveLength(1);
    expect(others[0]!.textContent).toContain("lab-b on frv_old, behind the shared head");
    expect(text).not.toContain("This cluster");
    // One apply is one cluster: the block offers nothing to press or choose, and pressing nothing sent nothing.
    expect(block()!.querySelector("input, button, select")).toBeNull();
    expect(submit).not.toHaveBeenCalled();
  });

  test("an adoption removed says so and reaches no machine; no other cluster behind it says that", async () => {
    await mount(release({ ...moved, revision: null }), served({ "/fragments/frg_1/adoptions?behind=true": { items: [row("cl_here")] } }));
    const text = block()!.textContent!;
    expect(text).toContain("no longer followed by this cluster");
    expect(text).toContain("Reaches no machine of this release.");
    expect(text).toContain("No other cluster is behind it.");
    expect(block()!.querySelector('[data-testid="apply-shared-other"]')).toBeNull();
  });

  test("a release that moved no shared name shows no block, and an unmoved adoption does not count", async () => {
    await mount(release({ ...moved, previous: "frv_new" }), served());
    expect(block()).toBeNull();
  });

  test("clusters that could not be read are not reported as none behind, and the reach still shows", async () => {
    const a = served();
    delete a["/fragments/frg_1/adoptions?behind=true"];
    await mount(release(moved), a);
    const text = block()!.textContent!;
    expect(text).toContain("Reaches 2 machines here");
    expect(text).not.toContain("No other cluster is behind it.");
    expect(text).not.toContain("This cluster");
  });

  test("every page of the clusters behind it is read, with no button to press for more", async () => {
    await mount(release(moved), served({
      "/fragments/frg_1/adoptions?behind=true": { items: [row("cl_here")], next: "c1" },
      "/fragments/frg_1/adoptions?behind=true&cursor=c1": { items: [row("cl_other")] },
    }));
    expect(block()!.querySelectorAll('[data-testid="apply-shared-other"]')).toHaveLength(1);
    expect(block()!.textContent).toContain("lab-b");
    expect(block()!.querySelector("input, button, select")).toBeNull();
  });

  test("a reach that could not be read at all is not reported as none", async () => {
    const a = served();
    delete a["/assignment-revisions/asr_a"];
    delete a["/assignment-revisions/asr_b"];
    await mount(release(moved), a);
    const text = block()!.textContent!;
    expect(text).toContain("Which machines it reaches could not be read.");
    expect(text).not.toContain("Reaches no machine");
  });

  test("another cluster whose name cannot be read is shown by its identifier, never as this cluster", async () => {
    const a = served();
    delete a["/clusters/cl_other"];
    await mount(release(moved), a);
    const other = block()!.querySelector('[data-testid="apply-shared-other"]')!;
    expect(other.textContent).not.toContain("This cluster");
    expect(other.querySelector("a")!.getAttribute("title")).toBe("cl_other");
  });

  test("a machine whose assignment cannot be read says the list may be short", async () => {
    const a = served();
    delete a["/assignment-revisions/asr_b"];
    await mount(release(moved), a);
    expect(block()!.textContent).toContain("Reaches 1 machine here: alpha. Some machines could not be read, so the list may be short.");
  });

  test("a shared source of a kind the page does not know is named, never dropped", async () => {
    await mount(release({ ...moved, kind: "widget", head: "wdg_1" }), served());
    expect(block()!.textContent).toContain("widget: moved by this release; this page does not know how to show its reach.");
  });
});
