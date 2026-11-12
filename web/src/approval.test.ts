import { describe, expect, test } from "vitest";
import { afterApproval, overwrites, predict, recordedReasons, REVOKE_LIMIT, type Source, UNPREDICTED } from "./approval.tsx";
import type { Load } from "./list.tsx";

type P = Parameters<typeof predict>;
const REL = `rel_${"i".repeat(26)}`;
const plan = (more: Record<string, unknown> = {}) => ({ id: `pln_${"q".repeat(26)}`, release: REL, operation: "apply-config", driftRecord: null, createdBy: { principal: "prn_creator", role: "publisher" }, ...more }) as unknown as P[0];
const ok = <T>(data: T): Load<T> => ({ kind: "ok", data });
const me = (principal: string | null) => ok({ principal });
const release = (publisher: string, more: Record<string, unknown> = {}) => ok({ draft: `drf_${"d".repeat(26)}`, publishedBy: { principal: publisher, role: "publisher" }, ...more }) as unknown as P[2];
const draft = (...revisions: string[]) => ok({ entries: revisions.map((revision) => ({ kind: "fragment", revision })) }) as unknown as P[3];
const source = (revision: string, author: Load<{ author: string }>): Source => ({ kind: "fragment", revision, load: author });
const text = (lines: string[]) => lines.join(" ");

describe("the approval's prediction (ginsys/bronzeward#358, B21, W7)", () => {
  test("it says whether the approver created the plan and published the release, in W7's words", () => {
    expect(text(predict(plan(), me("prn_other"), release("prn_pub"), draft(), []))).toContain("Self-approval, predicted: you did not create this plan or publish this release, and you authored no fragment, profile or assignment revision in it.");
    expect(text(predict(plan(), me("prn_creator"), release("prn_creator"), draft(), []))).toContain("you created this plan and published this release");
    expect(text(predict(plan(), me("prn_creator"), release("prn_pub"), draft(), []))).toContain("you created this plan but did not publish this release");
    expect(text(predict(plan(), me("prn_pub"), release("prn_pub"), draft(), []))).toContain("you did not create this plan but published this release");
  });

  test("an authored revision is a change when the release's draft introduced it, else a reuse", () => {
    const rev = (c: string) => `frg_${c.repeat(26)}`;
    const sources = [source(rev("a"), ok({ author: "prn_me" })), source(rev("b"), ok({ author: "prn_me" })), source(rev("c"), ok({ author: "prn_x" }))];
    const out = text(predict(plan(), me("prn_me"), release("prn_pub"), draft(rev("a")), sources));
    expect(out).toContain("which this release's draft introduced");
    expect(out).toContain("which this release reuses");
    expect(out).not.toContain("frg_ccc");
  });

  test("a read in flight or failed is named and its reason is not claimed either way", () => {
    const failed: Load<{ author: string }> = { kind: "transport" };
    const out = predict(plan(), me("prn_other"), release("prn_pub"), draft(), [source(`frg_${"a".repeat(26)}`, { kind: "loading" }), source(`frg_${"b".repeat(26)}`, failed)]);
    const joined = text(out);
    expect(joined).toContain("Not predicted yet: fragment revision");
    expect(joined).toContain("Not predicted, since this read failed: fragment revision");
    expect(joined).not.toContain("you authored no fragment");
  });

  test("with no answer for the release the two cheap reasons are shown, and with no identity nothing is claimed", () => {
    const partial = predict(plan(), me("prn_other"), { kind: "loading" }, undefined, []);
    expect(partial[0]).toBe("Self-approval, predicted: you did not create this plan.");
    expect(partial[1]).toContain("Not predicted yet: release");
    expect(predict(plan(), me(null), release("prn_pub"), draft(), [])[0]).toBe("Self-approval is not predicted.");
    expect(predict(plan(), { kind: "loading" }, release("prn_pub"), draft(), [])[0]).toBe("Self-approval is not predicted.");
  });

  test("every prediction ends with what no read can predict", () => {
    expect(predict(plan(), me("prn_other"), release("prn_pub"), draft(), []).at(-1)).toBe(UNPREDICTED);
  });
});

describe("the approval's other lines", () => {
  test("only an apply-config plan bound to a drift record names an unseen overwrite", () => {
    expect(overwrites(plan({ driftRecord: 4 }))).toContain("drift record 4");
    expect(overwrites(plan())).toBeUndefined();
    expect(overwrites(plan({ operation: "adopt", driftRecord: 4 }))).toBeUndefined();
  });

  test("an adopt approval says nothing is sent; an apply-config approval says the plan is checked again", () => {
    expect(afterApproval(plan({ operation: "adopt" }))).toContain("without sending anything");
    expect(afterApproval(plan())).toContain("re-checks the plan at use time");
  });

  test("the recorded reasons are the server's codes, or none", () => {
    const approval = (reasons: string[]) => ({ selfApproval: { marked: reasons.length > 0, reasons } }) as unknown as Parameters<typeof recordedReasons>[0];
    expect(recordedReasons(approval([]))).toBe("Recorded self-approval reasons: none.");
    expect(recordedReasons(approval(["created-plan", "published"]))).toBe("Recorded self-approval reasons: created-plan, published.");
  });

  test("the revoke limit is B22's sentence", () => {
    expect(REVOKE_LIMIT).toBe("Revoking after the controller commits does not stop an attempt already recorded.");
  });
});
