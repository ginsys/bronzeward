// @vitest-environment happy-dom
// The imported configuration page shows the parsed fields by default, and the text on a switch (ginsys/bronzeward#1270,
// docs/spec/configuration-editing.md §5 and §6). The protected-document rules stay: read on request, memory only, dropped
// on Close, pagehide and sign-out. Every value is synthetic.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { BaseDocument } from "./claim.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// The schema is a lazy chunk, and a loaded runner takes longer to fetch it than a quiet one. Here it always arrives late, so no case
// can pass on a fixed wait: each waits for the state it asserts (`until`).
vi.mock("./talos-schema.ts", async (importOriginal) => {
  const real = await importOriginal<typeof import("./talos-schema.ts")>();
  return { ...real, schemaFor: async (contract: string | null | undefined) => (await new Promise((r) => setTimeout(r, 150)), real.schemaFor(contract)) };
});

const IBR = "ibr_k2pq3rstuvwxyz234567abcdef";
const TEXT = [
  "# SYNTH-COMMENT",
  "version: v1alpha1",
  "machine:",
  "  type: controlplane",
  "  token: !bwref reg/machine-token",
  "  ca:",
  "    crt: SYNTH-CRT-VALUE",
  "  network:",
  "    hostname: SYNTH-HOST",
  "",
].join("\n");

