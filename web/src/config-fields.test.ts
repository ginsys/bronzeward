// @vitest-environment happy-dom
// The read-only tree of the parsed view (ginsys/bronzeward#1270, docs/spec/configuration-editing.md §5 and §7): headings
// in order, help text tied to its item, text only, and no value in any attribute. Every value is synthetic.

import { readFileSync } from "node:fs";
import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { ConfigFields } from "./config-fields.tsx";
import { buildConfigView, type DocView } from "./config-tree.ts";
import { schemaFor } from "./talos-schema.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const TEXT = [
  "# SYNTH-COMMENT",
  "version: v1alpha1",
  "machine:",
  "  type: controlplane",
  "  token: !bwref reg/machine-token",
  "  certSANs:",
  "    - cp1.example.test",
  "  ca:",
  "    crt: SYNTH-CRT-VALUE",
  "  network:",
  "    hostname: SYNTH-HOST",
  "cluster:",
  "  etcd:",
  "    extraArgs:",
  "      listen-metrics-urls: SYNTH-URL",
  "",
].join("\n");

const docsOf = async (text: string): Promise<readonly DocView[]> => {
  const s = await schemaFor("v1.14");
  if (!s.form) throw new Error("no schema");
  const v = buildConfigView(text, s);
  if (!v.ok) throw new Error("parse error");
  return v.docs;
};

describe("the parsed fields", () => {
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
  const draw = async (text: string) => {
    const docs = await docsOf(text);
    await act(async () => root.render(h(ConfigFields, { docs, label: "Imported configuration of cp1, parsed" })));
  };

  test("the headings are real headings in order, never skipping a level: a document is an h4, its groups h5 and h6", async () => {
    await draw(TEXT);
    const levels = [...box.querySelectorAll("h1,h2,h3,h4,h5,h6")].map((e) => [e.tagName, e.textContent]);
    expect(levels).toEqual([
      ["H4", "Document 1: v1alpha1 configuration"],
      ["H5", "machine"],
      ["H6", "ca"],
      ["H5", "cluster"],
      ["H6", "etcd"],
      ["H5", "Other settings"],
    ]);
    // No heading is more than one level deeper than the one before it.
    const n = [...box.querySelectorAll("h1,h2,h3,h4,h5,h6")].map((e) => Number(e.tagName.slice(1)));
    n.forEach((lvl, i) => expect(lvl - (n[i - 1] ?? 3)).toBeLessThanOrEqual(1));
  });

  test("every item is text: no input, textarea, select or button is drawn", async () => {
    await draw(TEXT);
    expect(box.querySelectorAll("input, textarea, select, button, [contenteditable]")).toHaveLength(0);
    expect(box.textContent).toContain("SYNTH-CRT-VALUE");
  });

  test("a field's help text is tied to its value, a reference is its name, a list is rows and a map is rows of key and value", async () => {
    await draw(TEXT);
    const typeDt = [...box.querySelectorAll("dt")].find((e) => e.textContent === "type")!;
    const value = typeDt.nextElementSibling as HTMLElement;
    expect(value.textContent).toBe("controlplane");
    const help = box.querySelector(`#${CSS.escape(value.getAttribute("aria-describedby")!)}`)!;
    expect(help.textContent).toMatch(/^Defines the role of the machine/);
    const token = [...box.querySelectorAll("dt")].find((e) => e.textContent === "token")!.nextElementSibling!;
    expect(token.textContent).toBe("Reference reg/machine-token");
    expect(token.querySelector("code")!.textContent).toBe("reg/machine-token");
    expect([...box.querySelectorAll("ol.rows > li")].map((e) => e.textContent)).toEqual(["cp1.example.test"]);
    const map = box.querySelector("dl.rows")!;
    expect([...map.querySelectorAll(":scope > div")].map((e) => [e.querySelector("dt")!.textContent, e.querySelector("dd")!.textContent])).toEqual([["listen-metrics-urls", "SYNTH-URL"]]);
  });

  test("Other settings follow the fields, each with its path and raw lines", async () => {
    await draw(TEXT);
    const items = [...box.querySelectorAll("ul.other > li")].map((e) => [e.querySelector("code")!.textContent, e.querySelector("pre")!.textContent]);
    expect(items).toEqual([["machine/network", "  network:\n    hostname: SYNTH-HOST"]]);
    const doc = box.querySelector("h4")!.parentElement!;
    expect(doc.lastElementChild!.querySelector("h5")!.textContent).toBe("Other settings");
  });

  test("no comment is drawn, and no value is in any attribute, id or title", async () => {
    await draw(TEXT);
    expect(box.textContent).not.toContain("SYNTH-COMMENT");
    const attrs = [...box.querySelectorAll("*")].flatMap((e) => [...e.attributes].map((a) => a.value));
    expect(attrs.length).toBeGreaterThan(0);
    for (const v of ["SYNTH-CRT-VALUE", "SYNTH-HOST", "SYNTH-URL", "controlplane", "cp1.example.test", "reg/machine-token"]) expect(attrs.join("\n")).not.toContain(v);
  });

  // The browser cases at 320 CSS pixels measure the page (web/e2e/auth/protected.e2e.ts); here the rules that let each part wrap are pinned.
  test("a label, a value, a raw line and a reference name wrap anywhere, and a label stacks over its value on a narrow screen", () => {
    const css = readFileSync(`${import.meta.dirname}/app.css`, "utf8");
    const rule = (sel: string) => new RegExp(`${sel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}[^{]*\\{([^}]*)\\}`).exec(css)?.[1] ?? "";
    expect(rule(".config-fields dt, .config-fields dd")).toContain("overflow-wrap: anywhere");
    expect(rule(".config-fields pre")).toContain("overflow-wrap: anywhere");
    expect(rule(".config-fields pre")).toContain("white-space: pre-wrap");
    expect(css).toMatch(/@media \(max-width: 40rem\) \{\s*\.config-fields dl > div \{ grid-template-columns: minmax\(0, 1fr\); \}/);
  });

  test("text with no settings says so", async () => {
    await draw("# only a comment\n");
    expect(box.textContent).toContain("There are no settings in this text");
    expect(box.querySelector("h4")).toBeNull();
  });
});
