// @vitest-environment happy-dom
// The handover button on a mounted draft page (ginsys/bronzeward#1066): a press while the draft editor holds
// unsaved text sends nothing and says why, because the press publishes only what is stored.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { unsaved, WritesProvider } from "./forms.tsx";
import { Handover } from "./handover.tsx";
import { LINES, UNSAVED } from "./handover.ts";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const read = vi.fn(() => new Promise(() => {}));
const noReads = { client: { read }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null } as unknown as Reads;

describe("the handover button with unsaved text in the draft editor", () => {
  let box: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
    read.mockClear();
  });
  afterEach(async () => {
    unsaved.clear();
    await act(async () => root.unmount());
    box.remove();
  });
  const mount = () =>
    act(async () =>
      root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: noReads }, h(Handover, { draft: "drf_1", machines: ["mch_a"], etag: () => '"1"', written: () => {} }))))),
    );
  const press = () => act(async () => void box.querySelector("button")!.click());

  test("a press with unsaved text reads nothing, sends nothing and says to save or discard it", async () => {
    unsaved.add("the draft editor");
    await mount();
    await press();
    expect(box.querySelector('[data-testid="handover-unsaved"]')!.textContent).toBe(UNSAVED);
    expect(box.querySelectorAll('[data-testid="handover-line"]')).toHaveLength(0);
    expect(read).not.toHaveBeenCalled();
  });

  test("a press the shell invalidated (an epoch change) reads nothing it acts on and sends nothing, though each later read is itself current", async () => {
    // The shell invalidates the reads a press began with while the page stays (it retains unsaved text). A read
    // that starts afterwards is current by its own snapshot, so the press itself must stop: a page barred from
    // writes sends no plan or approval. The invalidation lands here after the press began and before its first read.
    let generation = 0;
    const answered = vi.fn(async () => ({ kind: "ok", data: { state: "open" }, etag: '"1"' }));
    const reads = {
      client: { read: answered },
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
      undropped: () => {
        const at = generation;
        return () => at === generation;
      },
      view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
    } as unknown as Reads;
    const writes = new Writes(() => "p");
    const submit = vi.spyOn(writes, "submit");
    await act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(Handover, { draft: "drf_1", machines: ["mch_a"], etag: () => (generation++, '"1"'), written: () => {} }))))),
    );
    await press();
    await act(async () => void (await new Promise((r) => setTimeout(r, 20))));
    expect(submit).not.toHaveBeenCalled();
    expect(box.querySelector('[data-testid="handover-line"]')?.textContent).toContain(LINES.noRead);
  });

  test("with nothing unsaved the press starts: no unsaved notice, and the draft is read", async () => {
    await mount();
    await press();
    expect(box.querySelector('[data-testid="handover-unsaved"]')).toBeNull();
    expect(read).toHaveBeenCalled();
  });
});
