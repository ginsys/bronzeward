import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import { AddCluster, guardsUnload, leaving, Result, WriteNotices, WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Client } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { REPEATING, REPLAYED, setAside, unknown } from "./states.tsx";
import { EPOCH_CHANGED_ASIDE, NO_EPOCH, type Outcome, Write, Writes } from "./write.ts";

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
    expect(out).toContain(`${setAside(EPOCH_CHANGED_ASIDE)} Key ${w.key}`);
    expect(out).toContain('<button type="button" disabled="">Repeat exactly</button><button type="button">Show current state</button>');
    writes.dismiss(w);
    expect(html(writes, h(WriteNotices, { go: () => {} }))).toBe("");
  });

  test("set aside because the epoch read got no answer, it says so, not that the epoch changed", async () => {
    const [writes, w] = await unknownWrite();
    writes.setAside(NO_EPOCH);
    expect(html(writes, h(WriteNotices, { go: () => {} }))).toContain(`${setAside(NO_EPOCH)} Key ${w.key}`);
  });

  test("while [Repeat exactly] runs, the notice stays with its key, route and resource, and offers no second repeat", async () => {
    const [writes, w] = await unknownWrite();
    const run = writes.submit(w);
    const out = html(writes, h(WriteNotices, { go: () => {} }));
    expect(out).toContain(`${unknown("the connection dropped")}${REPEATING} Key ${w.key}, POST /api/v1/clusters. To learn the outcome, read /clusters.`);
    expect(out).not.toContain("Repeat exactly");
    expect(out).not.toContain("Dismiss");
    await run;
  });

  test("set aside while its answer is still on its way, the notice shows at once, with no dismissal; the answer settles it", async () => {
    const writes = new Writes(() => PRINCIPAL, async () => {});
    let answer: (a: unknown) => void = () => {};
    writes.client = { currentEpoch: "e1", attempt: () => new Promise((r) => (answer = r)) } as unknown as Client;
    const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });
    const run = writes.submit(w);
    writes.setAside(EPOCH_CHANGED_ASIDE);
    const out = html(writes, h(WriteNotices, { go: () => {} }));
    expect(out).toContain(`${setAside(EPOCH_CHANGED_ASIDE)} Key ${w.key}, POST /api/v1/clusters. To learn the outcome, read /clusters.`);
    expect(out).toContain('<button type="button" disabled="">Repeat exactly</button><button type="button">Show current state</button>');
    expect(out).not.toContain("Dismiss");
    answer({ status: 201, headers: new Headers(), data: { id: "c1" } });
    expect((await run).kind).toBe("done");
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

  test("a notice's own [Show current state] leaves without asking; any other move still asks", async () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    vi.stubGlobal("location", new URL("https://bw.invalid/clusters/new"));
    const [writes] = await unknownWrite();
    expect(leaving(writes, "/clusters", true)).toBe(true);
    expect(ask).not.toHaveBeenCalled();
    expect(leaving(writes, "/clusters")).toBe(false);
    expect(ask).toHaveBeenCalledTimes(1);
  });

  test("Back or Forward asks against the page shown, which the address has already left", async () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    vi.stubGlobal("location", new URL("https://bw.invalid/machines"));
    const [writes] = await unknownWrite();
    expect(leaving(writes, "/machines", false, "/clusters/new")).toBe(false);
    expect(ask).toHaveBeenCalledTimes(1);
    expect(leaving(writes, "/machines?cluster=c1", false, "/machines?cluster=c2")).toBe(true);
    expect(ask).toHaveBeenCalledTimes(1);
  });

  test("set aside while its first answer is on its way, the dialog says the outcome is unknown, as the notice does", async () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    vi.stubGlobal("location", new URL("https://bw.invalid/clusters/new"));
    const writes = new Writes(() => PRINCIPAL, async () => {});
    let answer: (a: unknown) => void = () => {};
    writes.client = { currentEpoch: "e1", attempt: () => new Promise((r) => (answer = r)) } as unknown as Client;
    const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });
    const run = writes.submit(w);
    writes.setAside(EPOCH_CHANGED_ASIDE);
    expect(leaving(writes, "/machines")).toBe(false);
    expect(ask).toHaveBeenCalledWith(`Unknown whether this was saved. Key ${w.key}, POST /api/v1/clusters. To learn the outcome, read /clusters.\nLeave this page?`);
    answer({ status: 201, headers: new Headers(), data: { id: "c1" } });
    await run;
  });

  test("while [Repeat exactly] runs, the dialog says the outcome is unknown, as the notice does", async () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    vi.stubGlobal("location", new URL("https://bw.invalid/clusters/new"));
    const [writes, w] = await unknownWrite();
    const run = writes.submit(w);
    expect(w.outcome).toBeUndefined();
    expect(leaving(writes, "/machines")).toBe(false);
    expect(ask).toHaveBeenCalledWith(`Unknown whether this was saved. Key ${w.key}, POST /api/v1/clusters. To learn the outcome, read /clusters.\nLeave this page?`);
    await run;
  });
});

