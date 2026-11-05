import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { forgetETag, keepETag, keptETag } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { againPath, allowed, documentRefusal, ended, heldInput, type Ingestion, MAX_DOCUMENT, StartImport, startBody, startParams, started, unsent } from "./imports.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { eventLine } from "./progress.ts";
import type { ReadResult } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

type Operation = components["schemas"]["OperationBody"];

const NOW = Date.parse("2026-11-01T07:00:00Z");
const ing = (o: Partial<Ingestion> = {}): Ingestion => ({
  id: "ing_6fdq2la", kind: "import", mode: "encrypted", state: "held", machine: "mch_b3caa2q", draft: "drf_2rmp457a", operation: "op_zcfw5mcm",
  review: "pending", ownerGeneration: 1, leaseUntil: "2026-11-01T07:05:00Z", expiresAt: "2026-11-01T08:00:00Z", createdAt: "2026-11-01T06:55:00Z", ...o,
});
const op = (o: Partial<Operation>): Operation => ({
  id: "op_zcfw5mcm", kind: "ingest", state: "running", epoch: "e1", lastEvent: 3, subject: null, createdBy: null, createdAt: "2026-11-01T06:55:00Z",
  result: null, error: null, ...o,
} as Operation);
const noReads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const text = (n: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: noReads }, n)).replace(/<[^>]+>/g, "").replaceAll("&#x27;", "'");
const reading = (res: ReadResult<unknown>) => {
  const paths: string[] = [];
  return { paths, client: { read: async <T,>(p: string) => (paths.push(p), res as ReadResult<T>) } };
};
const page = (query: string) =>
  renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } },
    h(ReadsProvider, { value: noReads }, h(StartImport, { query })))));

afterEach(() => {
  started.clear();
  forgetETag("drf_2rmp457a");
});

describe("what an import's state allows (B15, as the CLI's import status)", () => {
  test("a paused claim can be continued or abandoned", () => {
    expect(text(allowed(ing({ state: "paused" }), NOW))).toBe("Paused for review. Continue it, or abandon it.");
  });
  test("a held claim within its lease can be abandoned, and taken over once its lease lapsed", () => {
    expect(text(allowed(ing(), NOW))).toBe("Its run holds the claim. Once the lease lapses it can be taken over. It can be abandoned now.");
    expect(text(allowed(ing({ state: "resumed" }), Date.parse("2026-11-01T07:06:00Z")))).toBe("Its run stopped renewing the lease. Take it over to resume, or abandon it.");
  });
  test("a released claim has nothing left; an abandoned one offers Import again against its machine and draft", () => {
    expect(text(allowed(ing({ state: "released" }), NOW))).toBe("Released into draft drf_2rmp457a. Nothing is left to do.");
    expect(text(allowed(ing({ state: "abandoned" }), NOW))).toBe("Ended: abandoned. Import again");
    expect(againPath(ing())).toBe("/ingestions/new?machine=mch_b3caa2q&draft=drf_2rmp457a");
  });
});

test("an event line names its type, generation and code, and nothing else of its data", () => {
  expect(eventLine({ id: "e1:2", type: "paused", data: { generation: 1 } })).toBe("paused (generation 1)");
  expect(eventLine({ id: "e1:3", type: "resume-failed", data: { code: "dependency-unavailable", extra: "x" } })).toBe("resume-failed: dependency-unavailable");
});

describe("ETAG-02: after a released import the draft's ETag is adopted only at the import's draft revision", () => {
  const result = { draft: "drf_2rmp457a", draftRevision: 2, importBaseRevision: "ibr_1" };
  test("an import this page started, with the draft at its revision, adopts the re-read ETag", async () => {
    started.set("ing_6fdq2la", "drf_2rmp457a");
    keepETag("drf_2rmp457a", '"1-a"', "e1");
    const r = reading({ kind: "ok", data: { revision: 2 }, etag: '"2-b"' });
    const out = text(await ended(r.client, ing(), op({ state: "succeeded", result }), "succeeded", NOW, "e1"));
    expect(out).toBe("Import ing_6fdq2la released into draft drf_2rmp457a (revision 2), import base ibr_1.");
    expect(r.paths).toEqual(["/drafts/drf_2rmp457a"]);
    expect(keptETag("drf_2rmp457a", "e1")).toBe('"2-b"');
  });
  test("a higher revision is treated as a 412: the ETag is not adopted and the change is to be reviewed", async () => {
    started.set("ing_6fdq2la", "drf_2rmp457a");
    keepETag("drf_2rmp457a", '"1-a"', "e1");
    const r = reading({ kind: "ok", data: { revision: 3 }, etag: '"3-c"' });
    const out = text(await ended(r.client, ing(), op({ state: "succeeded", result }), "succeeded", NOW, "e1"));
    expect(out).toContain("Draft drf_2rmp457a is now revision 3: another write followed this import's revision 2. Its ETag is not kept. Review the change before writing again.");
    expect(keptETag("drf_2rmp457a", "e1")).toBe('"1-a"');
  });
  test("an import another page started reads no draft and keeps no ETag", async () => {
    const r = reading({ kind: "ok", data: { revision: 2 }, etag: '"2-b"' });
    await ended(r.client, ing(), op({ state: "succeeded", result }), "succeeded", NOW, "e1");
    expect(r.paths).toEqual([]);
    expect(keptETag("drf_2rmp457a", "e1")).toBeUndefined();
  });
});

