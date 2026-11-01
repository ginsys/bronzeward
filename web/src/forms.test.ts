import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import { AddCluster, leaving, WriteNotices, WritesProvider } from "./forms.tsx";
import type { Client } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { SET_ASIDE, unknown } from "./states.tsx";
import { EPOCH_CHANGED_ASIDE, Write, Writes } from "./write.ts";

const PRINCIPAL = "https://idp.invalid SYNTH-sub-1";
const html = (writes: Writes, e: ReactElement, roles: string[] = ["author"]) =>
  renderToStaticMarkup(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: roles as never, offline: false } }, e))).replace(/<!-- -->/g, "").replace(/&#x27;/g, "'");

// A page whose every write gets no answer, under epoch e1.
async function unknownWrite(): Promise<[Writes, Write]> {
  const writes = new Writes(() => PRINCIPAL, async () => {});
  writes.client = {
    currentEpoch: "e1",
    attempt: async (_m: string, path: string) => (path === "/recovery" ? { status: 200, headers: new Headers({ "Bronzeward-Epoch": "e1" }), data: {} } : undefined),
  } as unknown as Client;
  const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });
  await writes.submit(w);
  return [writes, w];
}

afterEach(() => vi.unstubAllGlobals());

describe("the notice (B11)", () => {
  test("an unknown outcome names the key, the route and the resource, offers [Repeat exactly] and Dismiss", async () => {
    const [writes, w] = await unknownWrite();
    const out = html(writes, h(WriteNotices, { go: () => {} }));
    expect(out).toContain(`${unknown("the connection dropped")} Key ${w.key}, POST /api/v1/clusters. To learn the outcome, read /clusters.`);
    expect(out).toContain(">Repeat exactly</button>");
    expect(out).toContain(">Dismiss</button>");
  });

  test("set aside, repeating stays disabled; dismissed, the notice goes", async () => {
    const [writes, w] = await unknownWrite();
    writes.setAside(EPOCH_CHANGED_ASIDE);
    const out = html(writes, h(WriteNotices, { go: () => {} }));
    expect(out).toContain(SET_ASIDE);
    expect(out).toContain('<button type="button" disabled="">Repeat exactly</button><button type="button">Show current state</button>');
    writes.dismiss(w);
    expect(html(writes, h(WriteNotices, { go: () => {} }))).toBe("");
  });
});

describe("the in-app leave guard (B11)", () => {
  test("asks only while a write is open and the move is to another page, repeating the notice", async () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    vi.stubGlobal("location", new URL("https://bw.invalid/clusters/new"));
    const idle = new Writes(() => PRINCIPAL);
    expect(leaving(idle, "/machines")).toBe(true);
    const [writes, w] = await unknownWrite();
    expect(leaving(writes, "/clusters/new?x=1")).toBe(true);
    expect(ask).not.toHaveBeenCalled();
    expect(leaving(writes, "/machines")).toBe(false);
    expect(ask).toHaveBeenCalledWith(`Unknown whether this was saved. Key ${w.key}, POST /api/v1/clusters. To learn the outcome, read /clusters.\nLeave this page?`);
  });
});

describe("the forms", () => {
  test("Add cluster is disabled with its reason without the author role", () => {
    const writes = new Writes(() => PRINCIPAL);
    expect(html(writes, h(AddCluster), ["viewer"])).toMatch(/<button type="submit" disabled="" aria-describedby="(.+?)">Add cluster<\/button><span id="\1"> Needs author<\/span>/);
    expect(html(writes, h(AddCluster))).toMatch(/<button type="submit">Add cluster<\/button>/);
  });
});
