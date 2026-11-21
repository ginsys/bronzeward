// @vitest-environment happy-dom
// The titles and machine names in the views (client contracts A8 and A9, ginsys/bronzeward#987): each
// object shows its title as the text and its whole identifier as the title attribute, falls back to the
// shortened identifier when a read fails or the text is not safe, and a view reads each distinct draft,
// release, plan, ingestion and machine once. The client answers by path and records every path asked.

import { act, createElement as h, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { type Reads, ReadsProvider, useItem } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { Status, StatusPage, when } from "./states.tsx";
import { Outcome } from "./operation.tsx";
import { PlanState } from "./plan.tsx";
import { ACTS, Change, DependencyItem, Operations, Plans, ReleaseItem } from "./resources.tsx";
import { MachineById, Named, OperationHeading, OperationRef, PlanHeading, PlanRef, ReleaseHeading, ReleaseRef } from "./titles.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ok = (data: unknown): ReadResult<unknown> => ({ kind: "ok", data });
const missing: ReadResult<unknown> = { kind: "refused", status: 404, problem: null };
const at = "2026-11-19T10:00:00Z";
const MCH = "mch_k2pq3rstuvwxyz234567abcdef";
const MCH2 = "mch_zzzzzzzzzzzzzzzzzzzzzzzzzz";
const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const REL = "rel_k2pq3rstuvwxyz234567abcdef";
const PLN = "pln_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdefg";
const ING = "ing_k2pq3rstuvwxyz234567abcdef";
const release = (draft = DRF) => ({ id: REL, cluster: "cl_a", draft, draftRevision: 3, operation: OP, publishedBy: null, publishedAt: at, renderer: {}, sources: [], machines: [] });
const plan = (operation: string, machine = MCH) => ({ id: PLN, state: "proposed", cluster: "cl_a", machine, release: REL, operation });
const operation = (kind: string, subject: unknown) => ({ id: OP, kind, state: "succeeded", epoch: "ep_a", lastEvent: 1, subject, createdBy: null, createdAt: at });

let root: Root;
let box: HTMLElement;
let answers: Record<string, ReadResult<unknown>>;
let asked: string[];

const settle: Reads["settle"] = (res, set, live, held) => {
  if (res.kind !== "ok" && held()) return true;
  if (live()) set(res);
  return false;
};
// A client of its own for each test: the titles' store is one per client.
let reads: Reads;
const freshReads = (): Reads => ({
  client: {
    read: async <T,>(path: string) => {
      asked.push(path);
      // A list is asked for with a query; its answer is kept under its path alone.
      return (answers[path] ?? answers[path.split("?")[0]!] ?? missing) as ReadResult<T>;
    },
  },
  settle, undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED",
});

const mount = (view: ReactElement, r: Reads = reads) => act(async () => root.render(h(ReadsProvider, { value: r }, view)));const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 60))));
const text = () => box.textContent;
const titled = (t: string) => [...box.querySelectorAll("[title]")].find((e) => e.textContent?.includes(t))?.getAttribute("title");

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  asked = [];
  reads = freshReads();
  answers = {
    [`/machines/${MCH}`]: ok({ id: MCH, name: "worker-1" }),
    [`/drafts/${DRF}`]: ok({ id: DRF, title: "Add the office labels" }),
    [`/releases/${REL}`]: ok(release()),
    [`/plans/${PLN}`]: ok(plan("apply-config")),
  };
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
  localStorage.clear();
  sessionStorage.clear();
});

