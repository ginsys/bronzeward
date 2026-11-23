// What the keyboard-only case of the operation page adds to a failure, so that the next one says whether the page was
// mounted anew (ginsys/bronzeward#1121).
import { describe, expect, it } from "vitest";
import { bannerEpoch, epochRead, failedWith, withFocusContext } from "./focus-context.ts";

describe("bannerEpoch", () => {
  it("reads the epoch the recovery banner names, in both of its wordings", () => {
    expect(bannerEpoch("Recovery mode since 18:09, epoch ep_3wan6qedajzz33fdk2js63nhpa (entering recovery mode starts a new one). 3 of 3 scopes ")).toBe("ep_3wan6qedajzz33fdk2js63nhpa");
    expect(bannerEpoch("Recovery mode since 13:53, epoch ep_erucuy5uxz57uzfjqsubkhl2mm. 3 of 3 scopes not released: none can commit.")).toBe("ep_erucuy5uxz57uzfjqsubkhl2mm");
  });

  it("says none where the page shows no banner or the banner names no epoch", () => {
    expect(bannerEpoch(null)).toBe("none");
    expect(bannerEpoch(undefined)).toBe("none");
    expect(bannerEpoch("")).toBe("none");
    expect(bannerEpoch("Recovery mode since 18:09.")).toBe("none");
  });
});

describe("withFocusContext", () => {
  it("keeps the failure's message and cause and adds the marks and both epochs", () => {
    const first = new Error('focus on BODY "" is not visible: outline none 3px');
    const e = withFocusContext(first, 2, 5, "ep_a", "ep_b");
    expect(e).toBeInstanceOf(Error);
    expect(e.message).toBe('focus on BODY "" is not visible: outline none 3px; [data-t9] marks 2 of 5; epoch at marking ep_a, now ep_b');
    expect(e.cause).toBe(first);
  });

  it("takes the text of a thrown value that is no Error", () => {
    expect(withFocusContext("plain", 5, 5, "none", "none").message).toBe("plain; [data-t9] marks 5 of 5; epoch at marking none, now none");
  });

  it("takes a read that did not answer as unread", () => {
    expect(withFocusContext("plain", "unread", 5, "ep_a", "unread").message).toBe("plain; [data-t9] marks unread of 5; epoch at marking ep_a, now unread");
  });
});

describe("failedWith", () => {
  const first = new Error("focus on BODY is not visible");
  const down = () => Promise.reject(new Error("no such window"));

  it("reads the marks and the banner now and adds them to the failure", async () => {
    const e = await failedWith(first, async () => 3, async () => "Recovery mode since 18:09, epoch ep_b.", 5, "ep_a");
    expect(e.message).toBe("focus on BODY is not visible; [data-t9] marks 3 of 5; epoch at marking ep_a, now ep_b");
    expect(e.cause).toBe(first);
  });

  it("keeps the original failure when either read fails, or both", async () => {
    for (const [marks, banner, want] of [
      [down, async () => "epoch ep_b", "marks unread of 5; epoch at marking ep_a, now ep_b"],
      [async () => 3, down, "marks 3 of 5; epoch at marking ep_a, now unread"],
      [down, down, "marks unread of 5; epoch at marking ep_a, now unread"],
    ] as const) {
      const e = await failedWith(first, marks, banner, 5, "ep_a");
      expect(e.message).toBe(`focus on BODY is not visible; [data-t9] ${want}`);
      expect(e.cause).toBe(first);
    }
  });
});

describe("epochRead", () => {
  it("is the epoch the banner names, none for no banner, and unread where the read failed", async () => {
    expect(await epochRead(async () => "Recovery mode since 18:09, epoch ep_a.")).toBe("ep_a");
    expect(await epochRead(async () => null)).toBe("none");
    expect(await epochRead(() => Promise.reject(new Error("no such window")))).toBe("unread");
  });
});
