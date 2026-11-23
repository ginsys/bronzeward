import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { publishPath } from "./drafts.tsx";
import { type Reads, ReadsProvider, short } from "./list.tsx";
import { accepted, NO_DRAFT, NOT_OPEN, notEstablished, operationId, PublishDraft, publishEnd, publishLocked, SENT_NOTHING } from "./publish.tsx";
import type { Outcome } from "./write.ts";

type Operation = components["schemas"]["OperationBody"];
const op = (state: string, result: unknown, error: unknown = null) => ({ id: "op_BWSYNTH-pub", state, result, error }) as unknown as Operation;

const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));

describe("the publication page (ginsys/bronzeward#342, journeys §5.5)", () => {
  test("a succeeded publication names its release from the operation's result", () => {
    expect(publishEnd(op("succeeded", { release: "rel_BWSYNTH" }))).toEqual({ release: "rel_BWSYNTH" });
  });

  test("a success that names no release is said so, never shown as a release (G4)", () => {
    for (const result of [null, {}, { release: "" }, { release: 7 }]) {
      const e = publishEnd(op("succeeded", result));
      expect(e, JSON.stringify(result)).toEqual({ text: "The publication succeeded without naming its release." });
    }
  });

  test("a failed publication gives its code and says nothing was sent to any machine", () => {
    const e = publishEnd(op("failed", null, { type: "urn:bronzeward:problem:validation-failed", status: 422 }));
    expect(e).toEqual({ text: `The publication failed: validation-failed. ${SENT_NOTHING}` });
  });

  test("a failed publication says why: the rule, the input, the paths and the server's message, and names its machine for the page to show", () => {
    const error = { type: "urn:bronzeward:problem:validation-failed", status: 422, rule: "invalid", machine: "mch_BWSYNTH", input: "base", paths: ["spec.a"], message: "the field is not allowed: <redacted:value>" };
    expect(publishEnd(op("failed", null, error))).toEqual({
      text: `The publication failed: validation-failed. The rule: invalid. The input: the base. The paths: spec.a. The server says: the field is not allowed: <redacted:value>. ${SENT_NOTHING}`,
      machine: "mch_BWSYNTH",
    });
  });

  test("a refused fragment is named by its name and layer, with the position in brackets; without a name the position stays as it was", () => {
    const base = { type: "urn:bronzeward:problem:validation-failed", status: 422, rule: "kind-mismatch", input: "fragment[1]" };
    const text = (error: unknown) => (publishEnd(op("failed", null, error)) as { text: string }).text;
    expect(text({ ...base, fragment: "network", layer: "site" })).toContain("The input: the fragment network in layer site (position 1, the first is 0).");
    expect(text(base)).toContain("The input: the fragment at position 1 (the first is 0).");
    expect(text({ ...base, input: "base" })).toContain("The input: the base.");
  });

  test("a fragment name or layer that is not safe text reads (not shown), with the position kept", () => {
    const error = { type: "urn:bronzeward:problem:validation-failed", status: 422, input: "fragment[0]", fragment: "net\u001b[2Jwork", layer: "si\u001bte" };
    expect((publishEnd(op("failed", null, error)) as { text: string }).text).toContain("The input: the fragment (not shown) in layer (not shown) (position 0, the first is 0).");
  });

  test("a refusal's rule, input, paths and message are safe text, a long path list says how many are left out, and a cluster refusal names no machine", () => {
    const paths = Array.from({ length: 23 }, (_, i) => `p${i}`);
    const e = publishEnd(op("failed", null, { type: "urn:bronzeward:problem:validation-failed", rule: "fidelity\u001b[2J", input: "fragment\u001b[2J", paths, message: "no stop" }));
    expect("machine" in e).toBe(false);
    expect((e as { text: string }).text).toBe(`The publication failed: validation-failed. The rule: (not shown). The input: (not shown). The paths: ${paths.slice(0, 20).join(", ")} and 3 more. The server says: no stop. ${SENT_NOTHING}`);
  });

  test("a failure whose error carries no usable code still says it failed, and echoes no server text", () => {
    expect(publishEnd(op("failed", null, null))).toEqual({ text: `The publication failed. ${SENT_NOTHING}` });
    const e = publishEnd(op("rejected", null, { type: "urn:bronzeward:problem:\u001b[2J bad", title: "BWSYNTH\u001b[2J" }));
    expect(JSON.stringify(e)).not.toContain("\u001b");
    expect(e).toEqual({ text: `The publication rejected. ${SENT_NOTHING}` });
  });

  test("an operation id is read from the accepted answer only when it names one", () => {
    expect(operationId({ id: "op_a" })).toBe("op_a");
    for (const data of [null, "op_a", {}, { id: "" }, { id: 3 }]) expect(operationId(data), JSON.stringify(data)).toBeUndefined();
  });

  test("an accepted answer that is null or not an object still renders, naming only what it holds", () => {
    for (const data of [null, undefined, "text", 7, {}]) expect(accepted("drf_a", data)).toBe("Publishing this draft at revision ?.");
    expect(accepted("drf_a", { id: "op_x", subject: { draftRevision: 3 } })).toBe("Publishing this draft at revision 3.");
  });

  test("an operation read under another epoch is not an outcome", () => {
    expect(notEstablished()).toContain("its outcome is not established");
    expect(notEstablished()).not.toContain("succeeded");
  });

  test("the draft page links to the publication page by the draft's id", () => {
    expect(publishPath("drf_a")).toBe("/publications/new?draft=drf_a");
    expect(NOT_OPEN("published")).toBe("This draft is published, not open: it cannot be published.");
  });

  test("the page with no draft in its address sends nothing and says so; with one it starts by reading it", () => {
    expect(html(h(PublishDraft, { query: "" }))).toContain(NO_DRAFT);
    const page = html(h(PublishDraft, { query: "draft=drf_a" }));
    expect(page).toContain("Loading the draft");
    expect(page).not.toContain(">Publish<");
  });

  // ginsys/bronzeward#798 review thread: a dismissed unknown outcome left Publish locked for good.
  test("Publish is locked while a write is open or accepted, and free once an unknown outcome's notice is dismissed", () => {
    const done = (data: unknown): Outcome => ({ kind: "done", status: 202, data, replayed: false });
    const unknown: Outcome = { kind: "unknown", why: "the answer was lost", problem: null };
    expect(publishLocked(true, undefined, undefined), "in flight").toBe(true);
    expect(publishLocked(true, unknown, undefined), "unknown, notice still open").toBe(true);
    expect(publishLocked(false, unknown, undefined), "unknown, notice dismissed").toBe(false);
    expect(publishLocked(false, undefined, undefined), "nothing sent").toBe(false);
    expect(publishLocked(false, { kind: "refused", status: 412, problem: null, replayed: false }, undefined), "refused").toBe(false);
    expect(publishLocked(false, done({ id: "op_a" }), undefined), "accepted, operation not ended").toBe(true);
    expect(publishLocked(false, done({ id: "op_a" }), "op_b"), "another operation failed").toBe(true);
    expect(publishLocked(false, done({ id: "op_a" }), "op_a"), "accepted operation failed").toBe(false);
    expect(publishLocked(false, done(null), undefined), "accepted, names no operation").toBe(true);
  });
});
