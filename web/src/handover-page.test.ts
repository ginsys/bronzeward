// @vitest-environment happy-dom
// The handover button on a mounted draft page (ginsys/bronzeward#1066): a press while the draft editor holds
// unsaved text sends nothing and says why, because the press publishes only what is stored.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { DraftItem, EDITOR_FIRST, forgetETag, keptETag } from "./drafts.tsx";
import { unsaved, WritesProvider } from "./forms.tsx";
import { Handover } from "./handover.tsx";
import { LINES, NOT_PUBLISHED, UNSAVED } from "./handover.ts";
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
      root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: noReads }, h(Handover, { draft: "drf_1", machines: ["mch_a"], etag: () => '"1"', seeChange: () => undefined, written: () => {} }))))),
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
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(Handover, { draft: "drf_1", machines: ["mch_a"], etag: () => (generation++, '"1"'), seeChange: () => undefined, written: () => {} }))))),
    );
    await press();
    await act(async () => void (await new Promise((r) => setTimeout(r, 20))));
    expect(submit).not.toHaveBeenCalled();
    expect(box.querySelector('[data-testid="handover-line"]')?.textContent).toContain(LINES.noRead);
  });

  test("a publication refused 412 offers Show their change, and the press after it carries the new ETag", async () => {
    // On the real draft page (DraftItem): the page keeps one ETag per draft read, and after the refusal it keeps a
    // newer one only through Show their change, so the next press must send what the page holds then. Unsaved
    // text in the editor holds that reread back, as it does for the discard's 412.
    const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
    forgetETag(DRF);
    let served = '"1"';
    let draftReads = 0;
    const draft = { id: DRF, title: "Add the office labels", state: "open", revision: 3, cluster: "cl_a", entries: [{ kind: "import-base", machine: "mch_a", revision: "ibr_a" }] };
    const reads = {
      client: {
        read: vi.fn(async (path: string) => {
          if (path !== `/drafts/${DRF}`) return { kind: "refused", status: 404, problem: null };
          draftReads++;
          return { kind: "ok", data: draft, etag: served };
        }),
      },
      settle: (res: { kind: string }, set: (r: unknown) => void, live: () => boolean, held: () => boolean) => {
        if (res.kind !== "ok" && held()) return true;
        if (live()) set(res);
        return false;
      },
      undropped: () => () => true,
      view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
    } as unknown as Reads;
    const writes = new Writes(() => "p");
    const sent: (string | undefined)[] = [];
    const epochs: (string | undefined)[] = [];
    writes.client = { currentEpoch: "e0" } as unknown as Writes["client"];
    vi.spyOn(writes, "submit").mockImplementation(async (w) => {
      sent.push(w.spec.ifMatch);
      epochs.push(w.spec.epoch);
      // The first publication meets a draft changed since; the second is accepted without naming an operation.
      w.outcome = sent.length === 1 ? { kind: "refused", status: 412, problem: null, replayed: false } : { kind: "done", status: 202, data: {}, replayed: false };
      return w.outcome;
    });
    const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 40))));
    const click = (label: string) => act(async () => void [...box.querySelectorAll("button")].find((b) => b.textContent === label)!.click());
    await act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(DraftItem, { id: DRF }))))),
    );
    await settled();
    expect(keptETag(DRF, "e0")).toBe('"1"');
    await click("Start managing these machines");
    await settled();
    expect(box.querySelector('[data-testid="handover-line"]')!.textContent).toContain(NOT_PUBLISHED);
    expect(sent).toEqual(['"1"']);
    // The draft changed on the server. With unsaved text in the editor, Show their change reads nothing and names why.
    served = '"2"';
    unsaved.add("the fragment editor");
    const before = draftReads;
    await click("Show their change");
    await settled();
    expect(box.querySelector('[data-testid="handover-note"]')!.textContent).toBe(EDITOR_FIRST);
    expect(draftReads).toBe(before);
    expect(keptETag(DRF, "e0")).toBe('"1"');
    // Without it, the page reads the draft again, keeps that read's ETag, and the next press sends it.
    unsaved.clear();
    await click("Show their change");
    await settled();
    expect(keptETag(DRF, "e0")).toBe('"2"');
    await click("Start managing these machines");
    await settled();
    expect(sent).toEqual(['"1"', '"2"']);
    expect(epochs).toEqual(["e0", "e0"]);
  });

  test("with nothing unsaved the press starts: no unsaved notice, and the draft is read", async () => {
    await mount();
    await press();
    expect(box.querySelector('[data-testid="handover-unsaved"]')).toBeNull();
    expect(read).toHaveBeenCalled();
  });
});
