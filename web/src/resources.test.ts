import { readFileSync } from "node:fs";
import { createElement as h, type ReactElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { CreateDraft, DraftItem } from "./drafts.tsx";
import { EmptyList, Id, type Listed, readPages, type Reads, ReadsProvider, RESTARTED, viewOf } from "./list.tsx";
import { MachineItem } from "./machine.tsx";
import { ClusterItem, Clusters, Machines } from "./machines.tsx";
import {
  Audit, Change, Changes, DEPENDENCY_CLASSES, DependencyItem, klass, OPERATION_KINDS, OPERATION_STATES, OperationItem, Operations, PLAN_STATES,
  PlanItem, Plans, ReleaseItem, RevisionItem, seconds, SourceItem, Sources, view,
} from "./resources.tsx";

const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));
const at = (path: string, query = "") => view(path, query, go) as ReactElement<Record<string, unknown>> | undefined;

describe("paths (B3)", () => {
  test("each navigation page and each resource's item at its path", () => {
    const pages: [string, unknown][] = [
      ["/", Machines], ["/machines", Machines], ["/clusters", Clusters], ["/changes", Changes], ["/plans", Plans],
      ["/operations", Operations], ["/sources", Sources], ["/audit", Audit],
    ];
    for (const [path, type] of pages) expect(at(path)?.type, path).toBe(type);
    const items: [string, unknown, Record<string, unknown>][] = [
      ["/machines/mch_a", MachineItem, { id: "mch_a" }], ["/clusters/cl_a", ClusterItem, { id: "cl_a" }],
      ["/fragments/frg_a", SourceItem, { kind: "fragment", id: "frg_a" }], ["/profiles/prf_a", SourceItem, { kind: "profile", id: "prf_a" }],
      ["/assignments/asg_a", SourceItem, { kind: "assignment", id: "asg_a" }],
      ["/fragment-revisions/frv_a", RevisionItem, { kind: "fragment", id: "frv_a" }], ["/profile-revisions/prv_a", RevisionItem, { kind: "profile", id: "prv_a" }],
      ["/assignment-revisions/asr_a", RevisionItem, { kind: "assignment", id: "asr_a" }], ["/releases/rel_a", ReleaseItem, { id: "rel_a" }],
      ["/drafts/drf_a", DraftItem, { id: "drf_a" }], ["/plans/pln_a", PlanItem, { id: "pln_a" }], ["/operations/op_a", OperationItem, { id: "op_a" }], ["/dependencies/dep_a", DependencyItem, { id: "dep_a" }],
    ];
    for (const [path, type, props] of items) {
      expect(at(path)?.type, path).toBe(type);
      expect(at(path)?.props, path).toEqual(props);
    }
    expect(at("/drafts/new")?.type).toBe(CreateDraft);
  });

  test("ginsys/bronzeward#517 H2: each draft's page is its own, so a move to another draft keeps none of its reads or editor", () => {
    expect(at("/drafts/drf_a")?.key).toBe("drf_a");
    expect(at("/drafts/drf_b")?.key).toBe("drf_b");
  });

  test("anything else is not found, an object's own property names included", () => {
    for (const path of ["/recovery", "/constructor", "/toString", "/drafts/drf_a/fragments", "/acts", "/sources/frg_a", "/audit/act_a", "/machines/mch_a/review", "/Machines"]) {
      expect(at(path), path).toBeUndefined();
    }
  });

  test("an identifier links to its view by its prefix; any other is shown shortened, with the whole as its title", () => {
    expect(viewOf("rel_hxrtq2bdaaaaaaaaaaaaaaaaaa")).toBe("releases");
    expect(viewOf("op_q8raaaaaaaaaaaaaaaaaaat2na")).toBe("operations");
    for (const id of ["idn_k2pqaaaaaaaaaaaaaaaaaaa7xz", "constructor_a", "workers", "_a"]) expect(viewOf(id), id).toBeUndefined();
    expect(html(h(Id, { id: "pln_f645aaaaaaaaaaaaaaaaaacawy" }))).toBe('<a href="/plans/pln_f645aaaaaaaaaaaaaaaaaacawy" title="pln_f645aaaaaaaaaaaaaaaaaacawy">pln_f645…cawy</a>');
    expect(html(h(Id, { id: "idn_k2pqaaaaaaaaaaaaaaaaaaa7xz" }))).toBe('<span title="idn_k2pqaaaaaaaaaaaaaaaaaaa7xz">idn_k2pq…a7xz</span>');
    expect(html(h(Id, { id: null, none: "removed" }))).toBe("removed");
  });
});