describe("a watch's end", () => {
  test("a continuation the draft overtook says the import was not applied and offers Import again", async () => {
    const out = text(await ended(reading({ kind: "transport" }).client, ing(), op({ state: "failed", error: { type: "urn:bronzeward:problem:precondition-failed", title: "precondition failed", status: 412, instance: "req_1" } }), "failed", NOW, "e1"));
    expect(out).toBe("Import ing_6fdq2la (operation op_zcfw5mcm) failed: precondition-failed. The draft changed after this import started, so the import was not applied and its claim is abandoned. Import again");
  });
  test("a pause, a resume-failed and a lapsed lease end the watch on a running operation, the last of unknown outcome", async () => {
    const c = reading({ kind: "transport" }).client;
    expect(text(await ended(c, ing({ state: "paused" }), op({}), "paused", NOW, "e1"))).toMatch(/^Paused for review \(expires /);
    expect(text(await ended(c, ing(), op({}), "resume-failed", NOW, "e1"))).toMatch(/was not applied: its staged input could not be decrypted/);
    expect(text(await ended(c, ing(), op({}), "staged", Date.parse("2026-11-01T07:06:00Z"), "e1"))).toMatch(/the outcome is unknown/);
  });
});

describe("the start route's kind and source parameters (RUL-0654-363, test 30)", () => {
  test("none given: an import from the machine, nothing refused", () => {
    expect(startParams(new URLSearchParams("machine=mch_b3caa2q"))).toEqual({ kind: "import", source: "machine" });
  });
  test("a drift adoption with the machine as its source is accepted", () => {
    expect(startParams(new URLSearchParams("kind=drift-adoption&source=machine"))).toEqual({ kind: "drift-adoption", source: "machine" });
    expect(startParams(new URLSearchParams("kind=drift-adoption"))).toEqual({ kind: "drift-adoption", source: "machine" });
  });
  test("an unknown kind or source, and a drift adoption from a document, are refused with a reason", () => {
    expect(startParams(new URLSearchParams("kind=restore")).refusal).toBe('Not sent: the kind "restore" is neither import nor drift-adoption.');
    expect(startParams(new URLSearchParams("source=file")).refusal).toBe('Not sent: the source "file" is neither machine nor document.');
    expect(startParams(new URLSearchParams("kind=drift-adoption&source=document")).refusal).toBe("Not sent: a drift adoption reads the machine's own configuration, so its source is machine, not a document.");
  });
  test("the page shows a refusal and a drift adoption's title, and sends nothing on load", () => {
    expect(page("kind=restore")).toContain("Not sent: the kind &quot;restore&quot; is neither import nor drift-adoption.");
    const out = page("machine=mch_b3caa2q&kind=drift-adoption");
    expect(out).toContain("<h2>Start drift adoption</h2>");
    expect(out.match(/<input[^>]*name="source"[^>]*value="document"[^>]*>/)?.[0]).toContain('disabled=""');
  });
});

describe("the start's body and its protected input (G5, §11.2)", () => {
  const base = { kind: "import", machine: "mch_b3caa2q", draft: "drf_2rmp457a", source: "machine", staging: "encrypted", review: true, doc: "", marks: "" } as const;
  test("a review is asked for only with encrypted staging", () => {
    expect(startBody(base)).toEqual({ kind: "import", machine: "mch_b3caa2q", draft: "drf_2rmp457a", source: "machine", staging: "encrypted", review: true });
    expect(startBody({ ...base, staging: "transient" })["review"]).toBe(false);
    expect(startBody({ ...base, review: false })["review"]).toBe(false);
  });
  test("marks are trimmed and blank lines dropped; a document is sent only for a document source", () => {
    expect(startBody({ ...base, marks: " doc[0]/a \n\n doc[0]/b\n", doc: "x: 1" })["marks"]).toEqual(["doc[0]/a", "doc[0]/b"]);
    expect(startBody({ ...base, doc: "x: 1" })).not.toHaveProperty("document");
    expect(startBody({ ...base, source: "document", doc: "x: 1" })["document"]).toBe("x: 1");
    expect(startBody(base)).not.toHaveProperty("marks");
  });
  test("a document source without text, or over the server's limit, is refused before any request", () => {
    expect(documentRefusal("document", "")).toBe("Not sent: paste the document or choose its file.");
    expect(documentRefusal("document", "a".repeat(MAX_DOCUMENT + 1))).toBe("Not sent: the document is over the server's 512 KiB limit.");
    expect(documentRefusal("document", "a".repeat(MAX_DOCUMENT))).toBeUndefined();
    expect(documentRefusal("machine", "")).toBeUndefined();
  });
  test("any outcome clears the held document and marks; unsent input asks before leaving", () => {
    const input = { doc: "secret: x", marks: "doc[0]/a" };
    expect(heldInput(input, undefined)).toBe(input);
    expect(heldInput(input, { kind: "refused" } as never)).toEqual({ doc: "", marks: "" });
    expect(unsent(input, undefined)).toBe(true);
    expect(unsent(input, { kind: "done" } as never)).toBe(false);
    expect(unsent({ doc: "", marks: "" }, undefined)).toBe(false);
  });
});

test("§11.4 item 2: the marks field gets no browser spelling, fill or correction; the document field shows only for a document source", () => {
  const out = page("machine=mch_b3caa2q");
  const areas = out.match(/<textarea[^>]*>/g) ?? [];
  expect(areas).toHaveLength(1);
  for (const a of ['autoComplete="off"', 'spellcheck="false"', 'autoCorrect="off"', 'autoCapitalize="off"']) expect(areas[0]!.toLowerCase()).toContain(a.toLowerCase());
  // Encrypted staging with review is the default (B15, G5).
  expect(out).toContain('name="staging" checked="" value="encrypted"');
  expect(out.match(/<input[^>]*name="review"[^>]*>/)?.[0]).toContain('checked=""');
});
