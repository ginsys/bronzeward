import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { afterApproval, ApproveControl, findRevocation, overwrites, predict, recordedReasons, RevokeControl, REVOKE_LIMIT, type Source, UNPREDICTED } from "./approval.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Load, type Reads, ReadsProvider } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

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

describe("the revocation's reason on the machine's timeline (ginsys/bronzeward#358, B22)", () => {
  const entry = (approval: string, reason: string) => ({ kind: "approval-revocation", entry: { approval, reason, principal: "prn_x" } });
  const other = { kind: "observation", entry: {} };
  // Serves the pages by cursor and records each path read.
  const timeline = (pages: Record<string, { items: unknown[]; next?: string }>) => {
    const paths: string[] = [];
    const reads: Pick<Reads, "client" | "settle" | "undropped"> = {
      client: { read: <T,>(path: string) => (paths.push(path), Promise.resolve({ kind: "ok", data: pages[new URL(path, "http://x").searchParams.get("cursor") ?? ""] } as ReadResult<T>)) },
      settle: (res, set) => (set(res as never), false),
      undropped: () => () => true,
    };
    return { reads, paths };
  };

  test("a match on the second page is found, and the reading stops there", async () => {
    const { reads, paths } = timeline({ "": { items: [other, entry("apr_other", "no")], next: "c2" }, c2: { items: [entry("apr_1", "second page"), other], next: "c3" }, c3: { items: [] } });
    expect(await findRevocation(reads, "mch_1", "apr_1", () => true)).toEqual({ kind: "ok", data: { approval: "apr_1", reason: "second page", principal: "prn_x" } });
    expect(paths).toEqual(["/machines/mch_1/timeline?order=newest", "/machines/mch_1/timeline?order=newest&cursor=c2"]);
  });

  test("a timeline without the entry is read to its end and answers no entry", async () => {
    const { reads, paths } = timeline({ "": { items: [other], next: "c2" }, c2: { items: [entry("apr_other", "no")] } });
    expect(await findRevocation(reads, "mch_1", "apr_1", () => true)).toEqual({ kind: "ok", data: undefined });
    expect(paths).toHaveLength(2);
  });
});

describe("the answers of the approval and the revocation (ginsys/bronzeward#358, T8, 2.4.3)", () => {
  const noReads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
  const page = (n: ReactNode) => renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["approver"], offline: false } }, h(ReadsProvider, { value: noReads }, n))));
  const at = (markup: string, testid: string) => [...markup.matchAll(new RegExp(`<div[^>]*data-testid="${testid}"[^>]*>`, "g"))];
  const noop = () => {};

  test("in every plan state the approval's answer is one focusable wrapper, after the form where there is one, so the reread that replaces the form leaves it mounted", () => {
    for (const state of ["proposed", "approved", "committed", "revoked", "expired", "cancelled"]) {
      const markup = page(h(ApproveControl, { p: plan({ state, approval: state === "proposed" ? null : "apr_1" }), reread: noop }));
      const found = at(markup, "approval-answer");
      expect(found.length, state).toBe(1);
      expect(found[0]![0], state).toContain('tabindex="-1"');
      // The form shows for a proposed plan only here (an approved plan's form waits for its reads), and the wrapper follows its end.
      const formEnd = markup.indexOf("</form>");
      expect(formEnd >= 0, state).toBe(state === "proposed");
      if (formEnd >= 0) expect(formEnd, state).toBeLessThan(found[0]!.index!);
    }
  });

  test("in every plan state the revocation's answer is one focusable wrapper, after the form where there is one", () => {
    for (const state of ["proposed", "approved", "committed", "revoked", "expired", "cancelled"]) {
      const markup = page(h(RevokeControl, { p: plan({ state, approval: state === "proposed" ? null : "apr_1" }), reread: noop }));
      const found = at(markup, "revoke-answer");
      expect(found.length, state).toBe(1);
      expect(found[0]![0], state).toContain('tabindex="-1"');
      const formEnd = markup.indexOf("</form>");
      expect(formEnd >= 0, state).toBe(state === "approved" || state === "committed");
      if (formEnd >= 0) expect(formEnd, state).toBeLessThan(found[0]!.index!);
    }
  });
});
