import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { Shown } from "./claim.tsx";
import { WritesProvider } from "./forms.tsx";
import { ended, type Ingestion, onEvent, startBody, startRun } from "./imports.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { candidates, markLines, markRefusal, markSent, MarksPanel, markSpec, MAX_CANDIDATES, MAX_MARKS, withMark } from "./marks.tsx";
import { eventLine, refusalOf, refusalText } from "./progress.ts";
import { ShellContext } from "./shell.tsx";
import { Refused } from "./states.tsx";
import { Writes } from "./write.ts";

type Operation = components["schemas"]["OperationBody"];
const SENTINEL = "BWSYNTH-sentinel";
const NOW = Date.parse("2026-11-01T07:00:00Z");
const ing = (o: Partial<Ingestion> = {}): Ingestion => ({
  id: "ing_6fdq2la", kind: "import", mode: "encrypted", state: "paused", machine: "mch_b3caa2q", draft: "drf_2rmp457a", operation: "op_zcfw5mcm",
  review: "pending", ownerGeneration: 2, leaseUntil: "2026-11-01T07:05:00Z", expiresAt: "2026-11-01T08:00:00Z", createdAt: "2026-11-01T06:55:00Z", ...o,
});
const op = (o: Partial<Operation> = {}): Operation => ({
  id: "op_zcfw5mcm", kind: "ingest", state: "running", epoch: "e1", lastEvent: 3, subject: null, createdBy: null, createdAt: "2026-11-01T06:55:00Z", result: null, error: null, ...o,
} as Operation);
const noReads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const page = (n: ReactNode) =>
  renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: noReads }, n))));
const unused = { readWithEpoch: async () => { throw new Error("unused"); } } as never;
const problem = (o: Record<string, unknown>) => ({ type: "urn:bronzeward:problem:validation-failed", title: "Validation failed", status: 422, ...o });

describe("the marks a request carries (§11.2, compilation §2.2)", () => {
  test("each nonempty line is one path, sent as typed with its whitespace", () => {
    expect(markLines("doc[0]/a\n\n doc[0]/b \n")).toEqual(["doc[0]/a", " doc[0]/b "]);
    expect(startBody({ kind: "import", machine: "m", draft: "d", source: "machine", staging: "encrypted", review: true, doc: "", marks: "doc[0]/a\n" })["marks"]).toEqual(["doc[0]/a"]);
  });
  test("no mark, or more than the server's 1024, is not sent", () => {
    expect(markRefusal([])).toMatch(/^Not sent: type at least one path/);
    expect(markRefusal(Array(MAX_MARKS).fill("x"))).toBeUndefined();
    expect(markRefusal(Array(MAX_MARKS + 1).fill("x"))).toMatch(/at most 1024/);
  });
  test("the write posts the marks to the ingestion, which is the resource to read, and drops them when released", () => {
    const s = markSpec("ing_6fdq2la", ["doc[0]/a"]);
    expect([s.method, s.path, s.resource, s.body]).toEqual(["POST", "/ingestions/ing_6fdq2la/marks", "/ingestions/ing_6fdq2la", { marks: ["doc[0]/a"] }]);
    s.released?.({} as never);
    expect(s.body).toEqual({});
  });
  test("an accepted send names its operation and generation and never a path", () => {
    expect(markSent({ operation: "op_zcfw5mcm", generation: 3, ingestion: "ing_6fdq2la" })).toBe("The marks were accepted in operation op_zcfw5mcm at generation 3. How the run ended shows under Progress.");
    expect(markSent(null)).toContain("generation ?");
  });
  test("choosing a value adds its path once, on its own line", () => {
    expect(withMark("", "doc[0]/a")).toBe("doc[0]/a\n");
    expect(withMark("doc[0]/a\n", "doc[0]/b")).toBe("doc[0]/a\ndoc[0]/b\n");
    expect(withMark("doc[0]/a", "doc[0]/b")).toBe("doc[0]/a\ndoc[0]/b\n");
    expect(withMark("doc[0]/a\n", "doc[0]/a")).toBe("doc[0]/a\n");
  });
});

