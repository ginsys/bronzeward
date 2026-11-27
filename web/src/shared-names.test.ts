import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { type Reads, ReadsProvider } from "./list.tsx";
import { sentence, staleAdoption, wordsFor } from "./problems.tsx";
import { publishEnd, SENT_NOTHING } from "./publish.tsx";
import type { Problem } from "./request.ts";
import { adoptionWrite, canAdopt, canStop, recorded, SharedNames, shownName, slotOf, stateOf } from "./shared-names.tsx";

type S = components["schemas"];
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const text = (e: Parameters<typeof renderToStaticMarkup>[0]) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e)).replace(/<[^>]*>/g, "");

const adoption = (over: Partial<S["AdoptionEntry"]> = {}) => ({ kind: "adoption", of: "fragment", name: "ntp", head: "frg_2", base: null, revision: "frv_a", ...over }) as never;
const shared = (over: Record<string, unknown> = {}) => ({ kind: "fragment", name: "ntp", scope: "library", head: "frg_2", base: 1, adoptionBase: null, revision: "frv_s", ...over }) as never;
const own = { kind: "fragment", name: "ntp", head: "frg_1", base: 1, revision: "frv_own" } as never;
const row = (over: Partial<S["ClusterAdoptionBody"]> = {}) => ({ cluster: "cl_1", revision: "frv_old", adoptionRevision: 2, behind: false, release: null, via: null, machines: [], ...over }) as S["ClusterAdoptionBody"];

describe("what the draft says of a shared name", () => {
  test("an adoption, its removal, a shared edit and a shared removal; the cluster's own entry and other names say nothing", () => {
    expect(slotOf([adoption()], "fragment", "ntp")).toEqual({ kind: "adopts", revision: "frv_a", base: null });
    expect(slotOf([adoption({ base: 3 })], "fragment", "ntp")).toEqual({ kind: "adopts", revision: "frv_a", base: 3 });
    expect(slotOf([adoption({ revision: null })], "fragment", "ntp")).toEqual({ kind: "stops", base: null });
    expect(slotOf([adoption({ revision: null, base: 4 })], "fragment", "ntp")).toEqual({ kind: "stops", base: 4 });
    expect(slotOf([shared()], "fragment", "ntp")).toEqual({ kind: "edits" });
    expect(slotOf([shared({ revision: null })], "fragment", "ntp")).toEqual({ kind: "removed" });
    expect(slotOf([own], "fragment", "ntp")).toBeUndefined();
    expect(slotOf([adoption()], "profile", "ntp")).toBeUndefined();
    expect(slotOf([adoption()], "fragment", "dns")).toBeUndefined();
  });
});

