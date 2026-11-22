// The next step a refused mark or release carries (ginsys/bronzeward#365): the server's 409 on these routes names no next step, so the
// console adds one by the refusal's detail. Every value is synthetic.

import { describe, expect, test } from "vitest";
import type { Problem } from "./request.ts";
import { scopeNext } from "./scope-decisions.tsx";
import { MARK_NEXT, MISSING_MEANING, RELEASE_NOT_APPROVAL } from "./words.ts";

const refused = (detail: string, over: Record<string, unknown> = {}): Problem => ({ type: "about:blank", title: "Conflict", status: 409, detail, ...over });

describe("scopeNext", () => {
  test("a release of a scope that is not ready says to mark it until it is, and that a release is no approval", () => {
    const s = scopeNext(refused("the scope is blocked; only a ready scope is released"));
    expect(s).toContain("Mark the scope until it is ready");
    expect(s).toContain(RELEASE_NOT_APPROVAL);
  });
  test("a changed Desired release says to mark again", () => {
    expect(scopeNext(refused("the Desired release changed since the scope was marked; mark it again"))).toBe("Mark the scope again.");
  });
  test("a scope that is not marked points to the page's own read of it", () => {
    expect(scopeNext(refused("the scope is ready and is not marked"))).toContain("Read the scope's state");
  });
  test("a scope still pre-restore-unaccounted says to account it first, whatever the detail", () => {
    expect(scopeNext(refused("anything", { scope: "pre-restore-unaccounted" }))).toContain("Account the scope first");
  });
  test("no problem, or a refusal with no known detail, adds nothing", () => {
    expect(scopeNext(null)).toBeUndefined();
    expect(scopeNext(refused("something else"))).toBeUndefined();
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