describe("the values a review offers to mark (compilation §2.2 paths)", () => {
  const review = [
    "machine:", "  install:", "    disk: /dev/sda", "  files:", `    - content: "token = ${SENTINEL}"`, "  nodeLabels:", "    example.test/role: worker",
    "  ca:", "    key: !bwref example/ca-key", "  \"a|b\": hidden", "  port: 8080", "  empty: \"\"", "---", `second: ${SENTINEL}-2`,
  ].join("\n");
  test("string values are listed as doc[n] and a JSON Pointer, with ~ and / escaped", () => {
    expect(candidates(review).paths).toEqual([
      "doc[0]/machine/install/disk", "doc[0]/machine/files/0/content", "doc[0]/machine/nodeLabels/example.test~1role", "doc[1]/second",
    ]);
    expect(candidates("a~b: x").paths).toEqual(["doc[0]/a~0b"]);
  });
  test("an extracted value, a key holding a pipe, a number and an empty string are not offered", () => {
    const paths = candidates(review).paths.join("\n");
    for (const left of ["ca/key", "a|b", "port", "empty"]) expect(paths).not.toContain(left);
  });
  test("an alias is not followed", () => {
    expect(candidates("a: &x one\nb: *x").paths).toEqual(["doc[0]/a"]);
  });
  test("past the cap the rest is counted, not listed", () => {
    const text = Array.from({ length: MAX_CANDIDATES + 5 }, (_, i) => `k${i}: v`).join("\n");
    const c = candidates(text);
    expect([c.paths.length, c.more]).toEqual([MAX_CANDIDATES, 5]);
  });
  test("a value under a key with a line break is counted, not offered, so choosing never marks a sibling (local review)", () => {
    const c = candidates('"a\\n": one\na: two\n"b\\r": three');
    expect([c.paths, c.unaddressable]).toEqual([["doc[0]/a"], 2]);
    expect(withMark("", c.paths[0] ?? "")).toBe("doc[0]/a\n");
  });
  test("text that is not a document offers nothing and does not throw", () => {
    expect(candidates("").paths).toEqual([]);
    expect(candidates(": : :\n\t- [").paths).toBeInstanceOf(Array);
  });
});

describe("the marks panel", () => {
  const html = page(h(MarksPanel, { text: review(), send: () => {}, busy: false }));
  function review() {
    return `machine:\n  files:\n    - content: "token = ${SENTINEL}"\n`;
  }
  test("lists each value with its own button, a marks field that offers no assist, and Send marks", () => {
    expect(html).toContain("doc[0]/machine/files/0/content");
    expect(html).toContain("Mark as secret</button>");
    expect(html).toContain("Send marks");
    for (const a of ['autoComplete="off"', 'spellCheck="false"', 'autoCorrect="off"', 'autoCapitalize="off"']) expect(html).toContain(a);
  });
  test("shows the path of a value, never the value", () => {
    expect(html).not.toContain(SENTINEL);
  });
  test("says how many values it cannot mark, and says nothing when there are none", () => {
    expect(html).not.toContain("line break");
    expect(page(h(MarksPanel, { text: '"a\\n": one\na: two', send: () => {}, busy: false }))).toContain("Values under a key with a line break cannot be marked here: 1.");
  });
});

describe("the marks panel inside the open review (the seam with the review view)", () => {
  const text = `machine:\n  files:\n    - content: "token = ${SENTINEL}"\n`;
  const shown = (marking?: { send: (marks: string[]) => void; busy: boolean }) => page(h(Shown, { id: "ing_6fdq2la", text, close: () => {}, ...(marking === undefined ? {} : { marking }) }));
  test("the review carries the panel when marking is passed, and Close review stays after it", () => {
    const html = shown({ send: () => {}, busy: false });
    expect(html).toContain("Send marks");
    expect(html.indexOf("Send marks")).toBeLessThan(html.indexOf("Close review"));
  });
  test("without marking the review shows the text and Close review and no panel", () => {
    const html = shown();
    expect(html).not.toContain("Send marks");
    expect(html).toContain("Close review");
  });
  test("Close review is a plain button, so a busy send does not disable it", () => {
    expect(shown({ send: () => {}, busy: true })).not.toMatch(/<button[^>]*disabled[^>]*>Close review/);
  });
});

