import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import { AddCluster, existing, guardsUnload, leaving, record, Result, WriteNotices, WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Client, Problem } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { REPEATING, REPLAYED, setAside, unknown } from "./states.tsx";
import { EARLIER_EPOCH, EPOCH_CHANGED_ASIDE, NO_EPOCH, type Outcome, Write, Writes } from "./write.ts";

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

  test("offline, an unknown outcome's notice offers no [Repeat exactly], and offers it again online (journeys §8 Offline row)", async () => {
    const [writes, w] = await unknownWrite();
    const at = (offline: boolean) =>
      renderToStaticMarkup(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author"], offline } }, h(WriteNotices, { go: () => {} }))));
    expect(at(true)).not.toContain("Repeat exactly");
    expect(at(true)).toContain(">Show current state</button>");
    expect(at(false)).toContain(">Repeat exactly</button>");
    expect(writes.repeatable(w)).toBe(true);
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

  test("an unknown outcome refused 403 forbidden on its repeat shows what the route accepts and the problem's fields, in the notice and after it settles aside", async () => {
    const writes = new Writes(() => PRINCIPAL, async () => {});
    let sends = 0;
    const p = { type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: 403, detail: "not allowed", roles: ["author"], instance: "req-1" };
    writes.client = {
      currentEpoch: "e1",
      attempt: async (_m: string, path: string) => {
        if (path === "/recovery") return { status: 200, headers: new Headers({ "Bronzeward-Epoch": "e1" }), data: {} };
        return ++sends === 1 ? undefined : { status: 403, headers: new Headers(), data: p };
      },
    } as unknown as Client;
    const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });
    await writes.submit(w);
    const fields = " Needs author. forbidden: not allowed. Request req-1.";
    expect(html(writes, h(WriteNotices, { go: () => {} }))).toContain(`${unknown("the server answered 403 forbidden")}${fields} Key ${w.key}`);
    writes.setAside(EPOCH_CHANGED_ASIDE);
    expect(html(writes, h(WriteNotices, { go: () => {} }))).toContain(`${setAside(EPOCH_CHANGED_ASIDE)}${fields} Key ${w.key}`);
  });

  // A page under epoch e1 whose write answers come from `answer`, and whose waits before each
  // repeat call `waited`.
  function scripted(answer: (n: number) => unknown, waited: (writes: Writes) => void = () => {}): [Writes, Write] {
    const writes: Writes = new Writes(() => PRINCIPAL, async () => waited(writes));
    let n = 0;
    writes.client = {
      currentEpoch: "e1",
      attempt: async (_m: string, path: string) => (path === "/recovery" ? { status: 200, headers: new Headers({ "Bronzeward-Epoch": "e1" }), data: {} } : answer(++n)),
    } as unknown as Client;
    return [writes, new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" })];
  }
  const internal = { status: 500, headers: new Headers(), data: { type: "urn:bronzeward:problem:internal-error", title: "Internal error", status: 500, detail: "boom", instance: "req-5" } };
  const fields500 = " Internal error: boom. Request req-5.";

  test("set aside while its repeats run after a 500, the notice shows the 500's problem at once", async () => {
    let out = "";
    const [writes, w] = scripted(() => internal, (page) => {
      if (out !== "") return;
      page.setAside(EPOCH_CHANGED_ASIDE);
      out = html(page, h(WriteNotices, { go: () => {} }));
    });
    await writes.submit(w);
    expect(out).toContain(`${setAside(EPOCH_CHANGED_ASIDE)}${fields500} Key ${w.key}`);
  });

  test("a [Repeat exactly] whose attempt drops shows that drop while it repeats, not the earlier 500", async () => {
    let out = "";
    const [writes, w] = scripted((n) => (n <= 4 ? internal : undefined), (page) => {
      if (w.repeating !== undefined && w.doubt !== undefined && out === "") out = html(page, h(WriteNotices, { go: () => {} }));
    });
    expect(await writes.submit(w)).toMatchObject({ kind: "unknown", why: "the server answered 500 internal-error" });
    await writes.submit(w);
    expect(out).toContain(`${unknown("the connection dropped")}${REPEATING} Key ${w.key}`);
    expect(out).not.toContain("Internal error");
  });

  test("a refusal that settles a notice kept since it was set aside shows what the route accepts", async () => {
    const p = { type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: 403, detail: "not allowed", roles: ["author"], instance: "req-1" };
    let page: Writes | undefined;
    const [writes, w] = scripted(() => (page!.setAside(EPOCH_CHANGED_ASIDE), { status: 403, headers: new Headers(), data: p }));
    page = writes;
    expect(await writes.submit(w)).toMatchObject({ kind: "refused", status: 403 });
    expect(html(writes, h(WriteNotices, { go: () => {} }))).toContain("Not saved: the server answered 403. Needs author. forbidden: not allowed. Request req-1. Key");
  });

  test("a kept notice settled by a 409 conflict naming the cluster already recorded names it and opens it (§8.2 409 row)", async () => {
    const conflict = { type: "urn:bronzeward:problem:conflict", title: "Conflict", status: 409, detail: "already recorded", cluster: "cl_SYNTH1" };
    // The first answer is lost, so the notice shows; the repeat is refused with the named record.
    const [writes, w] = scripted((n) => (n === 1 ? undefined : { status: 409, headers: new Headers(), data: conflict }));
    expect(await writes.submit(w)).toMatchObject({ kind: "refused", status: 409 });
    expect(w.kept).toBe(true);
    expect(html(writes, h(WriteNotices, { go: () => {} }))).toContain(
      `Not saved: the server answered 409. Conflict: already recorded. The cluster already recorded: cl_SYNTH1. Key ${w.key}, POST /api/v1/clusters. Read /clusters/cl_SYNTH1.`,
    );
    // [Show current state] reads that record, not the list the write went to.
    expect(record(w)).toEqual({ named: " The cluster already recorded: cl_SYNTH1.", to: "/clusters/cl_SYNTH1" });
  });

  test("a repeat that meets the key's earlier-epoch record names that record's request and epoch, in the notice and the form (§8.2 409 row, §8.4 item 7)", async () => {
    const earlier = { type: "urn:bronzeward:problem:conflict", title: "Conflict", status: 409, detail: "from an earlier recovery epoch", request: "req_SYNTH0", epoch: "e0", instance: "req-9" };
    // The first answer is lost; the repeat meets the record the first attempt left in epoch e0.
    const [writes, w] = scripted((n) => (n === 1 ? undefined : { status: 409, headers: new Headers(), data: earlier }));
    await writes.submit(w);
    const fields = " Conflict: from an earlier recovery epoch. Request req-9. The key's earlier record: request req_SYNTH0, epoch e0.";
    const out = html(writes, h(WriteNotices, { go: () => {} }));
    expect(out).toContain(`${setAside(EARLIER_EPOCH)}${fields} Key ${w.key}`);
    expect(out).not.toContain("Not saved");
    expect(html(writes, h(Result, { w, done: () => "" }))).toContain("See the notice at the top of the page");
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

  test("set aside while its answer is still on its way, the notice shows at once, with no dismissal; the answer settles it, and the notice shows the result until dismissed", async () => {
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
    const settled = html(writes, h(WriteNotices, { go: () => {} }));
    expect(settled).toContain(`Saved (the server answered 201). Key ${w.key}, POST /api/v1/clusters. Read /clusters.`);
    expect(settled).toContain("Dismiss");
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

  test("a 409 conflict naming the cluster or machine already recorded links to it; the write's own resource is not repeated", () => {
    const problem = (extra: Record<string, string>): Problem => ({ type: "urn:bronzeward:problem:conflict", title: "Conflict", status: 409, detail: "already recorded", ...extra });
    const conflict = (extra: Record<string, string>): Outcome => ({ kind: "refused", status: 409, replayed: false, problem: problem(extra) });
    expect(result(conflict({ cluster: "cl_SYNTH1" }))).toContain('The cluster already recorded: <a href="/clusters/cl_SYNTH1" title="cl_SYNTH1">cl_SYNTH1</a>.');
    expect(result(conflict({ machine: "mch_SYNTH1" }))).toContain('The machine already recorded: <a href="/machines/mch_SYNTH1" title="mch_SYNTH1">mch_SYNTH1</a>.');
    expect(existing(409, problem({ machine: "mch_SYNTH1" }), "/machines/mch_SYNTH1")).toBeUndefined();
    expect(result(conflict({}))).not.toContain("already recorded:");
  });

  test("a success whose answer could not be read is saved, names the page to read, and says when replayed", () => {
    expect(result({ kind: "done", status: 201, data: null, replayed: false })).toContain('Saved (the server answered 201), but its answer could not be read. Read <a href="/clusters" title="/clusters">/clusters</a>.');
    expect(result({ kind: "done", status: 201, data: "text", replayed: true })).toContain(`${REPLAYED} Saved (the server answered 201)`);
    expect(result({ kind: "done", status: 201, data: {}, replayed: false })).toContain(">DONE</p>");
  });

  test("an unknown outcome points to its notice, and says what the notice said once it is dismissed (B11)", async () => {
    const [writes, w] = await unknownWrite();
    const form = () => html(writes, h(ReadsProvider, { value: reads }, h(Result, { w, done: () => "DONE" })));
    expect(form()).toContain("See the notice at the top of the page");
    writes.dismiss(w);
    expect(form()).not.toContain("See the notice");
    expect(form()).toContain(`Unknown whether this was saved, and its notice was dismissed. Key ${w.key}, POST /api/v1/clusters. To learn the outcome, read /clusters.`);
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
