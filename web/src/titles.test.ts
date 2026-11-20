// The titles of releases, plans and operations (client contracts A8, ginsys/bronzeward#987): the store
// that reads each draft, release, plan, ingestion and machine once for each generation of a page's
// reads, and the builders that make a title from what the bodies carry. The store copies the three
// behaviours of the principal names' store (principals.ts) and adds a bound on reads in flight.

import { describe, expect, test } from "vitest";
import { createBodies, TITLE_READS } from "./bodies.ts";
import { draftText, operationTitle, planTitle, releaseTitle } from "./titles.ts";
import type { ReadResult } from "./request.ts";
import { when } from "./states.tsx";

const ok = (data: unknown): ReadResult<unknown> => ({ kind: "ok", data });
const failed: ReadResult<unknown> = { kind: "unavailable" } as unknown as ReadResult<unknown>;

// A read that answers when its path is let go, so a test decides the order and the moment.
function gated() {
  const asked: string[] = [];
  const open = new Map<string, (r: ReadResult<unknown>) => void>();
  let live = 0;
  let most = 0;
  const read = (path: string) => {
    asked.push(path);
    live++;
    most = Math.max(most, live);
    return new Promise<ReadResult<unknown>>((resolve) => open.set(path, (r) => { live--; resolve(r); }));
  };
  return { read, asked, open, most: () => most };
}
const flush = () => new Promise((r) => setTimeout(r, 0));

describe("ginsys/bronzeward#987: the titles' store", () => {
  test("asks each distinct path once for one generation, however often it is wanted", async () => {
    const g = gated();
    const bodies = createBodies(g.read);
    bodies.want("v|0", ["/drafts/drf_a", "/drafts/drf_b", "/drafts/drf_a"]);
    bodies.want("v|0", ["/drafts/drf_a", "/drafts/drf_b"]);
    expect(g.asked).toEqual(["/drafts/drf_a", "/drafts/drf_b"]);
    g.open.get("/drafts/drf_a")!(ok({ title: "A" }));
    g.open.get("/drafts/drf_b")!(ok({ title: "B" }));
    await bodies.idle();
    bodies.want("v|0", ["/drafts/drf_a"]);
    expect(g.asked).toHaveLength(2);
    expect(bodies.get("v|0", "/drafts/drf_a")).toEqual({ title: "A" });
  });

  test("has at most four reads in flight and starts the next as one answers", async () => {
    const g = gated();
    const bodies = createBodies(g.read);
    const paths = Array.from({ length: 9 }, (_, i) => `/drafts/drf_${i}`);
    bodies.want("v|0", paths);
    expect(TITLE_READS).toBe(4);
    expect(g.asked).toEqual(paths.slice(0, 4));
    g.open.get(paths[0]!)!(ok({ title: "0" }));
    await flush();
    expect(g.asked).toEqual(paths.slice(0, 5));
    for (const p of paths) {
      await flush();
      g.open.get(p)?.(ok({ title: p }));
    }
    await bodies.idle();
    expect(g.asked).toEqual(paths);
    expect(g.most()).toBe(4);
  });

  test("a failed read is null, and a path not yet answered is undefined", async () => {
    const g = gated();
    const bodies = createBodies(g.read);
    bodies.want("v|0", ["/drafts/drf_a"]);
    expect(bodies.get("v|0", "/drafts/drf_a")).toBeUndefined();
    g.open.get("/drafts/drf_a")!(failed);
    await bodies.idle();
    expect(bodies.get("v|0", "/drafts/drf_a")).toBeNull();
  });

  test("a new generation forgets what an older one held, and reads again", async () => {
    const g = gated();
    const bodies = createBodies(g.read);
    bodies.want("v|0", ["/drafts/drf_a"]);
    g.open.get("/drafts/drf_a")!(ok({ title: "old" }));
    await bodies.idle();
    bodies.want("v|1", ["/drafts/drf_a"]);
    expect(bodies.get("v|0", "/drafts/drf_a")).toBeUndefined();
    expect(bodies.get("v|1", "/drafts/drf_a")).toBeUndefined();
    expect(g.asked).toEqual(["/drafts/drf_a", "/drafts/drf_a"]);
  });

  test("an answer that arrives after its generation was replaced is dropped", async () => {
    const g = gated();
    const bodies = createBodies(g.read);
    bodies.want("v|0", ["/drafts/drf_a"]);
    bodies.want("v|1", []);
    g.open.get("/drafts/drf_a")!(ok({ title: "late" }));
    await bodies.idle();
    expect(bodies.get("v|0", "/drafts/drf_a")).toBeUndefined();
    expect(bodies.get("v|1", "/drafts/drf_a")).toBeUndefined();
  });

  test("forgets when the last subscriber leaves, so a return to a view reads again", async () => {
    const g = gated();
    const bodies = createBodies(g.read);
    const off = bodies.subscribe(() => {});
    bodies.want("v|0", ["/drafts/drf_a"]);
    g.open.get("/drafts/drf_a")!(ok({ title: "T" }));
    await bodies.idle();
    off();
    await flush();
    bodies.want("v|0", ["/drafts/drf_a"]);
    expect(g.asked).toEqual(["/drafts/drf_a", "/drafts/drf_a"]);
  });

  test("only a refusal the shell must act on goes to the page's settle", async () => {
    const g = gated();
    const bodies = createBodies(g.read);
    const seen: string[] = [];
    bodies.want("v|0", ["/drafts/drf_a", "/drafts/drf_b", "/drafts/drf_c"], (res) => void seen.push(res.kind));
    g.open.get("/drafts/drf_a")!({ kind: "unauthenticated" } as ReadResult<unknown>);
    g.open.get("/drafts/drf_b")!(failed);
    g.open.get("/drafts/drf_c")!(ok({ title: "T" }));
    await bodies.idle();
    expect(seen).toEqual(["unauthenticated"]);
  });
});

