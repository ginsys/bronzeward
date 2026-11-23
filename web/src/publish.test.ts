import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { publishPath } from "./drafts.tsx";
import { type Reads, ReadsProvider, short } from "./list.tsx";
import { accepted, changesRevision, DraftChanges, NO_DRAFT, NOT_OPEN, notEstablished, operationId, PublishDraft, publishEnd, publishLocked, SENT_NOTHING } from "./publish.tsx";
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
    const error = { type: "urn:bronzeward:problem:validation-failed", status: 422, rule: "kind-mismatch", machine: "mch_BWSYNTH", input: "base", paths: ["spec.a"], message: "the field is not allowed: <redacted:value>" };
    expect(publishEnd(op("failed", null, error))).toEqual({
      text: `The publication failed: validation-failed. The rule: kind-mismatch. The input: the base. The paths: spec.a. The server says: the field is not allowed: <redacted:value>. ${SENT_NOTHING}`,
      machine: "mch_BWSYNTH",
    });
  });

  // ginsys/bronzeward#1055 part 9b: the rule `invalid` is Talos's validation of the machine's whole configuration, and
  // the bare word said nothing a person could act on.
  test("the rule invalid says what it means, and the other rules keep their name", () => {
    const text = (rule: string) => (publishEnd(op("failed", null, { type: "urn:bronzeward:problem:validation-failed", status: 422, rule, message: "x" })) as { text: string }).text;
    expect(text("invalid")).toContain("The configuration of the machine as a whole, its adopted configuration plus the fragments selected for it, did not pass Talos's validation; the server's message is Talos's.");
    expect(text("invalid")).not.toContain("The rule: invalid");
    expect(text("kind-mismatch")).toContain("The rule: kind-mismatch.");
  });

  test("a run of tabs and line breaks in the server's message reads as one space, and another control character still reads U+FFFD", () => {
    const message = "1 error occurred:\n\t* .machine.nodeLabels is already set in v1alpha1 config";
    const text = (m: string) => (publishEnd(op("failed", null, { type: "urn:bronzeward:problem:validation-failed", status: 422, rule: "invalid", message: m })) as { text: string }).text;
    expect(text(message)).toContain("The server says: 1 error occurred: * .machine.nodeLabels is already set in v1alpha1 config.");
    expect(text("a\r\n\r\nb\u001bc")).toContain("The server says: a b�c.");
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

// ginsys/bronzeward#1055 part 9b: after a refused publication the page says what the draft it holds changes, since the
// refusal of the whole configuration names no fragment.
describe("what the draft changes, said after a refused publication", () => {
  const entry = (kind: string, name: string, head: string | null, revision: string | null) => ({ kind, name, head, base: head === null ? null : 1, revision });
  const draft = (revision: number, entries: unknown[]) => ({ id: "drf_a", cluster: "cl_a", title: "t", state: "open", revision, entries }) as unknown as components["schemas"]["DraftBody"];
  const said = (d: ReturnType<typeof draft> | undefined, revision: number | undefined) => html(h(DraftChanges, { d, revision })).replace(/<[^>]*>/g, "");

  test("names each changed entry by kind and name, and says the cause may be a fragment the draft does not change", () => {
    const d = draft(3, [entry("fragment", "network", "fgh_a", "frv_a"), entry("fragment", "site-a", null, "frv_b"), entry("profile", "old", "prh_a", null), { kind: "import-base", id: "imb_a" }]);
    expect(said(d, 3)).toBe(" This draft changes: fragment network (changed), fragment site-a (new), profile old (removed). The conflict may also be with a fragment this draft does not change, one selected for the machine.");
  });

  test("lists at most 10 entries and counts the rest", () => {
    const d = draft(3, Array.from({ length: 13 }, (_, i) => entry("fragment", `f${i}`, "fgh_a", "frv_a")));
    const s = said(d, 3);
    expect(s).toContain("fragment f9 (changed) and 3 more.");
    expect(s).not.toContain("f10");
  });

  test("a draft saved again since the refusal lists nothing and says so", () => {
    const d = draft(4, [entry("fragment", "network", "fgh_a", "frv_a")]);
    expect(said(d, 3)).toBe(" This draft has changed since this publication was refused, so the changes it made are not listed.");
  });

  // The refusal of the machine's whole configuration is the one that names no fragment; a deadlock, a scope that is busy or an
  // entry into recovery mode is no reason to look at the draft's fragments.
  test("only a refusal of rule invalid asks for the draft's changes, with the revision the publication was made from", () => {
    const failed = (error: unknown) => ({ ...op("failed", null, error), subject: { draft: "drf_a", draftRevision: 3 } }) as unknown as Operation;
    const code = (c: string, more: Record<string, unknown> = {}) => ({ type: `urn:bronzeward:problem:${c}`, status: 409, ...more });
    expect(changesRevision(failed(code("validation-failed", { rule: "invalid" })))).toBe(3);
    for (const c of ["scope-busy", "transient-conflict", "internal-error", "stale-input", "recovery-mode-active", "recovery-mode-entered"]) expect(changesRevision(failed(code(c))), c).toBeUndefined();
    expect(changesRevision(failed(code("validation-failed", { rule: "kind-mismatch", input: "fragment[1]" })))).toBeUndefined();
    expect(changesRevision(failed(null))).toBeUndefined();
    expect(changesRevision({ ...failed(code("validation-failed", { rule: "invalid" })), subject: {} } as unknown as Operation)).toBeUndefined();
  });

  test("a draft not read yet, an operation that names no revision and a draft with no source entry say nothing", () => {
    expect(said(undefined, 3)).toBe("");
    expect(said(draft(3, [entry("fragment", "network", "fgh_a", "frv_a")]), undefined)).toBe("");
    expect(said(draft(3, [{ kind: "import-base", id: "imb_a" }]), 3)).toBe("");
    expect(said(draft(4, [{ kind: "import-base", id: "imb_a" }]), 3), "saved again, and no source entry").toBe("");
  });

  test("a name that is not safe text reads (name not shown)", () => {
    expect(said(draft(3, [entry("fragment", "net\u001b[2Jwork", "fgh_a", "frv_a")]), 3)).toContain("fragment (name not shown) (changed)");
  });
});
