import { expect, test } from "vitest";
import { portBase } from "./ports.ts";

test("#459: BW_E2E_PORT_BASE unset is 16000, and each aligned block from 16000 to 19984 is accepted", () => {
  expect(portBase(undefined)).toBe(16000);
  for (const b of [16000, 16016, 17600, 19984]) expect(portBase(String(b))).toBe(b);
});

test("#459: a base off the 16-port grid, outside 16000-19984 or not a plain number is refused", () => {
  for (const b of ["16001", "16008", "15984", "20000", "58443", "", " 16000", "16000 ", "1.6e4", "0x3e80", "-16000"]) {
    expect(() => portBase(b), b).toThrow("BW_E2E_PORT_BASE must be 16000+16*n with n from 0 to 249");
  }
});