describe("a refused mark is named by rule and position, never a path (§11.3, PROT-03)", () => {
  test("the rule and position are kept, a path is not", () => {
    const r = refusalOf({ generation: 2, problem: problem({ rule: "mark-kind", position: 1, path: `doc[0]/${SENTINEL}`, detail: SENTINEL }) });
    expect(r).toEqual({ code: "validation-failed", rule: "mark-kind", position: 1 });
    expect(JSON.stringify(r)).not.toContain("BWSYNTH");
    expect(refusalText(r)).toBe("validation-failed, rule mark-kind, the mark at position 1 (the first is 0)");
  });
  test("a rule, code or position that is not a short token or a whole number is dropped, so nothing typed can come back", () => {
    const r = refusalOf({ problem: problem({ type: `urn:bronzeward:problem:${SENTINEL} x`, rule: `${SENTINEL} doc[0]/a`, position: "doc[0]/a" }) });
    expect(r).toEqual({});
    expect(refusalOf({ problem: problem({ position: -1 }) }).position).toBeUndefined();
    expect(refusalOf({ problem: problem({ position: 1.5 }) }).position).toBeUndefined();
    expect(refusalText({})).toBe("no problem named");
    expect(refusalOf(null)).toEqual({});
  });
  test("a problem without a position is named by its code alone", () => {
    expect(refusalText(refusalOf({ problem: { type: "urn:bronzeward:problem:dependency-unavailable", title: "x", status: 503 } }))).toBe("dependency-unavailable");
  });
  test("the event line carries them, and a mark-refused line never a path", () => {
    const line = eventLine({ id: "e1:5", type: "mark-refused", data: { generation: 2, problem: problem({ rule: "mark-kind", position: 0, path: SENTINEL }) } });
    expect(line).toBe("mark-refused (generation 2): validation-failed, rule mark-kind, the mark at position 0 (the first is 0)");
    expect(eventLine({ id: "e1:4", type: "marked", data: { generation: 2, paths: 1 } })).toBe("marked (generation 2)");
  });
  test("the 422 answer to a send shows the position and no path", () => {
    const out = renderToStaticMarkup(h(Refused, { problem: problem({ position: 2, path: SENTINEL, instance: "req_1" }), status: 422, verb: "saved" }));
    expect(out).toContain("The mark at position 2 (the first is 0) is refused.");
    expect(out).not.toContain("BWSYNTH");
    expect(renderToStaticMarkup(h(Refused, { problem: problem({ status: 409, position: 2 }), status: 409, verb: "saved" }))).not.toContain("position");
  });
});

