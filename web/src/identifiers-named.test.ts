// @vitest-environment happy-dom
// No bare identifier where the object has a name (part 8 of ginsys/bronzeward#1055): a release, a cluster, an
// operation, a plan and a person show their name or title as the text on every page the console walk found
// them bare, with the identifier kept as the title attribute, and "you" for the signed-in person.

import { act, createElement as h, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { MachineShown, TimelineRows } from "./machine.tsx";
import { NextStepSection } from "./review.tsx";
import { NodeCell } from "./machines.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { DraftItem } from "./drafts.tsx";
import { PlanItem, ReleaseItem } from "./resources.tsx";
import { ShellContext } from "./shell.tsx";
import type { ReadResult } from "./request.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ok = (data: unknown): ReadResult<unknown> => ({ kind: "ok", data });
const missing: ReadResult<unknown> = { kind: "refused", status: 404, problem: null };
const at = "2026-11-19T10:00:00Z";
const MCH = "mch_k2pq3rstuvwxyz234567abcdef";
const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const REL = "rel_k2pq3rstuvwxyz234567abcdef";
const PLN = "pln_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdef";
const CL = "cl_k2pq3rstuvwxyz234567abcdef";
const YOU = "idn_k2pq3rstuvwxyz234567abcdea";
const OTHER = "idn_zzzzzzzzzzzzzzzzzzzzzzzzza";

let root: Root;
let box: HTMLElement;
let answers: Record<string, ReadResult<unknown>>;

const settle: Reads["settle"] = (res, set, live, held) => {
  if (res.kind !== "ok" && held()) return true;
  if (live()) set(res);
  return false;
};
const reads = (): Reads => ({
  client: { read: async <T,>(path: string) => (answers[path] ?? answers[path.split("?")[0]!] ?? missing) as ReadResult<T> },
  settle, undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED",
});
const mount = (view: ReactElement, principal: string | null = null) =>
  act(async () => root.render(h(ShellContext, { value: { roles: [], offline: false, principal } as never }, h(ReadsProvider, { value: reads() }, view))));
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 60))));
const text = () => box.textContent ?? "";
const field = (name: string) => [...box.querySelectorAll("dt")].find((dt) => dt.textContent === name)?.nextElementSibling?.textContent;
const titles = () => [...box.querySelectorAll("[title]")].map((e) => e.getAttribute("title"));

const release = { id: REL, cluster: CL, draft: DRF, draftRevision: 5, operation: OP, publishedBy: { principal: YOU, role: "publisher" }, publishedAt: at, renderer: { contract: 1, machineryVersion: "m", kubernetesVersion: "k" }, sources: [], machines: [] };
const plan = {
  id: PLN, state: "proposed", mode: "apply-config", route: "api", cluster: CL, machine: MCH, release: REL, desiredRelease: REL, operation: "apply-config", expiresAt: at, createdAt: at,
  createdBy: { principal: YOU, role: "publisher" }, approvalPolicy: "any", approval: null, assignmentRevision: null, baselineRevision: null, driftRecord: null,
  evidence: { diff: null, validation: { result: "valid", platformMode: "m" }, dryRun: { available: false, reason: "r" } },
};
const machine = { id: MCH, name: "worker-1", cluster: CL, talosEndpoint: "10.0.0.1:50000", hardware: { talosNodeId: "n1" }, platform: "metal", scopeState: "managed", desired: REL, applied: { release: REL, source: "publication", importBase: null }, observed: null, openDrift: null, frozen: false };

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  answers = {
    [`/machines/${MCH}`]: ok(machine),
    [`/clusters/${CL}`]: ok({ id: CL, name: "office" }),
    [`/drafts/${DRF}`]: ok({ id: DRF, title: "Add the office labels", state: "published", revision: 6, cluster: CL, entries: [] }),
    [`/releases/${REL}`]: ok(release),
    [`/plans/${PLN}`]: ok(plan),
    [`/operations/${OP}`]: ok({ id: OP, kind: "publish", state: "succeeded", epoch: "ep_a", lastEvent: 1, subject: { draft: DRF, draftRevision: 5 }, createdBy: null, createdAt: at }),
  };
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("a release is named where the machine page shows it as Desired and Applied", () => {
  const shown = async () => {
    await mount(h(MachineShown, {
      id: MCH, m: { kind: "ok", data: machine }, obs: { kind: "ok", data: { items: [], next: undefined, restarted: false } }, km: { view: "v", data: machine, restarted: false },
      ko: { view: "v", data: undefined, restarted: false }, tl: { kind: "ok", data: { items: [], older: undefined, tail: undefined, read: true, restarted: false } },
      kt: { view: "v", data: undefined, restarted: false }, older: undefined, busy: false,
    } as never));
    await settled();
  };
  test("Desired and Applied show the release's title, and the identifier is the title attribute", async () => {
    await shown();
    expect(field("Desired")).toContain("Add the office labels");
    expect(field("Applied")).toContain("Add the office labels");
    expect(field("Desired")).not.toContain("rel_");
    expect(titles()).toContain(REL);
  });
  test("an unread release shows the shortened identifier, never a blank", async () => {
    answers[`/releases/${REL}`] = missing;
    await shown();
    expect(field("Desired")).toBe("rel_k2pq…cdef");
  });
});

