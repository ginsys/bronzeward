// The recovery page of one operation (ginsys/bronzeward#365): the path it answers, the owner generation a takeover sends, and when
// each control is off. Every value is synthetic.

import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { recoveryOperationId, ownerGeneration, resolveWhy, takeOverWhy } from "./recovery-operation.tsx";
import { recoveryOperationPath } from "./operation-words.ts";

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

describe("when a control is off", () => {
  test("take over is off for a finished operation and on for the others", () => {
    for (const s of ["completed", "failed", "rejected", "cancelled", "succeeded"]) expect(takeOverWhy(op(s)), s).toContain(`is ${s}`);
    for (const s of ["unresolved", "running", "verifying"]) expect(takeOverWhy(op(s)), s).toBeUndefined();
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
