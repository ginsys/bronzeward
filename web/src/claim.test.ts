import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { components } from "./api/v1";
import { BaseDocument, Claim, ClaimView, claimSpec, discardOn, readBaseDocument, EXPIRY_TICK, expiry, newest, offers, readReview, recheckOnOffer, rereadOnce, rereads, reviewed, Shown, shownVerbs, watchesExpiry, watchExpiry } from "./claim.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Client } from "./request.ts";
import { type Outcome, Write, Writes } from "./write.ts";

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

describe("the review is closed and superseded reads are dropped (local review of 362ca7a3)", () => {
  test("Close review stays enabled for every role, offline and with a kept page", () => {
    const shell = (value: { roles: string[]; offline: boolean; kept?: boolean }) =>
      renderToStaticMarkup(h(ShellContext, { value: value as never }, h(Shown, { id: "ing_6fdq2la", text: "x = 1", close: () => {} })));
    for (const value of [{ roles: [], offline: false }, { roles: ["author"], offline: true }, { roles: ["author"], offline: false, kept: true }]) {
      const html = shell(value);
      expect(html).toContain("Close review");
      expect(html).not.toMatch(/<button[^>]*disabled[^>]*>Close review/);
    }
  });
  test("only the newest read may set the review, and a drop ends every read in flight", () => {
    const reads = newest();
    const first = reads.start();
    const second = reads.start();
    expect(reads.live(first)).toBe(false);
    expect(reads.live(second)).toBe(true);
    reads.drop();
    expect(reads.live(second)).toBe(false);
    expect(reads.live(reads.start())).toBe(true);
  });
});

describe("an idle paused page and a late review read (Copilot threads on web/src/claim.tsx)", () => {
  test("a paused claim before its review time ends is watched for its expiry", () => {
    const paused = ing({ state: "paused" });
    expect(watchesExpiry(paused, NOW)).toBe(true);
    expect(watchesExpiry(paused, Date.parse("2026-11-01T07:38:00Z"))).toBe(true);
    expect(watchesExpiry(paused, Date.parse("2026-11-01T07:38:01Z"))).toBe(false);
  });
  test("a claim that is no longer live is not watched, nor is a state this page does not know", () => {
    for (const state of ["released", "abandoned", "something-new"]) expect(watchesExpiry(ing({ state }), NOW)).toBe(false);
  });
  // ginsys/bronzeward#724: an interrupted continuation or takeover ends the progress watch and the lease
  // refresh while the claim is still held or resumed, and the API treats it as abandoned at expiresAt.
  describe.each(["held", "resumed"])("a %s claim is watched for its expiry", (state) => {
    beforeEach(() => {
      vi.useFakeTimers();
      vi.setSystemTime(NOW);
    });
    afterEach(() => vi.useRealTimers());
    test("it is watched before its end, up to the end itself, and not after", () => {
      const b = ing({ state });
      expect(watchesExpiry(b, NOW)).toBe(true);
      expect(watchesExpiry(b, Date.parse(b.expiresAt))).toBe(true);
      expect(watchesExpiry(b, Date.parse(b.expiresAt) + 1)).toBe(false);
    });
    test("the page reads the import again once, on the first tick after its expiry", () => {
      const b = ing({ state });
      const again = vi.fn();
      if (watchesExpiry(b, Date.now())) watchExpiry(b.expiresAt, again);
      vi.advanceTimersByTime(37 * 60_000);
      expect(again).not.toHaveBeenCalled();
      vi.advanceTimersByTime(60_000 + EXPIRY_TICK);
      expect(again).toHaveBeenCalledTimes(1);
      vi.advanceTimersByTime(10 * 60_000);
      expect(again).toHaveBeenCalledTimes(1);
    });
  });
  describe("the expiry check follows the clock", () => {
    beforeEach(() => {
      vi.useFakeTimers();
      vi.setSystemTime(NOW);
    });
    afterEach(() => vi.useRealTimers());
    const END = "2026-11-01T07:38:00Z";
    test("it reads again once, on the first tick after the end, and not before", () => {
      const again = vi.fn();
      watchExpiry(END, again);
      vi.advanceTimersByTime(37 * 60_000);
      expect(again).not.toHaveBeenCalled();
      vi.advanceTimersByTime(60_000 + EXPIRY_TICK);
      expect(again).toHaveBeenCalledTimes(1);
      vi.advanceTimersByTime(10 * 60_000);
      expect(again).toHaveBeenCalledTimes(1);
    });
    test("a clock set forward past the end is noticed within one tick, not at the first wait", () => {
      const again = vi.fn();
      watchExpiry(END, again);
      vi.setSystemTime(Date.parse("2026-11-01T07:39:00Z"));
      vi.advanceTimersByTime(EXPIRY_TICK);
      expect(again).toHaveBeenCalledTimes(1);
    });
    test("an expiry far away still fires, with no cap on the wait", () => {
      const again = vi.fn();
      watchExpiry("2026-12-31T07:00:00Z", again);
      vi.setSystemTime(Date.parse("2026-12-31T07:00:01Z"));
      vi.advanceTimersByTime(EXPIRY_TICK);
      expect(again).toHaveBeenCalledTimes(1);
    });
    test("leaving the page ends the check", () => {
      const again = vi.fn();
      const stop = watchExpiry(END, again);
      stop();
      vi.setSystemTime(Date.parse("2026-11-01T08:00:00Z"));
      vi.advanceTimersByTime(60_000);
      expect(again).not.toHaveBeenCalled();
    });
  });
  test("a paused claim past its review time offers no review, continue and abandon stay for the server to judge", () => {
    expect(offers(ing({ state: "paused" }), Date.parse("2026-11-01T07:38:01Z"))).toEqual({ review: false, verbs: ["continue", "abandon"] });
    expect(page(h(Claim, { b: ing({ state: "paused" }), again: () => {}, now: Date.parse("2026-11-01T07:39:00Z") }))).not.toContain("Review import");
  });
  test("the review read hands its liveness check to the shell settle", async () => {
    const seen: { path?: string; live?: (() => boolean) | undefined } = {};
    const live = () => false;
    await readReview(async (path, l) => { seen.path = path; seen.live = l; return undefined; }, "ing_6fdq2la", live);
    expect(seen.path).toBe("/ingestions/ing_6fdq2la/review");
    expect(seen.live).toBe(live);
  });
});