describe("the read-only views", () => {
  test("the dependencies' lastPass is kept from the last page read; a plain page keeps nothing more", async () => {
    const start: Listed<number> = { items: [], next: undefined, restarted: false };
    const one = (data: unknown) => ({ read: <T,>() => Promise.resolve({ kind: "ok", data } as const as { kind: "ok"; data: T }) });
    expect(await readPages(one({ items: [1], lastPass: "2026-10-26T08:00:00Z" }), "/dependencies", "", start, true))
      .toEqual({ kind: "ok", data: { items: [1], next: undefined, restarted: false, extra: { lastPass: "2026-10-26T08:00:00Z" } } });
    expect(await readPages(one({ items: [1] }), "/fragments", "", start, true)).toStrictEqual({ kind: "ok", data: { items: [1], next: undefined, restarted: false } });
  });

  test("an empty list read again from its start still says it restarted (PAGE-02)", () => {
    const empty = (restarted: boolean) => html(h(EmptyList, { list: { items: [], next: undefined, restarted }, text: "No plans created yet." }));
    // The markup escapes the notice's apostrophe; its end has none.
    const notice = RESTARTED.slice(RESTARTED.indexOf("so it was"));
    expect(empty(true)).toContain(notice);
    expect(empty(true)).toContain("No plans created yet.");
    expect(empty(false)).not.toContain(notice);
  });

  test("a dependency's class and staleness are words, never a colour alone", () => {
    expect(klass({ class: "retained", stale: false })).toBe("retained");
    expect(klass({ class: "unknown", stale: true })).toBe("unknown (stale: not observed recently)");
    expect(klass({ class: null })).toBe("none");
  });

  test("a plan's change: the diff, withheld, absent, and an adoption that sends nothing", () => {
    const plan = (over: Partial<components["schemas"]["PlanBody"]>, diff: components["schemas"]["PlanDiff"] | null) =>
      ({ operation: "apply-config", evidence: { diff, validation: {}, dryRun: { available: false, reason: "none" } }, ...over }) as components["schemas"]["PlanBody"];
    const diff = { from: "rel_a", to: "rel_b", withheld: false, unified: "-team: a\n+team: <b>storage</b>" };
    expect(html(h(Change, { p: plan({}, diff) }))).toContain('<pre data-testid="diff">-team: a\n+team: &lt;b&gt;storage&lt;/b&gt;</pre>');
    expect(html(h(Change, { p: plan({}, { ...diff, withheld: true }) }))).toBe("<p>The change cannot be shown.</p>");
    expect(html(h(Change, { p: plan({}, null) }))).toBe("<p>No change recorded.</p>");
    expect(html(h(Change, { p: plan({ operation: "adopt" }, null) }))).toBe("<p>Sends nothing. Records the machine&#x27;s current configuration as applied.</p>");
  });

  test("limits in minutes when whole, else in seconds", () => {
    expect([seconds(60), seconds(600), seconds(90), seconds(0), seconds(null)]).toEqual(["1 min", "10 min", "90 s", "0 s", "none"]);
  });

  test("the filter values are the ones the server accepts", () => {
    const go = (file: string, name: string) => {
      const src = readFileSync(new URL(`../../internal/api/${file}`, import.meta.url), "utf8");
      const list = new RegExp(`${name}\\s*=\\s*\\[\\]string\\{([^}]*)\\}`).exec(src)?.[1] ?? "";
      return [...list.matchAll(/"([^"]+)"/g)].map((m) => m[1]);
    };
    expect(PLAN_STATES).toEqual(go("plans.go", "planStates"));
    expect(OPERATION_STATES).toEqual(go("operations.go", "operationStates"));
    expect(OPERATION_KINDS).toEqual(go("operations.go", "operationKinds"));
    expect(DEPENDENCY_CLASSES).toEqual(go("dependencies.go", "dependencyClasses"));
  });
});
