import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { components } from "./api/v1";
import { forgetETag, keepETag, keptETag } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { ABANDONED_SETTLE, abandonedSince, againPath, allowed, caughtUp, documentRefusal, ended, fileReader, followRun, forgetRefused, type Ingestion, keepTyped, lapsedRead, MAX_DOCUMENT, onEvent, REFRESH_FAILED, type ReadStamp, refreshDelay, refreshRetry, refreshWait, REVIEW_FIRST, reviewFirst, sendStart, StartImport, type StartInput, startBody, startETag, startParams, settledReads, stampRead, startRun, started, stopsWatch, typedDocument, unsent, watchDeps, watchView } from "./imports.tsx";
import { type Load, type Reads, ReadsProvider, short } from "./list.tsx";
import { eventLine, watch } from "./progress.ts";
import { Client, type ReadResult } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { EPOCH_CHANGED_ASIDE, Write, Writes, type WriteSpec } from "./write.ts";

type Operation = components["schemas"]["OperationBody"];
type Draft = components["schemas"]["DraftBody"];

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
// A client whose reads answer `res` under `epoch`. `read` stays for the red run of the check, which
// puts imports.tsx back to the source that read the draft with it.
const reading = (res: ReadResult<unknown>, epoch: string | undefined = "e1") => {
  const paths: string[] = [];
  return {
    paths,
    client: {
      read: async <T,>(p: string) => (paths.push(p), res as ReadResult<T>),
      readWithEpoch: async <T,>(p: string) => (paths.push(p), { result: res as ReadResult<T>, epoch }),
    },
  };
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
  test("transient staging is never offered a takeover, held or lapsed (the CLI's import status)", () => {
    const transient = (o: Partial<Ingestion> = {}) => ing({ mode: "transient", ...o });
    const lateNow = Date.parse("2026-11-01T07:06:00Z");
    for (const shown of [text(allowed(transient(), NOW)), text(allowed(transient({ state: "resumed" }), lateNow))]) {
      expect(shown).toContain("Transient staging cannot be taken over");
      expect(shown).toContain("abandon");
      expect(shown).not.toMatch(/Take it over|can be taken over/);
    }
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

// An import started by this page: sendStart's write settles as done under the epoch it was sent under.
const begun = (ingestion: string, draft: string, epoch: string) => {
  const got: { spec?: WriteSpec } = {};
  const i: StartInput = { kind: "import", machine: "mch_b3caa2q", draft, source: "document", staging: "encrypted", review: true, doc: "x: 1", marks: "" };
  sendStart((s) => { got.spec = s; }, () => {}, i, '"1-a"');
  got.spec!.settled!({ kind: "done", data: { ingestion } } as never, { epoch } as never);
};

describe("ETAG-02: after a released import the draft's ETag is adopted only at the import's draft revision", () => {
  const result = { draft: "drf_2rmp457a", draftRevision: 2, importBaseRevision: "ibr_1" };
  test("an import this page started, with the draft at its revision, adopts the re-read ETag", async () => {
    begun("ing_6fdq2la", "drf_2rmp457a", "e1");
    keepETag("drf_2rmp457a", '"1-a"', "e1");
    const r = reading({ kind: "ok", data: { revision: 2 }, etag: '"2-b"' });
    const out = text(await ended(r.client, ing(), op({ state: "succeeded", result }), "succeeded", NOW, "e1"));
    expect(out).toBe("Import ing_6fdq2la released into draft drf_2rmp457a (revision 2), import base ibr_1.");
    expect(r.paths).toEqual(["/drafts/drf_2rmp457a"]);
    expect(keptETag("drf_2rmp457a", "e1")).toBe('"2-b"');
  });
  test("a higher revision is treated as a 412: the ETag is not adopted and the change is to be reviewed", async () => {
    begun("ing_6fdq2la", "drf_2rmp457a", "e1");
    keepETag("drf_2rmp457a", '"1-a"', "e1");
    const r = reading({ kind: "ok", data: { revision: 3 }, etag: '"3-c"' });
    const out = text(await ended(r.client, ing(), op({ state: "succeeded", result }), "succeeded", NOW, "e1"));
    expect(out).toContain("Draft drf_2rmp457a is now revision 3: another write followed this import's revision 2. Its ETag is not kept. Review the change before writing again.");
    expect(keptETag("drf_2rmp457a", "e1")).toBe('"1-a"');
  });
  test("a draft read that answers under another epoch than the operation read keeps no ETag (§8.5)", async () => {
    begun("ing_6fdq2la", "drf_2rmp457a", "e1");
    keepETag("drf_2rmp457a", '"1-a"', "e1");
    const r = reading({ kind: "ok", data: { revision: 2 }, etag: '"2-b"' }, "e2");
    const out = text(await ended(r.client, ing(), op({ state: "succeeded", result }), "succeeded", NOW, "e1"));
    expect(out).toContain("The installation entered recovery mode or was restored while the draft was read again, so its ETag is not kept.");
    expect(r.paths).toEqual(["/drafts/drf_2rmp457a"]);
    // Neither epoch holds the new ETag; the earlier one is the one kept before.
    expect(keptETag("drf_2rmp457a", "e2")).toBeUndefined();
    expect(keptETag("drf_2rmp457a", "e1")).toBe('"1-a"');
  });
  test("an import started under an earlier epoch keeps no ETag, though both reads answer under the new one and the revision matches", async () => {
    // Revisions rewind with a restore and can be issued again with other content: revision 2 under e2
    // is not the revision 2 this import produced under e1.
    begun("ing_6fdq2la", "drf_2rmp457a", "e1");
    keepETag("drf_2rmp457a", '"1-a"', "e1");
    const r = reading({ kind: "ok", data: { revision: 2 }, etag: '"2-other"' }, "e2");
    const out = text(await ended(r.client, ing(), op({ state: "succeeded", result }), "succeeded", NOW, "e2"));
    expect(out).toContain("The installation entered recovery mode or was restored since this import started, so the draft's ETag is not kept.");
    expect(keptETag("drf_2rmp457a", "e2")).toBeUndefined();
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
  test("a lapsed transient claim is not told to be taken over", async () => {
    const out = text(await ended(reading({ kind: "transport" }).client, ing({ mode: "transient" }), op({}), "staged", Date.parse("2026-11-01T07:06:00Z"), "e1"));
    expect(out).toContain("the outcome is unknown");
    expect(out).toContain("transient staging cannot be taken over");
    expect(out).not.toContain("take it over");
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
  test("marks are sent verbatim and empty lines dropped; a document is sent only for a document source", () => {
    expect(startBody({ ...base, marks: " doc[0]/a \n\n doc[0]/b\n", doc: "x: 1" })["marks"]).toEqual([" doc[0]/a ", " doc[0]/b"]);
    // A key with trailing whitespace and its sibling without it are two nodes: the body names each exactly.
    expect(startBody({ ...base, marks: "doc[0]/env/KEY\ndoc[0]/env/KEY \n" })).toEqual({
      kind: "import", machine: "mch_b3caa2q", draft: "drf_2rmp457a", source: "machine", staging: "encrypted", review: true, marks: ["doc[0]/env/KEY", "doc[0]/env/KEY "],
    });
    // A line of only spaces is sent as typed, and the server refuses it.
    expect(startBody({ ...base, marks: "  \ndoc[0]/a" })["marks"]).toEqual(["  ", "doc[0]/a"]);
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
  test("unsent input asks before leaving", () => {
    expect(unsent({ doc: "secret: x", marks: "" })).toBe(true);
    expect(unsent({ doc: "", marks: "doc[0]/a" })).toBe(true);
    expect(unsent({ doc: "", marks: "" })).toBe(false);
  });
  const full = { kind: "import", machine: "mch_b3caa2q", draft: "drf_2rmp457a", source: "document", staging: "encrypted", review: true, doc: "secret: x", marks: "doc[0]/a" } as const;
  const start = () => {
    const calls: string[] = [];
    const got: { spec?: WriteSpec } = {};
    sendStart((s) => { calls.push("send"); got.spec = s; }, () => calls.push("clear"), full, '"1-a"');
    return { calls, spec: got.spec! };
  };
  test("the fields are emptied when the request is sent, so input typed after a refusal is kept", () => {
    const { calls, spec } = start();
    expect(calls).toEqual(["clear", "send"]);
    expect(spec).toMatchObject({ method: "POST", path: "/ingestions", ifMatch: '"1-a"', body: { document: "secret: x", marks: ["doc[0]/a"] } });
  });
  test("the start asks the write layer to tell it when the write can no longer be repeated", () => {
    const { spec } = start();
    expect(spec.released).toBeTypeOf("function");
  });
  // The tests below send the start through a real `Writes` and read the page's own body object, which
  // `Writes.release` cannot clear: only the start's `released` callback can (ginsys/bronzeward#663).
  const SIGNED = "https://idp.invalid SYNTH-sub-1";
  const drop = () => new TypeError("Failed to fetch");
  const epochAnswer = () => new Response(JSON.stringify({ mode: "normal" }), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": "e1" } });
  const answered = (data: unknown) => new Response(JSON.stringify(data), { status: 201, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": "e1" } });
  function sending(answers: (Response | Error)[]) {
    const queue = [...answers];
    const fetchMock = (async (url: RequestInfo | URL) => {
      if (String(url).endsWith("/recovery")) return epochAnswer();
      const next = queue.shift();
      if (next === undefined) throw new Error("no answer left");
      if (next instanceof Error) throw next;
      return next;
    }) as unknown as typeof fetch;
    const client = new Client({ token: () => "SYNTH-token", fetch: fetchMock });
    const writes = new Writes(() => SIGNED, async () => {});
    writes.client = client;
    return { client, writes, fetch: fetchMock };
  }
  // Sends the start; `body` is the page's own object, read after the layer let go of its spec.
  async function beginStart(p: ReturnType<typeof sending>) {
    const got: { w?: Write } = {};
    sendStart((s) => { got.w = new Write(s); }, () => {}, full, '"1-a"');
    const w = got.w!;
    const body = w.spec.body as Record<string, unknown>;
    const out = await p.writes.submit(w);
    return { w, body, out };
  }
  const held = (b: Record<string, unknown>) => ({ document: b["document"], marks: b["marks"] });
  const emptied = { document: undefined, marks: undefined };
  const unknownStart = async (p: ReturnType<typeof sending>) => {
    const s = await beginStart(p);
    expect(s.out.kind).toBe("unknown");
    expect(p.writes.repeatable(s.w)).toBe(true);
    return s;
  };
  const four = () => [drop(), drop(), drop(), drop()];
  test("an unknown start that is set aside drops the document and marks (T1, HX2 and HC6)", async () => {
    const p = sending(four());
    const s = await unknownStart(p);
    expect(held(s.body)).toEqual({ document: "secret: x", marks: ["doc[0]/a"] });
    p.writes.setAside(EPOCH_CHANGED_ASIDE);
    expect(held(s.body)).toEqual(emptied);
    expect(s.body["machine"]).toBe("mch_b3caa2q");
    expect(p.writes.repeatable(s.w)).toBe(false);
  });
  test("an unknown start that is dismissed drops the document and marks (T2, R7-1)", async () => {
    const p = sending(four());
    const s = await unknownStart(p);
    p.writes.dismiss(s.w);
    expect(held(s.body)).toEqual(emptied);
  });
  test("an unknown start whose client ends its writes after a disconnect drops the document and marks (T3, R6-2)", async () => {
    const p = sending(four());
    const s = await unknownStart(p);
    p.writes.disconnect(p.client);
    expect(held(s.body).document).toBe("secret: x");
    p.client.endWrites();
    expect(held(s.body)).toEqual(emptied);
  });
  test("a client bound after the sign-in ended does not repeat an unknown start, and the input is gone (T4, R6-1)", async () => {
    const p = sending(four());
    const s = await unknownStart(p);
    p.client.endWrites();
    const later = new Client({ token: () => "SYNTH-token", fetch: p.fetch });
    (later as unknown as { observeEpoch(r: Response): boolean }).observeEpoch(epochAnswer());
    p.writes.client = later;
    expect(p.writes.repeatable(s.w)).toBe(false);
    expect(held(s.body)).toEqual(emptied);
  });
  test("control: an unknown start keeps the document and marks, and stays repeatable, until one of the above happens (T5)", async () => {
    const p = sending(four());
    const s = await unknownStart(p);
    expect(held(s.body)).toEqual({ document: "secret: x", marks: ["doc[0]/a"] });
    expect(p.writes.repeatable(s.w)).toBe(true);
  });
  test("control: a done start drops the document and marks, and remembers its ingestion for the draft (T6)", async () => {
    const p = sending([answered({ ingestion: "ing_new" })]);
    const s = await beginStart(p);
    expect(s.out.kind).toBe("done");
    expect(held(s.body)).toEqual(emptied);
    expect(s.body["machine"]).toBe("mch_b3caa2q");
    expect(started.get("ing_new")).toEqual({ draft: "drf_2rmp457a", epoch: "e1" });
  });
  test("a refused start drops the document and marks too", async () => {
    const refusal = new Response(JSON.stringify({ type: "urn:bronzeward:problem:invalid-input", title: "x", status: 422 }), { status: 422, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": "e1" } });
    const p = sending([refusal]);
    const s = await beginStart(p);
    expect(s.out.kind).toBe("refused");
    expect(held(s.body)).toEqual(emptied);
  });
  test("the kept request does not reach the typed input once the send has returned", () => {
    let returned = false;
    const input = new Proxy<StartInput>({ ...full }, {
      get: (t, k, r) => {
        if (returned) throw new Error(`the input was read after the send: ${String(k)}`);
        return Reflect.get(t, k, r);
      },
    });
    const got: { spec?: WriteSpec } = {};
    sendStart((s) => { got.spec = s; }, () => {}, input, '"1-a"');
    returned = true;
    expect(() => got.spec!.settled!({ kind: "done", data: { ingestion: "ing_kept" } } as never, { epoch: "e1" } as never)).not.toThrow();
    expect(started.get("ing_kept")).toEqual({ draft: "drf_2rmp457a", epoch: "e1" });
  });
});

describe("the start's ETag is sent only under the epoch it was read under (§8.3, §8.5)", () => {
  const draft = "drf_2rmp457a";
  const ok = (etag: string) => ({ kind: "ok", data: { revision: 2 }, etag }) as never;
  test("a draft read for the start's ETag is not sent when the epoch moved during the read", async () => {
    let epoch = "e1";
    const sent: string[] = [];
    const why = await startETag(async () => { epoch = "e2"; return ok('"2-b"'); }, () => epoch, draft, (m) => void sent.push(m));
    expect(why).toMatch(/^Not sent: the installation entered recovery mode or was restored while the draft was read/);
    expect(sent).toEqual([]);
    expect(keptETag(draft, "e1")).toBeUndefined();
    expect(keptETag(draft, "e2")).toBeUndefined();
  });
  test("the ETag read under an unmoved epoch is kept and sent; a kept one is sent with no read", async () => {
    const sent: string[] = [];
    expect(await startETag(async () => ok('"2-b"'), () => "e1", draft, (m) => void sent.push(m))).toBeUndefined();
    expect(keptETag(draft, "e1")).toBe('"2-b"');
    expect(await startETag(async () => { throw new Error("read again"); }, () => "e1", draft, (m) => void sent.push(m))).toBeUndefined();
    expect(sent).toEqual(['"2-b"', '"2-b"']);
  });
  test("a draft that cannot be read sends nothing", async () => {
    const sent: string[] = [];
    expect(await startETag(async () => undefined, () => "e1", draft, (m) => void sent.push(m))).toBe("Not sent: the draft could not be read for its ETag. Try again.");
    expect(await startETag(async () => ({ kind: "transport" }) as never, () => "e1", draft, (m) => void sent.push(m))).toMatch(/^Not sent: the draft could not be read/);
    expect(sent).toEqual([]);
  });
});

describe("what ends the watch of an import's operation (§11.3, the CLI's follow)", () => {
  const events = (gen: number, list: [string, unknown][]) => list.reduce((s, [type, data]) => onEvent(s, type, data), startRun(gen));
  test("a pause or resume-failed of an earlier run settles nothing", () => {
    expect(events(1, [["staged", {}], ["paused", { generation: 1 }]]).settled).toBe("paused");
    expect(events(2, [["paused", { generation: 1 }], ["continued", { generation: 2 }]]).settled).toBeUndefined();
    expect(events(2, [["paused", { generation: 1 }], ["continued", { generation: 2 }], ["paused", { generation: 2 }]]).settled).toBe("paused");
    expect(events(2, [["resume-failed", { code: "x" }], ["continued", { generation: 2 }]]).settled).toBeUndefined();
    expect(events(2, [["continued", { generation: 2 }], ["resume-failed", { code: "x" }]]).settled).toBe("resume-failed");
  });
  // Item D of ginsys/bronzeward#644: the state after each event, so that removing the `ours` guard of
  // onEvent fails here. Without it an earlier run's pause or resume-failed settles the watch.
  test("the run's state after each event: an earlier run's events settle nothing, whichever event begins the run", () => {
    const after = (gen: number, list: [string, unknown][]) => {
      let s = startRun(gen);
      return list.map(([type, data]) => ((s = onEvent(s, type, data)), s.settled));
    };
    // Generation 2 starts not ours: the pause and the failure of the run before it settle nothing.
    expect(after(2, [["paused", { generation: 1 }], ["resume-failed", { code: "x" }], ["staged", {}]])).toEqual([undefined, undefined, undefined]);
    // A continuation, a takeover or a mark at the claim's generation begins our run, and only then do they settle.
    for (const begins of ["continued", "taken-over", "marked"]) {
      expect(after(2, [["paused", { generation: 1 }], [begins, { generation: 2 }], ["paused", { generation: 2 }]])).toEqual([undefined, undefined, "paused"]);
      expect(after(2, [["resume-failed", { code: "x" }], [begins, { generation: 2 }], ["resume-failed", { code: "x" }]])).toEqual([undefined, undefined, "resume-failed"]);
    }
    // A begin at an earlier generation than the claim's is not ours either.
    expect(after(3, [["continued", { generation: 2 }], ["paused", { generation: 2 }]])).toEqual([undefined, undefined]);
    // The first run is ours from the start.
    expect(after(1, [["staged", {}], ["paused", { generation: 1 }]])).toEqual([undefined, "paused"]);
  });
  test("an event before the refresh does not stop a healthy watch on a lease that only looked lapsed", () => {
    const at = (s: string) => Date.parse(`2026-11-01T${s}Z`);
    // The lease ran to 07:05:00; the page read it at 07:04:00, and an event arrives at 07:05:05.
    expect(stopsWatch(undefined, { b: ing(), at: at("07:04:00") }, at("07:05:05"))).toBe(false);
    // A read made after the lease and the margin passed that still shows it held and lapsed stops the
    // watch once the operation's newest event was shown (item F), and not before.
    expect(stopsWatch(undefined, { b: ing(), at: at("07:05:11") }, at("07:05:12"), undefined, true)).toBe(true);
    expect(stopsWatch(undefined, { b: ing(), at: at("07:05:11") }, at("07:05:12"))).toBe(false);
    // A read made then that shows a renewed lease does not.
    expect(stopsWatch(undefined, { b: ing({ leaseUntil: "2026-11-01T07:10:00Z" }), at: at("07:05:11") }, at("07:05:12"))).toBe(false);
    expect(stopsWatch("paused", { b: ing(), at: 0 }, 0)).toBe(true);
  });
});

describe("an abandoned claim is followed to its operation's terminal event (Copilot review of 919d185f on web/src/imports.tsx:168)", () => {
  const at = Date.parse("2026-11-01T07:01:00Z");
  test("the old pause settles nothing for an abandoned claim, and the terminal failure ends the watch", async () => {
    // The expiry made the ingestion read abandoned before the sweep failed the operation; the restarted
    // watch replays the old pause first.
    const replayed = onEvent(startRun(1), "paused", { generation: 1 });
    expect(replayed.settled).toBe("paused");
    expect(stopsWatch(replayed.settled, { b: ing({ state: "abandoned" }), at }, at)).toBe(false);
    expect(stopsWatch("resume-failed", { b: ing({ state: "abandoned" }), at }, at)).toBe(false);
    // The failure event is the watch's end: the page says the ingestion was abandoned, not that it is paused.
    const failed = op({ state: "failed", error: { type: "urn:bronzeward:problem:ingestion-abandoned", title: "ingestion abandoned", status: 409, instance: "req_1" } });
    const out = text(await ended(reading({ kind: "transport" }).client, ing({ state: "abandoned" }), failed, replayed.settled ?? "", at, "e1"));
    expect(out).toContain("The ingestion was abandoned, so nothing of it reached the draft.");
    expect(out).not.toContain("Paused for review");
  });
  describe("and the follow has a bound, so a sweep that never ends the operation does not hold it open (round 5 of the local review)", () => {
    beforeEach(() => {
      vi.useFakeTimers();
      vi.setSystemTime(NOW);
    });
    afterEach(() => vi.useRealTimers());
    const abandoned = ing({ state: "abandoned" });
    const stops = (since: number | undefined, settled: "paused" | undefined = "paused") => stopsWatch(settled, { b: abandoned, at: Date.now() }, Date.now(), since);
    test("with only the old pause and no terminal event, the watch ends at the bound and says the outcome is unknown", async () => {
      const since = abandonedSince(abandoned, undefined, Date.now());
      expect(since).toBe(NOW);
      vi.advanceTimersByTime(ABANDONED_SETTLE - 1);
      expect(stops(since)).toBe(false);
      vi.advanceTimersByTime(1);
      expect(stops(since)).toBe(true);
      const out = text(await ended(reading({ kind: "transport" }).client, abandoned, op({ state: "running" }), "paused", Date.now(), "e1"));
      expect(out).toBe(`Import ${short(abandoned.id)} is abandoned, but operation ${short("op_zcfw5mcm")} has no recorded end yet: its outcome is unknown. Read it again later.`);
      expect(out).not.toContain("Paused for review");
    });
    test("a terminal event before the bound ends the watch with the real failure", async () => {
      const since = abandonedSince(abandoned, undefined, Date.now());
      vi.advanceTimersByTime(ABANDONED_SETTLE / 2);
      expect(stops(since)).toBe(false);
      const failed = op({ state: "failed", error: { type: "urn:bronzeward:problem:ingestion-abandoned", title: "ingestion abandoned", status: 409, instance: "req_1" } });
      expect(text(await ended(reading({ kind: "transport" }).client, abandoned, failed, "paused", Date.now(), "e1"))).toContain("The ingestion was abandoned, so nothing of it reached the draft.");
    });
    test("a reread or a reconnect keeps the first deadline, and leaving the abandoned state drops it", () => {
      const since = abandonedSince(abandoned, undefined, Date.now());
      vi.advanceTimersByTime(90_000);
      expect(abandonedSince(ing({ state: "abandoned" }), since, Date.now())).toBe(since);
      vi.advanceTimersByTime(ABANDONED_SETTLE - 90_000);
      expect(stops(abandonedSince(ing({ state: "abandoned" }), since, Date.now()))).toBe(true);
      expect(abandonedSince(ing({ state: "held" }), since, Date.now())).toBeUndefined();
    });
    test("an abandoned claim with no first instant known is not stopped by the bound", () => {
      vi.advanceTimersByTime(ABANDONED_SETTLE * 10);
      expect(stops(undefined)).toBe(false);
    });
  });
  test("every other state keeps the generation rule: a pause or resume-failed of the run ends the watch", () => {
    for (const state of ["paused", "held", "resumed"]) {
      expect(stopsWatch("paused", { b: ing({ state }), at }, at)).toBe(true);
      expect(stopsWatch("resume-failed", { b: ing({ state }), at }, at)).toBe(true);
    }
    expect(stopsWatch(undefined, { b: ing({ state: "paused" }), at }, at)).toBe(false);
  });
});

describe("the progress watch follows the claim generation (Codex and Copilot threads on web/src/imports.tsx:205)", () => {
  test("the same operation at a new claim generation restarts the watch, and the new run's pause ends it", () => {
    const client = new Client();
    const again = () => {};
    const first = watchDeps(client, ing({ operation: "op1", ownerGeneration: 1 }), again);
    expect(watchDeps(client, ing({ operation: "op1", ownerGeneration: 1 }), again)).toEqual(first);
    expect(watchDeps(client, ing({ operation: "op1", ownerGeneration: 2 }), again)).not.toEqual(first);
    expect(watchDeps(client, ing({ operation: "op2", ownerGeneration: 1 }), again)).not.toEqual(first);
    // An abandonment keeps the operation and the generation: the watch stopped at the pause restarts and reads the failure.
    const paused = watchDeps(client, ing({ operation: "op1", ownerGeneration: 1, state: "paused" }), again);
    expect(watchDeps(client, ing({ operation: "op1", ownerGeneration: 1, state: "abandoned" }), again)).not.toEqual(paused);
    // The restarted watch begins at the new generation: it settles on that run's pause, not on the earlier run's.
    const events: [string, unknown][] = [["paused", { generation: 1 }], ["continued", { generation: 2 }], ["paused", { generation: 2 }]];
    expect(events.reduce((s, [t, d]) => onEvent(s, t, d), startRun(2)).settled).toBe("paused");
  });
});

describe("a file chosen while an earlier one is still being read (Copilot thread on web/src/imports.tsx:340)", () => {
  const deferred = () => {
    let done: (t: string) => void = () => {};
    let fail: () => void = () => {};
    const text = new Promise<string>((res, rej) => { done = res; fail = () => rej(new Error("x")); });
    return { file: { text: () => text }, done, fail };
  };
  const settle = async () => { for (let i = 0; i < 4; i++) await Promise.resolve(); };
  test("when the first read finishes last, the second file's text is kept", async () => {
    const got: string[] = [];
    const notes: string[] = [];
    const choose = fileReader((t) => got.push(t), (n) => notes.push(n));
    const a = deferred();
    const b = deferred();
    choose(a.file);
    choose(b.file);
    b.done("second");
    await settle();
    a.done("first");
    await settle();
    expect(got).toEqual(["second"]);
    expect(notes).toEqual([]);
  });
  test("a failed read of a superseded file says nothing, and a failed read of the newest does", async () => {
    const notes: string[] = [];
    const choose = fileReader(() => {}, (n) => notes.push(n));
    const a = deferred();
    const b = deferred();
    choose(a.file);
    choose(b.file);
    a.fail();
    await settle();
    expect(notes).toEqual([]);
    b.fail();
    await settle();
    expect(notes).toEqual(["The file could not be read."]);
  });
  test("a document typed before the file read finishes is kept, and a failed read then says nothing", async () => {
    const got: string[] = [];
    const notes: string[] = [];
    const choose = fileReader((t) => got.push(t), (n) => notes.push(n));
    const type = typedDocument(choose, (t) => got.push(t));
    const a = deferred();
    choose(a.file);
    type("typed");
    a.done("from the file");
    await settle();
    expect(got).toEqual(["typed"]);
    const b = deferred();
    choose(b.file);
    type("typed again");
    b.fail();
    await settle();
    expect(got).toEqual(["typed", "typed again"]);
    expect(notes).toEqual([]);
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

describe("the progress view across watches (ginsys/bronzeward#644 item E)", () => {
  test("the cleared note of an earlier operation is gone when the watch starts again", () => {
    let v = watchView({ lines: [] }, { type: "start" });
    v = watchView(v, { type: "show", line: "started" });
    v = watchView(v, { type: "clear", why: "The events were cleared: a new epoch." });
    expect(v).toEqual({ lines: [], cleared: "The events were cleared: a new epoch." });
    v = watchView(v, { type: "start" });
    expect(v.cleared).toBeUndefined();
    expect(v.lines).toEqual([]);
    expect(v.end).toBeUndefined();
  });
  test("an event after a clear keeps the note, and the end text is kept beside the lines", () => {
    let v = watchView({ lines: ["old"] }, { type: "clear", why: "cleared" });
    v = watchView(v, { type: "show", line: "new" });
    v = watchView(v, { type: "end", text: "done" });
    expect(v).toEqual({ lines: ["new"], cleared: "cleared", end: "done" });
  });
});

describe("the reads of the watch and of its end go through the page's settle (ginsys/bronzeward#644 item I)", () => {
  const OPID = "op_zcfw5mcm";
  const answer = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": "e1" } });
  const unauthenticated: ReadResult<never> = { kind: "unauthenticated", problem: null };
  // A reader whose reads answer from `by`; `open` answers JSON, so the watch reads the pages.
  const scripted = (by: (path: string) => ReadResult<unknown>) => ({
    read: async <T,>(p: string) => by(p) as ReadResult<T>,
    readWithEpoch: async <T,>(p: string) => ({ result: by(p) as ReadResult<T>, epoch: "e1" }),
    open: async () => answer({}),
    writesEnded: false,
  });
  const settling = () => {
    const settled: ReadResult<unknown>[] = [];
    const reads = { settle: ((res: ReadResult<unknown>, _set: unknown, live: () => boolean) => (settled.push(res), live())) as Reads["settle"], undropped: () => () => true };
    return { settled, reads };
  };
  const watched = async (by: (path: string) => ReadResult<unknown>) => {
    const { settled, reads } = settling();
    const client = settledReads(scripted(by), reads, () => true);
    const r = await watch(client, OPID, { show: () => {}, clear: () => {}, signal: new AbortController().signal }, { sleep: async () => {}, now: () => 0, idle: 20 });
    return { r, settled };
  };
  test("an unresolved 401 on the operation read reaches the shell", async () => {
    const { r, settled } = await watched((p) => (p.endsWith("/events") ? { kind: "ok", data: { items: [] } } : unauthenticated));
    expect(r.kind).toBe("failed");
    expect(settled.filter((s) => s.kind === "unauthenticated")).toHaveLength(1);
  });
  test("an unresolved 401 on the events page read reaches the shell", async () => {
    const { r, settled } = await watched((p) => (p.includes("/events") ? unauthenticated : { kind: "ok", data: op({}) }));
    expect(r.kind).toBe("failed");
    expect(settled.filter((s) => s.kind === "unauthenticated")).toHaveLength(1);
  });
  test("an unresolved 401 on the final draft read reaches the shell", async () => {
    const b = ing();
    started.set(b.id, { draft: b.draft, epoch: "e1" });
    const { settled, reads } = settling();
    const client = settledReads(scripted(() => unauthenticated), reads, () => true);
    const done = op({ state: "succeeded", result: { draft: b.draft, draftRevision: 2, importBaseRevision: "rev_base" } });
    expect(text(await ended(client, b, done, "", NOW, "e1"))).toContain("could not be read again");
    expect(settled.map((s) => s.kind)).toEqual(["unauthenticated"]);
  });
  test("an answer the shell invalidated, or one that came after the watch left, is discarded and never reaches the watch", async () => {
    let held = true;
    let live = true;
    const reads = { settle: ((_r: unknown, _s: unknown, l: () => boolean) => l()) as Reads["settle"], undropped: () => () => held };
    const wrapped = settledReads(scripted(() => ({ kind: "ok", data: { lastEvent: 1 } })), reads, () => live);
    expect((await wrapped.read("/x")).kind).toBe("ok");
    expect((await wrapped.readWithEpoch("/x")).result.kind).toBe("ok");
    held = false;
    expect((await wrapped.read("/x")).kind).toBe("discarded");
    const got = await wrapped.readWithEpoch("/x");
    expect(got.result.kind).toBe("discarded");
    expect(got.epoch).toBe("e1");
    held = true;
    live = false;
    expect((await wrapped.read("/x")).kind).toBe("discarded");
  });
  test("a read made after the watch left is not rendered, and open and writesEnded pass through", async () => {
    const { reads } = settling();
    let live = true;
    const calls: boolean[] = [];
    const wrapped = settledReads(scripted(() => ({ kind: "ok", data: {} })), { ...reads, settle: ((_r: unknown, _s: unknown, l: () => boolean) => (calls.push(l()), true)) as Reads["settle"] }, () => live);
    await wrapped.read("/x");
    live = false;
    await wrapped.readWithEpoch("/x");
    expect(calls).toEqual([true, false]);
    expect(wrapped.writesEnded).toBe(false);
    const opened = await wrapped.open("/x", {}, new AbortController().signal);
    expect(opened instanceof Response && opened.status).toBe(200);
  });
});

describe("a lapsed lease decides only after the operation's newest event was shown (ginsys/bronzeward#644 item F, the CLI's Until)", () => {
  const at = (s: string) => Date.parse(`2026-11-01T${s}Z`);
  const OPID = "op_zcfw5mcm";
  const backlog: [string, unknown][] = [["started", {}], ["paused", { generation: 1 }], ["continued", { generation: 2 }], ["resume-failed", { code: "decrypt-failed" }]];
  // A client over a server whose events come as JSON pages and whose operation still runs.
  const serving = (lastEvent: number, claim: Ingestion = ing({ ownerGeneration: 2 })) => {
    const answer = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": "e1" } });
    const fetch = async (url: RequestInfo | URL): Promise<Response> => {
      const u = String(url);
      if (u.includes(`/operations/${OPID}/events`)) return answer({ items: backlog.map(([type, data], i) => ({ id: `e1:${i + 1}`, type, data, at: "2026-11-01T06:56:00Z" })) });
      if (u.endsWith(`/operations/${OPID}`)) return answer(op({ lastEvent }));
      if (u.endsWith("/ingestions/ing_6fdq2la")) return answer(claim);
      throw new Error(`unexpected ${u}`);
    };
    return new Client({ fetch: fetch as typeof globalThis.fetch, token: () => "SYNTH-token", sleep: async () => {} });
  };
  test("a lease lapsed when read does not cut the replay of started, paused, continued, resume-failed after the first event", async () => {
    const b = ing({ ownerGeneration: 2 });
    const client = serving(4);
    const lines: string[] = [];
    const f = followRun({
      client, ingestion: b.id, op: OPID, generation: 2, signal: new AbortController().signal,
      latest: () => ({ b, at: at("07:05:15") }), abandonedAt: () => undefined, now: () => at("07:05:20"),
      view: (a) => { if (a.type === "show") lines.push(a.line); },
    });
    const r = await watch(client, OPID, f.w, { sleep: async () => {}, now: () => 0, idle: 20 });
    expect(lines).toEqual(["started", "paused (generation 1)", "continued (generation 2)", "resume-failed: decrypt-failed"]);
    expect(f.settled()).toBe("resume-failed");
    if (r.kind !== "ended") throw new Error(`the watch did not end: ${r.kind}`);
    expect(text(await ended(client, b, r.operation, f.settled() ?? "", at("07:05:20"), r.epoch))).toContain("could not be decrypted (resume-failed)");
  });
  test("the check reads the claim again: a lease renewed since the page's read keeps the watch, and a claim still lapsed stops it", async () => {
    const b = ing({ ownerGeneration: 2 });
    const settleTick = () => new Promise<void>((r) => setTimeout(r, 0));
    const follow = async (claim: Ingestion) => {
      const f = followRun({
        client: serving(1, claim), ingestion: b.id, op: OPID, generation: 2, signal: new AbortController().signal,
        latest: () => ({ b, at: at("07:05:15") }), abandonedAt: () => undefined, now: () => at("07:05:20"), view: () => {},
      });
      f.w.show({ id: "e1:1", type: "started", data: {}, at: "2026-11-01T06:56:00Z" } as never);
      expect(f.w.until!()).toBe(false);
      await settleTick();
      return f.w.until!();
    };
    // The same lease, read again: nothing renewed it, and the newest event was shown.
    expect(await follow(ing({ ownerGeneration: 2 }))).toBe(true);
    // Renewed until 07:10:00 after the page's read: the watch goes on.
    expect(await follow(ing({ ownerGeneration: 2, leaseUntil: "2026-11-01T07:10:00Z" }))).toBe(false);
  });
  describe("the check's reads (Copilot threads on web/src/imports.tsx:280 and :282)", () => {
    const b = ing({ ownerGeneration: 2 });
    const tick = () => new Promise<void>((r) => setTimeout(r, 0));
    const okClaim = { kind: "ok", data: ing({ ownerGeneration: 2 }) } as const;
    const okOp = { kind: "ok", data: op({ lastEvent: 1 }) } as const;
    const refused = { kind: "refused", status: 403, problem: null } as const;
    // A check started after the first event, over a client that answers the claim and the operation as given.
    const check = async (claim: ReadResult<unknown>, operation: ReadResult<unknown>, leave = false) => {
      const paths: string[] = [];
      const abort = new AbortController();
      const client = {
        readWithEpoch: async <T,>(p: string) => {
          paths.push(p);
          if (leave) abort.abort();
          return { result: (p.startsWith("/ingestions/") ? claim : operation) as ReadResult<T>, epoch: "e1" };
        },
      };
      const f = followRun({
        client, ingestion: b.id, op: OPID, generation: 2, signal: abort.signal,
        latest: () => ({ b, at: at("07:05:15") }), abandonedAt: () => undefined, now: () => at("07:05:20"), view: () => {},
      });
      f.w.show({ id: "e1:1", type: "started", data: {}, at: "2026-11-01T06:56:00Z" } as never);
      f.w.until!();
      await tick();
      // The reads of the first check; the ask that follows can start another where the first found no answer.
      const first = [...paths];
      return { paths: first, stops: f.w.until!(), failure: f.failure(), reads: first.length };
    };
    test("a watch that left during the claim's read sends no read of the operation, and a claim with no answer sends none either", async () => {
      expect((await check(okClaim, okOp, true)).paths).toEqual([`/ingestions/${b.id}`]);
      expect((await check({ kind: "transport" }, okOp)).paths).toEqual([`/ingestions/${b.id}`]);
      // A refusal that arrives after the watch left is not shown.
      expect((await check(refused, okOp, true)).failure).toBeUndefined();
    });
    test("a read refused for good (403) ends the check, the watch stops with the failure to show, and no further check is made", async () => {
      const claim = await check(refused, okOp);
      expect(claim).toMatchObject({ stops: true, failure: refused, reads: 1 });
      const operation = await check(okClaim, refused);
      expect(operation).toMatchObject({ stops: true, failure: refused, reads: 2 });
    });
    test("a clear takes back a refusal recorded before it: the reset watch is not ended by it", async () => {
      const client = { readWithEpoch: async <T,>(p: string) => ({ result: (p.startsWith("/ingestions/") ? refused : okOp) as ReadResult<T>, epoch: "e1" }) };
      const f = followRun({
        client, ingestion: b.id, op: OPID, generation: 2, signal: new AbortController().signal,
        latest: () => ({ b, at: at("07:05:15") }), abandonedAt: () => undefined, now: () => at("07:05:20"), view: () => {},
      });
      f.w.show({ id: "e1:1", type: "started", data: {}, at: "2026-11-01T06:56:00Z" } as never);
      f.w.until!();
      await tick();
      expect(f.failure()).toBe(refused);
      f.w.clear("restored");
      expect(f.failure()).toBeUndefined();
      expect(f.w.until!()).toBe(false);
    });
    test("a read with no answer or one for a new epoch is not a failure: the watch stays pending", async () => {
      expect(await check({ kind: "transport" }, okOp)).toMatchObject({ stops: false, failure: undefined });
      expect(await check(okClaim, { kind: "discarded" })).toMatchObject({ stops: false, failure: undefined });
      // 408 and 429 are asked again, not final.
      expect(await check({ kind: "refused", status: 429, problem: null }, okOp)).toMatchObject({ stops: false, failure: undefined });
      expect(await check(okClaim, { kind: "refused", status: 408, problem: null })).toMatchObject({ stops: false, failure: undefined });
    });
  });
  test("caughtUp: the last shown event number must reach the operation's lastEvent, and an epoch that differs decides nothing", () => {
    const shown = (n: number) => ({ epoch: "e1", n });
    expect(caughtUp(shown(3), "e1", "e1", 4)).toBe(false);
    expect(caughtUp(shown(4), "e1", "e1", 4)).toBe(true);
    expect(caughtUp(shown(5), "e1", "e1", 4)).toBe(true);
    expect(caughtUp(undefined, "e1", "e1", 0)).toBe(true);
    expect(caughtUp(undefined, "e1", "e1", 2)).toBe(false);
    // After a restore the operation can have fewer events, so its last number would read as shown.
    expect(caughtUp(shown(4), "e1", "e2", 2)).toBe("reset");
    expect(caughtUp(shown(4), "e2", "e1", 2)).toBe("reset");
    // A read that named no epoch is not a mismatch, as in the CLI.
    expect(caughtUp(shown(4), undefined, undefined, 4)).toBe(true);
  });
});

describe("the start while its draft's ETag is read (ginsys/bronzeward#644 item G)", () => {
  const input: StartInput = { kind: "import", machine: "mch_b3caa2q", draft: "drf_2rmp457a", source: "document", staging: "encrypted", review: true, doc: "a: 1", marks: "/a" };
  test("text typed while the draft was read is kept, with the leave guard, and the submitted text is emptied", async () => {
    let typed = { doc: "a: 1", marks: "/a" };
    const sent: WriteSpec[] = [];
    let release: (r: Load<Draft>) => void = () => {};
    const slow = () => new Promise<Load<Draft>>((done) => { release = done; });
    const done = startETag(slow, () => "e1", input.draft, (ifMatch) => sendStart((s) => void sent.push(s), () => { typed = keepTyped(typed, input); }, input, ifMatch));
    // The operator keeps typing in the document field while the read is pending; the marks stay as submitted.
    typed = { doc: "a: 1\nb: 2", marks: "/a" };
    release({ kind: "ok", data: {} as Draft, etag: '"3"' });
    expect(await done).toBeUndefined();
    expect(sent).toHaveLength(1);
    expect(sent[0]!.ifMatch).toBe('"3"');
    expect((sent[0]!.body as { document: string }).document).toBe("a: 1");
    expect(typed).toEqual({ doc: "a: 1\nb: 2", marks: "" });
    expect(unsent(typed)).toBe(true);
  });
  test("fields that still equal the submitted text are emptied, as before", () => {
    expect(keepTyped({ doc: "a: 1", marks: "/a" }, input)).toEqual({ doc: "", marks: "" });
    expect(unsent(keepTyped({ doc: "a: 1", marks: "/a" }, input))).toBe(false);
    expect(keepTyped({ doc: "other", marks: "/a" }, input)).toEqual({ doc: "other", marks: "" });
  });
});

describe("a 412 on the start leads to the draft, and the same ETag is not sent again (ginsys/bronzeward#644 item H)", () => {
  const input: StartInput = { kind: "import", machine: "mch_b3caa2q", draft: "drf_2rmp457a", source: "machine", staging: "encrypted", review: true, doc: "", marks: "" };
  const refusedOn = (etag: string, epoch = "e1") => {
    let spec: WriteSpec | undefined;
    sendStart((s) => { spec = s; }, () => {}, input, etag);
    spec!.settled!({ kind: "refused", status: 412, problem: null, replayed: false }, { epoch } as unknown as Write);
  };
  const attempt = async () => {
    const sent: string[] = [];
    const why = await startETag(async () => ({ kind: "ok", data: {} as Draft, etag: '"fresh"' }), () => "e1", input.draft, (m) => void sent.push(m));
    return { why, sent };
  };
  afterEach(() => forgetRefused(input.draft));
  test("the refused write shows a link to its draft, and no other outcome does", () => {
    const refused = { kind: "refused", status: 412, problem: null, replayed: false } as const;
    const out = renderToStaticMarkup(h(ReadsProvider, { value: noReads }, reviewFirst(refused, input.draft)));
    expect(out).toContain('href="/drafts/drf_2rmp457a"');
    expect(out).toContain("Review the draft");
    expect(reviewFirst({ ...refused, status: 409 }, input.draft)).toBeUndefined();
    expect(reviewFirst({ kind: "done", data: null, replayed: false } as never, input.draft)).toBeUndefined();
    expect(reviewFirst(undefined, input.draft)).toBeUndefined();
    expect(reviewFirst(refused, undefined)).toBeUndefined();
  });
  test("with the kept ETag the 412 refused, nothing is sent; once the draft page kept another, it is", async () => {
    keepETag(input.draft, '"old"', "e1");
    refusedOn('"old"');
    const blocked = await attempt();
    expect(blocked.sent).toEqual([]);
    expect(blocked.why).toBe("Not sent: the draft changed since this ETag was read. Review the draft first; its page reads the ETag again.");
    // The draft's page read it again and kept its new ETag.
    keepETag(input.draft, '"new"', "e1");
    const open = await attempt();
    expect(open.why).toBeUndefined();
    expect(open.sent).toEqual(['"new"']);
  });
  test("a refusal under one epoch stops nothing under another: the draft restored at the same ETag text starts", async () => {
    keepETag(input.draft, '"old"', "e1");
    refusedOn('"old"', "e1");
    // A restore: the epoch is e2, and the draft's page kept the same ETag text under it after the operator reviewed it.
    keepETag(input.draft, '"old"', "e2");
    const sent: string[] = [];
    expect(await startETag(async () => undefined, () => "e2", input.draft, (m) => void sent.push(m))).toBeUndefined();
    expect(sent).toEqual(['"old"']);
  });
  test("a delayed 412 of an older epoch does not replace the refusal recorded under the current one (Copilot thread on web/src/imports.tsx:494)", async () => {
    keepETag(input.draft, '"b"', "e2");
    refusedOn('"b"', "e2");
    // The write set aside by the epoch change settles late, with the tag it carried under e1.
    refusedOn('"a"', "e1");
    const sent: string[] = [];
    const why = await startETag(async () => undefined, () => "e2", input.draft, (m) => void sent.push(m));
    expect(why).toBe(REVIEW_FIRST);
    expect(sent).toEqual([]);
  });
  test("a 412 on one draft does not stop the start of another that holds the same ETag text", async () => {
    keepETag(input.draft, '"old"', "e1");
    refusedOn('"old"');
    keepETag("drf_other", '"old"', "e1");
    const sent: string[] = [];
    expect(await startETag(async () => undefined, () => "e1", "drf_other", (m) => void sent.push(m))).toBeUndefined();
    expect(sent).toEqual(['"old"']);
    forgetETag("drf_other");
  });
});

describe("the lease stop compares the time a read began, not the time it was delivered (ginsys/bronzeward#644 item C)", () => {
  const at = (s: string) => Date.parse(`2026-11-01T${s}Z`);
  const keys = (n: number) => [n, 0, "v"];
  test("a read begun at 07:04:59 on a lease ending 07:05:00 and delivered at 07:05:12 does not stop the watch", () => {
    const b = ing();
    let s: ReadStamp = { keys: [], began: at("07:04:00"), read: undefined };
    s = stampRead(s, undefined, keys(1), at("07:04:59"));
    s = stampRead(s, b, keys(1), at("07:05:12"));
    expect(s.read).toEqual({ b, at: at("07:04:59") });
    // The renewal came after the snapshot was taken, so the snapshot still shows the old lease.
    expect(stopsWatch(undefined, s.read!, at("07:05:12"), undefined, true)).toBe(false);
  });
  test("a read begun exactly at the lease plus the margin counts as after it, as the refresh timer's own deadline", () => {
    const b = ing();
    expect(lapsedRead({ b, at: at("07:05:10") }, at("07:05:12"))).toBe(true);
    expect(lapsedRead({ b, at: at("07:05:10") - 1 }, at("07:05:12"))).toBe(false);
  });
  test("a read begun before the lease plus the margin and delivered after it is refreshed at once (Codex thread on web/src/imports.tsx:305, Copilot :392)", () => {
    const b = ing();
    // Due at 07:05:10. Before it, the refresh waits for it.
    expect(refreshWait(b, at("07:04:00"), at("07:04:00"))).toBe(70_000);
    // Delivered at 07:05:12, begun at 07:04:59: it predates the deadline, so nothing else would read again.
    expect(refreshWait(b, at("07:04:59"), at("07:05:12"))).toBe(0);
    // A read begun at the deadline or after it already decides, and a run that is not running needs no read.
    expect(refreshWait(b, at("07:05:10"), at("07:05:12"))).toBeUndefined();
    expect(refreshWait(ing({ state: "paused" }), at("07:04:59"), at("07:05:12"))).toBeUndefined();
  });
  test("a read begun after the lease and the margin stops it, and a delivery in the render of a new trigger keeps the older start", () => {
    const b1 = ing();
    const b2 = ing();
    let s: ReadStamp = { keys: [], began: at("07:04:00"), read: undefined };
    s = stampRead(s, undefined, keys(1), at("07:05:11"));
    s = stampRead(s, b1, keys(1), at("07:05:12"));
    expect(stopsWatch(undefined, s.read!, at("07:05:12"), undefined, true)).toBe(true);
    // A new read is triggered in the same render that delivers the answer of the earlier one: the answer is
    // stamped with the start of the earlier read.
    s = stampRead(s, b2, keys(2), at("07:06:00"));
    expect(s.read).toEqual({ b: b2, at: at("07:05:11") });
    expect(s.began).toBe(at("07:06:00"));
  });
});

describe("the lease refresh is read again after a failure (ginsys/bronzeward#644 item J, client contracts §8.2)", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());
  test("one failure and then an answer: the read is made again after 1 s, and the count starts over", () => {
    const again = vi.fn();
    const failed = vi.fn();
    const r = refreshRetry(again, failed);
    r.lost(true);
    vi.advanceTimersByTime(999);
    expect(again).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(again).toHaveBeenCalledTimes(1);
    r.lost(false);
    expect(failed).toHaveBeenLastCalledWith(false);
    r.lost(true);
    vi.advanceTimersByTime(1000);
    expect(again).toHaveBeenCalledTimes(2);
  });
  test("every try failing: 1, 2, 4 and 8 s apart, then an explicit failure and no more reads", () => {
    const again = vi.fn();
    const failed = vi.fn();
    const r = refreshRetry(again, failed);
    let calls = 0;
    for (const wait of [1000, 2000, 4000, 8000]) {
      r.lost(true);
      expect(failed).not.toHaveBeenCalled();
      // Nothing is read just before the deadline, and exactly one read is made at it.
      vi.advanceTimersByTime(wait - 1);
      expect(again).toHaveBeenCalledTimes(calls);
      vi.advanceTimersByTime(1);
      expect(again).toHaveBeenCalledTimes(++calls);
    }
    expect(again).toHaveBeenCalledTimes(4);
    r.lost(true);
    expect(failed).toHaveBeenLastCalledWith(true);
    vi.advanceTimersByTime(120_000);
    expect(again).toHaveBeenCalledTimes(4);
    expect(REFRESH_FAILED).toContain("5 tries");
  });
  test("stop ends a pending retry, and the delay is capped at 30 s", () => {
    const again = vi.fn();
    const r = refreshRetry(again, () => {});
    r.lost(true);
    r.stop();
    vi.advanceTimersByTime(60_000);
    expect(again).not.toHaveBeenCalled();
    expect(refreshDelay(5)).toBe(16_000);
    expect(refreshDelay(7)).toBe(30_000);
  });
});
