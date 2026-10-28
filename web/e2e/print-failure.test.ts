import { afterEach, expect, test, vi } from "vitest";
import { printFailure } from "./print-failure.ts";

afterEach(() => vi.restoreAllMocks());

test("printFailure prints the error an onComplete throws, then rethrows it (#435)", async () => {
  const printed = vi.spyOn(console, "error").mockImplementation(() => {});
  const e = new Error("EACCES, Permission denied: '/profiles'");
  await expect(printFailure(async () => { throw e; })).rejects.toBe(e);
  expect(printed).toHaveBeenCalledWith("Error: EACCES, Permission denied: '/profiles'");
});

test("printFailure prints nothing when onComplete succeeds", async () => {
  const printed = vi.spyOn(console, "error").mockImplementation(() => {});
  await expect(printFailure(() => {})).resolves.toBeUndefined();
  expect(printed).not.toHaveBeenCalled();
});
