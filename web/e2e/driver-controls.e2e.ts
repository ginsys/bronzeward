// Positive controls for the harness's own commands. wdio.conf.ts changes four commands in Safari
// (getText, keys, the select commands and Tab), and a worker hook that throws is logged and ignored:
// a command that was not changed leaves every later case failing on the driver's own behaviour
// (ginsys/bronzeward#892). These cases pass in every browser, and fail in Safari when a change did not
// take, so the run says so, and names the command, before the cases that depend on it.
import { $, browser, expect } from "@wdio/globals";

describe("the harness's own commands", () => {
  before(async () => {
    await browser.url("about:blank");
    await browser.execute(() => {
      document.body.innerHTML = '<p id="t">Showing 4 of 4. </p><ul id="l"><li>started</li><li>succeeded</li></ul><table id="tb"><tbody><tr><td>2026</td><td>freeze x</td></tr></tbody></table><input id="i"><select id="s"><option value="a">a</option><option value="b">b</option></select><button id="b1">one</button><a id="a1" href="#x">two</a>';
    });
  });

  it("getText answers the rendered text: no trailing space, a line break between block items", async () => {
    expect(await $("#t").getText()).toBe("Showing 4 of 4.");
    expect(await $("#l").getText()).toBe("started\nsucceeded");
    expect(await $("#tb").getText()).toBe("2026 freeze x");
  });

  it("getText and an assertion on a text wait for an element that appears after the call", async () => {
    await browser.execute(() => {
      setTimeout(() => {
        const p = document.createElement("p");
        p.id = "late";
        p.textContent = "later ";
        document.body.append(p);
      }, 1000);
    });
    await expect($("#late")).toHaveText("later");
    expect(await $("#late").getText()).toBe("later");
  });

  it("keys sends repeated characters and spaces whole", async () => {
    await $("#i").click();
    await browser.keys("198.51.100.43  x".split(""));
    expect(await $("#i").getValue()).toBe("198.51.100.43  x");
  });

  it("a select takes a choice by its value", async () => {
    await $("#s").selectByAttribute("value", "b");
    expect(await $("#s").getValue()).toBe("b");
  });

  it("Tab reaches a button and then a link", async () => {
    await $("#i").click();
    await browser.keys("Tab");
    expect(await browser.execute(() => document.activeElement?.id)).toBe("s");
    await browser.keys("Tab");
    expect(await browser.execute(() => document.activeElement?.id)).toBe("b1");
    await browser.keys("Tab");
    expect(await browser.execute(() => document.activeElement?.id)).toBe("a1");
  });
});