describe("ginsys/bronzeward#987: the builders", () => {
  const at = "2026-11-19T10:00:00Z";
  test("a draft's title is shown only as safe text: no control or format character, at most 300 code points", () => {
    expect(draftText({ title: "Add the office labels" } as never)).toBe("Add the office labels");
    expect(draftText({ title: "x".repeat(300) } as never)).toHaveLength(300);
    expect(draftText({ title: "x".repeat(301) } as never)).toBeUndefined();
    expect(draftText({ title: "a\nb" } as never)).toBeUndefined();
    expect(draftText({ title: "a‮b" } as never)).toBeUndefined();
    expect(draftText({ title: "" } as never)).toBeUndefined();
    expect(draftText(null)).toBeUndefined();
    expect(draftText(undefined)).toBeUndefined();
  });

  test("a release is its draft's title and its date", () => {
    expect(releaseTitle({ publishedAt: at }, { title: "Office labels" } as never)).toBe(`Office labels, published ${when(at)}`);
    expect(releaseTitle({ publishedAt: at }, null)).toBeUndefined();
    expect(releaseTitle({ publishedAt: at }, { title: "a\nb" } as never)).toBeUndefined();
  });

  test("a plan is apply or adopt of its machine to its release's title", () => {
    expect(planTitle({ operation: "apply-config" }, "worker-1", "Office labels, published X")).toBe("Apply of worker-1 to Office labels, published X");
    expect(planTitle({ operation: "adopt" }, "mch_abcd…wxyz", "Office labels, published X")).toBe("Adopt of mch_abcd…wxyz to Office labels, published X");
    expect(planTitle({ operation: "adopt" }, "worker-1", undefined)).toBeUndefined();
  });

  test("an operation is its kind's title: publish, import, or its plan's", () => {
    const none = { draft: undefined, machine: undefined, plan: undefined };
    expect(operationTitle("publish", { ...none, draft: "Office labels" })).toBe("Publish of Office labels");
    expect(operationTitle("publish", none)).toBeUndefined();
    expect(operationTitle("ingest", { ...none, machine: "worker-1" })).toBe("Import of worker-1");
    expect(operationTitle("ingest", none)).toBe("Import");
    expect(operationTitle("apply-config", { ...none, plan: "Apply of worker-1 to X" })).toBe("Apply of worker-1 to X");
    expect(operationTitle("adopt", { ...none, plan: "Adopt of worker-1 to X" })).toBe("Adopt of worker-1 to X");
    expect(operationTitle("apply-config", none)).toBeUndefined();
    expect(operationTitle("something-new", none)).toBeUndefined();
  });
});