describe("a cluster is named on the release page and the plan page", () => {
  test("the release page's Cluster row is the cluster's name, with a link and the identifier as title", async () => {
    await mount(h(ReleaseItem, { id: REL }));
    await settled();
    expect(field("Cluster")).toBe("office");
    expect(box.querySelector("dd a")).not.toBeNull();
    expect(titles()).toContain(CL);
  });
  test("the plan page's Target row names the cluster", async () => {
    await mount(h(PlanItem, { id: PLN }));
    await settled();
    expect(field("Target")).toContain("cluster office");
    expect(field("Target")).not.toContain("cl_k2pq");
  });
  test("a cluster whose read fails reads as this cluster, never as its identifier", async () => {
    answers[`/clusters/${CL}`] = missing;
    await mount(h(ReleaseItem, { id: REL }));
    await settled();
    expect(field("Cluster")).toBe("This cluster");
  });
});

describe("a plan and an operation are named in the machine's timeline", () => {
  const entry = { epoch: "e0", revision: 1, at, kind: "plan-committed", entry: { plan: PLN, operation: OP, observation: "obs_aaaaaaaaaaaaaaaaaaaaaaaaaa" } };
  test("each is a link whose text is its title, and the identifier is its title attribute", async () => {
    await mount(h(TimelineRows, { tl: { items: [entry], older: undefined, tail: undefined, read: true, restarted: false }, older: undefined, busy: false } as never));
    await settled();
    const links = [...box.querySelectorAll("tbody td:nth-child(3) a")].map((a) => [a.textContent, a.getAttribute("title")]);
    expect(links).toContainEqual([expect.stringContaining("Add the office labels"), OP]);
    expect(links.map((l) => l[1])).toContain(PLN);
    expect(links.every((l) => !/^(pln|op)_/.test(l[0] ?? ""))).toBe(true);
  });
});