describe("this cluster's view of a shared name and what a press may do", () => {
  const head = "frv_head";
  test("the draft's entry decides first", () => {
    expect(stateOf({ kind: "adopts", revision: head, base: 2 }, [row({ behind: true })], "cl_1", head)).toEqual({ kind: "adopts", revision: head, behind: false, removable: true, again: false });
    expect(stateOf({ kind: "adopts", revision: "frv_old", base: null }, [], "cl_1", head)).toEqual({ kind: "adopts", revision: "frv_old", behind: true, removable: true, again: false });
    for (const kind of ["edits", "removed"] as const) expect(stateOf({ kind }, [row()], "cl_1", head)).toEqual({ kind });
    expect(stateOf({ kind: "stops", base: 2 }, [row()], "cl_1", head)).toEqual({ kind: "stops", again: false });
  });

  // Another draft changed the cluster's adoption after this entry was staged: publication would refuse it as stale, and a press
  // records the current counter (the carried finding G1 of ginsys/bronzeward#1227 and its twin for a staged removal).
  describe("an entry staged before the cluster's adoption changed offers its press again", () => {
    test("an adoption of the head whose base is not the row's counter, or whose row is gone, offers Adopt again though it is not behind", () => {
      const head = "frv_a";
      const adopts = (base: number | null) => ({ kind: "adopts", revision: head, base }) as const;
      // Staged on counter 2, the row is at 3 (another draft re-adopted): stale, and still removable (the row holds a revision).
      const moved = stateOf(adopts(2), [row({ adoptionRevision: 3, revision: head })], "cl_1", head);
      expect(moved).toMatchObject({ behind: false, again: true, removable: true });
      expect([canAdopt(moved), canStop(moved)]).toEqual([true, true]);
      // Staged on counter 2, the row is gone (another draft stopped following): Adopt again, no Stop (404 over a removed row).
      const gone = stateOf(adopts(2), [], "cl_1", head);
      expect(gone).toMatchObject({ behind: false, again: true, removable: false });
      expect([canAdopt(gone), canStop(gone)]).toEqual([true, false]);
      // Still on the counter it was staged on, or introduced with no row before: nothing to press again.
      const same = stateOf(adopts(2), [row({ adoptionRevision: 2, revision: head })], "cl_1", head);
      expect([canAdopt(same), canStop(same)]).toEqual([false, true]);
      const fresh = stateOf(adopts(null), [], "cl_1", head);
      expect(fresh).toMatchObject({ again: false });
      expect(canAdopt(fresh)).toBe(false);
      // Staged with no row (base none), then another draft adopted the name for this cluster: the row now holds a counter the
      // entry's absent base is not, so publication refuses it, though the revision is the head's and the cluster is not behind.
      const adoptedFirst = stateOf(adopts(null), [row({ adoptionRevision: 1, revision: head })], "cl_1", head);
      expect(adoptedFirst).toMatchObject({ behind: false, again: true, removable: true });
      expect([canAdopt(adoptedFirst), canStop(adoptedFirst)]).toEqual([true, true]);
    });

    test("a removal whose row holds another counter offers Stop following again; a removal whose row is gone cannot be pressed again", () => {
      const stops = { kind: "stops", base: 2 } as const;
      const moved = stateOf(stops, [row({ adoptionRevision: 3 })], "cl_1", "frv_h");
      expect(moved).toEqual({ kind: "stops", again: true });
      expect([canAdopt(moved), canStop(moved)]).toEqual([true, true]);
      const same = stateOf(stops, [row({ adoptionRevision: 2 })], "cl_1", "frv_h");
      expect([canAdopt(same), canStop(same)]).toEqual([true, false]);
      const gone = stateOf(stops, [], "cl_1", "frv_h");
      expect(gone).toEqual({ kind: "stops", again: false });
      expect(canStop(gone)).toBe(false);
      // Another cluster's row and a pin say nothing of this cluster's adoption.
      expect(stateOf(stops, [row({ cluster: "cl_2", adoptionRevision: 9 }), row({ via: "edge" })], "cl_1", "frv_h")).toEqual({ kind: "stops", again: false });
    });
  });

  // Persistence-api §3.1.1: over a removed adoption (a base, and no row that holds a revision) a removal is 404 and the
  // re-adoption stays, so Stop following is not offered; where the draft introduced the adoption or replaced a row it is.
  test("a re-adoption over a removed adoption cannot be removed again, and another cluster's row or a pin does not change that", () => {
    const adopts = { kind: "adopts", revision: "frv_a", base: 3 } as const;
    expect(stateOf(adopts, [], "cl_1", "frv_a")).toMatchObject({ removable: false });
    expect(stateOf(adopts, [row({ cluster: "cl_2" }), row({ via: "edge" })], "cl_1", "frv_a")).toMatchObject({ removable: false });
    expect(stateOf(adopts, [row({})], "cl_1", "frv_a")).toMatchObject({ removable: true });
    expect(stateOf({ ...adopts, base: null }, [], "cl_1", "frv_a")).toMatchObject({ removable: true });
    expect(canStop(stateOf(adopts, [], "cl_1", "frv_a"))).toBe(false);
  });

  test("else the cluster's adoption, else a shared profile's pin, else nothing; another cluster's rows are not this one's", () => {
    expect(stateOf(undefined, [row({ cluster: "cl_2", behind: true }), row({ behind: true })], "cl_1", head)).toEqual({ kind: "follows", revision: "frv_old", behind: true });
    expect(stateOf(undefined, [row({ via: "edge", adoptionRevision: null })], "cl_1", head)).toEqual({ kind: "via", via: "edge" });
    expect(stateOf(undefined, [row({ cluster: "cl_2" })], "cl_1", head)).toEqual({ kind: "none" });
    expect(stateOf(undefined, [], "cl_1", head)).toEqual({ kind: "none" });
    // An adoption and a profile pin of one cluster: the adoption is the view.
    expect(stateOf(undefined, [row({ via: "edge" }), row({ revision: "frv_a" })], "cl_1", head)).toEqual({ kind: "follows", revision: "frv_a", behind: false });
  });

  test("Adopt where the cluster is not on the head, Stop following where a name is followed", () => {
    const states = [
      { kind: "none" }, { kind: "stops", again: false }, { kind: "via", via: "e" }, { kind: "edits" }, { kind: "removed" },
      { kind: "adopts", revision: "r", behind: false, removable: true, again: false }, { kind: "adopts", revision: "r", behind: true, removable: true, again: false },
      { kind: "follows", revision: "r", behind: false }, { kind: "follows", revision: "r", behind: true },
      { kind: "adopts", revision: "r", behind: true, removable: false, again: false },
      { kind: "stops", again: true }, { kind: "adopts", revision: "r", behind: false, removable: false, again: true },
    ] as const;
    expect(states.map(canAdopt)).toEqual([true, true, true, false, false, false, true, false, true, true, true, true]);
    expect(states.map(canStop)).toEqual([false, false, false, false, false, true, true, true, true, false, true, false]);
  });
});

