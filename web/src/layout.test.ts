// The desktop shell's navigation groups and decorative icons (ginsys/bronzeward#973, client contracts §10.4).

import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { Icon, type IconName } from "./icons.tsx";
import { GROUPS, PAGES } from "./router.ts";

describe("the navigation groups", () => {
  test("hold every page exactly once, in the order of the pages within a group", () => {
    const grouped = GROUPS.flatMap(([, tos]) => [...tos]);
    expect([...grouped].sort()).toEqual(PAGES.map(([to]) => to as string).sort());
    for (const [, tos] of GROUPS) {
      const at = tos.map((to) => PAGES.findIndex(([p]) => p === to));
      expect(at).toEqual([...at].sort((a, b) => a - b));
    }
  });
});

describe("an icon", () => {
  test("exists for every page and is decorative, with no title and no style (the content policy refuses a style attribute)", () => {
    for (const [to] of PAGES) {
      const out = renderToStaticMarkup(h(Icon, { name: to.slice(1) as IconName }));
      expect(out).toMatch(/^<svg [^>]*aria-hidden="true"[^>]*><path d="[^"]+"><\/path><\/svg>$/);
      expect(out).not.toMatch(/<title|style=|<style|<text/);
    }
  });
});
