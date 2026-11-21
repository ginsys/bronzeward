import { afterEach, describe, expect, test, vi } from "vitest";
import { when } from "./states.tsx";

// The form of a recorded instant is the client's, not the browser's: Safari 27.2 joins a date and a time
// formatted in one call with " at " (ginsys/bronzeward#946).
describe("when", () => {
  afterEach(() => vi.restoreAllMocks());

  test("writes the date, a comma and the time, and none for an absent instant", () => {
    const at = new Date(2026, 9, 26, 10, 12).toISOString();
    expect(when(at)).toBe("26 Oct 2026, 10:12");
    expect(when(null)).toBe("none");
    expect(when(undefined)).toBe("none");
  });

  test("joins the parts itself, whatever one call with both styles would write", () => {
    vi.spyOn(Date.prototype, "toLocaleString").mockReturnValue("11 Nov 2026 at 22:00");
    vi.spyOn(Date.prototype, "toLocaleDateString").mockReturnValue("11 Nov 2026");
    vi.spyOn(Date.prototype, "toLocaleTimeString").mockReturnValue("22:00");
    expect(when("2026-11-11T21:00:00Z")).toBe("11 Nov 2026, 22:00");
  });
});
