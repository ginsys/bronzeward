// The titles of releases, plans and operations (client contracts A8, ginsys/bronzeward#987): the store
// that reads each draft, release, plan, ingestion and machine once for each generation of a page's
// reads, and the builders that make a title from what the bodies carry. The store copies the three
// behaviours of the principal names' store (principals.ts) and adds a bound on reads in flight.

import { describe, expect, test } from "vitest";
import { createBodies, TITLE_READS } from "./bodies.ts";
import { alertSentence, dependencyName, draftCalled, draftName, draftText, operationTitle, planTitle, releaseTitle } from "./titles.ts";
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
    expect(releaseTitle({ publishedAt: at }, { title: "a\nb" } as never)).toBe(`Untitled draft, published ${when(at)}`);
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

describe("a dependency's name (ginsys/bronzeward#1055)", () => {
  test("its object and version, or the shortened identifier where the object is not safe text", () => {
    expect(dependencyName({ id: "dep_k2pq3rstuvwxyz234567abcdef", object: "registry.test/app", version: 3 })).toBe("Dependency registry.test/app, version 3");
    expect(dependencyName({ id: "dep_k2pq3rstuvwxyz234567abcdef", object: "bad\u0007object", version: 3 })).toBe("Dependency dep_k2pq…cdef");
  });
});

describe("a draft with no safe title (ginsys/bronzeward#1055, client contracts A8 rule 3)", () => {
  test("is Untitled draft where the body is read, and nothing where it is not", () => {
    expect(draftName({ title: "Office labels" } as never)).toBe("Office labels");
    for (const title of ["a\nb", "a\u202Eb", "x".repeat(301), ""]) expect(draftName({ title } as never)).toBe("Untitled draft");
    expect(draftName(null)).toBeUndefined();
    expect(draftName(undefined)).toBeUndefined();
  });

  test("in a sentence it is Untitled draft with the shortened identifier, and a safe title is quoted", () => {
    const id = "drf_k2pq3rstuvwxyz234567abcdef";
    expect(draftCalled({ id, title: "a\nb" })).toBe("Untitled draft (drf_k2pq…cdef)");
    expect(draftCalled({ id, title: "Office labels" })).toBe('"Office labels"');
  });
});

describe("a dependency alert's sentence (ginsys/bronzeward#1055, dependency monitor section 6.2)", () => {
  const a = (kind: string, reason: string | null = null, deletion: string | null = null) => alertSentence({ kind, reason, deletion });
  test("each kind of the specification, and the claims no reason can falsify", () => {
    expect(a("lost")).toEqual({ subject: true, text: "is lost: the provider no longer has this version." });
    expect(a("blocked", "soft-deleted")).toEqual({ subject: true, text: "is blocked. The block can be undone; the Reason column says how." });
    expect(a("persistent", "denied")).toEqual({ subject: true, text: "still cannot be confirmed." });
    expect(a("monitor-stalled")).toEqual({ subject: false, text: "The dependency monitor has stopped making progress." });
  });

  test("a regression says the deleted-or-restored caveat only where the reason is absent", () => {
    const caveat = "The provider may have deleted it, or been restored from a backup older than Bronzeward's database.";
    expect(a("regression", "absent").text).toBe(`can no longer be confirmed. It was found before. ${caveat}`);
    for (const reason of ["denied", "unavailable", "unreachable", "unreadable", "identity-mismatch", null]) {
      expect(a("regression", reason).text).toBe("can no longer be confirmed. It was found before.");
    }
  });

  test("a scheduled deletion names its date, or says only that it is scheduled where the date is missing or not one", () => {
    expect(a("deletion-scheduled", "deletion-scheduled", "2026-12-01T09:00:00Z").text).toBe(`is scheduled for deletion on ${when("2026-12-01T09:00:00Z")}.`);
    expect(a("deletion-scheduled", "deletion-scheduled", null).text).toBe("is scheduled for deletion.");
    expect(a("deletion-scheduled", "deletion-scheduled", "not a date").text).toBe("is scheduled for deletion.");
  });

  test("a kind this client does not know is a whole sentence with the kind only where it is safe text", () => {
    expect(a("class-change")).toEqual({ subject: false, text: "Alert of kind class-change." });
    expect(a("bad\u202Ekind")).toEqual({ subject: false, text: "Alert." });
  });
});