describe("the request of a press", () => {
  test("Adopt puts the head's revision, Stop following deletes, and the name is escaped", () => {
    expect(adoptionWrite("drf_1", "fragment", "ntp-servers", "frv_h")).toEqual({ method: "PUT", path: "/drafts/drf_1/adoptions/fragments/ntp-servers", body: { revision: "frv_h" } });
    expect(adoptionWrite("drf_1", "profile", "a/b c", null)).toEqual({ method: "DELETE", path: "/drafts/drf_1/adoptions/profiles/a%2Fb%20c", body: undefined });
  });
});

describe("a publication that failed on a moved adoption", () => {
  const failed = (conflicts: unknown) => ({ state: "failed", result: null, error: { type: "urn:bronzeward:problem:stale-input", status: 409, conflicts } }) as never;
  test("it names the shared head that moved and says to adopt again, where the failed publication's text is worded", () => {
    expect(publishEnd(failed([{ scope: "adoption", head: "frg_9", expected: 2, actual: "absent" }]))).toEqual({
      text: `The publication failed: stale-input. ${SENT_NOTHING} The shared name moved after this draft was read (frg_9 was at adoption 2 and is at absent). Read the draft again, adopt the name again in this draft and publish that.`,
    });
  });

  test("a conflict of another kind, none, or a malformed one keeps the general step", () => {
    const general = `The publication failed: stale-input. ${SENT_NOTHING} Reload, read the current state, then decide whether to make the change again.`;
    for (const conflicts of [[{ head: "frg_9", expected: 1, actual: 2 }], [], undefined, "not a list", [null, 3]]) expect(publishEnd(failed(conflicts)), JSON.stringify(conflicts)).toEqual({ text: general });
    expect(publishEnd({ state: "failed", result: null, error: null } as never)).toEqual({ text: `The publication failed. ${SENT_NOTHING}` });
  });
});

describe("what a done press says", () => {
  test("it names the kind and the name, adds the pin note for a fragment's adoption only, and never shows an unsafe name", () => {
    expect(recorded("fragment", "ntp", false)).toBe("Recorded: this cluster adopts fragment ntp. A profile of this cluster that pins the name keeps its pin: set it again to use the new revision.");
    expect(recorded("profile", "edge", false)).toBe("Recorded: this cluster adopts profile edge.");
    expect(recorded("fragment", "ntp", true)).toBe("Recorded: this cluster stops following fragment ntp.");
    for (const unsafe of ["a‮b", "line\nbreak", "x".repeat(400)]) {
      expect(shownName(unsafe)).toBe("(name not shown)");
      expect(recorded("profile", unsafe, true)).toContain("(name not shown)");
      expect(recorded("fragment", unsafe, false)).not.toContain(unsafe);
    }
  });
});

