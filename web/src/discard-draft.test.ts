// @vitest-environment happy-dom
// Discard draft on a mounted draft page (ginsys/bronzeward#1241): the page says what the press records, and one press sends the
// discard, with no browser dialog (operator experience §5.9: it is confirmed by reading what it records and one press).

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { DiscardDraft, forgetETag, keepETag } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { ShellContext } from "./shell.tsx";
import { DISCARD_SENTENCE } from "./words.ts";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DRF = "drf_k2pq3rstuvwxyz234567abcdef";

describe("Discard draft on a draft's page", () => {
  let box: HTMLDivElement;
  let root: Root;
  let submit: ReturnType<typeof vi.spyOn>;
  const asked = vi.fn(() => false);
  beforeEach(async () => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
    asked.mockClear();
    vi.stubGlobal("confirm", asked);
    const writes = new Writes(() => "p");
    submit = vi.spyOn(writes, "submit").mockImplementation(async () => ({ kind: "unknown" }) as never);
    forgetETag(DRF);
    keepETag(DRF, '"5-t"', undefined);
    await act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(DiscardDraft, { draft: { id: DRF, cluster: "cl_a", state: "open", entries: [] } as never, written: () => {}, seeChange: () => {} })))),
    );
  });
  afterEach(async () => {
    vi.unstubAllGlobals();
    await act(async () => root.unmount());
    box.remove();
  });
  const press = () => [...box.querySelectorAll("button")].find((b) => b.textContent === "Discard draft")!;

  test("the page says what the press records, above the button: the draft can no longer be published", () => {
    const sentence = box.querySelector('[data-testid="discard-sentence"]')!;
    expect(sentence.textContent).toBe(DISCARD_SENTENCE);
    expect(DISCARD_SENTENCE).toMatch(/can no longer be published/);
    expect(sentence.compareDocumentPosition(press()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test("one press sends the discard under the draft's last ETag, and no dialog is asked", async () => {
    await act(async () => void press().click());
    expect(asked).not.toHaveBeenCalled();
    expect(submit).toHaveBeenCalledTimes(1);
    const spec = (submit.mock.calls[0]![0] as { spec: Record<string, unknown> }).spec;
    expect(spec).toMatchObject({ method: "POST", path: `/drafts/${DRF}/discard`, body: {}, ifMatch: '"5-t"', resource: `/drafts/${DRF}` });
  });
});
