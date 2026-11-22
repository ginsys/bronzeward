// The accounting form of the recovery decisions (ginsys/bronzeward#365): the observations it offers as a start and an end and why one
// is off, when an explanation is needed, and the next step of each refusal the server names. Every value is synthetic.

import { describe, expect, test } from "vitest";
import { accountRisk, ATTEMPT_WHAT, endWhy, explanationProblem, EXPLANATION_MAX, moved, SCOPE_WHAT, startWhy } from "./accounting.tsx";
import type { components } from "./api/v1";
import { SETTLE_CAUSES, settleNext } from "./settle-causes.ts";

type Observation = components["schemas"]["ObservationBody"];
const obs = (id: string, over: Partial<Observation> = {}): Observation =>
  ({ id, machine: "mch_x", basis: 1, purpose: "recovery", startedAt: "2026-11-22T10:00:10Z", at: "2026-11-22T10:00:20Z", resourceVersion: "rv1", ...over }) as Observation;

describe("the start and the end", () => {
  test("an observation that read no resource version is unusable at either end", () => {
    const none = obs("obs_n", { resourceVersion: null });
    expect(startWhy(none)).toMatch(/no machine-configuration resource version/);
    expect(endWhy(none, undefined)).toMatch(/no machine-configuration resource version/);
    expect(startWhy(obs("obs_a"))).toBeUndefined();
  });
  test("the end must be a recovery observation, another one than the start, that began after the start was recorded", () => {
    const start = obs("obs_s", { at: "2026-11-22T10:00:20Z" });
    expect(endWhy(obs("obs_e", { purpose: "completion" }), start)).toBe("its purpose is completion, not recovery");
    expect(endWhy(start, start)).toBe("it is the start");
    expect(endWhy(obs("obs_e", { startedAt: "2026-11-22T10:00:20Z" }), start)).toBe("it began before the start observation was recorded");
    expect(endWhy(obs("obs_e", { startedAt: "2026-11-22T10:01:00Z" }), start)).toBeUndefined();
  });
  test("a purpose with control characters is shown as safe text, never as it came", () => {
    expect(endWhy(obs("obs_e", { purpose: "a\nb" }), undefined)).toBe("its purpose is (not shown), not recovery");
  });
  test("no start chosen yet leaves the end's reasons that need none", () => {
    expect(endWhy(obs("obs_e"), undefined)).toBeUndefined();
  });
});

describe("the explanation", () => {
  test("is needed only where the resource version moved between the two observations", () => {
    expect(moved(obs("a", { resourceVersion: "rv1" }), obs("b", { resourceVersion: "rv2" }))).toBe(true);
    expect(moved(obs("a"), obs("b"))).toBe(false);
    expect(moved(undefined, obs("b"))).toBe(false);
    expect(moved(obs("a", { resourceVersion: null }), obs("b"))).toBe(false);
  });
  test("a moved version with a blank explanation, and an explanation over the limit, are stopped before any send", () => {
    expect(explanationProblem("  ", true)).toMatch(/say why/);
    expect(explanationProblem("", false)).toBeUndefined();
    expect(explanationProblem("firmware update", true)).toBeUndefined();
    expect(explanationProblem("x".repeat(EXPLANATION_MAX + 1), false)).toMatch(/at most 1024 bytes/);
    expect(explanationProblem("é".repeat(EXPLANATION_MAX / 2 + 1), false)).toMatch(/at most 1024 bytes/);
  });
});

describe("the judgment", () => {
  test("says what it records and what it does not show, for both decisions", () => {
    for (const what of [SCOPE_WHAT, ATTEMPT_WHAT]) {
      const text = accountRisk(what);
      expect(text).toContain(`your judgment that ${what} can still land`);
      expect(text).toContain("nothing here shows whether it did");
      expect(text).toContain("a minimum wait, not a proven bound");
    }
  });
});

describe("the next step of a refusal", () => {
  test("each of the nine causes the server names has its own step, found by the text of the detail", () => {
    expect(SETTLE_CAUSES).toHaveLength(9);
    expect(new Set(SETTLE_CAUSES.map((c) => c.next)).size).toBe(9);
    for (const c of SETTLE_CAUSES) expect(settleNext(`conflict: the end observation ${c.cause} of 5s`)).toBe(c.next);
  });
  test("a detail that names no known cause, and a detail that is not text, have no step", () => {
    expect(settleNext("something else")).toBeUndefined();
    expect(settleNext(undefined)).toBeUndefined();
    expect(settleNext(42)).toBeUndefined();
  });
  test("a cause is found in the raw detail, including one with control characters around it", () => {
    expect(settleNext("x\n the attempt is already accounted for ‮")).toBe(SETTLE_CAUSES.find((c) => c.cause.includes("already accounted"))!.next);
  });
});
