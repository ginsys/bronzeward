// What the draft editor's first case adds to a failed absence read of the name field, so that the next failure says
// what the pane showed and when (ginsys/bronzeward#1121).
import { describe, expect, it } from "vitest";
import { failedWithPane, paneTraceText, type Sample } from "./pane-trace.ts";

const at = (ms: number, over: Partial<Sample> = {}): Sample => ({ ms, field: true, which: "name ", remove: false, listed: false, sections: 1, same: true, ...over });
const SAVED = { field: false, which: "", remove: true, listed: true };

describe("paneTraceText", () => {
  it("names each change of what the pane showed and leaves out a sample that repeats the one before", () => {
    expect(paneTraceText([at(0), at(40), at(812, SAVED), at(900, SAVED)])).toBe(
      '0 ms name field yes (label "name "), Remove no, entry listed no, 1 Fragment section, same pane element; ' +
        "812 ms name field no, Remove yes, entry listed yes, 1 Fragment section, same pane element",
    );
  });

  it("says a second section and a replaced pane element", () => {
    expect(paneTraceText([at(0), at(70, { ...SAVED, sections: 2, same: false })])).toBe(
      '0 ms name field yes (label "name "), Remove no, entry listed no, 1 Fragment section, same pane element; ' +
        "70 ms name field no, Remove yes, entry listed yes, 2 Fragment sections, replaced pane element",
    );
  });

  it("names the label a matching field was found in, whichever it was", () => {
    expect(paneTraceText([at(5, { which: "Document" })])).toBe('5 ms name field yes (label "Document"), Remove no, entry listed no, 1 Fragment section, same pane element');
  });

  it("prints a note on its own line, and the state first where the note's sample differs from the one before", () => {
    expect(paneTraceText([at(0), at(30, { note: "remove waited" }), at(35, { ...SAVED, note: "absence read" })])).toBe(
      '0 ms name field yes (label "name "), Remove no, entry listed no, 1 Fragment section, same pane element; ' +
        "30 ms remove waited; " +
        "35 ms name field no, Remove yes, entry listed yes, 1 Fragment section, same pane element; " +
        "35 ms absence read",
    );
  });

  it("keeps the last twelve lines and counts the earlier ones left out", () => {
    const many = Array.from({ length: 15 }, (_, i) => at(i * 10, { sections: i + 1 }));
    const text = paneTraceText(many);
    expect(text.startsWith("3 earlier lines left out; 30 ms ")).toBe(true);
    expect(text.split("; ")).toHaveLength(13);
    expect(text.endsWith("140 ms name field yes (label \"name \"), Remove no, entry listed no, 15 Fragment sections, same pane element")).toBe(true);
  });

  it("says so where nothing was sampled", () => {
    expect(paneTraceText([])).toBe("no sample taken");
  });
});

describe("failedWithPane", () => {
  const first = new Error("the name field of a new entry in the pane after the save is absent");

  it("adds the trace to the failure and keeps the failure as its cause", async () => {
    const e = await failedWithPane(first, async () => [at(0), at(812, SAVED)]);
    expect(e.message).toBe(`${first.message}; pane trace: ${paneTraceText([at(0), at(812, SAVED)])}`);
    expect(e.cause).toBe(first);
  });

  it("keeps the original failure where the read of the trace fails", async () => {
    const e = await failedWithPane(first, () => Promise.reject(new Error("no such window")));
    expect(e.message).toBe(first.message);
    expect(e.cause).toBe(first);
  });

  it("takes the text of a failure that is not an Error", async () => {
    const e = await failedWithPane("plain", async () => []);
    expect(e.message).toBe("plain; pane trace: no sample taken");
    expect(e.cause).toBe("plain");
  });
});
