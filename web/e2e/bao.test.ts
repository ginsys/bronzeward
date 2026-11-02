import { describe, expect, test } from "vitest";
import { baoTarget, DEV_ROOT_TOKEN } from "./bao.ts";

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