describe("a person is named, and is you when they are the signed-in person", () => {
  test("the release page says Published by you, with the identifier as title", async () => {
    await mount(h(ReleaseItem, { id: REL }), YOU);
    await settled();
    expect(field("Published")).toContain("by you");
    expect(titles()).toContain(YOU);
  });
  test("another identity is not you", async () => {
    await mount(h(ReleaseItem, { id: REL }), OTHER);
    await settled();
    expect(field("Published")).not.toContain("by you");
  });
  test("with no signed-in identity read yet nothing is you", async () => {
    await mount(h(ReleaseItem, { id: REL }), null);
    await settled();
    expect(field("Published")).not.toContain("by you");
  });
  test("the plan page says Created by you", async () => {
    await mount(h(PlanItem, { id: PLN }), YOU);
    await settled();
    expect(field("Created")).toContain("by you");
  });
  test("a person with no name on record is labelled, so a reader knows what the number is", async () => {
    answers[`/principals?id=${YOU}`] = ok({ items: [{ id: YOU, kind: "human", display: null, subject: "310002201", issuer: "https://idp.example" }] });
    await mount(h(ReleaseItem, { id: REL }), OTHER);
    await settled();
    expect(field("Published")).toContain("an operator with no name on record");
    expect(field("Published")).toContain("310002201");
  });
  test("a person with a display name shows it", async () => {
    answers[`/principals?id=${YOU}`] = ok({ items: [{ id: YOU, kind: "human", display: "Ada Lovelace", subject: "310002201", issuer: "https://idp.example" }] });
    await mount(h(ReleaseItem, { id: REL }), OTHER);
    await settled();
    expect(field("Published")).toContain("Ada Lovelace");
  });
});

describe("a release named in a sentence is its title", () => {
  const REL2 = "rel_zzzzzzzzzzzzzzzzzzzzzzzzzz";
  beforeEach(() => {
    answers[`/releases/${REL2}`] = ok({ ...release, id: REL2, draft: "drf_zzzzzzzzzzzzzzzzzzzzzzzzzz" });
    answers["/drafts/drf_zzzzzzzzzzzzzzzzzzzzzzzzzz"] = ok({ id: "drf_zzzzzzzzzzzzzzzzzzzzzzzzzz", title: "Rename the lab hosts", state: "published", revision: 2, cluster: CL });
  });
  test("the machine page's status sentences name the selected release", async () => {
    const m = { ...machine, desired: REL2, applied: null, pendingConvergence: true, formOnly: false };
    await mount(h(MachineShown, {
      id: MCH, m: { kind: "ok", data: m }, obs: { kind: "ok", data: { items: [], next: undefined, restarted: false } }, km: { view: "v", data: m, restarted: false },
      ko: { view: "v", data: undefined, restarted: false }, tl: { kind: "ok", data: { items: [], older: undefined, tail: undefined, read: true, restarted: false } },
      kt: { view: "v", data: undefined, restarted: false }, older: undefined, busy: false,
    } as never));
    await settled();
    const sentence = box.querySelector('[data-testid="sentences"]')?.textContent ?? "";
    expect(sentence).toContain("the selected release Rename the lab hosts");
    expect(sentence).not.toContain("rel_zzzz");
  });
  test("a release page says which release is the machine's Desired one by its title", async () => {
    const pos = { kind: "ok", data: { step: "none", machine: { ...machine, desired: REL2, applied: null, pendingConvergence: false }, plan: null, draft: null, ingestion: null, operation: null, rolloutHeldBy: null } };
    await mount(h(NextStepSection, { release: REL, machine: MCH, pos: pos as never, readAgain: () => {} }));
    await settled();
    expect(box.querySelector('[data-testid="next-step"]')?.textContent).toContain("(Desired is Rename the lab hosts");
  });
  test("an approved plan says which Desired release the machine has now, and which it had, by title", async () => {
    answers[`/machines/${MCH}`] = ok({ ...machine, desired: REL2 });
    answers["/approvals/apr_k2pq3rstuvwxyz234567abcdef"] = ok({ id: "apr_k2pq3rstuvwxyz234567abcdef", epoch: "ep_a", selfApproval: { reasons: [] } });
    answers["/identity"] = ok({ kind: "human", principal: YOU, epoch: "ep_a", roles: [] });
    answers[`/plans/${PLN}`] = ok({ ...plan, state: "approved", approval: "apr_k2pq3rstuvwxyz234567abcdef" });
    await mount(h(PlanItem, { id: PLN }));
    await settled();
    expect(text()).toContain("Predicted stale: the machine's Desired release is now Rename the lab hosts, published");
    expect(text()).toContain("and it was Add the office labels, published");
    expect(text()).not.toContain("rel_zzzz");
  });
  test("an adopt plan's approval says which release Applied becomes, by its title", async () => {
    answers[`/plans/${PLN}`] = ok({ ...plan, operation: "adopt", mode: "adopt" });
    await mount(h(PlanItem, { id: PLN }));
    await settled();
    expect(text()).toContain("Applied becomes Add the office labels");
  });
});