// ginsys/bronzeward#858: the import base's document, read on request by the author who reviewed it.
describe("the import base document view (ginsys/bronzeward#858)", () => {
  const ibr = "ibr_2xxaaaaaaaaaaaaaaaaaaaqa4m";
  const asAuthor = { roles: ["author"], offline: false };
  const shell = (value: object, el: ReactNode) => renderToStaticMarkup(h(ShellContext, { value: value as never }, h(ReadsProvider, { value: noReads }, el)));

  test("its read goes to the document path with the shell's liveness check", async () => {
    const seen: { path?: string; live?: (() => boolean) | undefined } = {};
    const live = () => false;
    await readBaseDocument(async (path, l) => { seen.path = path; seen.live = l; return undefined; }, ibr, live);
    expect(seen.path).toBe(`/import-base-revisions/${ibr}/document`);
    expect(seen.live).toBe(live);
  });

  test("the text is read on request only: the page offers Show document and holds no text before it", () => {
    const out = shell(asAuthor, h(BaseDocument, { id: ibr }));
    expect(out).toContain("Show document");
    expect(out).not.toContain("<textarea");
  });

  test("the open document has its own notice and label, a close button every role can press, and no copy, download or print control", () => {
    for (const value of [{ roles: [], offline: false }, { roles: ["author"], offline: true }]) {
      const out = shell(value, h(Shown, { id: ibr, text: "machine:\n  token: BWSYNTH-base", close: () => {}, kind: "document" }));
      expect(out).toContain("PROTECTED DOCUMENT.");
      expect(out).not.toContain("PROTECTED REVIEW");
      expect(out).toContain("Document of import base ibr_2xxa…qa4m");
      expect(out).toContain("Close document");
      expect(out).not.toMatch(/<button[^>]*disabled[^>]*>Close document/);
      expect(out).toContain("readOnly");
      for (const control of ["Copy", "Download", "Print", "Save", "Export"]) expect(out).not.toContain(control);
    }
  });

  test("the review keeps its own wording and marks panel hook", () => {
    const out = shell(asAuthor, h(Shown, { id: "ing_6fdq2la", text: "x = 1", close: () => {} }));
    expect(out).toContain("PROTECTED REVIEW.");
    expect(out).toContain("Review of import ing_6fdq2la");
    expect(out).toContain("Close review");
  });

  test("a read that failed shows no text: only the reviewed text of an ok answer is kept", () => {
    expect(reviewed({ kind: "ok", data: { id: ibr, machine: "mch_b3caa2q", document: "token = BWSYNTH-base" } } as never)).toEqual({ text: "token = BWSYNTH-base" });
    for (const kind of ["forbidden", "conflict", "offline"]) {
      const r = reviewed({ kind } as never);
      expect(r !== undefined && "text" in r).toBe(false);
    }
  });
});