describe("ginsys/bronzeward#987: a release", () => {
  test("shows its draft's title and date, and keeps the whole identifier as the title attribute", async () => {
    await mount(h(ReleaseRef, { r: release() as never }));
    await settled();
    expect(text()).toBe(`Add the office labels, published ${when(at)}`);
    expect(box.querySelector("a")?.getAttribute("title")).toBe(REL);
    expect(asked).toEqual([`/drafts/${DRF}`]);
  });

  test("keeps the identifier when the draft's read fails", async () => {
    answers[`/drafts/${DRF}`] = missing;
    await mount(h(ReleaseRef, { r: release() as never }));
    await settled();
    expect(text()).toBe("rel_k2pq…cdef");
  });

  test.each([["a control character", "a\nb"], ["a format character", "a‮b"], ["301 code points", "x".repeat(301)]])("keeps the identifier for a draft title with %s", async (_, title) => {
    answers[`/drafts/${DRF}`] = ok({ id: DRF, title });
    await mount(h(ReleaseRef, { r: release() as never }));
    await settled();
    expect(text()).toBe("rel_k2pq…cdef");
  });

  test("shows markup in a title as text", async () => {
    answers[`/drafts/${DRF}`] = ok({ id: DRF, title: "<img src=x onerror=alert(1)>" });
    await mount(h(ReleaseRef, { r: release() as never }));
    await settled();
    expect(box.querySelector("img")).toBeNull();
    expect(text()).toContain("<img src=x onerror=alert(1)>");
  });

  test("the heading shows it, and the identifier is its title attribute", async () => {
    await mount(h(ReleaseHeading, { id: REL, r: release() as never }));
    await settled();
    expect(text()).toBe(`Release: Add the office labels, published ${when(at)}`);
    expect(titled("Release:")).toBe(REL);
  });

  test("the heading keeps the shortened identifier on a failed read", async () => {
    answers[`/drafts/${DRF}`] = missing;
    await mount(h(ReleaseHeading, { id: REL, r: release() as never }));
    await settled();
    expect(text()).toBe("Release rel_k2pq…cdef");
  });
});

describe("ginsys/bronzeward#987: a plan", () => {
  test.each([["apply-config", "Apply"], ["adopt", "Adopt"]])("%s shows its machine by name and its release's title", async (op, word) => {
    await mount(h(PlanRef, { p: plan(op) as never }));
    await settled();
    expect(text()).toBe(`${word} of worker-1 to Add the office labels, published ${when(at)}`);
    expect(box.querySelector("a")?.getAttribute("title")).toBe(PLN);
    // The release, its draft and the machine: three reads beyond the plan's own.
    expect([...asked].sort()).toEqual([`/drafts/${DRF}`, `/machines/${MCH}`, `/releases/${REL}`]);
  });

  test("names a machine with no name, or a name outside the grammar, by its shortened identifier", async () => {
    for (const [i, body] of [{ id: MCH }, { id: MCH, name: "Worker 1" }, { id: MCH, name: "worker-1\n" }].entries()) {
      answers[`/machines/${MCH}`] = ok(body);
      // Each reads under a generation of its own, as each visit to a view does.
      await mount(h(PlanRef, { p: plan("adopt") as never }), { ...reads, view: `v${i}` });
      await settled();
      expect(text()).toBe(`Adopt of mch_k2pq…cdef to Add the office labels, published ${when(at)}`);
    }
  });

  test("keeps the identifier when the release or the draft fails to read", async () => {
    answers[`/releases/${REL}`] = missing;
    await mount(h(PlanRef, { p: plan("apply-config") as never }));
    await settled();
    expect(text()).toBe("pln_k2pq…cdef");
  });

  test("a plan whose machine the view holds is titled from that body, waits for it, and never reads the machine", async () => {
    const heading = (machine: { body: unknown }, view: string) => mount(h(PlanHeading, { id: PLN, p: plan("apply-config") as never, machine: machine as never }), { ...reads, view });
    await heading({ body: undefined }, "v");
    await settled();
    // Nothing is built on a machine that is not read yet: the old heading text.
    expect(text()).toBe("Plan pln_k2pq…cdef: proposed");
    await heading({ body: { id: MCH, name: "worker-7" } }, "w");
    await settled();
    expect(text()).toBe(`Plan: Apply of worker-7 to Add the office labels, published ${when(at)} (proposed)`);
    expect(asked).not.toContain(`/machines/${MCH}`);
  });

  test("the heading keeps its state, with the title or without it", async () => {
    await mount(h(PlanHeading, { id: PLN, p: plan("apply-config") as never }));
    await settled();
    expect(text()).toBe(`Plan: Apply of worker-1 to Add the office labels, published ${when(at)} (proposed)`);
    expect(titled("Plan:")).toBe(PLN);
    answers[`/drafts/${DRF}`] = missing;
    await act(async () => root.render(null));
    await mount(h(PlanHeading, { id: PLN, p: plan("apply-config") as never }), { ...reads, view: "w" });
    await settled();
    expect(text()).toBe("Plan pln_k2pq…cdef: proposed");
  });
});

