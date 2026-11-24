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

  // The page over a fake draft: `draft` is what the server holds now, `held` takes the next draft read aside until
  // it is released, and every Publish press is answered 412.
  const mount = async () => {
    forgetETag(DRF);
    const draft = { revision: 3, etag: '"1"' };
    const held: { release?: () => void; next: boolean } = { next: false };
    let draftReads = 0;
    const lives: boolean[] = [];
    const reads = {
      client: {
        read: vi.fn(async (path: string) => {
          if (path !== `/drafts/${DRF}`) return { kind: "refused", status: 404, problem: null };
          draftReads++;
          const answer = { kind: "ok", data: { id: DRF, title: "Add the office labels", state: "open", revision: draft.revision, cluster: "cl_a", entries: [] }, etag: draft.etag };
          if (!held.next) return answer;
          held.next = false;
          await new Promise<void>((r) => (held.release = r));
          return answer;
        }),
      },
      // What the shell is told of each read: the real one acts on a read (the offline banner) only while it is live.
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (lives.push(live()), live() && set(res), true),
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
    return { draft, held, sent, settle, click, lives, reads: () => draftReads };
  };

  test("shows the draft's revision and state without a press, keeps the refusal and no ETag, and Publish then sends nothing", async () => {
    const p = await mount();
    expect(keptETag(DRF, "e0")).toBe('"1"');
    // Another author saved the draft; the first press meets the refusal.
    p.draft.revision = 5;
    p.draft.etag = '"2"';
    const before = p.reads();
    await p.click("Publish");
    await p.settle();
    expect(p.sent).toEqual(['"1"']);
    expect(p.reads()).toBe(before + 1);
    const text = box.textContent!;
    expect(text).toContain("Not published: someone changed this draft since this page read it.");
    expect(text).toContain("The draft is now at revision 5, state open.");
    // The re-read kept no ETag: Publish sends nothing, and the page says to read the draft again.
    expect(keptETag(DRF, "e0")).toBeUndefined();
    await p.click("Publish");
    await p.settle();
    expect(p.sent).toEqual(['"1"']);
    expect(box.querySelector('[data-testid="no-etag"]')!.textContent).toContain(NO_ETAG);
  });

  test("a read still pending when a newer refusal is answered cannot overwrite it", async () => {
    const p = await mount();
    // The first refusal's read is slow.
    p.draft.revision = 5;
    p.draft.etag = '"2"';
    p.held.next = true;
    await p.click("Publish");
    await p.settle();
    expect(box.textContent).not.toContain("The draft is now at revision");
    // The person reads the draft again, then publishes again: the second refusal's read answers at once.
    p.draft.revision = 7;
    p.draft.etag = '"3"';
    await p.click("Read the draft again");
    await p.settle();
    await p.click("Publish");
    await p.settle();
    expect(p.sent).toEqual(['"1"', '"3"']);
    expect(box.textContent).toContain("The draft is now at revision 7, state open.");
    // The slow read answers last, with the older revision.
    await act(async () => p.held.release!());
    await p.settle();
    expect(box.textContent).toContain("The draft is now at revision 7, state open.");
    expect(box.textContent).not.toContain("revision 5");
    // The shell was told the slow read is not live, so it cannot touch the connection state either.
    expect(p.lives.at(-1)).toBe(false);
  });

  test("a read answered after the page is gone is not live", async () => {
    const p = await mount();
    p.held.next = true;
    await p.click("Publish");
    await p.settle();
    const before = p.lives.length;
    await act(async () => root.unmount());
    await act(async () => p.held.release!());
    await p.settle();
    expect(p.lives.slice(before)).toEqual([false]);
    root = createRoot(box);
  });
});