describe("how the run of a mark ends (§11.3, PROT-03, PROT-05, PROT-06)", () => {
  const events = (gen: number, list: [string, unknown][]) => list.reduce((s, [type, data]) => onEvent(s, type, data), startRun(gen));
  const refusal = { generation: 2, problem: problem({ rule: "mark-kind", position: 0 }) };
  test("a pause after the marked event of this generation applies the mark", () => {
    const s = events(2, [["paused", { generation: 1 }], ["marked", { generation: 2, paths: 1 }], ["paused", { generation: 2 }]]);
    expect([s.settled, s.marked, s.refused]).toEqual(["paused", true, undefined]);
  });
  test("a mark-refused event then the pause settles it as refused, by rule and position", () => {
    const s = events(2, [["marked", { generation: 2 }], ["mark-refused", refusal], ["paused", { generation: 2 }]]);
    expect([s.settled, s.refused]).toEqual(["paused", { code: "validation-failed", rule: "mark-kind", position: 0 }]);
  });
  test("a refusal or a pause of an earlier run settles nothing", () => {
    const s = events(2, [["mark-refused", { generation: 1, problem: problem({ rule: "old", position: 0 }) }], ["paused", { generation: 1 }]]);
    expect([s.settled, s.refused, s.marked]).toEqual([undefined, undefined, undefined]);
  });
  test("a resume-failed after the marked event is not an applied mark", () => {
    const s = events(2, [["marked", { generation: 2 }], ["resume-failed", { code: "dependency-unavailable" }]]);
    expect([s.settled, s.marked]).toEqual(["resume-failed", true]);
  });
  describe("a continuation after the pause keeps what is known of the mark (the terminal drain reads the whole backlog)", () => {
    test("a refused mark stays refused after the continued event", () => {
      const s = events(2, [["marked", { generation: 2 }], ["mark-refused", refusal], ["paused", { generation: 2 }], ["continued", { generation: 3 }]]);
      expect([s.gen, s.settled, s.marked, s.refused]).toEqual([3, undefined, true, { code: "validation-failed", rule: "mark-kind", position: 0 }]);
    });
    test("a mark discarded by a takeover stays discarded after the continued event", () => {
      const s = events(3, [["marked", { generation: 2 }], ["taken-over", { generation: 3 }], ["paused", { generation: 3 }], ["continued", { generation: 4 }]]);
      expect([s.gen, s.replacedBy]).toEqual([4, 3]);
    });
    test("an applied mark, or no mark, adds nothing after the continued event", () => {
      const s = events(2, [["marked", { generation: 2 }], ["paused", { generation: 2 }], ["continued", { generation: 3 }]]);
      expect([s.marked, s.refused, s.replacedBy]).toEqual([undefined, undefined, undefined]);
    });
    test("the page still says the mark was refused when the import is released after the continuation", async () => {
      const s = events(2, [["marked", { generation: 2 }], ["mark-refused", refusal], ["paused", { generation: 2 }], ["continued", { generation: 3 }]]);
      const done = op({ state: "succeeded", result: { draft: "drf_2rmp457a", draftRevision: 5, importBaseRevision: "rev_1" } });
      const out = renderToStaticMarkup(h(ReadsProvider, { value: noReads }, await ended(unused, ing({ state: "released" }), done, "", NOW, "e1", s))).replace(/<[^>]+>/g, "");
      expect(out).toMatch(/^The mark was not applied: it was refused \(validation-failed, rule mark-kind/);
    });
  });
  test("the page says applied, refused by position and rule, or not applied by code, and never names a path", async () => {
    const applied = await ended(unused, ing(), op(), "paused", NOW, "e1", { marked: true });
    expect(applied).toMatch(/^The marks were applied: re-staged and paused again \(expires /);
    const refused = await ended(unused, ing(), op(), "paused", NOW, "e1", { marked: true, refused: { code: "validation-failed", rule: "mark-kind", position: 0 } });
    expect(refused).toMatch(/^The mark was not applied: it was refused \(validation-failed, rule mark-kind, the mark at position 0 \(the first is 0\)\)\. The import is paused again/);
    const noPosition = await ended(unused, ing(), op(), "paused", NOW, "e1", { marked: true, refused: { code: "dependency-unavailable" } });
    expect(noPosition).toContain("it was refused (dependency-unavailable)");
    expect(await ended(unused, ing(), op(), "paused", NOW, "e1")).toMatch(/^Paused for review/);
  });
  describe("a takeover after a mark that nothing ended fences it off (§11.3: the takeover resumes without it)", () => {
    // The watch restarts at the takeover's generation and reads the events from the start.
    test("a marked event with no pause or refusal at its generation, then a takeover, is a mark not applied", () => {
      const s = events(3, [["marked", { generation: 2 }], ["taken-over", { generation: 3 }], ["paused", { generation: 3 }]]);
      expect([s.settled, s.replacedBy]).toEqual(["paused", 3]);
    });
    test("a mark that was paused or refused at its generation is not fenced off by a later takeover", () => {
      expect(events(3, [["marked", { generation: 2 }], ["paused", { generation: 2 }], ["taken-over", { generation: 3 }]]).replacedBy).toBeUndefined();
      expect(events(3, [["marked", { generation: 2 }], ["mark-refused", { generation: 2 }], ["paused", { generation: 2 }], ["taken-over", { generation: 3 }]]).replacedBy).toBeUndefined();
      expect(events(2, [["taken-over", { generation: 2 }], ["paused", { generation: 2 }]]).replacedBy).toBeUndefined();
    });
    test("a mark sent after the takeover is a new run: the earlier one no longer counts", () => {
      const s = events(3, [["marked", { generation: 2 }], ["taken-over", { generation: 3 }], ["marked", { generation: 4 }]]);
      expect([s.replacedBy, s.marked]).toEqual([undefined, true]);
    });
    test("the page says the earlier mark was not applied, and that the import pauses again or was released", async () => {
      const paused = await ended(unused, ing(), op(), "paused", NOW, "e1", { replacedBy: 3 });
      expect(paused).toMatch(/^The earlier mark was not applied: a takeover at generation 3 resumed the import without it\. The import is paused again \(expires /);
      const flat = (n: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: noReads }, n)).replace(/<[^>]+>/g, "");
      const done = op({ state: "succeeded", result: { draft: "drf_2rmp457a", draftRevision: 5, importBaseRevision: "rev_1" } });
      expect(flat(await ended(unused, ing({ state: "released" }), done, "paused", NOW, "e1", { replacedBy: 3 }))).toMatch(
        /^The mark was not applied: a takeover at generation 3 resumed the import without it, and operation op_zcfw5mcm then released it\. Import ing_6fdq2la released/);
    });
    test("a takeover run that stalls and lapses is unknown, and the earlier mark is still said not applied (local review)", async () => {
      const out = String(await ended(unused, ing({ state: "held" }), op(), "", Date.parse("2026-11-01T07:06:00Z"), "e1", { replacedBy: 3 }));
      expect(out).toMatch(/^The earlier mark was not applied: a takeover at generation 3 resumed the import without it\. Its claim is held/);
      expect(out).toContain("the outcome is unknown");
    });
  });
  describe("an operation that ended after a mark that no pause applied (the CLI's markOutcome)", () => {
    const done = op({ state: "succeeded", result: { draft: "drf_2rmp457a", draftRevision: 5, importBaseRevision: "rev_1" } });
    const flat = (n: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: noReads }, n)).replace(/<[^>]+>/g, "").replaceAll("&#x27;", "'");
    const text = async (run: Parameters<typeof ended>[6], last = "paused") => flat(await ended(unused, ing({ state: "released" }), done, last, NOW, "e1", run));
    test("a success with no pause at the mark's generation says the mark was not applied", async () => {
      expect(await text({ marked: true }, "")).toMatch(/^The mark was not applied: operation op_zcfw5mcm succeeded without pausing at the mark's generation\. Import ing_6fdq2la released/);
    });
    test("a refusal or a resume-failed before the release is named, and the mark is not applied", async () => {
      expect(await text({ marked: true, refused: { code: "validation-failed", rule: "mark-kind", position: 0 } })).toMatch(
        /^The mark was not applied: it was refused \(validation-failed, rule mark-kind, the mark at position 0 \(the first is 0\)\), and operation op_zcfw5mcm then released the import without it\./);
      expect(await text({ marked: true }, "resume-failed")).toMatch(
        /^The mark was not applied: its staged input could not be decrypted \(resume-failed\), and operation op_zcfw5mcm then released the import without it\./);
    });
    test("a mark applied at its pause, then continued, and an import with no mark, add no such line", async () => {
      expect(await text({ marked: true })).not.toContain("not applied");
      expect(await text(undefined)).not.toContain("not applied");
    });
  });
  test("a lease that lapsed with none of these leaves the outcome unknown, never not applied", async () => {
    const out = await ended(unused, ing({ state: "held" }), op(), "", Date.parse("2026-11-01T07:06:00Z"), "e1", { marked: true });
    expect(String(out)).toContain("the outcome is unknown");
    expect(String(out)).not.toContain("not applied");
  });
});
