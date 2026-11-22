// The next step a refused mark or release carries (ginsys/bronzeward#365): the server's 409 on these routes names no next step, so the
// console adds one by the refusal's detail. Every value is synthetic.

import { describe, expect, test } from "vitest";
import type { Problem } from "./request.ts";
import { nextStep, scopeNext } from "./scope-decisions.tsx";
import { MARK_NEXT, MISSING_MEANING, RELEASE_NOT_APPROVAL } from "./words.ts";

const refused = (detail: string, over: Record<string, unknown> = {}): Problem => ({ type: "about:blank", title: "Conflict", status: 409, detail, ...over });

describe("scopeNext", () => {
  test("a refusal that carries the scope's state gets that state's own next step, whatever its text says", () => {
    for (const state of ["pre-restore-unaccounted", "blocked", "unresolved", "released"]) {
      expect(scopeNext(refused("worded some other way", { scope: state })), state).toBe(nextStep(state));
    }
  });
  test("a release of a blocked scope says to mark it", () => {
    expect(scopeNext(refused("the scope is blocked; only a ready scope is released", { scope: "blocked" }))).toMatch(/^Mark the scope, then read its recorded cause/);
  });
  test("a release of a ready scope that has no mark says to mark it, then release, and that a release is no approval", () => {
    const s = scopeNext(refused("the scope is ready; only a ready scope is released", { scope: "ready" }));
    expect(s).toMatch(/^Mark the scope, then release it\./);
    expect(s).toContain(RELEASE_NOT_APPROVAL);
  });
  test("a changed Desired release, which carries no scope, says to mark again", () => {
    expect(scopeNext(refused("the Desired release changed since the scope was marked; mark it again"))).toBe("Mark the scope again.");
    expect(scopeNext(refused("the Desired release changed while the scope was checked; nothing was marked"))).toBe("Mark the scope again.");
  });
  test("no problem, or a refusal with neither a scope nor a known text, adds nothing", () => {
    expect(scopeNext(null)).toBeUndefined();
    expect(scopeNext(refused("something else"))).toBeUndefined();
    expect(scopeNext(refused("something else", { scope: 3 }))).toBeUndefined();
  });
});

describe("the words of the answers", () => {
  test("each missing item the server can name has a meaning of a full sentence's length", () => {
    for (const k of ["observation", "identity", "access", "artifact"]) expect(MISSING_MEANING[k]!.length).toBeGreaterThan(20);
  });
  test("each state a mark can answer has a next step, and only the release sentence says it is not an approval", () => {
    for (const s of ["ready", "unresolved", "blocked"]) expect(MARK_NEXT[s]).toBeTruthy();
    expect(RELEASE_NOT_APPROVAL).toMatch(/not an approval/);
    for (const text of Object.values(MARK_NEXT)) expect(text).not.toMatch(/approv/i);
  });
});
