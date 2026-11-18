// The machine page's "where you are, what next" line (ginsys/bronzeward#860): the not-handed-over state of
// docs/spec/operator-experience.md §2.3, from the machine's own fields, and no line in any other state.

import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { ReadsProvider, type Reads } from "./list.tsx";
import { NextStep, NextStepLine, nextStep, NOT_HANDED_OVER } from "./nextstep.tsx";

type Machine = components["schemas"]["MachineBody"];

const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));

const machine = (over: Partial<Machine>): Machine => ({
  id: "mch_x", cluster: "cl_a", talosEndpoint: "10.0.0.1:50000", hardware: { talosNodeId: "n1", smbiosUuid: null }, platform: "metal",
  openDrift: null, frozen: false, pendingConvergence: false, desired: null, applied: null, observed: null, ...over,
}) as Machine;
const drift = { revision: 1, observation: "obs_1", lastObservation: "obs_1", lateLanding: null };

describe("the line for a machine that is not handed over", () => {
  test("no Desired and no Applied: it says so, names the import and links to it", () => {
    const s = nextStep(machine({}));
    expect(s?.text).toBe(NOT_HANDED_OVER);
    expect(s?.text).toContain("Not handed over");
    expect(s?.text).toContain("writes nothing to it");
    expect(s?.links).toEqual([{ to: "/ingestions/new?machine=mch_x", label: "Import this machine", id: "mch_x" }]);
  });

  test("the machine's id is in the link", () => {
    expect(nextStep(machine({ id: "mch_other" }))?.links[0]?.to).toBe("/ingestions/new?machine=mch_other");
  });
});

describe("no line in any other state", () => {
  test("a selected release, an applied one, or both give no line", () => {
    expect(nextStep(machine({ desired: "rel_a" }))).toBeUndefined();
    expect(nextStep(machine({ applied: { release: "rel_a", source: "adoption" } }))).toBeUndefined();
    expect(nextStep(machine({ desired: "rel_a", applied: { release: "rel_a", source: "operation" } }))).toBeUndefined();
    expect(nextStep(machine({ desired: "rel_a", applied: { release: "rel_old", source: "operation" }, pendingConvergence: true }))).toBeUndefined();
  });

  test("open drift gives no line", () => {
    expect(nextStep(machine({ openDrift: drift }))).toBeUndefined();
  });

  test("the line never says unchanged", () => {
    expect(NOT_HANDED_OVER.toLowerCase()).not.toContain("unchanged");
  });
});

describe("the markup", () => {
  test("a step renders as one paragraph with its link", () => {
    const out = html(h(NextStep, { machine: machine({}) }));
    expect(out).toContain('data-testid="next-step"');
    expect(out).toContain('href="/ingestions/new?machine=mch_x"');
    expect(out).toContain("Import this machine");
  });

  test("no step renders nothing", () => {
    expect(html(h(NextStepLine, { step: undefined }))).toBe("");
    expect(html(h(NextStep, { machine: machine({ desired: "rel_a" }) }))).toBe("");
  });
});
