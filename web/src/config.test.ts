import { expect, test, vi } from "vitest";
import { CLIENT_CONFIG_PATH, loadClientConfig } from "./config.ts";

const valid = { issuer: "https://idp.example.invalid", audience: "bronzeward", cliClientId: "cli", webClientId: "web" };
const answer = (body: unknown, status = 200) => vi.fn(async () => new Response(JSON.stringify(body), { status })) as unknown as typeof fetch;

test("reads the four members from the page's own origin, no-store, refusing redirects", async () => {
  const f = answer({ ...valid, extra: 1 });
  expect(await loadClientConfig(f)).toEqual({ kind: "ok", config: valid });
  expect(f).toHaveBeenCalledWith(CLIENT_CONFIG_PATH, { cache: "no-store", redirect: "error", credentials: "omit" });
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

test("a missing file, a non-JSON body and a transport failure", async () => {
  expect((await loadClientConfig(answer({}, 404))).kind).toBe("invalid");
  expect((await loadClientConfig((async () => new Response("<html>")) as unknown as typeof fetch)).kind).toBe("invalid");
  expect((await loadClientConfig((async () => Promise.reject(new TypeError("x"))) as unknown as typeof fetch)).kind).toBe("transport");
});
