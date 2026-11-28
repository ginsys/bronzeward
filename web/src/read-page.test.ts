// @vitest-environment happy-dom
// The read page of an import (ginsys/bronzeward#1070): each import entry of the draft page offers the machine's
// imported configuration on request, holds the text in memory only, and against a server that still refuses an import
// read from a machine shows the refusal in the console's words and offers nothing broken.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { BaseDocument } from "./claim.tsx";
import { DraftItem, forgetETag } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Refused } from "./states.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const IBR = "ibr_k2pq3rstuvwxyz234567abcdef";
const TEXT = "machine:\n  hostname: synthetic-cp-1\n";
const refusal = (status: number, code: string, rule: string) => ({ kind: "refused", status, problem: { type: `urn:bronzeward:problem:${code}`, title: code, status, rule } });

describe("an import entry of the draft page and its imported configuration", () => {
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
  // `answer` is what the server says to the document read; `reads` counts the document reads made.
  const reading = (answer: unknown) => {
    const documents = vi.fn(async () => answer);
    const reads = {
      client: { read: vi.fn(async (path: string) => (path.endsWith("/document") ? documents() : { kind: "refused", status: 404, problem: null })) },
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
      undropped: () => () => true,
      view: "", again: 0, regained: 0, go: () => {},
      failure: (res: { status: number; problem: never }) => h(Refused, { problem: res.problem, status: res.status, verb: "read" }),
    } as unknown as Reads;
    return { reads, documents };
  };
  const mount = (reads: Reads, page: ReturnType<typeof h>) =>
    act(async () =>
      root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: reads }, page)))),
    );
  const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 30))));
  const click = (label: string) => act(async () => void [...box.querySelectorAll("button")].find((b) => b.textContent === label)!.click());
  const textarea = () => box.querySelector<HTMLTextAreaElement>('[data-testid="review-text"]');

  // The draft page offers one Open link per import entry, to the page that shows the text on request (the layout design, ginsys/bronzeward#1255):
  // the draft page itself mounts no protected document, so it reads no text and shows no notice, however many imports it holds.
  test("the draft page offers one Open link per import entry and reads no text, and shows no protected document", async () => {
    const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
    forgetETag(DRF);
    const draft = { id: DRF, title: "Onboard", state: "open", revision: 3, cluster: "cl_a", entries: [
      { kind: "import-base", machine: "mch_a", revision: IBR },
      { kind: "import-base", machine: "mch_b", revision: "ibr_b2pq3rstuvwxyz234567abcdef" },
    ] };
    const { reads, documents } = reading({ kind: "ok", data: { id: IBR, machine: "mch_a", document: TEXT }, etag: undefined });
    const draftRead = reads.client.read;
    (reads.client as { read: unknown }).read = vi.fn(async (path: string) => (path === `/drafts/${DRF}` ? { kind: "ok", data: draft, etag: '"1"' } : draftRead(path)));
    await mount(reads, h(DraftItem, { id: DRF }));
    await settled();
    expect([...box.querySelectorAll("a.button")].filter((a) => a.textContent === "Open").map((a) => a.getAttribute("href"))).toEqual([`/import-base-revisions/${IBR}`, "/import-base-revisions/ibr_b2pq3rstuvwxyz234567abcdef"]);
    expect([...box.querySelectorAll("button")].filter((b) => b.textContent === "Show imported configuration")).toHaveLength(0);
    expect(box.querySelectorAll('section[aria-label="Protected document"]')).toHaveLength(0);
    expect(documents).not.toHaveBeenCalled();
    expect(textarea()).toBeNull();
  });

  test("pressed, the control shows the text and says a value found in it cannot be marked here, with the page where it can", async () => {
    const { reads, documents } = reading({ kind: "ok", data: { id: IBR, machine: "mch_a", document: TEXT }, etag: undefined });
    await mount(reads, h(BaseDocument, { id: IBR, machine: "mch_a" }));
    await click("Show imported configuration");
    await settled();
    expect(documents).toHaveBeenCalledTimes(1);
    expect(textarea()!.value).toBe(TEXT);
    const note = box.querySelector('[data-testid="hide-note"]')!;
    expect(note.textContent).toContain("cannot be marked on this import");
    expect(note.textContent).toContain("This copy stays stored and readable as it is.");
    expect(note.querySelector("a")!.getAttribute("href")).toBe("/ingestions/new?machine=mch_a");
  });

  test("the text is memory only: closing it, and leaving the page, drop it", async () => {
    const { reads } = reading({ kind: "ok", data: { id: IBR, machine: "mch_a", document: TEXT }, etag: undefined });
    await mount(reads, h(BaseDocument, { id: IBR, machine: "mch_a" }));
    await click("Show imported configuration");
    await settled();
    await click("Close imported configuration");
    expect(textarea()).toBeNull();
    expect(box.textContent).not.toContain("synthetic-cp-1");
    await click("Show imported configuration");
    await settled();
    expect(textarea()).not.toBeNull();
    await act(async () => void window.dispatchEvent(new Event("pagehide")));
    expect(textarea()).toBeNull();
    expect(box.textContent).not.toContain("synthetic-cp-1");
    expect([...box.querySelectorAll("button")].some((b) => b.textContent === "Show imported configuration")).toBe(true);
  });

  test.each([
    ["a server that still refuses an import read from a machine (409 import-not-reviewed)", refusal(409, "conflict", "import-not-reviewed"), "Nothing needs doing"],
    ["a file import read by another author (403 not-the-importer)", refusal(403, "forbidden", "not-the-importer"), "Ask that person."],
  ])("%s is shown in words, with no text, no code and no marking note, and the control stays", async (_, answer, next) => {
    const { reads } = reading(answer);
    await mount(reads, h(BaseDocument, { id: IBR, machine: "mch_a" }));
    await click("Show imported configuration");
    await settled();
    expect(textarea()).toBeNull();
    expect(box.querySelector('[data-testid="hide-note"]')).toBeNull();
    expect(box.textContent).toContain("The server did not show you this import's text.");
    expect(box.textContent).toContain(next);
    expect(box.textContent).toContain("ask whoever runs the server whether it is up to date");
    // The code stays only in the reference block for a bug report, which is closed and outside the sentence.
    const sentence = box.cloneNode(true) as HTMLElement;
    sentence.querySelectorAll('[data-testid="request-ref"]').forEach((e) => e.remove());
    // Codes and rules both: `conflict` and `forbidden` are plain words nowhere in these sentences.
    for (const code of ["import-not-reviewed", "not-the-importer", "conflict", "forbidden"]) expect(sentence.textContent!.toLowerCase()).not.toContain(code);
    expect(box.querySelector('[data-testid="request-ref"]')!.textContent).toContain("Rule:");
    expect([...box.querySelectorAll("button")].some((b) => b.textContent === "Show imported configuration")).toBe(true);
  });
});