describe("the outcome of a sent action stays after its verb leaves the offered list (advisory and Copilot findings on web/src/claim.tsx)", () => {
  const done = (data: unknown): Outcome => ({ kind: "done", status: 200, data, replayed: false });
  const refused = (status: number): Outcome => ({ kind: "refused", status, problem: null, replayed: false });
  const sent = (verb: Parameters<typeof claimSpec>[1], outcome?: Outcome) => {
    const w = new Write(claimSpec("ing_6fdq2la", verb));
    if (outcome !== undefined) w.outcome = outcome;
    return w;
  };
  const view = (verb: Parameters<typeof claimSpec>[1], offered: boolean, w: Write | undefined) => page(h(ClaimView, { verb, offered, w, send: () => {} }));

  test("Abandon: no verb is left, the section stays, and the confirmation stays", () => {
    expect(offers(ing({ state: "abandoned" }), NOW).verbs).toEqual([]);
    expect(shownVerbs([], ["abandon"])).toEqual(["abandon"]);
    const html = view("abandon", false, sent("abandon", done({ operation: "op_zcfw5mcm" })));
    expect(html).toContain("The import was abandoned. Its staged input is discarded.");
    expect(html).not.toContain("Abandon import");
  });
  test("Continue: the verb set changes, and the confirmation stays beside the verbs still offered", () => {
    expect(offers(ing({ state: "resumed" }), NOW).verbs).toEqual(["abandon"]);
    expect(shownVerbs(["abandon"], ["continue"])).toEqual(["continue", "abandon"]);
    expect(shownVerbs(["continue", "abandon"], [])).toEqual(["continue", "abandon"]);
    expect(shownVerbs(["takeover", "abandon"], ["takeover", "abandon"])).toEqual(["takeover", "abandon"]);
    const html = view("continue", false, sent("continue", done({ operation: "op_zcfw5mcm" })));
    expect(html).toContain("The import continues in operation");
    expect(html).not.toContain("Continue import");
    // While the verb is offered, its button shows with the line.
    expect(view("continue", true, sent("continue", done({ operation: "op_zcfw5mcm" })))).toContain("Continue import");
  });
  test("a running action stays too: the line says it is saving until the answer", () => {
    expect(view("abandon", false, sent("abandon"))).toContain("Saving");
  });
  test("Continue refused with 409: the ingestion is read again once, and the write is never sent again", async () => {
    expect(rereads(done({}))).toBe(true);
    expect(rereads(refused(409))).toBe(true);
    for (const o of [refused(412), refused(422), refused(500), { kind: "not-done", why: "x", problem: null }, { kind: "unknown", why: "x", problem: null }, undefined] as (Outcome | undefined)[]) expect(rereads(o)).toBe(false);
    const posts: string[] = [];
    const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url).replace("/api/v1", "");
      if (path === "/recovery") return new Response(JSON.stringify({ mode: "normal" }), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": "e1" } });
      posts.push(`${init?.method ?? "GET"} ${path}`);
      return new Response(JSON.stringify({ type: "urn:bronzeward:problem:conflict", title: "conflict", status: 409 }), { status: 409, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": "e1" } });
    });
    const writes = new Writes(() => "https://idp.invalid SYNTH-sub-1", async () => {});
    writes.client = new Client({ token: () => "SYNTH-token", fetch: fetchMock as unknown as typeof fetch });
    const w = sent("continue");
    await writes.submit(w);
    expect(w.outcome).toMatchObject({ kind: "refused", status: 409 });
    const again = vi.fn();
    let last = rereadOnce(w, undefined, again);
    last = rereadOnce(w, last, again);
    expect(again).toHaveBeenCalledTimes(1);
    expect(posts).toEqual(["POST /ingestions/ing_6fdq2la/continuations"]);
    // A later write that ends the same way is read for as well.
    const next = sent("continue", refused(409));
    expect(rereadOnce(next, last, again)).toBe(next);
    expect(again).toHaveBeenCalledTimes(2);
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

// Where focus goes after an action of the claim section (ginsys/bronzeward#896).
describe("the focus check after the offer changes", () => {
  const was = "false continue,abandon";
  const now = "false abandon";
  test("the first change of the offer after an action ended checks focus once and disarms", () => {
    expect(recheckOnOffer(true, was, now)).toEqual({ check: true, armed: false });
  });
  test("an offer that did not change keeps the check armed for the read that follows", () => {
    expect(recheckOnOffer(true, was, was)).toEqual({ check: false, armed: true });
  });
  test("a later change of the offer (the expiry tick, a lease that lapsed, another operator) moves nothing", () => {
    const first = recheckOnOffer(true, was, now);
    expect(recheckOnOffer(first.armed, now, "false takeover,abandon")).toEqual({ check: false, armed: false });
  });
  test("a change of the offer before any action ended moves nothing", () => {
    expect(recheckOnOffer(false, was, now)).toEqual({ check: false, armed: false });
  });
});
