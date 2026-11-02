import { readFileSync } from "node:fs";
import { describe, expect, test, vi } from "vitest";
import { baoTarget, cleanBao, DEV_ROOT_TOKEN, teardownFailure, undoSteps, upstreamFailed } from "./bao.ts";

describe("ginsys/bronzeward#517 H19 and H20: the OpenBao cleanup and proxy fail visibly", () => {
  test("a cleanup step that fails stays for the next cleanup, which runs only it again", async () => {
    const order: string[] = [];
    let down = true;
    const undo = [async () => void order.push("policy"), async () => {
      order.push("token");
      if (down) throw new Error("synthetic revoke failure");
    }];
    await expect(undoSteps(undo, "bwweb-x-")).rejects.toThrow(/OpenBao cleanup \(records named bwweb-x-\*\): Error: synthetic revoke failure/);
    expect(order).toEqual(["token", "policy"]);
    expect(undo).toHaveLength(1);
    down = false;
    await undoSteps(undo, "bwweb-x-");
    expect(order).toEqual(["token", "policy", "token"]);
    expect(undo).toHaveLength(0);
  });

  test("the teardown keeps the instance whose cleanup failed, and drops it once a later cleanup succeeds", async () => {
    let down = true;
    const bao = { cleanup: vi.fn(async () => {
      if (down) throw new Error("synthetic revoke failure");
    }) };
    const first = await cleanBao(bao);
    expect(first.kept).toBe(bao);
    expect(String(first.failed)).toMatch(/synthetic revoke failure/);
    down = false;
    const second = await cleanBao(first.kept);
    expect(second).toEqual({ kept: undefined, failed: undefined });
    expect(bao.cleanup).toHaveBeenCalledTimes(2);
    expect(await cleanBao(undefined)).toEqual({ kept: undefined, failed: undefined });
  });

  test("auth.conf.ts keeps its OpenBao handle through cleanBao", () => {
    const src = readFileSync(new URL("./auth.conf.ts", import.meta.url), "utf8");
    expect(src).toMatch(/const \{ kept, failed: baoFailed \} = await cleanBao\(bao\);\s*bao = kept;/);
    expect(src).not.toMatch(/bao\?\.cleanup\(\)/);
  });

  test("databases left behind and a failed OpenBao cleanup are both reported", () => {
    const lost = new Error("databases left behind: bw_a");
    const bao = new Error("OpenBao cleanup: synthetic");
    const both = teardownFailure(lost, bao);
    expect(both).toBeInstanceOf(AggregateError);
    expect((both as AggregateError).errors).toEqual([lost, bao]);
    expect((both as Error).message).toMatch(/bw_a; Error: OpenBao cleanup: synthetic/);
    expect(teardownFailure(lost, undefined)).toBe(lost);
    expect(teardownFailure(undefined, bao)).toBe(bao);
    expect(teardownFailure(undefined, undefined)).toBeUndefined();
  });

  test("an upstream error is a 502 before the headers, a cut response after them", () => {
    const end = vi.fn();
    const before = { headersSent: false, destroy: vi.fn(), writeHead: vi.fn(() => ({ end })) };
    upstreamFailed(before as never);
    expect(before.writeHead).toHaveBeenCalledWith(502);
    expect(end).toHaveBeenCalled();
    expect(before.destroy).not.toHaveBeenCalled();
    const after = { headersSent: true, destroy: vi.fn(), writeHead: vi.fn() };
    upstreamFailed(after as never);
    expect(after.destroy).toHaveBeenCalled();
    expect(after.writeHead).not.toHaveBeenCalled();
  });
});

describe("the OpenBao the draft cases administer is the instance's disposable dev-bao", () => {
  test("the instance's loopback address with dev-bao's root token", () => {
    expect(baoTarget({ BW_DEV_BAO_ADDR: "http://127.0.0.1:13002/" })).toEqual({ addr: "http://127.0.0.1:13002", admin: DEV_ROOT_TOKEN });
    // The same address and token, as mise's shared test environment sets them, are accepted.
    expect(baoTarget({ BW_DEV_BAO_ADDR: "http://127.0.0.1:11200", BW_TEST_BAO_ADDR: "http://127.0.0.1:11200", BW_TEST_BAO_TOKEN: DEV_ROOT_TOKEN }).addr).toBe("http://127.0.0.1:11200");
  });

  test("no instance address refuses: there is no default", () => {
    expect(() => baoTarget({})).toThrow(/BW_DEV_BAO_ADDR is unset/);
  });

  test("an inherited BW_TEST_BAO_ADDR or BW_TEST_BAO_TOKEN that names another provider refuses", () => {
    expect(() => baoTarget({ BW_DEV_BAO_ADDR: "http://127.0.0.1:11200", BW_TEST_BAO_ADDR: "https://vault.example.test:8200" })).toThrow(/BW_TEST_BAO_ADDR/);
    expect(() => baoTarget({ BW_DEV_BAO_ADDR: "http://127.0.0.1:11200", BW_TEST_BAO_TOKEN: "SYNTH-other-token" })).toThrow(/BW_TEST_BAO_TOKEN/);
  });

  test("an instance address off the loopback refuses", () => {
    for (const a of ["http://10.0.0.5:11200", "http://localhost.example.test:11200", "https://127.0.0.1:11200", "http://127.0.0.1:11200/v1"]) expect(() => baoTarget({ BW_DEV_BAO_ADDR: a })).toThrow(/loopback/);
  });
});