describe("the unload guard (B11)", () => {
  test("asks while a write is open, unless a notice's own [Show current state] is leaving the page, for that unload only", async () => {
    expect(guardsUnload(new Writes(() => PRINCIPAL))).toBe(false);
    const [writes] = await unknownWrite();
    expect(guardsUnload(writes)).toBe(true);
    writes.unloading = true;
    expect(guardsUnload(writes)).toBe(false);
    // The navigation was stopped: the next reload or close asks again.
    expect(guardsUnload(writes)).toBe(true);
  });
});

describe("the form's result", () => {
  const reads = { go: () => {} } as unknown as Reads;
  const result = (o: Outcome) => {
    const w = new Write({ method: "POST", path: "/clusters", body: {}, resource: "/clusters" });
    w.outcome = o;
    return html(new Writes(() => PRINCIPAL), h(ReadsProvider, { value: reads }, h(Result, { w, done: () => "DONE" })));
  };

  test("a success whose answer could not be read is saved, names the page to read, and says when replayed", () => {
    expect(result({ kind: "done", status: 201, data: null, replayed: false })).toContain('Saved (the server answered 201), but its answer could not be read. Read <a href="/clusters" title="/clusters">/clusters</a>.');
    expect(result({ kind: "done", status: 201, data: "text", replayed: true })).toContain(`${REPLAYED} Saved (the server answered 201)`);
    expect(result({ kind: "done", status: 201, data: {}, replayed: false })).toContain(">DONE</p>");
  });

  test("a replayed refusal is the earlier attempt's, and says so", () => {
    expect(result({ kind: "refused", status: 409, problem: null, replayed: true })).toContain(`${REPLAYED} Not saved: the server answered 409.`);
    expect(result({ kind: "refused", status: 409, problem: null, replayed: false })).toContain(">Not saved: the server answered 409.</p>");
  });

  test("not done shows the last answer's problem fields (§8.2)", () => {
    const problem = { type: "urn:bronzeward:problem:epoch-superseded", title: "Epoch superseded", status: 503, detail: "this process started before the current epoch", instance: "req-9" };
    expect(result({ kind: "not-done", why: "every attempt was answered 503 epoch-superseded, which commits nothing", problem })).toContain(
      ">Not saved: every attempt was answered 503 epoch-superseded, which commits nothing. Epoch superseded: this process started before the current epoch. Request req-9.</p>",
    );
    expect(result({ kind: "not-done", why: NO_EPOCH, problem: null })).toContain(`>Not saved: ${NO_EPOCH}.</p>`);
  });
});

describe("the forms", () => {
  test("Add cluster is disabled with its reason without the author role", () => {
    const writes = new Writes(() => PRINCIPAL);
    expect(html(writes, h(AddCluster), ["viewer"])).toMatch(/<button type="submit" disabled="" aria-describedby="(.+?)">Add cluster<\/button><span id="\1"> Needs author<\/span>/);
    expect(html(writes, h(AddCluster))).toMatch(/<button type="submit">Add cluster<\/button>/);
  });
});
