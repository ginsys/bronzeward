import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test, vi } from "vitest";
import { Action, connection, Deprecations, deprecated, ending, Failure, type Identity, identityMain, incompatible, KEPT_PAGE, keepsPage, Kept, MIN_API_REVISION, observed, offlineSince, reading, regaining, ShellContext, type ShellEvents, ShellReads, moveFree, showCurrent } from "./shell.tsx";
import { Conflict, Disconnected, Empty, HUMAN_ONLY, Loading, NOT_ACCEPTED, NotAccepted, Offline, Refused, REPEATING, REPLAYED, Replayed, SET_ASIDE, SetAside, Unknown, UNKNOWN, Unread, Withheld, WITHHELD } from "./states.tsx";
import { NO_EPOCH, SHELL_GONE, Write, Writes } from "./write.ts";
import { type Attempt, type Client, TRANSPORT_FAILURE } from "./request.ts";

const html = (e: ReactElement) => renderToStaticMarkup(e);
// The status message's text, and that it is announced as one.
function status(e: ReactElement): string {
  const m = /<p role="status" data-testid="[a-z]+">(.*?)<\/p>/.exec(html(e));
  expect(m, html(e)).not.toBeNull();
  return m![1]!.replace(/<!-- -->/g, "").replace(/&#x27;/g, "'").replace(/&quot;/g, '"');
}
const at = (hhmm: string) => new Date(`2026-10-26T${hhmm}:00`);
const noop = () => {};

describe("W11 state catalogue: every text a status message", () => {
  test("read states", () => {
    expect(status(h(Loading, { what: "machines" }))).toBe("Loading machines");
    expect(status(h(Empty, { text: "No drafts yet. A draft collects changes before they are published." }))).toBe("No drafts yet. A draft collects changes before they are published.");
    expect(status(h(Withheld))).toBe(WITHHELD);
    expect(status(h(Unread, { at: at("09:46"), causes: ["node unreachable", "provider access denied"] }))).toBe("Observed: not read at 09:46 — node unreachable; provider access denied.");
    expect(status(h(Offline, { since: at("10:12"), readAt: at("10:11") }))).toBe("Offline since 10:12. Showing data read at 10:11. Actions are disabled.");
    expect(status(h(Offline, { since: at("10:12"), readAt: undefined }))).toBe("Offline since 10:12. Actions are disabled.");
    expect(status(h(Disconnected))).toBe(TRANSPORT_FAILURE);
  });

  test("a refusal shows its code, rule, title, detail and instance, never a body; another answer only its status", () => {
    const problem = { type: "urn:bronzeward:problem:validation-failed", title: "Validation failed", status: 422, detail: '"registry/x" has no declaration.', instance: "req-7", rule: "unknown-reference" };
    expect(status(h(Refused, { problem, status: 422, verb: "saved" }))).toBe('Not saved: validation-failed (rule unknown-reference). Validation failed: "registry/x" has no declaration. Request req-7.');
    expect(status(h(Refused, { problem: { type: problem.type, title: problem.title, status: 422 }, status: 422, verb: "read" }))).toBe("Not read: validation-failed. Validation failed");
    expect(status(h(Refused, { problem: null, status: 502, verb: "read" }))).toBe("Not read: the server answered 502.");
  });

  test("a write's 403 names what the route accepts, as a read's does (§8.2 403 row)", () => {
    const forbidden = (extra: Record<string, unknown>) => ({ type: "urn:bronzeward:problem:forbidden", title: "Forbidden", status: 403, instance: "req-2", ...extra });
    expect(status(h(Refused, { problem: forbidden({ roles: ["author"] }), status: 403, verb: "saved" }))).toBe("Not saved: forbidden. Needs author. Forbidden Request req-2.");
    expect(status(h(Refused, { problem: forbidden({ humanOnly: true, roles: ["approver"] }), status: 403, verb: "saved" }))).toBe(`Not saved: forbidden. ${HUMAN_ONLY} Forbidden Request req-2.`);
    // The plan cancellation's creator check says it in its detail.
    expect(status(h(Refused, { problem: forbidden({ plan: "pln_1", roles: ["publisher"], detail: "only the creator" }), status: 403, verb: "saved" }))).toBe("Not saved: forbidden. Forbidden: only the creator Request req-2.");
  });

  test("write states, exercised from #257 on", () => {
    expect(status(h(Conflict, { what: "draft", revision: 7, onShow: noop }))).toBe("Someone changed this draft (revision 7). Your text is not saved.");
    expect(html(h(Conflict, { what: "draft", revision: 7, onShow: noop }))).toContain(">Show their change</button>");
    expect(status(h(Unknown, { onShow: noop }))).toBe(UNKNOWN);
    // A transport failure of a write names a refused redirect as one possible cause (client contracts §6 item 7).
    expect(UNKNOWN).toBe("Unknown whether this was saved: the connection failed. The network may be down, or the server answered with a redirect, which this page refuses.");
    // Repeat exactly only while the page holds the request.
    expect(html(h(Unknown, { onRepeat: noop, onShow: noop }))).toContain(">Repeat exactly</button>");
    expect(html(h(Unknown, { onShow: noop }))).not.toContain("Repeat exactly");
    // The reason it was set aside comes first: an epoch read with no answer is not an epoch change.
    expect(status(h(SetAside, { why: NO_EPOCH, onShow: noop }))).toBe(`Set aside: ${NO_EPOCH}. ${SET_ASIDE}`);
    expect(html(h(SetAside, { why: NO_EPOCH, onShow: noop }))).toContain('<button type="button" disabled="">Repeat exactly</button>');
    // While [Repeat exactly] runs, the notice stays, with no second repeat and no dismissal.
    const notice = h(Unknown, { onRepeat: noop, onShow: noop, onDismiss: noop, repeating: true });
    expect(status(notice)).toBe(`${UNKNOWN}${REPEATING}`);
    const repeating = html(notice);
    expect(repeating).toContain(">Show current state</button>");
    expect(repeating).not.toContain("Repeat exactly");
    expect(repeating).not.toContain("Dismiss");
    expect(status(h(Replayed))).toBe(REPLAYED);
    expect(status(h(NotAccepted, { onSignIn: noop }))).toBe(NOT_ACCEPTED);
  });
});

describe("the shell", () => {
  test("[Show current state] on the view already shown reads it again; on another view it moves there", () => {
    const moved: string[] = [];
    let reads = 0;
    const show = (to: string, here: string) => showCurrent(to, here, (t) => void moved.push(t), () => void reads++);
    show("/clusters", "/clusters");
    expect([moved, reads]).toEqual([[], 1]);
    show("/clusters", "/machines");
    show("/clusters", "/clusters?cluster=c1");
    expect([moved, reads]).toEqual([["/clusters", "/clusters"], 1]);
  });

  test("during a refused move's return, go and show ask nothing and do nothing; afterwards they ask first", () => {
    let busy = true;
    const asked: string[] = [];
    const moved: string[] = [];
    let reads = 0;
    const leave = (to: string, answer: boolean) => () => (asked.push(to), answer);
    // The shell's go and show, as SignedIn builds them from the same helper.
    const go = (to: string, answer = true) => moveFree(() => busy, leave(to, answer), () => void moved.push(to));
    const show = (to: string, here: string) => moveFree(() => busy, leave(to, true), () => showCurrent(to, here, (t) => void moved.push(t), () => void reads++));
    go("/plans");
    show("/clusters", "/machines");
    show("/clusters", "/clusters");
    expect([asked, moved, reads]).toEqual([[], [], 0]);
    busy = false;
    go("/plans");
    show("/clusters", "/machines");
    show("/clusters", "/clusters");
    expect([asked, moved, reads]).toEqual([["/plans", "/clusters", "/clusters"], ["/plans", "/clusters"], 1]);
    // A refused question still stops the move.
    go("/sources", false);
    expect(moved).toEqual(["/plans", "/clusters"]);
  });

  const action = (roles: string[], offline = false) =>
    html(h(ShellContext, { value: { roles: roles as never, offline } }, h(Action, { label: "Create draft", needs: ["author"], onClick: noop })));

  test("an action the roles do not allow is disabled with its reason, and offline every action is", () => {
    expect(action(["author"])).toMatch(/<button type="button">Create draft<\/button>/);
    expect(action(["viewer", "publisher"])).toMatch(/<button type="button" disabled="" aria-describedby="(.+?)">Create draft<\/button><span id="\1"> Needs author<\/span>/);
    expect(action(["author"], true)).toMatch(/disabled="".*<span id=".+?"> Offline<\/span>/);
  });

  test("a read refused 403 names what the route accepts, from the server's answer", () => {
    const forbidden = (extra: Record<string, unknown>) => ({ kind: "refused" as const, status: 403, problem: { type: "urn:bronzeward:problem:forbidden", title: "Forbidden", status: 403, ...extra } });
    expect(status(h(Failure, { res: forbidden({ roles: ["author", "publisher"] }), sub: "h-viewer" }))).toBe("Needs author or publisher; you are signed in as h-viewer");
    expect(status(h(Failure, { res: forbidden({ humanOnly: true, roles: ["approver"] }), sub: "svc" }))).toBe(HUMAN_ONLY);
    expect(status(h(Failure, { res: forbidden({ plan: "pln_1", detail: "only the plan's creator may cancel it" }), sub: "h-publisher" }))).toBe("only the plan's creator may cancel it; you are signed in as h-publisher");
    // §8.2: each row's problem fields under it.
    const fields = '<p data-testid="problem">Not read: Forbidden (needs author). Request req-1.</p>';
    expect(html(h(Failure, { res: forbidden({ roles: ["author"], detail: "needs author", instance: "req-1" }), sub: "u" }))).toContain(fields);
    expect(html(h(Failure, { res: forbidden({ humanOnly: true, detail: "needs author", instance: "req-1" }), sub: "u" }))).toContain(fields);
    expect(status(h(Failure, { res: { kind: "transport" }, sub: "" }))).toBe(TRANSPORT_FAILURE);
    expect(status(h(Failure, { res: { kind: "defect", message: "the response is not JSON" }, sub: "" }))).toBe("Not read: the response is not JSON.");
  });

  test("offline: each cause ends only on its own event, and the banner dates from the earlier", () => {
    const t1 = at("10:11");
    const t2 = at("10:12");
    let c = connection({}, "offline", t2);
    // A late answer while the browser is offline keeps the banner and the actions disabled.
    c = connection(c, "answered", t2);
    expect(offlineSince(c)).toEqual(t2);
    c = connection(c, "lost", t1);
    expect(offlineSince(c)).toEqual(t1);
    // Back online, a read that found no answer still holds until one answers.
    c = connection(c, "online", t2);
    expect(offlineSince(c)).toEqual(t1);
    expect(offlineSince(connection(c, "answered", t2))).toBeUndefined();
  });

  test("offline: any answer the server gives ends a lost read, a refusal too; no answer is lost only after data was shown", () => {
    const problem = { type: "urn:bronzeward:problem:not-found", title: "Not found", status: 404 };
    for (const res of [
      { kind: "ok" as const, data: {} },
      { kind: "refused" as const, status: 404, problem },
      { kind: "unavailable" as const, status: 503, problem: null },
      { kind: "restart" as const, problem },
      { kind: "defect" as const, message: "the response is not JSON" },
    ]) {
      expect(observed(res, true)).toBe("answered");
      // After a lost read, the refusal clears the banner it would otherwise stay under.
      expect(offlineSince(connection(connection({}, "lost", at("10:11")), observed(res, true)!, at("10:12")))).toBeUndefined();
    }
    expect(observed({ kind: "transport" }, true)).toBe("lost");
    expect(observed({ kind: "transport" }, false)).toBeUndefined();
    expect(observed({ kind: "discarded" }, true)).toBeUndefined();
  });

  test("#443: the count bumps only when a read's answer ends the banner", () => {
    const [t1, t2] = [at("10:11"), at("10:12")];
    const lost = regaining([{}, 0], "lost", t1);
    expect(lost[1]).toBe(0);
    expect(regaining(lost, "answered", t2)).toEqual([{ lost: undefined }, 1]);
    // An answer with no banner, or one that leaves the browser's offline event standing, does not.
    expect(regaining([{}, 0], "answered", t2)[1]).toBe(0);
    expect(regaining(regaining(lost, "offline", t2), "answered", t2)[1]).toBe(0);
    // The online event ends its own cause; the shell reads every page again then, through `again`.
    expect(regaining(regaining([{}, 0], "offline", t1), "online", t2)[1]).toBe(0);
  });

  test("§7 item 4: the deprecation notices render once each, as status messages", () => {
    const shown = html(h(Deprecations, { routes: ["/api/v1/server", "/api/v1/identity"] }));
    expect(shown.match(/role="status" data-testid="deprecation"/g)).toHaveLength(2);
    expect(shown).toContain(deprecated("/api/v1/server"));
  });

  test("§7 item 3: the refusal of an older server names both revisions", () => {
    expect(incompatible(1)).toBe(`This server serves API revision 1; this client needs API revision ${MIN_API_REVISION} or later.`);
  });

  test("ginsys/bronzeward#517 H10: a page holding unsaved text stays shown while a new epoch's server and identity are read", () => {
    const loading = { kind: "loading" } as const;
    const server = (apiRevision: number) => ({ kind: "ok", data: { version: "v", apiRevision } }) as unknown as Parameters<typeof keepsPage>[1];
    const identity = { kind: "ok", data: {} } as unknown as Parameters<typeof keepsPage>[2];
    expect(keepsPage(true, loading, loading)).toBe(true);
    expect(keepsPage(true, server(MIN_API_REVISION), loading)).toBe(true);
    expect(keepsPage(false, loading, loading)).toBe(false);
    expect(keepsPage(true, server(MIN_API_REVISION), identity)).toBe(false);
    // An incompatible server reads no identity: the page goes.
    expect(keepsPage(true, server(MIN_API_REVISION - 1), loading)).toBe(false);
    expect(keepsPage(true, { kind: "refused", status: 409, problem: {} } as unknown as Parameters<typeof keepsPage>[1], loading)).toBe(false);
    // A read a later one can mend keeps it: no answer, or a server error, of either read.
    const transport = { kind: "transport" } as unknown as Parameters<typeof keepsPage>[1];
    const unavailable = { kind: "refused", status: 503, problem: null } as const;
    expect(keepsPage(true, transport, loading)).toBe(true);
    expect(keepsPage(true, unavailable, loading)).toBe(true);
    expect(keepsPage(true, server(MIN_API_REVISION), transport as unknown as Parameters<typeof keepsPage>[2])).toBe(true);
    expect(keepsPage(true, server(MIN_API_REVISION), unavailable)).toBe(true);
    expect(keepsPage(true, server(MIN_API_REVISION), { kind: "refused", status: 403, problem: null })).toBe(false);
    // PR review: the client's own answer once its retries of a 500 or 503 run out keeps it too.
    const exhausted = { kind: "unavailable", status: 503, problem: null } as const;
    expect(keepsPage(true, exhausted, loading)).toBe(true);
    expect(keepsPage(true, server(MIN_API_REVISION), exhausted)).toBe(true);
  });

  test("ginsys/bronzeward#517 H10: a page kept across an epoch change sends nothing", () => {
    const kept = html(h(ShellContext, { value: { roles: ["author"], offline: false, kept: true } }, h(Action, { label: "Save", needs: ["author"], onClick: noop })));
    expect(kept).toMatch(new RegExp(`disabled="".*<span id=".+?"> ${KEPT_PAGE}</span>`));
  });
});

// #410: the shell's one invalidation point, against a client whose fetch answers each request in
// turn; an answer given as a promise holds its request until the test resolves it.
describe("the shell's reads", () => {
  const view = { principal: "iss sub", route: "/machines", query: "" };
  const answer = (status: number, body: unknown, epoch: string) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": status === 200 ? "application/json" : "application/problem+json", "Bronzeward-Epoch": epoch },
    });
  const problem = (status: number, code: string, epoch: string) =>
    answer(status, { type: `urn:bronzeward:problem:${code}`, title: code, status }, epoch);
  function shell(answers: (Response | Promise<Response>)[]) {
    const fetch = vi.fn(async () => {
      const next = answers.shift();
      if (next === undefined) throw new Error("no answer left");
      return next;
    });
    const events: ShellEvents = { onUnauthenticated: vi.fn(), onRevoked: vi.fn(), onEpochChange: vi.fn(), onPreEntry: vi.fn(), setMode: vi.fn(), observe: vi.fn() };
    const reads = new ShellReads({ fetch: fetch as unknown as typeof globalThis.fetch, sleep: async () => {} }, () => events);
    reads.client.setView(view);
    return { reads, fetch, events };
  }

  test("an identity answer from the old epoch, settled after the new epoch cleared the shell, is not shown", async () => {
    const { reads, events } = shell([answer(200, { version: "v" }, "e1"), answer(200, { subject: "old" }, "e1"), answer(200, {}, "e2")]);
    await reads.client.read("/server"); // e1 recorded
    const valid = reads.since();
    const old = await reads.client.read("/identity"); // answered under e1, its settle still to come
    expect(old).toMatchObject({ kind: "ok" });
    expect(await reads.client.read("/recovery")).toEqual({ kind: "discarded" }); // announces e2
    expect(events.onEpochChange).toHaveBeenCalledTimes(1);
    const set = vi.fn();
    reads.settle(old, set, "identity", valid, () => false);
    expect(set).not.toHaveBeenCalled();
    expect(reads.shown.identity).toBe(false);
  });

  test("GET /server's refusal before the entry drops the reads in flight: none is sent again", async () => {
    let release!: (r: Response) => void;
    const held = new Promise<Response>((r) => (release = r));
    const { reads, fetch, events } = shell([held, problem(409, "recovery-mode-active", "e2")]);
    const pending = reads.client.read("/identity"); // sent before any epoch was recorded
    const refusal = await reads.client.read("/server"); // e2 recorded
    const set = vi.fn();
    reads.settle(refusal, set, "server", reads.since(), () => false);
    expect(set).toHaveBeenCalledWith(refusal); // shown as GET /server's own answer
    expect(events.onPreEntry).not.toHaveBeenCalled();
    release(answer(200, { subject: "old" }, "e1")); // an epoch other than e2: ambiguous, not read again
    expect(await pending).toEqual({ kind: "discarded" });
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  test("a write's process refusal is handled as a page read's: the reads in flight are dropped and the state before the entry shown", async () => {
    let release!: (r: Response) => void;
    const held = new Promise<Response>((r) => (release = r));
    const { reads, fetch, events } = shell([held]);
    reads.shown = { server: true, identity: true, recovery: true };
    const valid = reads.since();
    const pending = reads.client.read("/identity");
    const refusal = { kind: "refused", status: 409, problem: { type: "urn:bronzeward:problem:recovery-mode-active", title: "Recovery mode active", status: 409 } } as const;
    reads.preEntry(refusal);
    expect([valid(), reads.shown]).toEqual([false, { server: false, identity: false, recovery: false }]);
    expect(events.setMode).toHaveBeenCalledWith("recovery");
    expect(events.onPreEntry).toHaveBeenCalledWith(refusal);
    release(answer(200, { subject: "old" }, "e1"));
    expect(await pending).toEqual({ kind: "discarded" });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  test("a revocation ends the page, and an answer from before it is not shown", async () => {
    const { reads, events } = shell([answer(200, { subject: "old" }, "e1"), problem(403, "identity-revoked", "e1")]);
    const valid = reads.since();
    const old = await reads.client.read("/identity");
    const revoked = await reads.client.read("/server");
    reads.settle(revoked, vi.fn(), "server", reads.since(), () => false);
    expect(events.onRevoked).toHaveBeenCalledWith(revoked);
    const set = vi.fn();
    reads.settle(old, set, "identity", valid, () => false);
    expect(set).not.toHaveBeenCalled();
  });

  test("sign-out: a read answered before it is not shown, and one in flight is not sent again", async () => {
    let release!: (r: Response) => void;
    const held = new Promise<Response>((r) => (release = r));
    const { reads, fetch } = shell([answer(200, { subject: "old" }, "e1"), held]);
    const valid = reads.since();
    const old = await reads.client.read("/identity");
    const retrying = reads.client.read("/server");
    const signOut = vi.fn();
    reads.signOut(signOut);
    expect(signOut).toHaveBeenCalledTimes(1);
    const set = vi.fn();
    reads.settle(old, set, "identity", valid, () => false);
    expect(set).not.toHaveBeenCalled();
    release(problem(503, "dependency-unavailable", "e1")); // retried, but for the sign-out
    expect(await retrying).toEqual({ kind: "discarded" });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});

// #513: an identity read that fails after a good one keeps the page mounted, with its unsaved text.
describe("an identity read that fails after a good one", () => {
  const identity: Identity = { kind: "human", issuer: "i", subject: "s", service: null, principal: "p", roles: ["author"], expiresAt: "2026-11-02T12:00:00Z", epoch: "e" };
  const ok = { kind: "ok", data: identity } as const;
  const unavailable = { kind: "unavailable", status: 503, problem: null } as const;
  const reads = () => {
    const events: ShellEvents = { onUnauthenticated: vi.fn(), onRevoked: vi.fn(), onEpochChange: vi.fn(), onPreEntry: vi.fn(), setMode: vi.fn(), observe: vi.fn() };
    return { reads: new ShellReads({}, () => events), events };
  };
  const settled = (r: ShellReads, res: Parameters<ShellReads["settle"]>[0], what: "identity" | "server" = "identity") => {
    const set = vi.fn();
    const lost = r.settle(res, set, what, () => true, () => r.shown[what]);
    return { set, lost };
  };

  test("the shell keeps the answer and is told the failure was kept", () => {
    const { reads: r } = reads();
    expect(settled(r, ok).set).toHaveBeenCalledWith(ok);
    const { set } = settled(r, unavailable);
    expect(set).toHaveBeenCalledWith(unavailable, true);
    expect(r.shown.identity).toBe(true); // a later read without answer still keeps the data under the banner
    expect(settled(r, { kind: "transport" })).toMatchObject({ lost: true });
  });

  test("a failure with no good read before it replaces the page as before", () => {
    const { reads: r } = reads();
    expect(settled(r, unavailable).set).toHaveBeenCalledWith(unavailable);
    expect(r.shown.identity).toBe(false);
  });

  test("an answer after the failure shows the page again, with no failure above it", () => {
    const { reads: r } = reads();
    settled(r, ok);
    settled(r, unavailable);
    expect(settled(r, ok).set).toHaveBeenCalledWith(ok);
    expect(reading({ load: ok, failed: unavailable }, ok, false)).toEqual({ load: ok });
  });

  test("the state keeps the last answer and the failure; without a kept answer the failure replaces it", () => {
    expect(reading({ load: ok }, unavailable, true)).toEqual({ load: ok, failed: unavailable });
    expect(reading({ load: ok, failed: unavailable }, { kind: "defect", message: "m" }, true)).toEqual({ load: ok, failed: { kind: "defect", message: "m" } });
    expect(reading({ load: ok }, unavailable, false)).toEqual({ load: unavailable });
    expect(reading({ load: { kind: "loading" } }, unavailable, true)).toEqual({ load: unavailable });
  });

  test("the page keeps its place under the failure, so it stays mounted with its unsaved text", () => {
    const page = h("section", { id: "page" }, "editor");
    const before = Kept({ failed: undefined, sub: "s", children: page }) as ReactElement<{ children: unknown[] }>;
    const after = Kept({ failed: unavailable, sub: "s", children: page }) as ReactElement<{ children: unknown[] }>;
    // Same element type, same slot: React updates the page in place instead of remounting it.
    expect(before.type).toBe(after.type);
    expect(before.props.children[1]).toBe(page);
    expect(after.props.children[1]).toBe(page);
    const markup = html(h(Kept, { failed: unavailable, sub: "s", children: page }));
    expect(markup).toContain('<section id="page">editor</section>');
    expect(markup.indexOf("503")).toBeGreaterThanOrEqual(0);
    expect(markup.indexOf("503")).toBeLessThan(markup.indexOf('<section id="page">'));
  });

  test("the shell's identity branch hands the page the same type, key and slot before and after the failure", () => {
    const page = h("section", { id: "page" }, "editor");
    const slot = (identity: Parameters<typeof identityMain>[0]) => identityMain(identity, "s", page) as ReactElement<{ children: unknown; failed: unknown }>;
    const good = slot({ load: ok });
    const kept = slot(reading({ load: ok }, unavailable, true));
    const again = slot(reading({ load: ok, failed: unavailable }, ok, false));
    // React keeps a component mounted, with its state and unsaved guard, while its type, key and slot hold.
    for (const el of [kept, again]) {
      expect(el.type).toBe(good.type);
      expect(el.key).toBe(good.key);
      expect(el.props.children).toBe(page);
    }
    expect(kept.props.failed).toBe(unavailable);
    expect(good.props.failed).toBeUndefined();
    expect(again.props.failed).toBeUndefined();
    // A first read that fails has no page to keep.
    expect(slot({ load: unavailable }).type).not.toBe(good.type);
  });

  test("a refusal that ends the session still ends the page, and keeps nothing", () => {
    const { reads: r, events } = reads();
    settled(r, ok);
    const unauthenticated = { kind: "unauthenticated", problem: null } as const;
    const { set } = settled(r, unauthenticated);
    expect(events.onUnauthenticated).toHaveBeenCalledWith(unauthenticated);
    expect(set).not.toHaveBeenCalled();
    const revoked = { kind: "revoked", problem: { type: "urn:bronzeward:problem:identity-revoked", title: "Identity revoked", status: 403 } } as const;
    expect(settled(r, revoked).set).not.toHaveBeenCalled();
    expect(events.onRevoked).toHaveBeenCalledWith(revoked);
  });

  test("the process's refusal after a good read shows the state before the entry, not the page", () => {
    const { reads: r, events } = reads();
    settled(r, ok);
    const refusal = { kind: "refused", status: 409, problem: { type: "urn:bronzeward:problem:recovery-mode-active", title: "Recovery mode active", status: 409 } } as const;
    expect(settled(r, refusal).set).not.toHaveBeenCalled();
    expect(events.onPreEntry).toHaveBeenCalledWith(refusal);
    expect(r.shown.identity).toBe(false);
  });

  test("GET /server is not kept: it is read again only while it has no data", () => {
    const { reads: r } = reads();
    settled(r, { kind: "ok", data: { version: "v" } }, "server");
    expect(settled(r, unavailable, "server").set).toHaveBeenCalledWith(unavailable);
  });
});

// §8.2, §9.6: a refusal that ends the page stops the other writes at once, before the shell's
// effect cleanup runs.
test("a write's identity-revoked ends the page: another write waiting to repeat sends nothing more", async () => {
  const revoked = { type: "urn:bronzeward:problem:identity-revoked", title: "Identity revoked", status: 403 };
  // The first write's attempt is dropped, the second's revoked; a third POST would be the repeat.
  const posts: (Attempt | undefined)[] = [undefined, { status: 403, headers: new Headers(), data: revoked }, { status: 201, headers: new Headers(), data: {} }];
  let sent = 0;
  const client = {
    currentEpoch: "e1",
    revoked: () => {},
    attempt: async (_m: string, path: string) => {
      if (path === "/recovery") return { status: 200, headers: new Headers({ "Bronzeward-Epoch": "e1" }), data: null };
      sent++;
      return posts.shift();
    },
  } as unknown as Client;
  let go!: () => void;
  const waited = new Promise<void>((r) => (go = r));
  const writes = new Writes(() => "iss sub", () => waited);
  writes.client = client;
  const page = vi.fn();
  writes.onEnded = ending(writes, () => client, page);
  const waiting = writes.submit(new Write({ method: "POST", path: "/clusters", body: { name: "a" }, resource: "/clusters" }));
  await vi.waitFor(() => expect(sent).toBe(1));
  expect(await writes.submit(new Write({ method: "POST", path: "/clusters", body: { name: "b" }, resource: "/clusters" }))).toMatchObject({ kind: "refused", status: 403 });
  expect(page).toHaveBeenCalledTimes(1);
  go();
  expect(await waiting).toEqual({ kind: "set-aside", why: SHELL_GONE, ambiguous: true, problem: null });
  expect(sent).toBe(2);
});
