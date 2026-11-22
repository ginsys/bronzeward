import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { Discovery } from "./onboard.ts";
import { MemberRow } from "./onboard.tsx";
import { rolesLine } from "./shell.tsx";

const BAD = `bad${"‮"}word`;
const LONG = "a".repeat(301);
const member = (over: Partial<Discovery["members"][number]>): Discovery["members"][number] => ({
  nodeId: "n1", hostname: "cp-1", address: "10.0.0.1", role: "controlplane", smbiosUuid: "", platform: "metal", answered: true, ...over,
});
const cells = (m: Discovery["members"][number]) => [...renderToStaticMarkup(h("table", {}, h("tbody", {}, h(MemberRow, { m })))).matchAll(/<td>([^<]*)<\/td>/g)].map((c) => c[1]);

describe("the connect page's member table shows a server word only when it is safe text (ginsys/bronzeward#1055)", () => {
  test("a safe member reads as received", () => {
    expect(cells(member({}))).toEqual(["cp-1", "controlplane", "10.0.0.1", "metal", "yes"]);
  });

  test.each([["a bidirectional control", BAD], ["301 code points", LONG]])("a role, hostname or platform with %s is not shown; the address stays as received", (_what, word) => {
    expect(cells(member({ role: word as never, hostname: word, platform: word }))).toEqual(["(not shown)", "(not shown)", "10.0.0.1", "(not shown)", "yes"]);
  });
});

describe("the header's role list shows each role only when it is safe text", () => {
  test("safe roles read as received, none reads none", () => {
    expect(rolesLine(["author", "approver"])).toBe("Roles: author, approver");
    expect(rolesLine([])).toBe("Roles: none");
  });

  test.each([["a bidirectional control", BAD], ["301 code points", LONG]])("a role with %s is not shown, and the others keep their names", (_what, word) => {
    expect(rolesLine([word, "approver"])).toBe("Roles: (not shown), approver");
  });
});
