import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Plan } from "./onboard.ts";
import { ConfirmScreen, OnboardCluster } from "./onboard.tsx";
import { ShellContext } from "./shell.tsx";
import { CONNECT_A_CLUSTER, CONNECT_STEP_ONE, CONNECT_STEP_TWO, CONNECT_THIS_CLUSTER } from "./words.ts";
import { Writes } from "./write.ts";

// The two screens of Connect a cluster (ginsys/bronzeward#1232): each says which step it is and what it asks, the
// name is asked beside the file, and the second screen's button does not carry the page's own label.
const noReads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const page = (n: ReactNode) =>
  renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: noReads }, n))));
const buttons = (html: string) => [...html.matchAll(/<button[^>]*>([^<]*)<\/button>/g)].map((m) => m[1]);

const plan = {
  found: { version: "v1.11.0", contract: "v1.11", members: [{ nodeId: "n1", hostname: "cp1", role: "controlplane", address: "10.0.0.1", platform: "metal", answered: true }], endpoint: "10.0.0.1:50000", complete: true },
  endpoint: "10.0.0.1:50000", name: "office", kube: "https://10.0.0.1:6443",
} as unknown as Plan;

describe("the first screen of Connect a cluster", () => {
  const out = page(h(OnboardCluster));
  test("says it is step 1 and what it asks, under the page's heading", () => {
    expect(out).toContain(`<h2>${CONNECT_A_CLUSTER}</h2>`);
    expect(out).toContain(`<h3>${CONNECT_STEP_ONE}</h3>`);
    expect(out).not.toContain(CONNECT_STEP_TWO);
  });
  test("asks the file and the name, and its button reads the cluster", () => {
    expect(out).toContain('name="file"');
    expect(out).toMatch(/<label>Name: <input required=""[^>]*name="name"/);
    expect(buttons(out)).toEqual(["Read cluster"]);
  });
});

describe("the second screen of Connect a cluster", () => {
  const out = page(h(ConfirmScreen, { p: plan, busy: false, onSubmit: () => {} }));
  test("says it is step 2 and what it asks", () => {
    expect(out).toContain(`<h3>${CONNECT_STEP_TWO}</h3>`);
    expect(out).not.toContain(CONNECT_STEP_ONE);
  });
  test("shows the name given on the first screen and asks no name; it asks the Kubernetes endpoint", () => {
    expect(out).toContain('<strong data-testid="onboard-name">office</strong>');
    expect(out).not.toContain('name="name"');
    expect(out).toContain('name="kube"');
  });
  test("its button is Connect this cluster, never the page's own label", () => {
    expect(buttons(out)).toEqual([CONNECT_THIS_CLUSTER]);
    expect(CONNECT_THIS_CLUSTER).not.toBe(CONNECT_A_CLUSTER);
  });
});
