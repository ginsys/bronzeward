import { describe, expect, it } from "vitest";
import { openState, type Probe } from "./open-state.ts";

// A clock the sleeps move, and a page that shows what the script says at each instant.
function page(shows: (t: number) => { principal?: boolean; entry?: boolean; signin?: boolean }) {
  let t = 0;
  const probe: Probe = {
    principal: async () => shows(t).principal === true,
    entry: async () => shows(t).entry === true,
    signin: async () => shows(t).signin === true,
  };
  return { probe, now: () => t, sleep: async (ms: number) => void (t += ms) };
}
const base = { entryOk: true, signinOk: true, timeoutMs: 30_000, settleMs: 1_500 };

describe("the state a fixture page opens in", () => {
  it("is the principal when the page shows one", async () => {
    const { probe, now, sleep } = page(() => ({ principal: true }));
    expect(await openState(probe, { ...base, now, sleep })).toBe("principal");
  });

  it("is the entry form that takes the place of a Sign in button drawn for an instant", async () => {
    // The first answer of a first-hit wait would have been "signin" at 0 ms: the button is there for 500 ms, then the form.
    const { probe, now, sleep } = page((t) => (t < 500 ? { signin: true } : { entry: true }));
    expect(await openState(probe, { ...base, now, sleep })).toBe("entry");
  });

  it("is the Sign in button once it has stayed", async () => {
    const { probe, now, sleep } = page(() => ({ signin: true }));
    expect(await openState(probe, { ...base, now, sleep })).toBe("signin");
  });

  it("does not take the entry form for an act that has none", async () => {
    const { probe, now, sleep } = page((t) => (t < 5_000 ? { entry: true } : { principal: true }));
    expect(await openState(probe, { ...base, entryOk: false, now, sleep })).toBe("principal");
  });

  it("does not take a Sign in button after the click, where only a principal or the entry form can follow", async () => {
    const { probe, now, sleep } = page((t) => (t < 2_000 ? { signin: true } : { entry: true }));
    expect(await openState(probe, { ...base, signinOk: false, now, sleep })).toBe("entry");
  });

  it("fails when the page reaches no state", async () => {
    const { probe, now, sleep } = page(() => ({}));
    await expect(openState(probe, { ...base, now, sleep })).rejects.toThrow("did not reach a state");
  });
});
