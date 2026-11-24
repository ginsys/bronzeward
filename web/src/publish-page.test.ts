// @vitest-environment happy-dom
// The publication page after a 412 (client contracts §8.3): it shows the draft's current state at once, with the
// refusal still shown, and keeps no ETag, so Publish sends nothing until the person reads the draft again.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { forgetETag, keptETag, NO_ETAG } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { PublishDraft } from "./publish.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DRF = "drf_k2pq3rstuvwxyz234567abcdef";

describe("a publication refused 412", () => {
  let box: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    box.remove();
  });

  test("shows the draft's revision and state without a press, keeps the refusal and no ETag, and Publish then sends nothing", async () => {
    forgetETag(DRF);
    let revision = 3;
    let etag = '"1"';
    let draftReads = 0;
    const reads = {
      client: {
        read: vi.fn(async (path: string) => {
          if (path !== `/drafts/${DRF}`) return { kind: "refused", status: 404, problem: null };
          draftReads++;
          return { kind: "ok", data: { id: DRF, title: "Add the office labels", state: "open", revision, cluster: "cl_a", entries: [] }, etag };
        }),
      },
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
      undropped: () => () => true,
      view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
    } as unknown as Reads;
    const writes = new Writes(() => "p");
    writes.client = { currentEpoch: "e0" } as unknown as Writes["client"];
    const sent: (string | undefined)[] = [];
    vi.spyOn(writes, "submit").mockImplementation(async (w) => {
      sent.push(w.spec.ifMatch);
      w.outcome = { kind: "refused", status: 412, problem: null, replayed: false };
      w.spec.settled?.(w.outcome, w);
      return w.outcome;
    });
    const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 40))));
    const click = (label: string) => act(async () => void [...box.querySelectorAll("button")].find((b) => b.textContent === label)!.click());
    await act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(PublishDraft, { query: `draft=${DRF}` }))))),
    );
    await settle();
    expect(keptETag(DRF, "e0")).toBe('"1"');
    // Another author saved the draft; the first press meets the refusal.
    revision = 5;
    etag = '"2"';
    const before = draftReads;
    await click("Publish");
    await settle();
    expect(sent).toEqual(['"1"']);
    expect(draftReads).toBe(before + 1);
    const text = box.textContent!;
    expect(text).toContain("Not published: someone changed this draft since this page read it.");
    expect(text).toContain("The draft is now at revision 5, state open.");
    // The re-read kept no ETag: Publish sends nothing, and the page says to read the draft again.
    expect(keptETag(DRF, "e0")).toBeUndefined();
    await click("Publish");
    await settle();
    expect(sent).toEqual(['"1"']);
    expect(box.querySelector('[data-testid="no-etag"]')!.textContent).toContain(NO_ETAG);
  });
});
