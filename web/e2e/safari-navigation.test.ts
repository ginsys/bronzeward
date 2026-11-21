// The wait Safari's url override makes after a navigation (ginsys/bronzeward#1028).
import { describe, expect, it } from "vitest";
import { settled } from "./safari-navigation.ts";

// A wait that asks its condition up to `max` times, as webdriverio's waitUntil asks it until its timeout.
const poll = (max: number) => async (until: () => Promise<boolean>) => {
  for (let i = 0; i < max; i++) if (await until()) return;
  throw new Error("waitUntil condition timed out");
};

describe("settled", () => {
  it("waits through interactive reads and returns once the document is complete", async () => {
    const states = ["interactive", "interactive", "complete"];
    let reads = 0;
    await settled(async () => states[reads++]!, "/signed-out", poll(10));
    expect(reads).toBe(3);
  });

  it("fails with the path, the last state read and the wait's own error when the document never completes", async () => {
    await expect(settled(async () => "interactive", "/signed-out", poll(3))).rejects.toThrow('navigation to /signed-out left readyState "interactive" after 15 s: waitUntil condition timed out');
  });

  it("does not report a state when no read succeeded, and keeps the read's own error", async () => {
    const lost = async () => {
      throw new Error("no such window");
    };
    // webdriverio's waitUntil ends with its own timeout error and names the condition's error in it.
    const wait = async (until: () => Promise<boolean>) => {
      try {
        await until();
      } catch (e) {
        throw new Error(`waitUntil failed: ${(e as Error).message}`);
      }
    };
    await expect(settled(lost, "/", wait)).rejects.toThrow('navigation to / left readyState "unread" after 15 s: waitUntil failed: no such window');
  });
});