describe("the block on the draft page", () => {
  test("it says what adopting records and reads nothing until it is opened", () => {
    const draft = { id: "drf_1", cluster: "cl_1", entries: [] } as never;
    const out = text(h(SharedNames, { draft, seeChange: () => {} }));
    expect(out).toContain("Shared names");
    expect(out).toContain("nothing changes for this cluster until the draft is published, and no other cluster changes");
    expect(out).toContain("Show shared names");
    expect(out).not.toContain("Shared fragments");
    // No name is listed and no control to press exists yet (the note itself speaks of adopting).
    expect(out).not.toContain("Adopt records");
    expect(out).not.toContain("Stop following records");
  });
});

describe("the refusals of shared names, each with its next step", () => {
  const problem = (over: Record<string, unknown>): Problem => ({ type: "urn:bronzeward:problem:validation-failed", title: "Validation failed", status: 422, ...over }) as Problem;
  const rules: [string, string, string][] = [
    ["library-reference", "holds no secret reference", "stays in one cluster"],
    ["library-removed", "has no head to adopt", "A cluster that adopted it keeps its revision"],
    ["name-in-two-scopes", "as its own and as a shared one", "Stop following the shared one"],
    ["library-pin-conflict", "pin different revisions of one fragment", "make them agree"],
    ["detach-pin-not-in-view", "at a revision the cluster does not hold", "Adopt that fragment at the pinned revision"],
  ];
  test.each(rules)("%s", (rule, what, next) => {
    const words = wordsFor(problem({ rule }));
    expect(words?.what).toContain(what);
    expect(words?.next.toLowerCase()).toContain(next.toLowerCase());
    // The sentence a person reads leads with what happened and ends with the step.
    expect(sentence(problem({ rule }))).toBe(`${words!.what} ${words!.next}`);
    // The words are about the rule and carry neither the code nor a command-line flag.
    expect(`${words?.what} ${words?.next}`).not.toMatch(/--[a-z]/);
  });

  test("another rule of validation-failed keeps the server's explanation", () => {
    expect(wordsFor(problem({ rule: "something-else" }))?.explains).toBe(true);
  });

  test("a stale adoption names the shared head that moved; a conflict of another scope does not", () => {
    const stale = (conflicts: unknown) => ({ type: "urn:bronzeward:problem:stale-input", title: "Stale", status: 409, conflicts }) as Problem;
    const adopted = wordsFor(stale([{ scope: "adoption", head: "frg_9", expected: 2, actual: 3 }, { scope: "draft", head: "x" }]));
    expect(adopted?.what).toBe("The shared name moved after this draft was read (frg_9 was at adoption 2 and is at 3).");
    expect(adopted?.next).toContain("adopt the name again");
    expect(wordsFor(stale([{ scope: "draft", head: "x" }]))?.what).toContain("Something this change depends on changed");
    expect(staleAdoption(stale(undefined))).toBeUndefined();
    expect(staleAdoption(stale("not a list"))).toBeUndefined();
    expect(staleAdoption(stale([null, 3, { scope: "adoption", head: "a‮b", expected: null, actual: 1 }]))?.what).toContain("(not shown) was at adoption none and is at 1");
  });

  // The members are unvalidated server values: none may replace the refusal with a rendering error.
  test("a conflict member of another shape is shown as unknown, never thrown on", () => {
    const stale = (c: unknown) => ({ type: "urn:bronzeward:problem:stale-input", title: "Stale", status: 409, conflicts: [c] }) as Problem;
    const odd = { toString: null, valueOf: null };
    const out = staleAdoption(stale({ scope: "adoption", head: odd, expected: [1], actual: { a: 1 } }));
    expect(out?.what).toBe("The shared name moved after this draft was read (unknown was at adoption unknown and is at unknown).");
    expect(() => staleAdoption(stale({ scope: "adoption", head: Object.create(null), expected: Symbol("s"), actual: 10n }))).not.toThrow();
    expect(sentence({ type: "urn:bronzeward:problem:stale-input", title: "Stale", status: 409, conflicts: [{ scope: "adoption", head: odd }] } as Problem)).toContain("unknown was at adoption unknown");
  });
});
