// #969: the web client is styled with Oat (@knadh/oat) over our own tokens (src/app.css). Oat's rules sit
// in cascade layers and ours do not, so ours must win; these cases fail where Oat's would. They run on
// the signed-out page with elements built here, so no provider and no database is needed, and the page
// carries the real content policy: `img-src 'self'` refuses a `data:` image, which Oat draws its select
// arrow, check mark and radio dot with (client contracts §10.4).
import { $, browser, expect } from "@wdio/globals";
import { SIGNED_OUT } from "../src/app.tsx";

// Strict, like measure() in auth/helpers.ts: a colour that is not plain rgb() or an opaque rgba() cannot
// be read as three bytes, so it throws instead of reading as near black.
const rgb = (c: string): [number, number, number] => {
  const m = /^rgba?\((\d+(?:\.\d+)?), (\d+(?:\.\d+)?), (\d+(?:\.\d+)?)(?:, (\d+(?:\.\d+)?))?\)$/.exec(c);
  if (m === null || (m[4] !== undefined && Number(m[4]) !== 1)) throw new Error(`computed colour "${c}" is not an opaque rgb()`);
  return [Number(m[1]), Number(m[2]), Number(m[3])];
};
const lum = (c: string) => {
  const [r, g, b] = rgb(c).map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
};
const ratio = (fg: string, bg: string) => {
  const [a, b] = [lum(fg), lum(bg)];
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
};

// Our --ring token (src/app.css): the focus outline colour.
const RING = "rgb(61, 214, 140)";

// Puts one of each control the app uses at the top of the page, built with createElement because the page
// requires Trusted Types, and listens for content policy violations on the document, where a stylesheet's
// image request reports them.
const inject = () =>
  browser.execute(() => {
    const seen: string[] = [];
    (globalThis as unknown as { g969: string[] }).g969 = seen;
    document.addEventListener("securitypolicyviolation", (e) => seen.push(`${e.effectiveDirective} ${e.blockedURI}`));
    const box = document.createElement("div");
    const mk = <K extends keyof HTMLElementTagNameMap>(tag: K, id: string, text = "") => {
      const e = document.createElement(tag);
      e.id = id;
      e.textContent = text;
      box.append(e);
      return e;
    };
    mk("button", "g969-button", "Button");
    const input = mk("input", "g969-input");
    input.type = "text";
    input.setAttribute("aria-label", "Text");
    const select = mk("select", "g969-select");
    select.setAttribute("aria-label", "Choice");
    select.append(new Option("One"), new Option("Two"));
    const radio = mk("input", "g969-radio");
    radio.type = "radio";
    radio.checked = true;
    radio.setAttribute("aria-label", "Radio");
    const check = mk("input", "g969-check");
    check.type = "checkbox";
    check.checked = true;
    check.setAttribute("aria-label", "Check");
    const off = mk("button", "g969-off", "Disabled");
    off.disabled = true;
    const link = mk("a", "g969-link", "Link");
    link.href = "#g969";
    const table = mk("table", "g969-table");
    const tr = table.createTBody().insertRow();
    tr.id = "g969-row";
    tr.insertCell().textContent = "Row";
    document.body.prepend(box);
  });

// The element's computed colour and the first opaque background above it, as strings.
const colours = (id: string) =>
  browser.execute((i: string) => {
    const e = document.getElementById(i)!;
    const color = getComputedStyle(e).color;
    let b: Element | null = e;
    let back = "rgb(255, 255, 255)";
    for (; b !== null; b = b.parentElement) {
      const c = getComputedStyle(b).backgroundColor;
      if (!/rgba\(.*, 0\)|transparent/.test(c)) {
        back = c;
        break;
      }
    }
    return { color, back };
  }, id);

describe("the styled client", () => {
  beforeEach(async () => {
    await browser.url("/signed-out");
    await expect($('[data-testid="status"]')).toHaveText(SIGNED_OUT);
    await inject();
  });

  it("a focused control keeps the 3 px ring, not Oat's", async () => {
    for (const id of ["g969-button", "g969-input", "g969-select"]) {
      await browser.keys("Tab");
      const f = await browser.execute(() => {
        const e = document.activeElement as HTMLElement;
        const s = getComputedStyle(e);
        return { id: e.id, style: s.outlineStyle, width: s.outlineWidth, color: s.outlineColor };
      });
      expect([id, f]).toEqual([id, { id, style: "solid", width: "3px", color: RING }]);
    }
  });

  it("no data: image reaches the page, and the controls are native", async () => {
    const seen = () => browser.execute(() => (globalThis as unknown as { g969: string[] }).g969);
    await browser.pause(500);
    const native = await browser.execute(() => {
      const css = (id: string, pseudo?: string) => getComputedStyle(document.getElementById(id)!, pseudo);
      return {
        select: [css("g969-select").appearance || css("g969-select").webkitAppearance, css("g969-select").backgroundImage],
        radio: [css("g969-radio").appearance || css("g969-radio").webkitAppearance, css("g969-radio", "::after").content],
        check: [css("g969-check").appearance || css("g969-check").webkitAppearance, css("g969-check", "::after").content],
      };
    });
    expect(native).toEqual({ select: ["auto", "none"], radio: ["auto", "none"], check: ["auto", "none"] });
    expect(await seen()).toEqual([]);
    // Positive control: a data: image on this page is refused and reported, so zero above is a reading.
    await browser.execute(() => {
      const img = document.createElement("img");
      img.src = "data:image/gif;base64,R0lGODlhAQABAAAAACw=";
      document.body.append(img);
    });
    await browser.waitUntil(async () => (await seen()).length > 0, { timeout: 5_000, timeoutMsg: "the data: image raised no securitypolicyviolation, so the zero above is not a reading" });
  });

  it("a disabled button keeps its contrast", async () => {
    const o = await browser.execute(() => getComputedStyle(document.getElementById("g969-off")!).opacity);
    expect(o).toBe("1");
    const { color, back } = await colours("g969-off");
    expect(ratio(color, back)).toBeGreaterThanOrEqual(7);
  });

  it("a link outside a paragraph is underlined", async () => {
    const d = await browser.execute(() => getComputedStyle(document.getElementById("g969-link")!).textDecorationLine);
    expect(d).toBe("underline");
  });

  it("hover leaves every colour an opaque rgb() with its contrast", async () => {
    for (const [id, min] of [["g969-button", 7], ["g969-link", 7], ["g969-row", 7]] as const) {
      await $(`#${id}`).moveTo();
      const { color, back } = await colours(id);
      // rgb() throws on a hover colour that is color(), relative or translucent.
      expect([id, ratio(color, back) >= min]).toEqual([id, true]);
    }
  });
});
