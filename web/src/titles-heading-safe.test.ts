// @vitest-environment happy-dom
// An operation's heading prints the server's kind and state only as safe text: a word with a control or format character, or
// over 300 code points, reads "(not shown)" (client contracts A8 rule 6, ginsys/bronzeward#1055). Every value is synthetic.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { OperationHeading } from "./titles.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdefg";
const BAD: [string, string][] = [
  ["a bidirectional control", "bad‮word"],
  ["more than 300 code points", "x".repeat(301)],
];
const operation = (kind: string, state: string) => ({ id: OP, kind, state, epoch: "ep_a", lastEvent: 1, subject: { draft: DRF, draftRevision: 3 }, createdBy: null, createdAt: "2026-11-19T10:00:00Z" });

let root: Root;
let box: HTMLElement;
const reads: Reads = {
  client: { read: async <T,>(path: string) => ((path === `/drafts/${DRF}` ? { kind: "ok", data: { id: DRF, title: "Add the office labels" } } : { kind: "refused", status: 404, problem: null }) as ReadResult<T>) },
  settle: (res, set, live, held) => {
    if (res.kind !== "ok" && held()) return true;
    if (live()) set(res);
    return false;
  },
  undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED",
};
const heading = async (kind: string, state: string) => {
  await act(async () => root.render(h(ReadsProvider, { value: reads }, h(OperationHeading, { id: OP, o: operation(kind, state) as never }))));
  await act(async () => void (await new Promise((r) => setTimeout(r, 60))));
  return box.textContent;
};

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the operation heading", () => {
  test("safe words are shown as they are, titled and untitled", async () => {
    expect(await heading("publish", "succeeded")).toBe("Operation: Publish of Add the office labels (publish, succeeded)");
    expect(await heading("something-new", "succeeded")).toBe("Operation op_k2pq…defg: something-new, succeeded");
  });
  test.each(BAD)("a state with %s reads (not shown), titled and untitled, and the raw value is absent", async (_, bad) => {
    const titled = await heading("publish", bad);
    expect(titled).toBe("Operation: Publish of Add the office labels (publish, (not shown))");
    const untitled = await heading("something-new", bad);
    expect(untitled).toBe("Operation op_k2pq…defg: something-new, (not shown)");
    expect(titled).not.toContain(bad);
    expect(untitled).not.toContain(bad);
  });
  test.each(BAD)("a kind with %s reads (not shown), and the raw value is absent", async (_, bad) => {
    const out = await heading(bad, "unresolved");
    expect(out).toBe("Operation op_k2pq…defg: (not shown), unresolved");
    expect(out).not.toContain(bad);
  });
});
