// @vitest-environment happy-dom
// The handover button on a mounted draft page (ginsys/bronzeward#1066): a press while the draft editor holds
// unsaved text sends nothing and says why, because the press publishes only what is stored.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { unsaved, WritesProvider } from "./forms.tsx";
import { Handover } from "./handover.tsx";
import { UNSAVED } from "./handover.ts";
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

  test("with nothing unsaved the press starts: no unsaved notice, and the draft is read", async () => {
    await mount();
    await press();
    expect(box.querySelector('[data-testid="handover-unsaved"]')).toBeNull();
    expect(read).toHaveBeenCalled();
  });
});
