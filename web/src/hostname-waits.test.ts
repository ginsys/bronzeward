// A9 item 8 of docs/spec/client-contracts.md: a plan says when it waits for another machine's hostname. Each sentence is
// compared with the contract's own text (ginsys/bronzeward#1149).

import { readFileSync } from "node:fs";
import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { LINES, rowOf } from "./apply.ts";
import { HostWaitsLine, hostWaitsText, type Position } from "./nextstep.tsx";
import { HOST_WAITS_SHORT, hostWaits } from "./words.ts";

type Plan = NonNullable<Position["plan"]>;

// A9 item 8 of the contract, whitespace collapsed so a sentence the document wraps still matches.
const itemEight = () => {
  const spec = readFileSync(`${import.meta.dirname}/../../docs/spec/client-contracts.md`, "utf8");
  const from = spec.indexOf("8. **A plan says when it waits");
  const to = spec.indexOf("**A10.");
  expect(from).toBeGreaterThan(0);
  expect(to).toBeGreaterThan(from);
  return spec.slice(from, to).replace(/\s+/g, " ");
};

const at = "2026-11-18T10:00:00Z";
const plan = (over: Partial<Plan> = {}): Plan =>
  ({ id: "pln_1", state: "approved", operation: "apply-config", release: "rel_a", approvalCurrent: true, createdAt: at, expiresAt: at, ...over }) as Plan;
const machine = (name: string | null) => ({ id: "mch_b", cluster: "cl_a", name, talosEndpoint: "10.0.0.2:50000", frozen: false, openDrift: null, scopeState: "normal" }) as Position["machine"];
const held = (over: Partial<Position> = {}): Position =>
  ({ machine: machine("node-b"), ingestion: null, draft: null, plan: plan(), operation: null, rolloutHeldBy: null, hostnameHeldBy: { id: "mch_a", name: "node-a" },
    step: "awaiting-dispatch", ...over }) as Position;

describe("the sentences are the contract's own", () => {
  test("the waiting sentence, with the three values where the sentence puts them", () => {
    const item = itemEight();
    const s = hostWaits("node-a", "node-a", "node-b");
    expect(s).toBe(
      "This plan waits. The last release applied to machine node-a records the Kubernetes node name node-a, and this release gives the same name to machine node-b. " +
        "This plan has sent nothing to either machine. Apply the release for machine node-a first, or give machine node-b another hostname.",
    );
    expect(item).toContain(s);
    expect(hostWaits("web-1", "cp-9", "web-2")).toContain("machine web-1 records the Kubernetes node name cp-9, and this release gives the same name to machine web-2.");
    expect(item).toContain(HOST_WAITS_SHORT);
    expect(HOST_WAITS_SHORT).toBe("This plan waits for another machine's hostname.");
    for (const x of [s, HOST_WAITS_SHORT]) expect(x).not.toMatch(/refused|failed/);
  });
});

describe("hostWaitsText: the sentence of a position, machines named as A8 rule 1 names them in the web", () => {
  test("names both machines by name, with the node name from the release's review", () => {
    expect(hostWaitsText(held(), "node-a")).toBe(hostWaits("node-a", "node-a", "node-b"));
  });
  test("a machine with no name is 'an unnamed machine', never its identifier", () => {
    const s = hostWaitsText(held({ hostnameHeldBy: { id: "mch_a", name: null }, machine: machine(null) }), "node-a") ?? "";
    expect(s).toBe(hostWaits("an unnamed machine", "node-a", "an unnamed machine"));
    expect(s).not.toContain("mch_");
  });
  test("a node name that could not be read gives the short sentence, and no more", () => {
    expect(hostWaitsText(held(), undefined)).toBe(HOST_WAITS_SHORT);
  });
  test("no holder, a plan that is not an apply-config plan, or a plan not approved says nothing", () => {
    expect(hostWaitsText(held({ hostnameHeldBy: null }), "node-a")).toBeUndefined();
    expect(hostWaitsText(held({ plan: plan({ operation: "adopt" }) }), "node-a")).toBeUndefined();
    expect(hostWaitsText(held({ plan: null }), "node-a")).toBeUndefined();
    expect(hostWaitsText(held({ step: "awaiting-approval", plan: plan({ state: "proposed" }) }), "node-a")).toBeUndefined();
  });
  test("a position from a server that does not serve the member says nothing", () => {
    const p = held() as Record<string, unknown>;
    delete p.hostnameHeldBy;
    expect(hostWaitsText(p as unknown as Position, "node-a")).toBeUndefined();
  });
});

describe("HostWaitsLine", () => {
  test("draws the sentence in its own paragraph, and nothing where there is none", () => {
    const html = renderToStaticMarkup(h(HostWaitsLine, { position: held(), node: "node-a" }));
    expect(html).toContain(`data-testid="host-waits"`);
    expect(html).toContain(hostWaits("node-a", "node-a", "node-b"));
    expect(renderToStaticMarkup(h(HostWaitsLine, { position: held({ hostnameHeldBy: null }), node: "node-a" }))).toBe("");
  });
});

describe("the apply page's row", () => {
  test("an approved plan that waits for another machine's hostname is waiting, in the contract's short sentence", () => {
    const row = rowOf(held(), "rel_a");
    expect(row).toEqual({ state: "waiting", text: LINES.hostWaits });
    expect(LINES.hostWaits).toBe(HOST_WAITS_SHORT);
  });
  test("without a holder the row reads as before", () => {
    expect(rowOf(held({ hostnameHeldBy: null }), "rel_a")).toEqual({ state: "waiting", text: LINES.approved });
    expect(rowOf(held({ hostnameHeldBy: null, rolloutHeldBy: { id: "mch_c" } as never }), "rel_a")).toEqual({ state: "waiting", text: LINES.behind });
  });
  test("another release's plan is not this release's wait", () => {
    expect(rowOf(held({ plan: plan({ release: "rel_other" }) }), "rel_a").text).not.toBe(LINES.hostWaits);
  });
});
