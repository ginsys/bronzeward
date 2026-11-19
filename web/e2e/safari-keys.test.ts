// The calls Safari's keys override makes (ginsys/bronzeward#927 items 3, 4 and 6).
import { Key } from "webdriverio";
import { describe, expect, it } from "vitest";
import { safariKeys } from "./safari-keys.ts";

describe("safariKeys", () => {
  it("sends text one character at a time, a space and a line break as keys", () => {
    expect(safariKeys("seeded reason")).toEqual(["s", "e", "e", "d", "e", "d", Key.Space, "r", "e", "a", "s", "o", "n"]);
    expect(safariKeys("a\nb")).toEqual(["a", Key.Enter, "b"]);
    expect(safariKeys(["a", "a", " ", "b"])).toEqual(["a", "a", Key.Space, "b"]);
  });

  it("keeps a character outside the basic plane whole", () => {
    expect(safariKeys("a\u{1F600}b")).toEqual(["a", "\u{1F600}", "b"]);
  });

  it("sends a key name, a single key and a chord as they are", () => {
    expect(safariKeys("Enter")).toEqual(["Enter"]);
    expect(safariKeys("Backspace")).toEqual(["Backspace"]);
    expect(safariKeys("a")).toEqual(["a"]);
    // Key.Ctrl is a word, not one character: alone it is still one key.
    expect(safariKeys(Key.Ctrl)).toEqual([Key.Ctrl]);
    expect(safariKeys(Key.Shift)).toEqual([Key.Shift]);
    expect(safariKeys([Key.Ctrl, "a"])).toEqual([[Key.Ctrl, "a"]]);
    // Key.Shift is one character of the WebDriver key range: a chord that holds it stays one action.
    expect(safariKeys([Key.Shift, "a"])).toEqual([[Key.Shift, "a"]]);
    expect(safariKeys([Key.Control, "a"])).toEqual([[Key.Control, "a"]]);
  });

  it("sends Tab as Option+Tab, written as a name or as Key.Tab", () => {
    for (const tab of ["Tab", Key.Tab]) expect(safariKeys(tab)).toEqual([["Alt", "Tab"]]);
    for (const chord of [["Shift", "Tab"], [Key.Shift, Key.Tab]]) expect(safariKeys(chord)).toEqual([["Shift", "Alt", "Tab"]]);
  });
});
