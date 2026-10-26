import { expect, test, vi } from "vitest";
import { CLIENT_CONFIG_PATH, loadClientConfig } from "./config.ts";

const valid = { issuer: "https://idp.example.invalid", audience: "bronzeward", cliClientId: "cli", webClientId: "web" };
const answer = (body: unknown, status = 200) => vi.fn(async () => new Response(JSON.stringify(body), { status })) as unknown as typeof fetch;

test("reads the four members from the page's own origin, no-store, refusing redirects", async () => {
  const f = answer({ ...valid, extra: 1 });
  expect(await loadClientConfig(f)).toEqual({ kind: "ok", config: { ...valid, scopes: [] } });
  expect(f).toHaveBeenCalledWith(CLIENT_CONFIG_PATH, { cache: "no-store", redirect: "error", credentials: "omit" });
});

test("scopes: a space-separated string of extra scopes", async () => {
  const res = await loadClientConfig(answer({ ...valid, scopes: " urn:example:aud  profile " }));
  expect(res).toEqual({ kind: "ok", config: { ...valid, scopes: ["urn:example:aud", "profile"] } });
});

test.each([
  [["a", "b"], "scopes is not a string"],
  ["  ", "scopes is empty"],
  ["openid urn:x", "scopes repeats openid, which is always requested"],
  ["offline_access", "scopes repeats offline_access, which is always requested"],
  ["a\tb", "scopes holds a character a scope cannot"],
  ['a"b', "scopes holds a character a scope cannot"],
])("scopes %j is a configuration error", async (scopes, reason) => {
  expect(await loadClientConfig(answer({ ...valid, scopes }))).toEqual({ kind: "invalid", reason: `${CLIENT_CONFIG_PATH}: ${reason}` });
});

test.each(["issuer", "audience", "cliClientId", "webClientId"])("refuses a file without %s", async (m) => {
  const body: Record<string, unknown> = { ...valid };
  delete body[m];
  expect(await loadClientConfig(answer(body))).toEqual({ kind: "invalid", reason: `${CLIENT_CONFIG_PATH} has no ${m}` });
});

test.each([
  ["http://idp.example.invalid", "invalid"],
  ["http://localhost:58280", "ok"],
  ["http://127.0.0.1:5556", "ok"],
  ["not a url", "invalid"],
])("issuer %s is %s (§6 item 1)", async (issuer, kind) => {
  expect((await loadClientConfig(answer({ ...valid, issuer }))).kind).toBe(kind);
});

test("a missing file, a non-JSON body and a transport failure, before or during the body", async () => {
  expect((await loadClientConfig(answer({}, 404))).kind).toBe("invalid");
  expect((await loadClientConfig((async () => new Response("<html>")) as unknown as typeof fetch)).kind).toBe("invalid");
  expect((await loadClientConfig((async () => Promise.reject(new TypeError("x"))) as unknown as typeof fetch)).kind).toBe("transport");
  const cut = async () => new Response(new ReadableStream({ pull: (ctl) => ctl.error(new TypeError("network error")) }));
  expect((await loadClientConfig(cut as unknown as typeof fetch)).kind).toBe("transport");
});