describe("the imported configuration page, parsed", () => {
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

  // `served` maps a path to its body; any other path is a 404. `calls` lists every read, in order.
  const serve = (document_: string, contract: string | null = "v1.14") => {
    const calls: string[] = [];
    const served: Record<string, unknown> = {
      [`/import-base-revisions/${IBR}/document`]: { id: IBR, machine: "mch_a", document: document_ },
      "/machines/mch_a": { id: "mch_a", cluster: "cl_a", name: "cp1", role: "controlplane", hardware: {} },
      ...(contract === null ? {} : { "/clusters/cl_a": { id: "cl_a", name: "office", endpoint: "https://10.5.0.1:6443", contract, talosClusterId: "tcid" } }),
    };
    const reads = {
      client: {
        read: vi.fn(async (path: string) => {
          calls.push(path);
          return path in served ? { kind: "ok", data: served[path], etag: undefined } : { kind: "refused", status: 404, problem: null };
        }),
      },
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
      undropped: () => () => true,
      view: "", again: 0, regained: 0, go: () => {},
      failure: () => null,
    } as unknown as Reads;
    return { reads, calls };
  };
  const mount = (reads: Reads) =>
    act(async () =>
      root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: reads }, h(BaseDocument, { id: IBR, machine: "mch_a" }))))),
    );
  const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 60))));
  const buttons = () => [...box.querySelectorAll("button")];
  const click = (label: string) => act(async () => void buttons().find((b) => b.textContent === label)!.click());
  const filled = () => buttons().filter((b) => !b.classList.contains("outline")).map((b) => b.textContent);
  const textarea = () => box.querySelector<HTMLTextAreaElement>('[data-testid="review-text"]');
  const fields = () => box.querySelector<HTMLElement>('[data-testid="config-fields"]');
  // Waits for a state, not for a time: the schema is a lazy chunk, and a loaded machine takes longer than a quiet one to fetch it.
  const until = async (done: () => boolean) => {
    for (let i = 0; i < 200 && !done(); i++) await act(async () => void (await new Promise((r) => setTimeout(r, 25))));
    expect(done()).toBe(true);
  };
  // The text is open and the view has left "Reading the fields.": the parsed fields or the text are drawn.
  const shown = () => fields() !== null || textarea() !== null;
  const open = async (reads: Reads) => {
    await mount(reads);
    await click("Show imported configuration");
    await until(shown);
  };

  test("nothing about the cluster is read, and no schema is loaded, until the text is opened", async () => {
    const { reads, calls } = serve(TEXT);
    await mount(reads);
    await settled();
    expect(calls.some((c) => c.startsWith("/clusters/"))).toBe(false);
    expect(calls.some((c) => c.endsWith("/document"))).toBe(false);
    await click("Show imported configuration");
    await until(() => calls.includes("/clusters/cl_a"));
  });

  test("opened, the default view is the parsed fields: no text area, comments hidden, the notice and the one filled button as before", async () => {
    await open(serve(TEXT).reads);
    expect(textarea()).toBeNull();
    expect(fields()).not.toBeNull();
    expect([...fields()!.querySelectorAll("h4, h5, h6")].map((e) => e.textContent)).toEqual(["Document 1: v1alpha1 configuration", "machine", "ca", "Other settings"]);
    expect(box.textContent).not.toContain("SYNTH-COMMENT");
    expect(box.querySelector('[data-testid="review-notice"]')!.textContent).toContain("PROTECTED DOCUMENT. This may contain secrets nothing has recognised yet.");
    expect(box.querySelector('[data-testid="ca-key-note"]')).not.toBeNull();
    expect(box.querySelector('[data-testid="hide-note"]')).not.toBeNull();
    expect(filled()).toEqual(["Close imported configuration"]);
    expect(box.querySelectorAll("input, select")).toHaveLength(0);
  });

  test("a reference shows as its name, and Reference is not a value", async () => {
    await open(serve(TEXT).reads);
    expect(fields()!.textContent).toContain("Reference reg/machine-token");
  });

  test("Show raw text is a secondary button in the panel band; it shows the whole stored text with its comments, and Show parsed fields comes back, with no new read", async () => {
    const { reads, calls } = serve(TEXT);
    await open(reads);
    const band = box.querySelector("section.panel > .panel-head")!;
    const sw = [...band.querySelectorAll("button")];
    expect(sw.map((b) => [b.textContent, b.className])).toEqual([["Show raw text", "outline"]]);
    const before = calls.length;
    await click("Show raw text");
    expect(fields()).toBeNull();
    expect(textarea()!.value).toBe(TEXT);
    expect(band.querySelector("button")!.textContent).toBe("Show parsed fields");
    expect(filled()).toEqual(["Close imported configuration"]);
    await click("Show parsed fields");
    expect(fields()).not.toBeNull();
    expect(textarea()).toBeNull();
    expect(calls.length).toBe(before);
    expect(calls.filter((c) => c.endsWith("/document"))).toHaveLength(1);
  });

  test("a contract with no schema shows the text under one sentence, with no switch", async () => {
    await open(serve(TEXT, "v1.15").reads);
    expect(fields()).toBeNull();
    expect(box.querySelector('[data-testid="no-fields"]')!.textContent).toBe("There are no fields for this cluster's contract: the console holds the Talos schema of other versions only. The text is shown.");
    expect(textarea()!.value).toBe(TEXT);
    expect(buttons().map((b) => b.textContent)).toEqual(["Close imported configuration"]);
  });

  test("a cluster whose contract cannot be read shows the text under one sentence", async () => {
    await open(serve(TEXT, null).reads);
    expect(box.querySelector('[data-testid="no-fields"]')!.textContent).toBe("The cluster's contract could not be read, so there are no fields. The text is shown.");
    expect(textarea()!.value).toBe(TEXT);
  });

  test("text that does not parse shows the text under a sentence that names the code and the line and quotes none of it", async () => {
    await open(serve("a: [1, 2\nb: SYNTH-SECRET\n").reads);
    const s = box.querySelector('[data-testid="no-fields"]')!.textContent!;
    expect(s).toMatch(/^The text did not parse as YAML \([A-Z_]+, line \d+\), so there are no fields\. The text is shown\.$/);
    expect(s).not.toContain("SYNTH-SECRET");
    expect(textarea()).not.toBeNull();
  });

  test("closing drops the fields and the values, and the next opening is the parsed view again, also after the text was shown", async () => {
    const { reads } = serve(TEXT);
    await open(reads);
    await click("Show raw text");
    await click("Close imported configuration");
    expect(fields()).toBeNull();
    expect(textarea()).toBeNull();
    for (const v of ["SYNTH-CRT-VALUE", "SYNTH-HOST", "controlplane"]) expect(box.textContent).not.toContain(v);
    await click("Show imported configuration");
    await until(shown);
    expect(fields()).not.toBeNull();
    expect(textarea()).toBeNull();
  });

  test("pagehide and leaving the page drop the fields and the values", async () => {
    const { reads } = serve(TEXT);
    await open(reads);
    await act(async () => void window.dispatchEvent(new Event("pagehide")));
    expect(fields()).toBeNull();
    for (const v of ["SYNTH-CRT-VALUE", "SYNTH-HOST"]) expect(box.textContent).not.toContain(v);
    expect(buttons().some((b) => b.textContent === "Show imported configuration")).toBe(true);
    await open(reads);
    await act(async () => root.unmount());
    expect(box.textContent).toBe("");
    root = createRoot(box);
  });

  test("the fields hold focus after the opening button is gone, and no value is in an attribute", async () => {
    await open(serve(TEXT).reads);
    expect(document.activeElement).toBe(fields());
    const attrs = [...box.querySelectorAll("*")].flatMap((e) => [...e.attributes].map((a) => a.value)).join("\n");
    for (const v of ["SYNTH-CRT-VALUE", "SYNTH-HOST", "reg/machine-token", "controlplane"]) expect(attrs).not.toContain(v);
  });
});
