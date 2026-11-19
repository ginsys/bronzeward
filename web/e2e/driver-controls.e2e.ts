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
      document.body.innerHTML = '<p id="t">Showing 4 of 4. </p><ul id="l"><li>started</li><li>succeeded</li></ul><table id="tb"><tbody><tr><td>2026</td><td>freeze x</td></tr></tbody></table><input id="i"><select id="s"><option value="a">a</option><option value="b">b</option></select><button id="b1">one</button><a id="a1" href="#x">two</a><p id="ghost" style="opacity:0">hidden</p><p id="part">shown <span style="opacity:0">ghost</span></p><div id="blk">before<div style="opacity:0"></div>after</div><select id="sd" disabled><option value="x">x</option><option value="y">y</option></select><select id="so"><option value="p">p</option><option value="q" disabled>q</option></select>';
    });
  });

  it("getText answers the rendered text: no trailing space, a line break between block items", async () => {
    expect(await $("#t").getText()).toBe("Showing 4 of 4.");
    expect(await $("#l").getText()).toBe("started\nsucceeded");
    expect(await $("#tb").getText()).toBe("2026 freeze x");
  });

  it("getText leaves out transparent text, an element's own or a descendant's", async () => {
    expect(await $("#ghost").getText()).toBe("");
    expect(await $("#part").getText()).toBe("shown");
    // An empty transparent block between two lines leaves the line break where it was.
    expect(await $("#blk").getText()).toBe("before\nafter");
  });

  it("a disabled select, or a disabled option, keeps its value and the page hears nothing", async () => {
    await browser.execute(() => {
      const heard: string[] = [];
      (window as unknown as { heardDisabled: string[] }).heardDisabled = heard;
      for (const id of ["sd", "so"]) for (const type of ["input", "change"]) document.getElementById(id)!.addEventListener(type, () => heard.push(id));
    });
    // Each driver may refuse the choice or ignore it: the value stays, and no event is raised.
    await $("#sd").selectByAttribute("value", "y").catch(() => undefined);
    await $("#so").selectByAttribute("value", "q").catch(() => undefined);
    expect(await $("#sd").getValue()).toBe("x");
    expect(await $("#so").getValue()).toBe("p");
    expect(await browser.execute(() => (window as unknown as { heardDisabled: string[] }).heardDisabled)).toEqual([]);
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

  it("a click waits for the element to hold still, and lands on it after a shift", async function () {
    // The element is pushed down once, a moment after the command starts: the point it had is then another's.
    await browser.execute(() => {
      const hits: string[] = [];
      (window as unknown as { hits: string[] }).hits = hits;
      const b = document.createElement("button");
      b.id = "shift";
      b.textContent = "shifting";
      b.addEventListener("click", () => hits.push("shift"));
      document.body.append(b);
      setTimeout(() => {
        const pad = document.createElement("div");
        pad.style.height = "60px";
        document.body.insertBefore(pad, b);
      }, 150);
    });
    const before = (globalThis as { bwStillClicks?: number }).bwStillClicks ?? 0;
    await $("#shift").click();
    expect(await browser.execute(() => (window as unknown as { hits: string[] }).hits)).toEqual(["shift"]);
    // Only Safari's harness holds the click still: the count says the change took.
    if ((browser.capabilities as { browserName?: string }).browserName?.toLowerCase() === "safari") {
      expect((globalThis as { bwStillClicks?: number }).bwStillClicks ?? 0).toBeGreaterThan(before);
    }
  });

  it("keys sends repeated characters and spaces whole", async () => {
    await $("#i").click();
    await browser.keys("198.51.100.43  x".split(""));
    expect(await $("#i").getValue()).toBe("198.51.100.43  x");
  });

  it("a select takes each choice by its value, and the page hears each", async () => {
    await browser.execute(() => {
      const heard: string[] = [];
      (window as unknown as { heard: string[] }).heard = heard;
      document.getElementById("s")!.addEventListener("change", (e) => heard.push((e.target as HTMLSelectElement).value));
    });
    await $("#s").selectByAttribute("value", "b");
    expect(await $("#s").getValue()).toBe("b");
    await $("#s").selectByVisibleText("a");
    expect(await $("#s").getValue()).toBe("a");
    expect(await browser.execute(() => (window as unknown as { heard: string[] }).heard)).toEqual(["b", "a"]);
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