describe("a release's sources are named by what they are", () => {
  const ASG = "asg_k2pq3rstuvwxyz234567abcdef";
  const FRG = "frg_k2pq3rstuvwxyz234567abcdef";
  const sources = [
    { kind: "assignment", head: ASG, revision: "asr_k2pq3rstuvwxyz234567abcdef", headRevision: 2 },
    { kind: "fragment", head: FRG, revision: "frr_k2pq3rstuvwxyz234567abcdef", headRevision: 1 },
  ];
  beforeEach(() => {
    answers[`/releases/${REL}`] = ok({ ...release, sources });
    answers[`/assignments/${ASG}`] = ok({ id: ASG, machine: MCH });
    answers[`/fragments/${FRG}`] = ok({ id: FRG, name: "office-labels", layer: "base" });
  });
  test("an assignment reads as the assignment of its machine and a fragment by its name, with the identifier as title", async () => {
    await mount(h(ReleaseItem, { id: REL }));
    await settled();
    const cells = [...box.querySelectorAll('[aria-label="Source revisions"] tbody tr td:nth-child(2)')].map((c) => c.textContent);
    expect(cells).toEqual(["Assignment of worker-1", "office-labels"]);
    expect(titles()).toEqual(expect.arrayContaining([ASG, FRG]));
  });
  test("a source not read yet keeps its shortened identifier, never a blank", async () => {
    answers[`/assignments/${ASG}`] = missing;
    answers[`/fragments/${FRG}`] = missing;
    await mount(h(ReleaseItem, { id: REL }));
    await settled();
    const cells = [...box.querySelectorAll('[aria-label="Source revisions"] tbody tr td:nth-child(2)')].map((c) => c.textContent);
    expect(cells).toEqual(["asg_k2pq…cdef", "frg_k2pq…cdef"]);
  });
});

describe("the two revision numbers of one published draft say what each counts", () => {
  test("the release page says it was built from the revision, the draft page that publishing added the last one", async () => {
    await mount(h(ReleaseItem, { id: REL }));
    await settled();
    expect(field("Draft")).toBe("Add the office labels, built from its revision 5");
    await mount(h(DraftItem, { id: DRF }));
    await settled();
    expect(box.querySelector('[data-testid="draft-line"]')?.textContent).toBe("published, revision 6 (publishing added the last revision: its release was built from the one before), Cluster office.");
  });
  test("a draft that is not published says nothing of a release", async () => {
    answers[`/drafts/${DRF}`] = ok({ id: DRF, title: "Add the office labels", state: "open", revision: 6, cluster: CL, entries: [] });
    await mount(h(DraftItem, { id: DRF }));
    await settled();
    expect(box.querySelector('[data-testid="draft-line"]')?.textContent).toBe("open, revision 6, Cluster office.");
  });
});

describe("the node identifier is cut where a list shows it and labelled on the machine's page", () => {
  const whole = "4c1f9a0b7d2e4c1f9a0b7d2e4c1f9a0b";
  test("a list cell shows the start of the opaque value and keeps the whole as its title", async () => {
    await mount(h(NodeCell, { m: { hardware: { talosNodeId: whole, smbiosUuid: null } } as never }));
    expect(text()).toBe("node 4c1f…");
    expect(titles()).toEqual([whole]);
  });
  test("a short value is shown as it is", async () => {
    await mount(h(NodeCell, { m: { hardware: { talosNodeId: "n1", smbiosUuid: null } } as never }));
    expect(text()).toBe("node n1");
  });
});
