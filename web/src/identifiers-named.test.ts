// @vitest-environment happy-dom
// No bare identifier where the object has a name (part 8 of ginsys/bronzeward#1055): a release, a cluster, an
// operation, a plan and a person show their name or title as the text on every page the console walk found
// them bare, with the identifier kept as the title attribute, and "you" for the signed-in person.

import { act, createElement as h, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { MachineShown, TimelineRows } from "./machine.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
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
    [`/drafts/${DRF}`]: ok({ id: DRF, title: "Add the office labels", state: "published", revision: 6, cluster: CL }),
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
