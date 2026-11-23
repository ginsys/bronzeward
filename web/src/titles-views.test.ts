// @vitest-environment happy-dom
// The titles and machine names in the views (client contracts A8 and A9, ginsys/bronzeward#987): each
// object shows its title as the text and its whole identifier as the title attribute, falls back to the
// shortened identifier when a read fails (a draft whose title is not safe reads "Untitled draft"; other
// text that is not safe is not shown), and a view reads each distinct draft,
// release, plan, ingestion and machine once. The client answers by path and records every path asked.

import { act, createElement as h, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { CreatedDraft, DraftItem, Drafts } from "./drafts.tsx";
import { OpenDraftsView } from "./machines.tsx";
import { allowed, IngestionItem, StartImport } from "./imports.tsx";
import { MachineShown, sentences, TimelineRows } from "./machine.tsx";
import { nameOf } from "./onboard.ts";
import { RecoveryConsole } from "./recovery.tsx";
import { predict, recordedReasons, RevokedBy } from "./approval.tsx";
import { ReviewBody } from "./review.tsx";
import { nextStep } from "./nextstep.tsx";
import { PublishDraft } from "./publish.tsx";
import { Detail, type Reads, ReadsProvider, short, useItem } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";
import type { ReadResult } from "./request.ts";
import { needs, Status, StatusPage, when } from "./states.tsx";
import { Outcome } from "./operation.tsx";
import { PlanState } from "./plan.tsx";
import { Change } from "./change.tsx";
import { ACTS, DependencyItem, ImportBaseItem, Imports, OperationItem, Operations, PlanItem, Plans, ReleaseItem, RevisionItem, Sources } from "./resources.tsx";
import { MachineById, Named, OperationHeading, OperationRef, PlanHeading, PlanRef, ReleaseHeading, ReleaseRef, useClusterText } from "./titles.tsx";

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

  test.each([["a control character", "a\nb"], ["a format character", "a‮b"], ["301 code points", "x".repeat(301)]])("reads Untitled draft for a draft title with %s, never the received text", async (_, title) => {
    answers[`/drafts/${DRF}`] = ok({ id: DRF, title });
    await mount(h(ReleaseRef, { r: release() as never }));
    await settled();
    expect(text()).toBe(`Untitled draft, published ${when(at)}`);
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

  test("names a machine with no name, or a name outside the grammar, by its address, and one with no usable address as an unnamed machine", async () => {
    const cases = [
      [{ id: MCH, talosEndpoint: "172.31.104.15:50000" }, "172.31.104.15"],
      [{ id: MCH, name: "Worker 1", talosEndpoint: "172.31.104.15:50000" }, "172.31.104.15"],
      [{ id: MCH, name: "worker-1\n", talosEndpoint: "[fd00::1]:50000" }, "fd00::1"],
      [{ id: MCH }, "an unnamed machine"],
      [{ id: MCH, talosEndpoint: "cp1.office:50000" }, "an unnamed machine"],
    ] as const;
    for (const [i, [body, named]] of cases.entries()) {
      answers[`/machines/${MCH}`] = ok(body);
      // Each reads under a generation of its own, as each visit to a view does.
      await mount(h(PlanRef, { p: plan("adopt") as never }), { ...reads, view: `v${i}` });
      await settled();
      expect(text()).toBe(`Adopt of ${named} to Add the office labels, published ${when(at)}`);
    }
  });

  test("a plan whose machine read fails keeps its title, naming the machine as 'the machine'", async () => {
    answers[`/machines/${MCH}`] = missing;
    await mount(h(PlanRef, { p: plan("adopt") as never }));
    await settled();
    expect(text()).toBe(`Adopt of the machine to Add the office labels, published ${when(at)}`);
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
    // A held read that answered with no body keeps the title, as a read the store makes does.
    await heading({ body: null }, "x");
    await settled();
    expect(text()).toBe(`Plan: Apply of the machine to Add the office labels, published ${when(at)} (proposed)`);
    expect(asked).not.toContain(`/machines/${MCH}`);
  });

  // The shell answers an HTTP failure (a 404, an exhausted 503) and holds a transport failure: this settle answers.
  const answering: Reads["settle"] = (res, set, live) => {
    if (live()) set(res);
    return false;
  };
  test("an approved plan hands its machine read to the page, as the body, or as null where the read answered with none", async () => {
    const p = { ...plan("adopt"), state: "approved", approval: "apr_k2pq3rstuvwxyz234567abcdef" };
    for (const [i, [answer, expected]] of [[missing, null], [ok({ id: MCH, name: "worker-1" }), { id: MCH, name: "worker-1" }]].entries() as Iterable<[number, [ReadResult<unknown>, unknown]]>) {
      const seen: unknown[] = [];
      answers[`/machines/${MCH}`] = answer;
      await mount(h(PlanState, { p: p as never, reread: () => {}, onMachine: (m: unknown) => void seen.push(m) } as never), { ...reads, settle: answering, view: `a${i}` });
      await settled();
      expect(seen).toEqual([expected]);
    }
  });

  test("an approved plan's page reads its machine once, though every read answers with a new object", async () => {
    answers[`/plans/${PLN}`] = ok({ ...plan("adopt"), state: "approved", approval: "apr_k2pq3rstuvwxyz234567abcdef", createdBy: null, createdAt: at, evidence: { diff: null, validation: {}, dryRun: { available: false, reason: "none" } } });
    answers[`/machines/${MCH}`] = ok({ id: MCH, name: "worker-1" });
    // The page re-reads when a callback it passes changes, so a body that is a new object each time exposes a loop.
    const fresh: Reads = { ...reads, settle: answering, client: { read: async <T,>(path: string) => { asked.push(path); return structuredClone(answers[path] ?? missing) as ReadResult<T>; } } };
    await mount(h(PlanItem, { id: PLN }), fresh);
    await settled();
    expect(asked.filter((p) => p === `/machines/${MCH}`)).toHaveLength(1);
    expect(box.querySelector("h2")?.textContent).toContain("worker-1");
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

  test("ingest whose machine read fails is an import of 'the machine'", async () => {
    answers[`/ingestions/${ING}`] = ok({ id: ING, machine: MCH });
    answers[`/machines/${MCH}`] = missing;
    await mount(h(OperationRef, { o: operation("ingest", { draft: DRF, draftRevision: 1, ingestion: ING }) as never }));
    await settled();
    expect(text()).toBe("Import of the machine");
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
    // A held read that answered with no body shows the identifier as any unread machine does.
    await mount(h(MachineById, { id: MCH, held: { body: null } }), { ...reads, view: "x" });
    await settled();
    expect(text()).toBe("mch_k2pq…cdef");
    expect(asked).toEqual([]);
  });

  test("shows none for no identifier", async () => {
    await mount(h(MachineById, { id: null }));
    expect(text()).toBe("none");
  });
});

describe("ginsys/bronzeward#1055: a cluster's name and the identifier's detail line", () => {
  const Cluster = ({ id }: { id: string }) => h("span", null, useClusterText(id) ?? "-");
  test("a cluster is named from its read where the name is safe, and by nothing where it is not or the read fails", async () => {
    const malformed = [{ value: "bad" }, 7, null, ["lab-a"]].map((name) => [ok({ id: "cl_a", name }), "-"]);
    for (const [i, [answer, shown]] of [[ok({ id: "cl_a", name: "lab-a" }), "lab-a"], [ok({ id: "cl_a", name: "lab‮a" }), "-"], [missing, "-"], ...malformed].entries() as Iterable<[number, [ReadResult<unknown>, string]]>) {
      answers["/clusters/cl_a"] = answer;
      await mount(h(Cluster, { id: "cl_a" }), { ...reads, view: `c${i}` });
      await settled();
      expect(text()).toBe(shown);
    }
  });

  const click = () => act(async () => { box.querySelector("button")!.dispatchEvent(new MouseEvent("click", { bubbles: true })); await new Promise((r) => setTimeout(r, 10)); });
  const clipboard = (v: unknown) => Object.defineProperty(navigator, "clipboard", { value: v, configurable: true });
  test("Copy says Copied once the identifier is on the clipboard", async () => {
    const copied: string[] = [];
    clipboard({ writeText: async (s: string) => void copied.push(s) });
    await mount(h(Detail, { id: MCH }));
    expect(text()).toBe(`Identifier ${MCH} Copy`);
    await click();
    expect(copied).toEqual([MCH]);
    expect(text()).toBe(`Identifier ${MCH} Copy Copied`);
  });
  test.each([
    ["no clipboard", undefined],
    ["a refused write", { writeText: () => Promise.reject(new Error("denied")) }],
    ["a write that throws at once", { writeText: () => { throw new Error("blocked by policy"); } }],
  ])("Copy with %s says to select the identifier", async (_, board) => {
    clipboard(board);
    await mount(h(Detail, { id: MCH }));
    await click();
    expect(text()).toBe(`Identifier ${MCH} Copy Select the identifier to copy it`);
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

  test("a draft whose title is not safe is Untitled draft with its shortened identifier beside it, as plain text", async () => {
    answers[`/drafts/${DRF}`] = ok({ id: DRF, title: "a\nb" });
    await mount(h(Named, { id: DRF }));
    await settled();
    expect(text()).toBe("Untitled draft (drf_k2pq…cdef)");
    expect(box.querySelector("a")?.textContent).toBe("Untitled draft");
    expect(box.querySelector("a")?.getAttribute("title")).toBe(DRF);
  });

  test("a draft whose read failed keeps the shortened identifier", async () => {
    answers[`/drafts/${DRF}`] = missing;
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
    expect(text()).toContain("DraftAdd the office labels, built from its revision 3");
    expect(text()).toContain("PublicationPublish of Add the office labels");
    expect(hrefs()).toEqual(expect.arrayContaining([DRF, OP]));
  });

  describe("ginsys/bronzeward#1055: a release's publication observation", () => {
    const OBS = "obs_k2pq3rstuvwxyz234567abcdef";
    const covering = () => (answers[`/releases/${REL}`] = ok({ ...release(), machines: [{ machine: MCH, mode: "config", publicationObservation: OBS }] }));
    const observed = () => asked.filter((p) => p.endsWith("/observations"));

    test("reads when it was taken and why, the identifier staying the link's title, with one list read per covered machine", async () => {
      covering();
      answers[`/machines/${MCH}/observations`] = ok({ items: [{ id: OBS, machine: MCH, purpose: "publication", at }], next: null });
      await mount(h(ReleaseItem, { id: REL }));
      await settled();
      expect(text()).toContain(`Read ${when(at)} (publication)`);
      expect(titled("Read ")).toBe(OBS);
      expect(observed()).toEqual([`/machines/${MCH}/observations`]);
    });

    test("keeps the shortened identifier when the machine's list does not hold it or cannot be read", async () => {
      covering();
      answers[`/machines/${MCH}/observations`] = ok({ items: [], next: null });
      await mount(h(ReleaseItem, { id: REL }));
      await settled();
      expect(text()).toContain("obs_k2pq…cdef");
      expect(text()).not.toContain("(publication)");
      answers[`/machines/${MCH}/observations`] = missing;
      await mount(h(ReleaseItem, { id: REL }), { ...freshReads(), view: "w" });
      await settled();
      expect(text()).toContain("obs_k2pq…cdef");
    });
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

  test("ginsys/bronzeward#1055: the assignments list and the imports list name each row by its machine, the identifier staying the link's title", async () => {
    const ASG = "asg_k2pq3rstuvwxyz234567abcdef";
    answers["/assignments"] = ok({ items: [{ id: ASG, cluster: "cl_a", machine: MCH, revision: null, headRevision: 1, createdAt: at }], next: null });
    const dep = (id: string, object: string) => ({ id, provider: "p", object, version: 3, created: at, class: "retained", reason: null, firstRetainedAt: null, unknownSince: null, observedFrom: at, recordedAt: at, stale: false });
    answers["/dependencies"] = ok({ items: [dep("dep_k2pq3rstuvwxyz234567abcdef", "registry.test/app"), dep("dep_k2pq3rstuvwxyz234567abcdeg", "bad\u0007object")], next: null, lastPass: null });
    await mount(h(Sources, { query: "", go: () => {} }));
    await settled();
    const row = box.querySelector('a[title="' + ASG + '"]');
    expect(row?.textContent).toBe("Assignment of worker-1");
    // A dependency is named as its page is headed, and an object that is not safe text leaves the shortened identifier.
    expect(box.querySelector('a[title="dep_k2pq3rstuvwxyz234567abcdef"]')?.textContent).toBe("Dependency registry.test/app, version 3");
    expect(box.querySelector('a[title="dep_k2pq3rstuvwxyz234567abcdeg"]')?.textContent).toBe("Dependency dep_k2pq…cdeg");
    answers["/ingestions"] = ok({ items: [{ id: ING, machine: MCH, draft: DRF, operation: OP, state: "released", kind: "import", mode: "durable", review: null, ownerGeneration: 1, leaseUntil: at, expiresAt: at, createdAt: at }], next: null });
    await mount(h(Imports, { query: "", go: () => {} }), { ...reads, view: "i" });
    await settled();
    expect(box.querySelector('a[title="' + ING + '"]')?.textContent).toBe("Import of worker-1");
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

  test("the outcome line of a failed publication says why: the rule, the input, the paths, the server's message and the machine by name", async () => {
    const error = { type: "urn:bronzeward:problem:validation-failed", status: 422, rule: "base-override", machine: MCH, input: "fragment[1]", paths: ["spec.a", "spec.b"], message: "a fragment overrode <redacted:ref@1>\u001b[2J" };
    const o = { ...operation("publish", { draft: DRF }), state: "failed", error };
    await mount(h(Outcome, { o: o as never }));
    await settled();
    const line = box.querySelector("[data-testid=outcome]")?.textContent;
    expect(line).toBe("Failed: validation-failed. The rule: base-override. The input: the fragment at position 1 (the first is 0). The paths: spec.a, spec.b. The server says: a fragment overrode <redacted:ref@1>�[2J. The machine it names: worker-1.");
    expect(box.querySelector("[data-testid=outcome] a")?.getAttribute("title")).toBe(MCH);
  });

  test("the outcome line of a failed publication names the refused fragment by name and layer, with the position in brackets", async () => {
    const error = { type: "urn:bronzeward:problem:validation-failed", status: 422, rule: "kind-mismatch", machine: MCH, input: "fragment[1]", fragment: "network", layer: "site" };
    const o = { ...operation("publish", { draft: DRF }), state: "failed", error };
    await mount(h(Outcome, { o: o as never }));
    await settled();
    expect(box.querySelector("[data-testid=outcome]")?.textContent).toBe("Failed: validation-failed. The rule: kind-mismatch. The input: the fragment network in layer site (position 1, the first is 0). The machine it names: worker-1.");
  });

  test("ginsys/bronzeward#1055: the outcome line of a failed publication reads a tab or a line break in the server's message as a space", async () => {
    const error = { type: "urn:bronzeward:problem:validation-failed", status: 422, rule: "invalid", message: "1 error occurred:\n\t* .machine.nodeLabels is already set in v1alpha1 config" };
    const o = { ...operation("publish", { draft: DRF }), state: "failed", error };
    await mount(h(Outcome, { o: o as never }));
    await settled();
    expect(box.querySelector("[data-testid=outcome]")?.textContent).toContain("The server says: 1 error occurred: * .machine.nodeLabels is already set in v1alpha1 config.");
  });

  describe("ginsys/bronzeward#1055: the Error row of an operation's page", () => {
    const refused = { type: "urn:bronzeward:problem:validation-failed", title: "Validation failed", status: 422, rule: "invalid", machine: MCH, instance: "req_a", message: "1 error occurred:\n\t* .machine.nodeLabels is already set" };
    const failed = (error: unknown) => (answers[`/operations/${OP}`] = ok({ ...operation("publish", { draft: DRF, draftRevision: 3 }), state: "failed", error }));
    const labels = () => [...box.querySelectorAll("dt")].map((dt) => dt.textContent);

    test("a problem document sits whole in the one closed reference section, and no machine identifier is in the page's text outside it", async () => {
      failed(refused);
      await mount(h(OperationItem, { id: OP }));
      await settled();
      const refs = box.querySelectorAll("[data-testid=request-ref]");
      expect(refs).toHaveLength(1);
      const ref = refs[0]!;
      expect(ref.tagName).toBe("DETAILS");
      expect(ref.hasAttribute("open")).toBe(false);
      expect(labels()).not.toContain("Error");
      expect(JSON.parse(ref.querySelector("pre")?.textContent ?? "null")).toEqual(refused);
      expect(ref.textContent).toContain("Code: validation-failed");
      expect(ref.textContent).toContain("Status: 422");
      const outside = box.cloneNode(true) as HTMLElement;
      outside.querySelector("[data-testid=request-ref]")?.remove();
      expect(outside.textContent).not.toMatch(/mch_/);
    });

    test("a document that stores no status reads its code, never undefined", async () => {
      failed({ type: "urn:bronzeward:problem:validation-failed", rule: "invalid" });
      await mount(h(OperationItem, { id: OP }));
      await settled();
      const ref = box.querySelector("[data-testid=request-ref]");
      expect(ref?.textContent).toContain("Code: validation-failed");
      expect(ref?.textContent).not.toContain("undefined");
    });

    test("an apply's error record keeps its row and has no reference section", async () => {
      failed({ cause: "timeout", attempts: 2 });
      await mount(h(OperationItem, { id: OP }));
      await settled();
      expect(labels()).toContain("Error");
      expect(box.querySelector("[data-testid=request-ref]")).toBeNull();
    });

    test("an operation with no error keeps its row reading none", async () => {
      failed(null);
      await mount(h(OperationItem, { id: OP }));
      await settled();
      expect(box.querySelector("dt + dd")).not.toBeNull();
      expect([...box.querySelectorAll("dt")].find((dt) => dt.textContent === "Error")?.nextElementSibling?.textContent).toBe("none");
    });
  });

  test("an operation's page reads each distinct body once for its heading, Subject and outcome", async () => {
    answers[`/operations/${OP}`] = ok({ ...operation("apply-config", { plan: PLN, machine: MCH }), state: "completed", result: { appliedRelease: REL, observation: "obs_aaaaaaaaaaaaaaaaaaaaaaaaaa" } });
    await mount(h(OperationItem, { id: OP }));
    await settled();
    expect(box.querySelector("[data-testid=outcome]")?.textContent).toContain(`this operation applied release ${released}`);
    expect(box.querySelector("dl")?.textContent).toContain(`Subjectworker-1, Apply of worker-1 to ${released}`);
    const bodies = asked.filter((p) => /^\/(machines|drafts|releases|plans)\//.test(p));
    expect(new Set(bodies).size).toBe(bodies.length);
    expect([...bodies].sort()).toEqual([`/drafts/${DRF}`, `/machines/${MCH}`, `/plans/${PLN}`, `/releases/${REL}`]);
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

describe("ginsys/bronzeward#1055: the machine choices of Start import", () => {
  test("two clusters' machines named alike with one endpoint are offered apart, by node ID", async () => {
    const named = (id: string, node: string) => ({ id, cluster: id === MCH ? "cl_a" : "cl_b", name: "cp1", talosEndpoint: "10.0.0.1:50000", hardware: { smbiosUuid: null, talosNodeId: node, serial: null } });
    answers["/machines"] = ok({ items: [named(MCH, "4c1f9a2b3c4d"), named(MCH2, "9b22d0aa11bb")] });
    answers["/drafts"] = ok({ items: [] });
    await mount(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(StartImport, { query: "" }))));
    await settled();
    const shown = [...box.querySelectorAll('select[name="machine"] option')].map((o) => o.textContent);
    expect(shown).toEqual(["choose", "cp1 10.0.0.1:50000 (node 4c1f…)", "cp1 10.0.0.1:50000 (node 9b22…)"]);
  });
});

// ginsys/bronzeward#1055, part 2: the draft, publish, import and import base pages are headed by what a person knows.
const IBR = "ibr_k2pq3rstuvwxyz234567abcdef";
const ASR = "asr_k2pq3rstuvwxyz234567abcdef";
const FRV = "frv_k2pq3rstuvwxyz234567abcdef";
const providers = (view: ReactElement) => h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } }, view));
const headings = () => [...box.querySelectorAll("h2")].map((e) => e.textContent);

describe("ginsys/bronzeward#1055: the draft and publish pages", () => {
  const draft = {
    id: DRF, title: "Add the office labels", state: "open", revision: 3, cluster: "cl_a",
    entries: [
      { kind: "assignment", machine: MCH, name: null, revision: ASR, head: ASR, base: null },
      { kind: "import-base", machine: MCH, revision: IBR },
    ],
  };
  beforeEach(() => {
    answers[`/drafts/${DRF}`] = ok(draft);
    answers["/clusters/cl_a"] = ok({ id: "cl_a", name: "office" });
  });

  test("the draft page is headed by its stored title, with state, revision and cluster on a line and the identifier below", async () => {
    await mount(providers(h(DraftItem, { id: DRF })));
    await settled();
    expect(headings()[0]).toBe("Draft: Add the office labels");
    expect(text()).toContain("open, revision 3, Cluster office.");
    expect(text()).toContain(`Identifier ${DRF}`);
  });

  test("its entries name the machine, and the imported configuration is one link titled by the import base", async () => {
    await mount(providers(h(DraftItem, { id: DRF })));
    await settled();
    const entries = [...box.querySelectorAll('ul[aria-label="Entries"] li')];
    expect(entries.map((e) => e.querySelector("button, a")?.textContent)).toEqual(["assignment worker-1", "Imported configuration of worker-1"]);
    expect(entries[1]!.querySelector("a")?.getAttribute("title")).toBe(IBR);
    expect(entries[1]!.textContent).not.toContain("ibr_");
  });

  test("a title that is not safe text reads Untitled draft", async () => {
    answers[`/drafts/${DRF}`] = ok({ ...draft, title: "Add‮evil" });
    await mount(providers(h(DraftItem, { id: DRF })));
    await settled();
    expect(headings()[0]).toBe("Draft: Untitled draft");
    expect(text()).not.toContain("evil");
  });

  test("an entry whose name is not safe text shows no part of it, not even a short one", async () => {
    const entry = (name: string) => ({ kind: "fragment", name, layer: "role", revision: "frv_a", head: "frv_a", base: null });
    answers[`/drafts/${DRF}`] = ok({ ...draft, entries: [entry("a‮b"), entry("a\nb"), entry("ok-name")] });
    await mount(providers(h(DraftItem, { id: DRF })));
    await settled();
    const names = [...box.querySelectorAll('ul[aria-label="Entries"] li button')].map((b) => b.textContent);
    expect(names).toEqual(["fragment (name not shown)", "fragment (name not shown)", "fragment ok-name"]);
  });

  test("the publish page is headed by the same title, says the same line and carries the identifier with Copy", async () => {
    await mount(providers(h(PublishDraft, { query: `draft=${DRF}` })));
    await settled();
    expect(headings()[0]).toBe("Publish draft: Add the office labels");
    expect(text()).toContain("open, revision 3, Cluster office.");
    expect(box.querySelector('[data-testid="draft-line"]')?.textContent).toBe("open, revision 3, Cluster office.");
    expect(text()).toContain(`Identifier ${DRF}`);
    expect([...box.querySelectorAll("button")].map((b) => b.textContent)).toContain("Copy");
  });

  test("a draft state that is not safe text is not shown in the heading line, the not-open sentence or the drafts list", async () => {
    answers[`/drafts/${DRF}`] = ok({ ...draft, state: "bad\u202Estate" });
    await mount(providers(h(PublishDraft, { query: `draft=${DRF}` })));
    await settled();
    expect(box.querySelector('[data-testid="draft-line"]')?.textContent).toBe("(not shown), revision 3, Cluster office.");
    expect(box.querySelector('[data-testid="not-open"]')?.textContent).toBe("This draft is (not shown), not open: it cannot be published.");
    answers["/drafts"] = ok({ items: [{ ...draft, state: "bad\u202Estate" }], next: undefined });
    await mount(h(Drafts, { query: "", go: () => {} }), { ...reads, view: "list" });
    await settled();
    expect(box.querySelector("tbody tr")?.children[2]?.textContent).toBe("(not shown)");
    expect(box.textContent).not.toContain("bad");
  });
});

describe("ginsys/bronzeward#1055: the import and the import base pages", () => {
  const ingestion = (state: string) => ({
    id: ING, machine: MCH, draft: DRF, operation: OP, state, kind: "import", mode: "durable", review: null, ownerGeneration: 1,
    leaseUntil: at, expiresAt: at, createdAt: at,
  });

  test("the import page is headed by its machine and its state, with the identifier on a detail line", async () => {
    answers[`/ingestions/${ING}`] = ok(ingestion("released"));
    await mount(providers(h(IngestionItem, { id: ING })));
    await settled();
    expect(headings()[0]).toBe("Import of worker-1: released");
    expect(text()).toContain(`Identifier ${ING}`);
  });

  test("an import whose machine read gave no body says so by the machine, and never by an identifier", async () => {
    answers[`/ingestions/${ING}`] = ok(ingestion("paused"));
    answers[`/machines/${MCH}`] = missing;
    await mount(providers(h(IngestionItem, { id: ING })));
    await settled();
    expect(headings()[0]).toBe("Import of the machine: paused");
  });

  test("the import base page is headed by its machine and the day it was imported, with no number", async () => {
    answers[`/import-base-revisions/${IBR}`] = ok({ id: IBR, machine: MCH, author: "idn_k2pq3rstuvwxyz234567abcdef", createdAt: at, documentBytes: 12, references: 0, embedded: 0 });
    await mount(providers(h(ImportBaseItem, { id: IBR })));
    await settled();
    expect(headings()[0]).toBe(`Imported configuration of worker-1, imported ${when(at)}`);
    expect(text()).toContain(`Identifier ${IBR}`);
  });

  test("a fragment revision and an assignment revision are headed by what they revise and the day they were saved", async () => {
    answers[`/fragment-revisions/${FRV}`] = ok({ id: FRV, name: "network-base", cluster: "cl_a", layer: "base", author: "idn_k2pq3rstuvwxyz234567abcdef", createdAt: at, document: "a: 1", declarations: { references: {}, embedded: [] } });
    await mount(providers(h(RevisionItem, { kind: "fragment", id: FRV })));
    await settled();
    expect(headings()[0]).toBe(`Fragment network-base, saved ${when(at)}`);
    answers[`/assignment-revisions/${ASR}`] = ok({ id: ASR, machine: MCH, cluster: "cl_a", profiles: [], fragments: {}, author: "idn_k2pq3rstuvwxyz234567abcdef", createdAt: at });
    await mount(providers(h(RevisionItem, { kind: "assignment", id: ASR })));
    await settled();
    expect(headings()[0]).toBe(`Assignment of worker-1, saved ${when(at)}`);
  });

  test("a fragment name that is not safe text leaves the shortened identifier, and the cluster row names the cluster", async () => {
    answers[`/fragment-revisions/${FRV}`] = ok({ id: FRV, name: "network‮evil", cluster: "cl_a", layer: "base", author: "idn_k2pq3rstuvwxyz234567abcdef", createdAt: at, document: "a: 1", declarations: { references: {}, embedded: [] } });
    answers["/clusters/cl_a"] = ok({ id: "cl_a", name: "office" });
    await mount(providers(h(RevisionItem, { kind: "fragment", id: FRV })));
    await settled();
    expect(headings()[0]).toBe(`Fragment revision ${short(FRV)}`);
    expect(text()).toContain("Clusteroffice");
    expect(text()).not.toContain("cl_a");
  });
});

describe("ginsys/bronzeward#1055: the drafts lists open with the draft's name, the identifier last", () => {
  const draftRow = (id: string, title: string) => ({ id, cluster: "cl_a", title, state: "open", revision: 2, entries: [] });
  const DRF2 = "drf_k2pq3rstuvwxyz234567abcdeg";
  const cells = (row: Element) => [...row.children].map((c) => c.textContent);

  test("the Drafts and releases list: the name is the link, the cluster is named, a received title that is not safe never shows", async () => {
    answers["/drafts"] = ok({ items: [draftRow(DRF, "Add\u202Eevil"), draftRow(DRF2, "Office labels")], next: undefined });
    answers["/clusters/cl_a"] = ok({ id: "cl_a", name: "office" });
    await mount(h(Drafts));
    await settled();
    expect([...box.querySelectorAll("thead th")].map((c) => c.textContent)).toEqual(["Draft", "Cluster", "State", "Revision", "Identifier"]);
    const rows = [...box.querySelectorAll("tbody tr")];
    expect(cells(rows[0]!)).toEqual(["Untitled draft", "office", "open", "2", "drf_k2pq…cdef"]);
    expect(cells(rows[1]!)).toEqual(["Office labels", "office", "open", "2", "drf_k2pq…cdeg"]);
    expect(rows[0]!.querySelector("a")?.getAttribute("title")).toBe(DRF);
    expect(rows[0]!.querySelectorAll("a")).toHaveLength(2);
    expect(text()).not.toContain("evil");
    expect(box.querySelector("select option[value=cl_a]")?.textContent).toBe("office");
  });

  test("a cluster's open drafts: the same, with no cluster column", async () => {
    await mount(h(OpenDraftsView, { drafts: [draftRow(DRF, "a\nb") as never, draftRow(DRF2, "Office labels") as never] }));
    await settled();
    expect([...box.querySelectorAll("thead th")].map((c) => c.textContent)).toEqual(["Draft", "Revision", "Identifier"]);
    expect([...box.querySelectorAll("tbody tr")].map(cells)).toEqual([["Untitled draft", "2", "drf_k2pq…cdef"], ["Office labels", "2", "drf_k2pq…cdeg"]]);
  });
});

describe("ginsys/bronzeward#1055: a dependency alert is a sentence, the identifier last", () => {
  const T = "dep_k2pq3rstuvwxyz234567abcdef";
  const alert = (extra: Record<string, unknown>) => ({
    id: "dal_k2pq3rstuvwxyz234567abcdef", kind: "lost", dependency: T, provider: "p", object: "registry.test/app", version: 3, created: at, class: "lost", reason: "destroyed",
    releases: [REL], deletion: null, observedFrom: at, answerDate: null, epoch: "e", recordedAt: at, ...extra,
  });
  const dependency = { id: T, provider: "p", object: "registry.test/app", version: 3, created: at, class: "lost", reason: "destroyed", firstRetainedAt: at, unknownSince: null, observedFrom: at, recordedAt: at, stale: false };

  test("the Sources page: the dependency named once, as a link to its page; the releases by title; the identifier last", async () => {
    answers["/dependency-alerts"] = ok({ items: [alert({}), alert({ id: "dal_k2pq3rstuvwxyz234567abcdeg", kind: "monitor-stalled", dependency: null, object: null, version: null, class: null, reason: null, releases: null })], next: null });
    await mount(h(Sources, { query: "", go: () => {} }));
    await settled();
    const rows = [...box.querySelectorAll('table[aria-label="Dependency alerts"] tbody tr, [aria-label="Dependency alerts"] tbody tr')];
    expect(rows).toHaveLength(2);
    const cells = (row: Element) => [...row.children].map((c) => c.textContent);
    expect(cells(rows[0]!)).toEqual(["Dependency registry.test/app, version 3 is lost: the provider no longer has this version.", "lost", "lost", "destroyed", `Add the office labels, published ${when(at)}`, when(at), "dal_k2pq…cdef"]);
    expect(rows[0]!.querySelector("a")?.getAttribute("title")).toBe(T);
    expect(cells(rows[1]!)).toEqual(["The dependency monitor has stopped making progress.", "monitor-stalled", "none", "none", "none", when(at), "dal_k2pq…cdeg"]);
    expect([...box.querySelectorAll('[aria-label="Dependency alerts"] thead th')].map((c) => c.textContent)).toEqual(["Alert", "Kind", "Class", "Reason", "Releases", "Recorded", "Identifier"]);
  });

  test("the dependency's own page calls it This dependency, with no second link to itself", async () => {
    answers[`/dependencies/${T}`] = ok(dependency);
    answers[`/dependencies/${T}/releases`] = ok({ items: [], next: null });
    answers[`/dependencies/${T}/alerts`] = ok({ items: [alert({})], next: null });
    await mount(h(DependencyItem, { id: T }));
    await settled();
    const row = box.querySelector('[aria-label="Alerts"] tbody tr')!;
    expect(row.children[0]!.textContent).toBe("This dependency is lost: the provider no longer has this version.");
    expect(row.children[0]!.querySelectorAll("a")).toHaveLength(0);
    // The creation time is part of the version's identity: the date for the eye, the exact value as the title attribute.
    const created = [...box.querySelectorAll("dt")].find((dt) => dt.textContent === "Created")?.nextElementSibling?.querySelector("span");
    expect([created?.textContent, created?.getAttribute("title")]).toEqual([when(at), at]);
  });

  test("an alert whose kind is not the specification's, or whose dependency fields are missing, still reads as a sentence", async () => {
    answers["/dependency-alerts"] = ok({ items: [alert({ kind: "class-change" }), alert({ id: "dal_k2pq3rstuvwxyz234567abcdeg", object: null })], next: null });
    await mount(h(Sources, { query: "", go: () => {} }));
    await settled();
    const first = [...box.querySelectorAll('[aria-label="Dependency alerts"] tbody tr')].map((r) => r.children[0]!.textContent);
    expect(first).toEqual(["Alert of kind class-change.", "A dependency is lost: the provider no longer has this version."]);
  });

  test("a kind, class or reason that is not safe text is not shown in its cell either", async () => {
    answers["/dependency-alerts"] = ok({ items: [alert({ kind: "bad\u202Ekind", class: "bad\u202Eclass", reason: "bad\u0007reason" })], next: null });
    await mount(h(Sources, { query: "", go: () => {} }));
    await settled();
    const row = box.querySelector('[aria-label="Dependency alerts"] tbody tr')!;
    expect([...row.children].slice(0, 4).map((c) => c.textContent)).toEqual(["Alert.", "(not shown)", "(not shown)", "(not shown)"]);
    expect(box.textContent).not.toContain("bad");
  });

  test("the dependency list shows its provider, object and class, and the dependency page its provider, object, class and reason, as safe text", async () => {
    const bad = { created: "bad\u202Ecreated", provider: "bad\u202Eprovider", object: "bad\u0007object", class: "bad\u202Eclass", reason: "bad\u0007reason" };
    answers["/dependencies"] = ok({ items: [{ ...dependency, ...bad }], next: null, lastPass: null });
    await mount(h(Sources, { query: "", go: () => {} }));
    await settled();
    const row = box.querySelector('[aria-label="Dependencies"] tbody tr')!;
    expect([...row.children].slice(1, 4).map((c) => c.textContent)).toEqual(["(name not shown)", "(name not shown), version 3", "(not shown)"]);
    expect(box.querySelector('[aria-label="Dependencies"]')!.textContent).not.toContain("bad");
    answers[`/dependencies/${T}`] = ok({ ...dependency, ...bad });
    answers[`/dependencies/${T}/releases`] = ok({ items: [], next: null });
    answers[`/dependencies/${T}/alerts`] = ok({ items: [], next: null });
    await mount(h(DependencyItem, { id: T }), { ...reads, view: "page" });
    await settled();
    const fields = Object.fromEntries([...box.querySelectorAll("dt")].map((dt) => [dt.textContent, dt.nextElementSibling?.textContent]));
    expect([fields.Provider, fields.Object, fields.Class, fields.Reason]).toEqual(["(name not shown)", "(name not shown)", "(not shown)", "(not shown)"]);
    expect([...box.querySelectorAll("dt")].find((dt) => dt.textContent === "Created")?.nextElementSibling?.querySelector("span")?.getAttribute("title")).toBe("(not shown)");
    expect(box.textContent).not.toContain("bad\u202E");
  });

  test("a release record's kind on the dependency page is safe text too", async () => {
    answers[`/dependencies/${T}`] = ok(dependency);
    answers[`/dependencies/${T}/releases`] = ok({ items: [{ release: REL, cluster: "cl_a", publishedAt: at, records: [{ machine: "mch_k2pq3rstuvwxyz234567abcdef", kind: "bad\u202Ekind" }] }], next: null });
    answers[`/dependencies/${T}/alerts`] = ok({ items: [], next: null });
    await mount(h(DependencyItem, { id: T }));
    await settled();
    const cell = box.querySelector('[aria-label="Dependent releases"] tbody tr')!.children[3]!.textContent;
    expect(cell).toContain("(not shown)");
    expect(cell).not.toContain("bad");
  });
});

describe("ginsys/bronzeward#1055: the Create draft confirmation names the draft, with the short identifier beside Untitled draft", () => {
  const created = (title: string) => ({ id: DRF, cluster: "cl_a", title, state: "open", revision: 1, entries: [] });
  test("a titled draft: the title, no identifier text; an untitled one: Untitled draft (short identifier)", async () => {
    answers["/clusters/cl_a"] = ok({ id: "cl_a", name: "office" });
    await mount(h(CreatedDraft, { data: created("Office labels") }));
    await settled();
    expect(box.textContent).toBe("Draft Office labels created (Cluster office).");
    await mount(h(CreatedDraft, { data: created("a\nb") }), { ...reads, view: "untitled" });
    await settled();
    expect(box.textContent).toBe(`Draft Untitled draft (${short(DRF)}) created (Cluster office).`);
  });
});

// ginsys/bronzeward#1055 part 6: a word the server sets (a state, kind, class, reason, mode, source, role,
// route, action, cause, platform or hostname) is printed only when it is safe text (client contracts A8
// rule 6), on every page, not on the draft and dependency pages alone.
describe("ginsys/bronzeward#1055: a server word on a plan, operation, import, recovery, machine or audit page is safe text", () => {
  const BAD = "bad‮word";
  const UNKNOWN = "newstate";
  const NOT_SHOWN = "(not shown)";
  const principal = "idn_k2pq3rstuvwxyz234567abcdef";
  const planBody = (word: string) => ({
    ...plan("apply-config"), state: word, mode: word, route: word, expiresAt: at, createdAt: at, createdBy: { principal, role: word },
    evidence: { diff: null, validation: { result: word, platformMode: word }, dryRun: { available: false, reason: word } },
  });
  const opBody = (word: string) => ({
    ...operation("apply-config", { plan: PLN, machine: MCH }), kind: word, state: word, createdBy: { principal, role: word },
    attempts: [{ id: "att_a", number: 1, at, route: word, controller: "c", generation: 1, transportDeadline: at, verificationDeadline: at, response: { class: word, code: "c", at }, accounting: null }],
  });
  const ingestion = (word: string) => ({ id: ING, machine: MCH, draft: DRF, operation: OP, state: word, kind: word, mode: word, review: null, ownerGeneration: 1, leaseUntil: at, expiresAt: at, createdAt: at });
  const shown = (e: Element | null | undefined) => e?.textContent ?? "";
  const field = (name: string) => [...box.querySelectorAll("dt")].find((dt) => dt.textContent === name)?.nextElementSibling?.textContent;
  const clean = () => {
    expect(text()).not.toContain("bad");
    expect(text()).toContain(NOT_SHOWN);
  };

  test("the plan page: its heading, state, route, mode, validation mode, dry-run reason and creator's role", async () => {
    answers[`/plans/${PLN}`] = ok(planBody(BAD));
    await mount(h(PlanItem, { id: PLN }));
    await settled();
    expect(headings()[0]).toContain(`(${NOT_SHOWN})`);
    expect(field("State")).toBe(`${NOT_SHOWN}; expires ${when(at)}`);
    expect(field("Target")).toContain(`route ${NOT_SHOWN}`);
    expect(field("Operation")).toBe(`apply-config, mode ${NOT_SHOWN}`);
    expect(field("Created")).toContain(`(${NOT_SHOWN})`);
    expect(field("Validation")).toContain(`, ${NOT_SHOWN})`);
    expect(field("Validation")?.startsWith(`${NOT_SHOWN} at `)).toBe(true);
    expect(field("Dry run")).toBe(`not available (${NOT_SHOWN})`);
    clean();
  });

  test("the plans list shows its state as safe text", async () => {
    answers["/plans"] = ok({ items: [planBody(BAD)], next: null });
    await mount(h(Plans, { query: "", go: () => {} }));
    await settled();
    expect(box.querySelector("tbody")?.textContent).toContain(NOT_SHOWN);
    expect(text()).not.toContain("bad");
  });

  test("the operation page: its heading, kind, state, outcome line, description, creator's role and attempts", async () => {
    answers[`/operations/${OP}`] = ok(opBody(BAD));
    await mount(h(OperationItem, { id: OP }));
    await settled();
    expect(headings()[0]).toBe(`Operation op_k2pq…defg: ${NOT_SHOWN}, ${NOT_SHOWN}`);
    expect(field("Kind")).toBe(NOT_SHOWN);
    expect(field("State")).toBe(NOT_SHOWN);
    expect(box.querySelector("[data-testid=outcome]")?.textContent).toBe(`In progress: ${NOT_SHOWN}.`);
    expect(box.querySelector("[data-testid=now]")?.textContent).toBe(`An operation of kind ${NOT_SHOWN} in state ${NOT_SHOWN}; this page has no description of it.`);
    expect(field("Created")).toContain(`(${NOT_SHOWN})`);
    expect(shown(box.querySelector('[aria-label="Attempts"] tbody tr'))).toContain(`${NOT_SHOWN} ${"c"}`);
    clean();
  });

  test("the operations list shows its kind and state as safe text", async () => {
    answers["/operations"] = ok({ items: [opBody(BAD)], next: null });
    await mount(h(Operations, { query: "", go: () => {} }));
    await settled();
    const row = box.querySelector("tbody tr")!;
    expect([...row.children].filter((c) => c.textContent === NOT_SHOWN)).toHaveLength(2);
    expect(text()).not.toContain("bad");
  });

  test("a word that is safe text but unknown to the page is shown as it is", async () => {
    answers[`/operations/${OP}`] = ok(opBody(UNKNOWN));
    await mount(h(OperationItem, { id: OP }));
    await settled();
    expect(box.querySelector("[data-testid=outcome]")?.textContent).toBe(`In progress: ${UNKNOWN}.`);
    expect(field("Kind")).toBe(UNKNOWN);
    answers[`/plans/${PLN}`] = ok(planBody(UNKNOWN));
    await mount(h(PlanItem, { id: PLN }), { ...reads, view: "plan" });
    await settled();
    expect(field("State")).toBe(`${UNKNOWN}; expires ${when(at)}`);
  });

  test("the import page and the imports list: heading, state, kind, mode, and the sentences of an ended or lapsed claim", async () => {
    answers[`/ingestions/${ING}`] = ok(ingestion(BAD));
    await mount(providers(h(IngestionItem, { id: ING })));
    await settled();
    expect(headings()[0]).toBe(`Import of worker-1: ${NOT_SHOWN}`);
    expect(field("State")).toBe(`${NOT_SHOWN}, ${NOT_SHOWN}, ${NOT_SHOWN} staging`);
    clean();
    answers["/ingestions"] = ok({ items: [ingestion(BAD)], next: null });
    await mount(h(Imports, { query: "", go: () => {} }), { ...reads, view: "list" });
    await settled();
    expect(box.querySelector("tbody")?.textContent).toContain(NOT_SHOWN);
    expect(text()).not.toContain("bad");
  });

  test("what an import's state allows reads Ended with the state as safe text", async () => {
    await mount(h("div", {}, allowed(ingestion(BAD) as never, Date.now())));
    expect(text()).toContain(`Ended: ${NOT_SHOWN}.`);
    expect(text()).not.toContain("bad");
  });

  test("the recovery page: the entry's reason and every scope's state", async () => {
    answers["/recovery"] = ok({ epoch: "e0", recoveryMode: true, entry: { enteredAt: at, enteredBy: principal, reason: BAD, stoppedAt: at, restored: [] }, exit: null, scopes: [{ machine: MCH, state: BAD }] });
    await mount(providers(h(RecoveryConsole, {})), { ...reads, settle: (res, set, live) => (live() && set(res), false), view: "recovery" });
    await settled();
    expect(box.querySelector("[data-testid=recovery-entry]")?.textContent).toContain(`${NOT_SHOWN}. Restore stopped`);
    expect(box.querySelector('[data-testid=scopes] tbody td:nth-child(2)')?.textContent).toBe(NOT_SHOWN);
    expect(text()).not.toContain("bad");
  });

  test("the audit page: the principal's kind, role, action and the way the act came in", async () => {
    const [kind, role, action, via] = ["Principal", "Role", "Action", "Via"].map((n) => ACTS.find(([name]) => name === n)![1]);
    const a = { at, principal, principalKind: BAD, via: BAD, role: BAD, action: BAD, subjects: [], requestId: null, id: "act_a" } as never;
    await mount(h("div", {}, [kind!(a), role!(a), action!(a), via!(a)].map((c, i) => h("p", { key: i }, c))));
    await settled();
    expect([...box.querySelectorAll("p")].map((p) => p.textContent)).toEqual([expect.stringContaining(`(${NOT_SHOWN})`), NOT_SHOWN, NOT_SHOWN, NOT_SHOWN]);
    await mount(h("div", {}, ACTS.find(([name]) => name === "Role")![1]({ ...(a as object), role: null } as never)), { ...reads, view: "none" });
    expect(text()).toBe("none");
  });

  test("the audit page: the request an act came in with is shown only when it is safe text", async () => {
    const request = ACTS.find(([name]) => name === "Request")![1];
    const a = { at, principal, principalKind: "human", via: "api", role: "author", action: "x", subjects: [], requestId: BAD, id: "act_a" } as never;
    await mount(h("div", {}, request(a)));
    await settled();
    expect(text()).toBe(NOT_SHOWN);
    await mount(h("div", {}, request({ ...(a as object), requestId: "req_a" } as never)), { ...reads, view: "safe" });
    expect(text()).toBe("req_a");
    await mount(h("div", {}, request({ ...(a as object), requestId: null } as never)), { ...reads, view: "none" });
    expect(text()).toBe("none");
  });

  test("the machine page: platform, applied source, scope state, timeline kind, an observation's unread cause", async () => {
    const machine = {
      id: MCH, name: "worker-1", cluster: "cl_a", talosEndpoint: "https://10.0.0.1:50000", hardware: { talosNodeId: "n1" }, platform: BAD, scopeState: BAD, desired: REL,
      applied: { release: REL, source: BAD, importBase: null }, observed: null, openDrift: null, frozen: false,
    };
    const observation = { id: "obs_aaaaaaaaaaaaaaaaaaaaaaaaaa", basis: "b", at, purpose: "p", release: null, unread: { field: { cause: BAD }, [BAD]: { cause: "gone" } } };
    const entry = { epoch: "e0", revision: 1, at, kind: BAD };
    await mount(h(MachineShown, {
      id: MCH, m: { kind: "ok", data: machine }, obs: { kind: "ok", data: { items: [observation], next: undefined, restarted: false } }, km: { view: "v", data: machine, restarted: false },
      ko: { view: "v", data: undefined, restarted: false }, tl: { kind: "ok", data: { items: [entry], older: undefined, tail: undefined, read: true, restarted: false } },
      kt: { view: "v", data: undefined, restarted: false }, older: undefined, busy: false,
    } as never));
    await settled();
    expect([...box.querySelectorAll("p")].find((p) => p.textContent?.includes("10.0.0.1"))?.textContent).toContain(`, ${NOT_SHOWN}.`);
    expect(field("Applied")).toContain(`by ${NOT_SHOWN}`);
    expect(field("Scope")).toBe(NOT_SHOWN);
    expect(box.querySelector('[data-testid=timeline] tbody td:nth-child(2)')?.textContent).toBe(NOT_SHOWN);
    expect(box.querySelector('[data-testid=observations] tbody td:nth-child(5)')?.textContent).toBe(`field (${NOT_SHOWN}); ${NOT_SHOWN} (gone)`);
    expect(text()).not.toContain("bad");
  });

  test("the sentences of a machine whose Applied source is adoption still read as they did", () => {
    const m = { desired: REL, applied: { release: REL, source: "adoption" }, observed: { at, release: REL, observation: "obs_a" }, openDrift: null } as never;
    expect(sentences(m, []).some((s) => s.includes("(by adoption)"))).toBe(true);
    expect(sentences({ ...(m as object), applied: { release: REL, source: BAD } } as never, []).join(" ")).toContain(`(by ${NOT_SHOWN})`);
  });

  test("a role in the revocation and the freeze sentences, a revocation's reason, a review's mode and an operation's state in the next step", async () => {
    await mount(h(RevokedBy, { principal, reason: BAD }));
    expect(text()).toContain(`: ${NOT_SHOWN}`);
    await mount(h(ReviewBody, { r: { release: REL, machine: MCH, mode: BAD, configuration: null, provenance: [] } as never }), { ...reads, view: "review" });
    await settled();
    expect(field("Validated in mode")).toBe(NOT_SHOWN);
    const step = nextStep({ step: "operation-in-flight", machine: { id: MCH }, operation: { id: OP, state: BAD }, plan: null, draft: null, ingestion: null, rolloutHeldBy: null } as never);
    expect(step?.text).toBe(`An operation is changing this machine (${NOT_SHOWN}). Next: wait for it to end.`);
  });

  test("a timeline entry's role, reason and cause, and the plan's operation, are safe text", async () => {
    const entry = { epoch: "e0", revision: 1, at, kind: "freeze", entry: { role: BAD, reason: BAD, cause: BAD } };
    await mount(h(TimelineRows, { tl: { items: [entry], older: undefined, tail: undefined, read: true, restarted: false }, older: undefined, busy: false } as never));
    expect(box.querySelector("tbody td:nth-child(3)")?.textContent).toBe(`${NOT_SHOWN} "${NOT_SHOWN}" ${NOT_SHOWN}`);
    answers[`/plans/${PLN}`] = ok({ ...planBody("proposed"), operation: BAD });
    await mount(h(PlanItem, { id: PLN }), { ...reads, view: "operation" });
    await settled();
    expect(field("Operation")).toBe(`${NOT_SHOWN}, mode proposed`);
  });

  test("the note on a hostname that is not a valid name shows it only when it is safe text", () => {
    expect(nameOf(`bad${"‮"}host`).note).toBe(`no name: the hostname ${NOT_SHOWN} is not a valid name`);
    expect(nameOf("bad host").note).toBe('no name: the hostname "bad host" is not a valid name');
    // The whole hostname is checked, then cut: a bidirectional character past the cut, or 301 characters, is not shown either.
    for (const long of [`${"a".repeat(64)}${"‮"}host`, "a".repeat(301)]) expect(nameOf(long).note).toBe(`no name: the hostname ${NOT_SHOWN} is not a valid name`);
    expect(nameOf("a_".repeat(40)).note).toBe(`no name: the hostname ${JSON.stringify(`${"a_".repeat(32)}…`)} is not a valid name`);
  });

  test("what holds an approved plan names the machine's scope state as safe text", () => {
    const position = { step: "awaiting-dispatch", machine: { id: MCH, scopeState: BAD, frozen: false }, plan: { operation: "adopt", approvalCurrent: true }, draft: null, ingestion: null, operation: null, rolloutHeldBy: null };
    expect(nextStep(position as never)?.text).toContain(`Held now: the machine's scope state is ${NOT_SHOWN}.`);
  });

  test("the recorded self-approval reasons, the release source kinds of the prediction and the roles a refusal names are safe text", () => {
    expect(recordedReasons({ selfApproval: { reasons: [BAD, "created-plan"] } } as never)).toBe(`Recorded self-approval reasons: ${NOT_SHOWN}, created-plan.`);
    const prediction = predict({ id: PLN, release: REL, operation: "adopt", createdBy: { principal: "idn_other" } } as never, { kind: "ok", data: { principal } }, { kind: "loading" }, undefined, [{ kind: BAD, revision: "frr_a", load: { kind: "loading" } }]);
    expect(prediction.join(" ")).toContain(`${NOT_SHOWN} revision`);
    expect(prediction.join(" ")).not.toContain("bad");
    expect(needs(403, { roles: [BAD, "approver"] } as never)).toBe(` Needs ${NOT_SHOWN} or approver.`);
  });
});
