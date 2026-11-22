// The recovery page of one operation (ginsys/bronzeward#365): the path it answers, the owner generation a takeover sends, and when
// each control is off. Every value is synthetic.

import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { answeredGeneration, EVENTS_LOADING, eventsNote, recoveryOperationId, ownerGeneration, resolveWhy, takeOverWhy } from "./recovery-operation.tsx";
import { recoveryOperationPath } from "./operation-words.ts";
import { RESOLVE_TEXT } from "./words.ts";

type Operation = components["schemas"]["OperationBody"];
const op = (state: string, attempts: number[] = []): Operation =>
  ({ id: "op_x", kind: "apply-config", state, attempts: attempts.map((generation, i) => ({ id: `att_${i}`, number: i + 1, generation })) }) as unknown as Operation;

describe("the path", () => {
  test("an operation's path round-trips and nothing else is one", () => {
    expect(recoveryOperationId(recoveryOperationPath("op_abc"))).toBe("op_abc");
    for (const bad of ["/recovery", "/recovery/operations", "/recovery/operations/", "/recovery/operations/mch_abc", "/recovery/operations/op_a/b", "/recovery/operations/%E0%A4%A", "/operations/op_abc"]) {
      expect(recoveryOperationId(bad), bad).toBeUndefined();
    }
  });
});

describe("ownerGeneration", () => {
  const ev = (generation?: unknown) => ({ data: generation === undefined ? {} : { generation } });
  test("is the highest generation of the events and the attempts", () => {
    expect(ownerGeneration([ev(2), ev(5), ev(3)], [{ generation: 4 }])).toBe(5);
    expect(ownerGeneration([ev(2)], [{ generation: 7 }])).toBe(7);
  });
  test("is 1 where neither shows one", () => {
    expect(ownerGeneration([], [])).toBe(1);
    expect(ownerGeneration([ev(), { data: null }, { data: "x" }], [])).toBe(1);
  });
  test("ignores a value that is no generation", () => {
    expect(ownerGeneration([ev("9"), ev(0), ev(-3), ev(2.5), ev(3)], [])).toBe(3);
  });
});

describe("the resolve text", () => {
  test("says the action sends nothing, and never that nothing was sent before: no recorded attempt is no proof of that", () => {
    expect(RESOLVE_TEXT).toContain("This action sends nothing to the node");
    expect(RESOLVE_TEXT).toContain("No recorded attempt does not prove that nothing was ever sent");
    expect(RESOLVE_TEXT).not.toMatch(/nothing was sent/i);
  });
});

describe("answeredGeneration", () => {
  test("is the generation a takeover's answer names, and 0 for anything else", () => {
    expect(answeredGeneration({ generation: 4, state: "unresolved" })).toBe(4);
    for (const bad of [undefined, null, "4", 4, {}, { generation: "4" }, { generation: 0 }, { generation: -2 }, { generation: 2.5 }]) expect(answeredGeneration(bad), String(bad)).toBe(0);
  });
  test("the page's generation after a takeover is the newest of what it read and what the answer named", () => {
    const read = ownerGeneration([{ data: { generation: 2 } }], [{ generation: 1 }]);
    expect(Math.max(read, answeredGeneration({ generation: 3 }))).toBe(3);
    expect(Math.max(read, answeredGeneration({ generation: 1 }))).toBe(2);
    expect(Math.max(read, answeredGeneration(null))).toBe(2);
  });
});

describe("the note on the events read", () => {
  test("is said only once the read failed, never while it is loading or after it answered", () => {
    expect(eventsNote("loading")).toBeUndefined();
    expect(eventsNote("ok")).toBeUndefined();
    for (const k of ["refused", "unavailable", "dropped", "discarded"]) expect(eventsNote(k), k).toContain("were not read");
  });
});

describe("when a control is off", () => {
  test("take over is on in the four states the server accepts and off in the others", () => {
    for (const s of ["completed", "failed", "rejected", "cancelled", "succeeded", "running"]) expect(takeOverWhy(op(s)), s).toContain(`is ${s}`);
    for (const s of ["committed", "sending", "verifying", "unresolved"]) expect(takeOverWhy(op(s)), s).toBeUndefined();
  });
  test("take over is off for an operation that is not an apply-config", () => {
    expect(takeOverWhy({ ...op("unresolved"), kind: "publish" })).toContain("only an apply-config operation");
  });
  test("take over waits while the events are being read, and only then", () => {
    expect(takeOverWhy(op("unresolved"), true)).toBe("The operation's events are still being read: the owner generation comes from them.");
    expect(EVENTS_LOADING).toContain("still being read");
    expect(takeOverWhy(op("unresolved"), false)).toBeUndefined();
    expect(takeOverWhy(op("unresolved"))).toBeUndefined();
  });
  test("a reason of the operation itself comes before the wait for the events", () => {
    expect(takeOverWhy(op("cancelled"), true)).toContain("is cancelled");
    expect(takeOverWhy({ ...op("unresolved"), kind: "publish" }, true)).toContain("only an apply-config operation");
  });
  test("resolve never waits for the events", () => {
    expect(resolveWhy(op("unresolved"))).toBeUndefined();
    expect(resolveWhy.length).toBe(1);
  });
  test("resolve is on for an unresolved operation with no attempt only", () => {
    expect(resolveWhy(op("unresolved"))).toBeUndefined();
    expect(resolveWhy(op("running"))).toContain("only an unresolved operation");
    expect(resolveWhy(op("cancelled"))).toContain("only an unresolved operation");
  });
  test("resolve says an attempt may have landed, once and for several", () => {
    expect(resolveWhy(op("unresolved", [1]))).toMatch(/^An attempt is recorded, and the request may have landed/);
    expect(resolveWhy(op("unresolved", [1, 2]))).toMatch(/^2 attempts are recorded/);
  });
});
