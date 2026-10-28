import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test, vi } from "vitest";
import { Action, connection, Deprecations, deprecated, Failure, incompatible, MIN_API_REVISION, observed, offlineSince, ShellContext, type ShellEvents, ShellReads } from "./shell.tsx";
import { Conflict, Disconnected, Empty, HUMAN_ONLY, Loading, NOT_ACCEPTED, NotAccepted, Offline, Refused, REPLAYED, Replayed, SET_ASIDE, SetAside, Unknown, UNKNOWN, Unread, Withheld, WITHHELD } from "./states.tsx";
import { TRANSPORT_FAILURE } from "./request.ts";

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

  test("write states, exercised from #257 on", () => {
    expect(status(h(Conflict, { what: "draft", revision: 7, onShow: noop }))).toBe("Someone changed this draft (revision 7). Your text is not saved.");
    expect(html(h(Conflict, { what: "draft", revision: 7, onShow: noop }))).toContain(">Show their change</button>");
    expect(status(h(Unknown, { onShow: noop }))).toBe(UNKNOWN);
    // Repeat exactly only while the page holds the request.
    expect(html(h(Unknown, { onRepeat: noop, onShow: noop }))).toContain(">Repeat exactly</button>");
    expect(html(h(Unknown, { onShow: noop }))).not.toContain("Repeat exactly");
    expect(status(h(SetAside, { onShow: noop }))).toBe(SET_ASIDE);
    expect(html(h(SetAside, { onShow: noop }))).toContain('<button type="button" disabled="">Repeat exactly</button>');
    expect(status(h(Replayed))).toBe(REPLAYED);
    expect(status(h(NotAccepted, { onSignIn: noop }))).toBe(NOT_ACCEPTED);
  });
});

describe("the shell", () => {
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

  test("§7 item 4: the deprecation notices render once each, as status messages", () => {
    const shown = html(h(Deprecations, { routes: ["/api/v1/server", "/api/v1/identity"] }));
    expect(shown.match(/role="status" data-testid="deprecation"/g)).toHaveLength(2);
    expect(shown).toContain(deprecated("/api/v1/server"));
  });

  test("§7 item 3: the refusal of an older server names both revisions", () => {
    expect(incompatible(1)).toBe(`This server serves API revision 1; this client needs API revision ${MIN_API_REVISION} or later.`);
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