describe("ginsys/bronzeward#987: an operation", () => {
  test("publish shows its draft's title", async () => {
    await mount(h(OperationRef, { o: operation("publish", { draft: DRF, draftRevision: 3 }) as never }));
    await settled();
    expect(text()).toBe("Publish of Add the office labels");
    expect(asked).toEqual([`/drafts/${DRF}`]);
  });

  test("ingest reads its ingestion, then the machine the ingestion names: two reads", async () => {
    answers[`/ingestions/${ING}`] = ok({ id: ING, machine: MCH });
    await mount(h(OperationRef, { o: operation("ingest", { draft: DRF, draftRevision: 1, ingestion: ING }) as never }));
    await settled();
    expect(text()).toBe("Import of worker-1");
    expect(asked).toEqual([`/ingestions/${ING}`, `/machines/${MCH}`]);
  });

  test.each([
    ["an ingestion with no machine", () => (answers[`/ingestions/${ING}`] = ok({ id: ING, machine: null }))],
    ["an ingestion whose read fails", () => (answers[`/ingestions/${ING}`] = missing)],
  ])("ingest with %s is an import with no machine to name", async (_, set) => {
    set();
    await mount(h(OperationRef, { o: operation("ingest", { draft: DRF, draftRevision: 1, ingestion: ING }) as never }));
    await settled();
    expect(text()).toBe("Import");
  });

  test.each([["names no ingestion", { draft: DRF, draftRevision: 1 }], ["names a null ingestion", { draft: DRF, draftRevision: 1, ingestion: null }]])("ingest whose subject %s is an import, with no read", async (_, subject) => {
    await mount(h(OperationRef, { o: operation("ingest", subject) as never }));
    await settled();
    expect(text()).toBe("Import");
    expect(asked).toEqual([]);
  });

  test.each([["apply-config", "Apply"], ["adopt", "Adopt"]])("%s shows its plan's title: up to four reads", async (kind, word) => {
    answers[`/plans/${PLN}`] = ok(plan(kind));
    await mount(h(OperationRef, { o: operation(kind, { plan: PLN, machine: MCH }) as never }));
    await settled();
    expect(text()).toBe(`${word} of worker-1 to Add the office labels, published ${when(at)}`);
    expect([...asked].sort()).toEqual([`/drafts/${DRF}`, `/machines/${MCH}`, `/plans/${PLN}`, `/releases/${REL}`]);
  });

  test.each([
    ["a null subject", null],
    ["a publish subject without a draft", {}],
    ["an apply-config subject without a plan", { machine: MCH }],
  ])("%s keeps the identifier", async (_, subject) => {
    for (const kind of ["publish", "apply-config"]) {
      if (kind === "publish" && subject !== null && "machine" in subject) continue;
      await mount(h(OperationRef, { o: operation(kind, subject) as never }));
      await settled();
      expect(text()).toBe("op_k2pq…defg");
    }
  });

  test("keeps the identifier for a kind this client does not know", async () => {
    await mount(h(OperationRef, { o: operation("something-new", { draft: DRF }) as never }));
    await settled();
    expect(text()).toBe("op_k2pq…defg");
  });

  test("the heading keeps its kind and state", async () => {
    await mount(h(OperationHeading, { id: OP, o: operation("publish", { draft: DRF, draftRevision: 3 }) as never }));
    await settled();
    expect(text()).toBe("Operation: Publish of Add the office labels (publish, succeeded)");
    expect(titled("Operation:")).toBe(OP);
  });
});

describe("ginsys/bronzeward#987: a machine", () => {
  test("shows its name as the text and the whole identifier as the title attribute", async () => {
    await mount(h(MachineById, { id: MCH }));
    await settled();
    expect(text()).toBe("worker-1");
    expect(box.querySelector("a")?.getAttribute("title")).toBe(MCH);
  });

  test("shows the shortened identifier when its read fails, and reads an identifier of another shape never", async () => {
    answers[`/machines/${MCH}`] = missing;
    await mount(h(MachineById, { id: MCH }));
    await settled();
    expect(text()).toBe("mch_k2pq…cdef");
    await act(async () => root.render(null));
    asked = [];
    await mount(h(MachineById, { id: "machine-one" }), { ...reads, view: "w" });
    await settled();
    // Shown as every identifier of another shape is, by Id.
    expect(text()).toBe("mach…-one");
    expect(asked).toEqual([]);
  });

  test("a view that asks nothing still ends the generation before it, so a return reads again", async () => {
    await mount(h(MachineById, { id: MCH }), { ...reads, view: "v" });
    await settled();
    await mount(h(MachineById, { id: "machine-one" }), { ...reads, view: "w" });
    await settled();
    await mount(h(MachineById, { id: MCH }), { ...reads, view: "v" });
    await settled();
    expect(asked).toEqual([`/machines/${MCH}`, `/machines/${MCH}`]);
  });

  test("a machine the view holds is named from that body and asks for nothing", async () => {
    await mount(h(MachineById, { id: MCH, held: { body: { id: MCH, name: "worker-9" } as never } }));
    await settled();
    expect(text()).toBe("worker-9");
    await mount(h(MachineById, { id: MCH, held: { body: undefined } }), { ...reads, view: "w" });
    await settled();
    expect(text()).toBe("mch_k2pq…cdef");
    expect(asked).toEqual([]);
  });

  test("shows none for no identifier", async () => {
    await mount(h(MachineById, { id: null }));
    expect(text()).toBe("none");
  });
});

