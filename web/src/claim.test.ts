import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { Claim, claimSpec, discardOn, expiry, offers, reviewed } from "./claim.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

type Ingestion = components["schemas"]["IngestionBody"];

const NOW = Date.parse("2026-11-01T07:00:00Z");
const LATE = Date.parse("2026-11-01T07:06:00Z");
const ing = (o: Partial<Ingestion> = {}): Ingestion => ({
  id: "ing_6fdq2la", kind: "import", mode: "encrypted", state: "held", machine: "mch_b3caa2q", draft: "drf_2rmp457a", operation: "op_zcfw5mcm",
  review: "pending", ownerGeneration: 1, leaseUntil: "2026-11-01T07:05:00Z", expiresAt: "2026-11-01T07:38:00Z", createdAt: "2026-11-01T06:55:00Z", ...o,
});
const noReads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const page = (n: ReactNode) =>
  renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: noReads }, n))));

describe("what the claim offers (W5, compilation §3.6, journeys §8)", () => {
  test("a paused claim offers the review, continue and abandon, and no takeover", () => {
    expect(offers(ing({ state: "paused" }), NOW)).toEqual({ review: true, verbs: ["continue", "abandon"] });
  });
  test("a held claim within its lease offers abandon only", () => {
    expect(offers(ing(), NOW)).toEqual({ review: false, verbs: ["abandon"] });
  });
  test("a held or resumed claim whose lease lapsed offers takeover and abandon, and the review is never offered", () => {
    for (const state of ["held", "resumed"]) expect(offers(ing({ state }), LATE)).toEqual({ review: false, verbs: ["takeover", "abandon"] });
  });
  test("transient staging is never offered a takeover, also with a lapsed lease", () => {
    expect(offers(ing({ mode: "transient" }), LATE)).toEqual({ review: false, verbs: ["abandon"] });
  });
  test("an ended claim offers nothing", () => {
    for (const state of ["released", "abandoned", "failed", "something-new"]) expect(offers(ing({ state }), LATE)).toEqual({ review: false, verbs: [] });
  });
});

describe("the claim's writes", () => {
  test("each action posts an empty body to its own route and names the ingestion as the resource to read", () => {
    for (const [verb, route] of [["continue", "continuations"], ["takeover", "takeovers"], ["abandon", "abandonments"]] as const) {
      expect(claimSpec("ing_6fdq2la", verb)).toEqual({ method: "POST", path: `/ingestions/ing_6fdq2la/${route}`, resource: "/ingestions/ing_6fdq2la", body: {} });
    }
  });
});

describe("the review's expiry line", () => {
  test("says when it expires and in how many minutes", () => {
    expect(expiry("2026-11-01T07:38:00Z", NOW)).toMatch(/^Review expires .+ \(in 38 min\)\.$/);
    expect(expiry("2026-11-01T07:00:20Z", NOW)).toMatch(/\(in 1 min\)\.$/);
  });
  test("an expired review says so and sends the operator to read the import again, never a wait", () => {
    expect(expiry("2026-11-01T06:59:00Z", NOW)).toMatch(/^Review expired .+ read it again\.$/);
  });
});

describe("the review is held in memory only and dropped on leaving (§11.4 item 1)", () => {
  test("pagehide drops it, and leaving the page drops it and removes the listener", () => {
    const target = new EventTarget();
    let dropped = 0;
    const leave = discardOn(target, () => dropped++);
    target.dispatchEvent(new Event("pagehide"));
    expect(dropped).toBe(1);
    leave();
    expect(dropped).toBe(2);
    target.dispatchEvent(new Event("pagehide"));
    expect(dropped).toBe(2);
  });
  test("an answer with data keeps the document text; a failed, refused or dropped read keeps nothing (PROT-04)", () => {
    const body = { ingestion: "ing_6fdq2la", document: "token = BWSYNTH-sentinel", declarations: {} } as components["schemas"]["IngestionReviewBody"];
    expect(reviewed({ kind: "ok", data: body } as never)).toEqual({ text: "token = BWSYNTH-sentinel" });
    for (const res of [{ kind: "transport" }, { kind: "refused", status: 409, problem: null }] as never[]) {
      const r = reviewed(res);
      expect(r).toHaveProperty("why");
      expect(JSON.stringify(r)).not.toContain("BWSYNTH");
    }
    expect(reviewed({ kind: "discarded" } as never)).toBeUndefined();
    expect(reviewed({ kind: "loading" })).toBeUndefined();
    expect(reviewed(undefined)).toBeUndefined();
  });
});

describe("the import page's claim section", () => {
  test("a paused import shows the expiry, the review button, continue and abandon", () => {
    const html = page(h(Claim, { b: ing({ state: "paused" }), again: () => {}, now: NOW }));
    expect(html).toContain("Review import");
    expect(html).toContain("Continue import");
    expect(html).toContain("Abandon import");
    expect(html).not.toContain("Take over import");
    expect(html).toContain("in 38 min");
  });
  test("a lapsed lease shows takeover and abandon, with no review text and no wait", () => {
    const html = page(h(Claim, { b: ing(), again: () => {}, now: LATE }));
    expect(html).toContain("Take over import");
    expect(html).toContain("Abandon import");
    expect(html).not.toContain("Review import");
    expect(html).not.toContain("textarea");
  });
  test("a released import shows no claim section", () => {
    expect(page(h(Claim, { b: ing({ state: "released" }), again: () => {}, now: LATE }))).toBe("");
  });
});
