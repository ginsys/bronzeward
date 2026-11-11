import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { publishPath } from "./drafts.tsx";
import { ReadsProvider, type Reads } from "./list.tsx";
import { NO_DRAFT, NOT_OPEN, notEstablished, operationId, PublishDraft, publishEnd, SENT_NOTHING } from "./publish.tsx";

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
      expect(e, JSON.stringify(result)).toEqual({ text: "Publication operation op_BWSYNTH-pub succeeded without naming its release." });
    }
  });

  test("a failed publication gives its code and says nothing was sent to any machine", () => {
    const e = publishEnd(op("failed", null, { type: "urn:bronzeward:problem:validation-failed", status: 422 }));
    expect(e).toEqual({ text: `Publication operation op_BWSYNTH-pub failed: validation-failed. ${SENT_NOTHING}` });
  });

  test("a failure whose error carries no usable code still says it failed, and echoes no server text", () => {
    expect(publishEnd(op("failed", null, null))).toEqual({ text: `Publication operation op_BWSYNTH-pub failed. ${SENT_NOTHING}` });
    const e = publishEnd(op("rejected", null, { type: "urn:bronzeward:problem:\u001b[2J bad", title: "BWSYNTH\u001b[2J" }));
    expect(JSON.stringify(e)).not.toContain("\u001b");
    expect(e).toEqual({ text: `Publication operation op_BWSYNTH-pub rejected. ${SENT_NOTHING}` });
  });

  test("an operation id is read from the accepted answer only when it names one", () => {
    expect(operationId({ id: "op_a" })).toBe("op_a");
    for (const data of [null, "op_a", {}, { id: "" }, { id: 3 }]) expect(operationId(data), JSON.stringify(data)).toBeUndefined();
  });

  test("an operation read under another epoch is not an outcome", () => {
    expect(notEstablished("op_BWSYNTH-pub")).toContain("its outcome is not established");
    expect(notEstablished("op_BWSYNTH-pub")).not.toContain("succeeded");
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
});
