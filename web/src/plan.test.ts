import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test, vi } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import { CANCEL_NOTE, nextAfter, NO_TARGET, PlanNew, PlanState, planBody, planPath, poll, reasonProblem, REASON_MAX, staleReasons, STALE_AFTER_RECOVERY, takeFresh } from "./plan.tsx";
import { Change } from "./resources.tsx";
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

const plan = (state: string, more: Record<string, unknown> = {}) =>
  ({ id: "pln_a", state, release: "rel_a", machine: "mch_a", operation: "apply-config", desiredRelease: "rel_a", driftRecord: null, expiresAt: "2026-11-12T10:00:00Z", approval: null, committedOperation: null, evidence: {}, ...more }) as never;

describe("the plan page's state lines (ginsys/bronzeward#357, B20, B22)", () => {
  test("an expired, cancelled or revoked plan shows its state and links to a new plan, sending nothing", () => {
    for (const state of ["expired", "cancelled", "revoked"]) {
      const out = html(h(PlanState, { p: plan(state), reread: () => {} }));
      expect(out).toContain(`<span class="badge">${state}</span>`);
      expect(out).toContain(`href="${planPath("rel_a", "mch_a", "apply-config").replaceAll("&", "&amp;")}"`);
      expect(out).toContain(">Create a new plan</a>");
      expect(out).not.toContain("<form");
    }
  });

  test("a proposed plan shows no state line", () => {
    expect(html(h(PlanState, { p: plan("proposed"), reread: () => {} }))).toBe("");
  });

  test("a committed plan links to nothing the page invented: it names its operation", () => {
    expect(html(h(PlanState, { p: plan("committed", { committedOperation: "op_b" }), reread: () => {} }))).toContain("operation");
  });

  test("an approved plan is predicted stale when the machine's Desired release or drift record moved", () => {
    const p = { desiredRelease: "rel_a", driftRecord: 7 };
    expect(staleReasons(p, { desired: "rel_a", openDrift: { revision: 7 } } as never)).toEqual([]);
    expect(staleReasons(p, { desired: "rel_b", openDrift: { revision: 7 } } as never)).toHaveLength(1);
    expect(staleReasons(p, { desired: "rel_a", openDrift: null } as never)).toHaveLength(1);
    expect(staleReasons({ desiredRelease: "rel_a", driftRecord: null }, { desired: "rel_a", openDrift: null } as never)).toEqual([]);
    expect(staleReasons(p, { desired: "rel_b", openDrift: null } as never)).toHaveLength(2);
  });

  test("the earlier-epoch line says the approval authorizes nothing and an approver may approve again", () => {
    expect(STALE_AFTER_RECOVERY).toBe("This approval is from before the last recovery entry and authorizes nothing. An approver may approve again.");
  });

  test("a cancellation reason is 1 to 1024 bytes, counted in bytes", () => {
    expect(reasonProblem("")).toBeDefined();
    expect(reasonProblem("   ")).toBeDefined();
    expect(reasonProblem("x")).toBeUndefined();
    expect(reasonProblem("x".repeat(REASON_MAX))).toBeUndefined();
    expect(reasonProblem("x".repeat(REASON_MAX + 1))).toBeDefined();
    expect(reasonProblem("é".repeat(REASON_MAX / 2 + 1))).toBeDefined();
  });

  test("the cancel control states that a cancellation after commitment ends the operation only while no attempt is recorded", () => {
    expect(CANCEL_NOTE).toBe("Cancelling after the controller commits ends the operation only while no attempt is recorded.");
  });

  test("a plan read slower than the repeat is not followed by another read before it settles (ginsys/bronzeward#820)", () => {
    vi.useFakeTimers();
    try {
      const reread = vi.fn();
      const timer = poll(reread);
      timer.settled();
      vi.advanceTimersByTime(5000);
      expect(reread).toHaveBeenCalledTimes(1);
      vi.advanceTimersByTime(600000);
      expect(reread).toHaveBeenCalledTimes(1);
      timer.stop();
    } finally {
      vi.useRealTimers();
    }
  });

  test("the poll reads again 5 seconds after each settled read, once, so a read without answer is tried again", () => {
    vi.useFakeTimers();
    try {
      const reread = vi.fn();
      const timer = poll(reread);
      vi.advanceTimersByTime(60000);
      expect(reread).toHaveBeenCalledTimes(0);
      timer.settled();
      vi.advanceTimersByTime(4999);
      expect(reread).toHaveBeenCalledTimes(0);
      vi.advanceTimersByTime(1);
      expect(reread).toHaveBeenCalledTimes(1);
      timer.settled();
      vi.advanceTimersByTime(5000);
      expect(reread).toHaveBeenCalledTimes(2);
      timer.stop();
    } finally {
      vi.useRealTimers();
    }
  });

  test("a second settled read restarts the one timer, and stop clears it", () => {
    vi.useFakeTimers();
    try {
      const reread = vi.fn();
      const timer = poll(reread);
      timer.settled();
      vi.advanceTimersByTime(3000);
      timer.settled();
      vi.advanceTimersByTime(4999);
      expect(reread).toHaveBeenCalledTimes(0);
      vi.advanceTimersByTime(1);
      expect(reread).toHaveBeenCalledTimes(1);
      timer.settled();
      timer.stop();
      vi.advanceTimersByTime(60000);
      expect(reread).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("the change of a plan (ginsys/bronzeward#357, B20)", () => {
  const diff = (d: Record<string, unknown> | null, operation = "apply-config") => html(h(Change, { p: { operation, evidence: { diff: d } } as never }));
  test("a change shows in a read-only block that scrolls in its own region", () => {
    const out = diff({ from: "rel_a", to: "rel_b", withheld: false, unified: "-a\n+b\n" });
    expect(out).toContain("Change shown below");
    expect(out).toContain('<pre class="diff" data-testid="diff" tabindex="0" aria-label="Change">');
  });
  test("an empty change says there are no differences, never that the machine is unchanged", () => {
    const out = diff({ from: "rel_a", to: "rel_b", withheld: false, unified: "" });
    expect(out).toContain("No differences in the redacted configurations");
    expect(out).not.toContain("<pre");
  });
  test("a withheld change says it cannot be shown", () => {
    expect(diff({ from: "rel_a", to: "rel_b", withheld: true, unified: "" })).toContain("The change cannot be shown.");
  });
  test("an adopt plan sends nothing and records the current configuration as applied", () => {
    expect(diff(null, "adopt")).toContain("Sends nothing. Records the machine&#x27;s current configuration as applied.");
  });
});
