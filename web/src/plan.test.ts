import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import { nextAfter, NO_TARGET, PlanNew, planBody, planPath, takeFresh } from "./plan.tsx";
import { planFor } from "./review.tsx";

const go = () => {};
const reads = { go, failure: () => null } as unknown as Reads;
type M = Parameters<typeof planFor>[1];
const machine = (desired: string | null, applied: string | null, pendingConvergence: boolean) => ({ desired, applied: applied === null ? null : { release: applied }, pendingConvergence }) as unknown as M;
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));
const problem = (detail: string, more: Record<string, unknown> = {}) => ({ type: "urn:bronzeward:problem:conflict", title: "Conflict", status: 409, detail, ...more });

describe("plan creation (ginsys/bronzeward#357, B19, T19)", () => {
  test("a blank value is left out, so the configured default applies; an apply-config plan names its one mode", () => {
    expect(planBody("rel_a", "mch_b", "apply-config", { expiresInSeconds: "", maxAttempts: "  " })).toEqual({ body: { releaseId: "rel_a", machine: "mch_b", operation: "apply-config", mode: "no-reboot" } });
  });

  test("typed values are sent as whole numbers within their bounds", () => {
    const made = planBody("rel_a", "mch_b", "apply-config", { expiresInSeconds: "3600", transportDeadlineSeconds: "60", verificationDeadlineSeconds: "600", maxAttempts: "3" });
    expect(made).toEqual({ body: { releaseId: "rel_a", machine: "mch_b", operation: "apply-config", mode: "no-reboot", expiresInSeconds: 3600, transportDeadlineSeconds: 60, verificationDeadlineSeconds: 600, maxAttempts: 3 } });
    for (const bad of ["0", "-1", "1.5", "1e3", "abc", "604801"]) expect(planBody("rel_a", "mch_b", "apply-config", { expiresInSeconds: bad }), bad).toHaveProperty("error");
    expect(planBody("rel_a", "mch_b", "apply-config", { maxAttempts: "11" })).toHaveProperty("error");
    expect(planBody("rel_a", "mch_b", "apply-config", { transportDeadlineSeconds: "600", verificationDeadlineSeconds: "60" })).toHaveProperty("error");
  });

  test("an adopt plan carries no mode, deadline or attempt limit, even when typed before the operation was switched", () => {
    const made = planBody("rel_a", "mch_b", "adopt", { expiresInSeconds: "60", maxObservationAgeSeconds: "30", transportDeadlineSeconds: "5", verificationDeadlineSeconds: "9", maxAttempts: "2" });
    expect(made).toEqual({ body: { releaseId: "rel_a", machine: "mch_b", operation: "adopt", expiresInSeconds: 60, maxObservationAgeSeconds: 30 } });
  });

  test("the page needs a release and a machine, and the path round-trips one identifier of each", () => {
    expect(html(h(PlanNew, { query: "" }))).toContain(NO_TARGET);
    expect(html(h(PlanNew, { query: "release=rel_a" }))).toContain(NO_TARGET);
    const q = new URLSearchParams(planPath("rel_a", "mch_b&c", "adopt").split("?")[1]);
    expect([q.get("release"), q.get("machine"), q.get("operation")]).toEqual(["rel_a", "mch_b&c", "adopt"]);
  });

  test("each refusal names its next step and acts on none; an unknown refusal names none", () => {
    const desired = html(nextAfter(problem("an apply-config plan is for the machine's Desired release", { desired: "rel_other" }), "rel_a", "mch_b"));
    expect(desired).toContain("Plan the machine&#x27;s Desired release");
    expect(desired).toContain("A published draft cannot be published again");
    expect(desired).toContain(">rel_othe");
    const none = html(nextAfter(problem("the machine has no Applied configuration to plan an apply-config from; adopt it first"), "rel_a", "mch_b"));
    expect(none).toContain("create an adopt plan");
    expect(none).toContain("operation=adopt");
    expect(html(nextAfter(problem("the machine's assignment has moved since the release was compiled"), "rel_a", "mch_b"))).toContain("publish it, so a release is compiled");
    expect(html(nextAfter(problem("the machine has an Applied configuration and no open drift record; adopt only a drifted machine or one with none"), "rel_a", "mch_b"))).toContain("nothing to adopt");
    expect(nextAfter(problem("something else"), "rel_a", "mch_b")).toBeNull();
    expect(nextAfter(null, "rel_a", "mch_b")).toBeNull();
  });

  test("the proposed line is taken once per plan this browser created", () => {
    expect(takeFresh("pln_x")).toBe(false);
  });

  test("the review page offers a handover to a machine with no Applied and an apply plan only while the configuration differs", () => {
    const rel = "rel_a";
    expect(planFor(rel, machine(rel, null, true))).toBe("adopt");
    expect(planFor(rel, machine(rel, "rel_old", true))).toBe("apply-config");
    expect(planFor(rel, machine(rel, rel, true))).toBe("apply-config");
    expect(planFor(rel, machine(rel, "rel_old", false))).toBeUndefined();
    expect(planFor(rel, machine(rel, rel, false))).toBeUndefined();
    expect(planFor(rel, machine("rel_other", null, true))).toBeUndefined();
  });
});
