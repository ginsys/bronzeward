import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { type Reads, ReadsProvider } from "./list.tsx";
import { sentence, staleAdoption, wordsFor } from "./problems.tsx";
import type { Problem } from "./request.ts";
import { adoptionWrite, canAdopt, canStop, SharedNames, slotOf, stateOf } from "./shared-names.tsx";

type S = components["schemas"];
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const text = (e: Parameters<typeof renderToStaticMarkup>[0]) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e)).replace(/<[^>]*>/g, "");

const adoption = (over: Partial<S["AdoptionEntry"]> = {}) => ({ kind: "adoption", of: "fragment", name: "ntp", head: "frg_2", base: null, revision: "frv_a", ...over }) as never;
const shared = (over: Record<string, unknown> = {}) => ({ kind: "fragment", name: "ntp", scope: "library", head: "frg_2", base: 1, adoptionBase: null, revision: "frv_s", ...over }) as never;
const own = { kind: "fragment", name: "ntp", head: "frg_1", base: 1, revision: "frv_own" } as never;
const row = (over: Partial<S["ClusterAdoptionBody"]> = {}) => ({ cluster: "cl_1", revision: "frv_old", adoptionRevision: 2, behind: false, release: null, via: null, machines: [], ...over }) as S["ClusterAdoptionBody"];

describe("what the draft says of a shared name", () => {
  test("an adoption, its removal, a shared edit and a shared removal; the cluster's own entry and other names say nothing", () => {
    expect(slotOf([adoption()], "fragment", "ntp")).toEqual({ kind: "adopts", revision: "frv_a" });
    expect(slotOf([adoption({ revision: null })], "fragment", "ntp")).toEqual({ kind: "stops" });
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
    expect(stateOf({ kind: "adopts", revision: head }, [row({ behind: true })], "cl_1", head)).toEqual({ kind: "adopts", revision: head, behind: false });
    expect(stateOf({ kind: "adopts", revision: "frv_old" }, [], "cl_1", head)).toEqual({ kind: "adopts", revision: "frv_old", behind: true });
    for (const kind of ["stops", "edits", "removed"] as const) expect(stateOf({ kind }, [row()], "cl_1", head)).toEqual({ kind });
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
      { kind: "none" }, { kind: "stops" }, { kind: "via", via: "e" }, { kind: "edits" }, { kind: "removed" },
      { kind: "adopts", revision: "r", behind: false }, { kind: "adopts", revision: "r", behind: true },
      { kind: "follows", revision: "r", behind: false }, { kind: "follows", revision: "r", behind: true },
    ] as const;
    expect(states.map(canAdopt)).toEqual([true, true, true, false, false, false, true, false, true]);
    expect(states.map(canStop)).toEqual([false, false, false, false, false, true, true, true, true]);
  });
});

describe("the request of a press", () => {
  test("Adopt puts the head's revision, Stop following deletes, and the name is escaped", () => {
    expect(adoptionWrite("drf_1", "fragment", "ntp-servers", "frv_h")).toEqual({ method: "PUT", path: "/drafts/drf_1/adoptions/fragments/ntp-servers", body: { revision: "frv_h" } });
    expect(adoptionWrite("drf_1", "profile", "a/b c", null)).toEqual({ method: "DELETE", path: "/drafts/drf_1/adoptions/profiles/a%2Fb%20c", body: undefined });
  });
});

describe("the block on the draft page", () => {
  test("it says what adopting records and reads nothing until it is opened", () => {
    const draft = { id: "drf_1", cluster: "cl_1", entries: [] } as never;
    const out = text(h(SharedNames, { draft, written: () => {}, seeChange: () => {} }));
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
    expect(staleAdoption(stale([null, 3, { scope: "adoption", head: "a‮b", expected: null, actual: 1 }]))?.what).toContain("(not shown) was at adoption null and is at 1");
  });
});