describe("ginsys/bronzeward#987: the reads of a view", () => {
  test("a machine shown in three places, and two plans of one release, read each distinct object once", async () => {
    answers[`/machines/${MCH2}`] = ok({ id: MCH2, name: "worker-2" });
    await mount(h("div", null,
      h(PlanRef, { p: { ...plan("apply-config"), id: "pln_aaaaaaaaaaaaaaaaaaaaaaaaaa" } as never }),
      h(PlanRef, { p: { ...plan("adopt"), id: "pln_bbbbbbbbbbbbbbbbbbbbbbbbbb" } as never }),
      h(PlanRef, { p: { ...plan("adopt", MCH2), id: "pln_cccccccccccccccccccccccccc" } as never }),
      h(MachineById, { id: MCH }), h(MachineById, { id: MCH }), h(MachineById, { id: MCH2 })));
    await settled();
    expect([...asked].sort()).toEqual([`/drafts/${DRF}`, `/machines/${MCH}`, `/machines/${MCH2}`, `/releases/${REL}`]);
  });

  test("a view with failing title reads counts its own reads as one without titles: only a shell refusal reaches settle", async () => {
    const seen: string[] = [];
    const counted: Reads = { ...reads, settle: (res, set, live, held) => (seen.push(res.kind), reads.settle(res, set, live, held)) };
    answers[`/drafts/${DRF}`] = missing;
    await mount(h(ReleaseRef, { r: release() as never }), counted);
    await settled();
    expect(seen).toEqual(["refused"]);
    answers[`/drafts/${DRF}`] = { kind: "transport" };
    seen.length = 0;
    await mount(h(ReleaseRef, { r: release() as never }), { ...counted, view: "w" });
    await settled();
    expect(seen).toEqual([]);
  });

  test("a read again, and a return to the view, read the titles again", async () => {
    await mount(h(ReleaseRef, { r: release() as never }));
    await settled();
    await mount(h(ReleaseRef, { r: release() as never }), { ...reads, again: 1 });
    await settled();
    expect(asked).toEqual([`/drafts/${DRF}`, `/drafts/${DRF}`]);
  });
});

// An object a view names by its identifier (ginsys/bronzeward#996): the same titles, the whole identifier
// as the title attribute, the shortened identifier where a read does not answer.
describe("ginsys/bronzeward#996: an object named by its identifier", () => {
  const released = `Add the office labels, published ${when(at)}`;
  const VIEW: Record<string, string> = { drf: "drafts", rel: "releases", pln: "plans", op: "operations", ing: "ingestions", mch: "machines" };
  const pathOf = (id: string) => `/${VIEW[id.slice(0, id.indexOf("_"))]}/${id}`;
  const KINDS: [string, string, string, () => void][] = [
    ["a draft", DRF, "Add the office labels", () => {}],
    ["a release", REL, released, () => {}],
    ["a plan", PLN, `Apply of worker-1 to ${released}`, () => {}],
    ["an operation", OP, "Publish of Add the office labels", () => (answers[`/operations/${OP}`] = ok(operation("publish", { draft: DRF, draftRevision: 3 })))],
    ["an import", ING, "Import of worker-1", () => (answers[`/ingestions/${ING}`] = ok({ id: ING, machine: MCH }))],
    ["a machine", MCH, "worker-1", () => {}],
  ];

  test.each(KINDS)("%s shows its title as the text and its whole identifier as the title attribute", async (_, id, title, set) => {
    set();
    await mount(h(Named, { id }));
    await settled();
    expect(text()).toBe(title);
    expect(box.querySelector("a")?.getAttribute("title")).toBe(id);
    expect(box.querySelector("a")?.getAttribute("href")).toBe(pathOf(id));
  });

  test.each(KINDS)("%s keeps the shortened identifier, and the whole one as its title attribute, when its read fails", async (_, id, _title, set) => {
    set();
    for (const p of Object.keys(answers)) answers[p] = missing;
    answers[pathOf(id)] = missing;
    await mount(h(Named, { id }));
    await settled();
    expect(text()).toBe(`${id.slice(0, id.indexOf("_") + 5)}…${id.slice(-4)}`);
    expect(box.querySelector("a")?.getAttribute("title")).toBe(id);
  });

  test("a draft whose title is not safe is shown by its shortened identifier", async () => {
    answers[`/drafts/${DRF}`] = ok({ id: DRF, title: "a\nb" });
    await mount(h(Named, { id: DRF }));
    await settled();
    expect(text()).toBe("drf_k2pq…cdef");
  });

  test("an operation named by its identifier reads its body, then what its subject names", async () => {
    answers[`/operations/${OP}`] = ok(operation("publish", { draft: DRF, draftRevision: 3 }));
    await mount(h(Named, { id: OP }));
    await settled();
    expect(asked).toEqual([`/operations/${OP}`, `/drafts/${DRF}`]);
  });

  test("an import with no machine is Import, as the operation of an import is", async () => {
    answers[`/ingestions/${ING}`] = ok({ id: ING, machine: null });
    await mount(h(Named, { id: ING }));
    await settled();
    expect(text()).toBe("Import");
  });

  test("an identifier of another shape is shown as any other is, with no read, and no identifier is none", async () => {
    await mount(h("div", null, h(Named, { id: "machine-one" }), " ", h(Named, { id: null })));
    await settled();
    expect(text()).toBe("mach…-one none");
    expect(asked).toEqual([]);
  });

  // The views that name them: each family shows the title and keeps the identifier.
  const hrefs = () => [...box.querySelectorAll("a")].map((a) => a.getAttribute("title"));

  test("the audit's Subjects name each object by its title", async () => {
    const subjects = ACTS.find(([name]) => name === "Subjects")![1];
    await mount(h("div", null, subjects({ subjects: [REL, PLN, DRF] } as never)));
    await settled();
    expect(text()).toBe(`${released}, Apply of worker-1 to ${released}, Add the office labels`);
    expect(hrefs()).toEqual([REL, PLN, DRF]);
  });

  test("a plan's change names the applied and the planned release", async () => {
    await mount(h(Change, { p: { operation: "apply-config", evidence: { diff: { from: REL, to: REL, withheld: false, unified: "-a\n+b\n" } } } as never }));
    await settled();
    expect(text()).toContain(`applied ${released} → ${released}.`);
    expect(asked).toEqual([`/releases/${REL}`, `/drafts/${DRF}`]);
  });

  test("a committed plan names its operation", async () => {
    answers[`/operations/${OP}`] = ok(operation("publish", { draft: DRF, draftRevision: 3 }));
    await mount(h(PlanState, { p: { ...plan("apply-config"), state: "committed", committedOperation: OP } as never, reread: () => {} }));
    await settled();
    expect(text()).toContain("Committed: operation Publish of Add the office labels.");
    expect(hrefs()).toContain(OP);
  });

  test("a release's page names its draft and its publication", async () => {
    answers[`/operations/${OP}`] = ok(operation("publish", { draft: DRF, draftRevision: 3 }));
    await mount(h(ReleaseItem, { id: REL }));
    await settled();
    expect(text()).toContain("DraftAdd the office labels, revision 3");
    expect(text()).toContain("PublicationPublish of Add the office labels");
    expect(hrefs()).toEqual(expect.arrayContaining([DRF, OP]));
  });

  test("the plans list names each plan's release, the dependency's releases theirs", async () => {
    answers["/plans"] = ok({ items: [plan("apply-config")], next: null });
    await mount(h(Plans, { query: "", go: () => {} }));
    await settled();
    expect(box.querySelector("td:last-child, tbody")?.textContent).toContain(released);
    answers["/dependencies/dep_x"] = ok({ id: "dep_x", provider: "p", object: "o", version: "1", created: "c", class: null, reason: null, firstRetainedAt: at, unknownSince: null, observedFrom: at, recordedAt: at });
    answers["/dependencies/dep_x/releases"] = ok({ items: [{ release: REL, cluster: "cl_a", publishedAt: at, records: [] }], next: null });
    answers["/dependencies/dep_x/alerts"] = ok({ items: [], next: null });
    await mount(h(DependencyItem, { id: "dep_x" }), { ...reads, view: "w" });
    await settled();
    expect(text()).toContain(released);
  });

  test("an operation's Subject cell names its machine, plan, draft and import by their titles", async () => {
    answers["/operations"] = ok({ items: [operation("apply-config", { machine: MCH, plan: PLN }), { ...operation("ingest", { draft: DRF, draftRevision: 1, ingestion: ING }), id: "op_k2pq3rstuvwxyz234567abcdeh" }], next: null });
    answers[`/ingestions/${ING}`] = ok({ id: ING, machine: MCH });
    await mount(h(Operations, { query: "", go: () => {} }));
    await settled();
    const cells = [...box.querySelectorAll("tbody tr")].map((tr) => tr.querySelector("td:nth-child(4)")?.textContent);
    expect(cells).toEqual([`worker-1, Apply of worker-1 to ${released}`, "Add the office labels, Import of worker-1"]);
    expect([...box.querySelectorAll("td:nth-child(4) a")].map((a) => a.getAttribute("title"))).toEqual([MCH, PLN, DRF, ING]);
    expect(new Set(asked).size).toBe(asked.length);
  });

  test("a subject whose reads fail keeps the shortened identifiers, and one with nothing is none", async () => {
    answers["/operations"] = ok({ items: [operation("apply-config", { plan: PLN }), { ...operation("publish", {}), id: "op_k2pq3rstuvwxyz234567abcdeh" }], next: null });
    answers[`/plans/${PLN}`] = missing;
    await mount(h(Operations, { query: "", go: () => {} }));
    await settled();
    expect([...box.querySelectorAll("tbody tr")].map((tr) => tr.querySelector("td:nth-child(4)")?.textContent)).toEqual(["pln_k2pq…cdef", "none"]);
  });

  test("the outcome line of a completed apply names the applied release by its title", async () => {
    const o = { ...operation("apply-config", { plan: PLN, machine: MCH }), state: "completed", result: { appliedRelease: REL, observation: "obs_aaaaaaaaaaaaaaaaaaaaaaaaaa" } };
    await mount(h(Outcome, { o: o as never }));
    await settled();
    expect(box.querySelector("[data-testid=outcome]")?.textContent).toContain(`this operation applied release ${released}, confirmed by observation obs_aaaa`);
    expect(titled(released)).toBe(REL);
  });

  test("an object the page reads itself is not read again for a title built on it", async () => {
    answers[`/operations/${OP}`] = ok(operation("apply-config", { plan: PLN, machine: MCH }));
    const Page = () => {
      const item = useItem<unknown>(`/plans/${PLN}`);
      return item.kind === "ok" ? h(Named, { id: OP }) : null;
    };
    await mount(h(Page));
    await settled();
    expect(text()).toBe(`Apply of worker-1 to ${released}`);
    expect(asked.filter((p) => p === `/plans/${PLN}`)).toHaveLength(1);
    expect([...asked].sort()).toEqual([`/drafts/${DRF}`, `/machines/${MCH}`, `/operations/${OP}`, `/plans/${PLN}`, `/releases/${REL}`]);
  });

  test("a status that names an object by identifier keeps its mirror in step when the title arrives", async () => {
    let answer: (r: ReadResult<unknown>) => void = () => {};
    const late = new Promise<ReadResult<unknown>>((r) => (answer = r));
    const slow: Reads = { ...reads, client: { read: async <T,>(path: string) => (path === `/releases/${REL}` ? late : answers[path] ?? missing) as ReadResult<T> } };
    await mount(h(StatusPage, null, h(Status, { testid: "s", children: h("span", null, "Published ", h(Named, { id: REL }), ".") })), slow);
    await settled();
    const mirror = () => box.querySelector('[role="status"]')?.textContent;
    expect(mirror()).toBe("Published rel_k2pq…cdef.");
    await act(async () => answer(ok(release())));
    await settled();
    expect(text()).toContain(`Published ${released}.`);
    expect(mirror()).toBe(`Published ${released}.`);
  });

  test("a view that names one object in several places, and objects of one release, reads each distinct body once", async () => {
    await mount(h("div", null, h(Named, { id: REL }), h(Named, { id: REL }), h(Named, { id: PLN }), h(Named, { id: DRF }), h(Named, { id: MCH }), h(Named, { id: MCH })));
    await settled();
    expect([...asked].sort()).toEqual([`/drafts/${DRF}`, `/machines/${MCH}`, `/plans/${PLN}`, `/releases/${REL}`]);
  });
});
