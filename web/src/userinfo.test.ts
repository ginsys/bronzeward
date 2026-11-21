import { describe, expect, test, vi } from "vitest";
import type { Provider } from "./auth.ts";
import { readName, signedInAs } from "./userinfo.ts";

const ISSUER = "https://idp.example.invalid";
const provider: Provider = {
  issuer: ISSUER,
  authorizationEndpoint: `${ISSUER}/authorize`,
  tokenEndpoint: `${ISSUER}/token`,
  revocationEndpoint: undefined,
  endSessionEndpoint: undefined,
  userinfoEndpoint: `${ISSUER}/userinfo`,
};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const asking = (answer: () => Response | Promise<Response>) => vi.fn((_url: string | URL | Request, _init?: RequestInit) => Promise.resolve().then(answer));
const read = (f: ReturnType<typeof asking>, p: Provider = provider) => readName(p, "access-token", "sub-1", f as unknown as typeof fetch, 50);

describe("the name the identity provider gives (OpenID Connect Core §5.3)", () => {
  test("asks the provider's userinfo endpoint once, with the access token, and reads `name`", async () => {
    const f = asking(() => json({ sub: "sub-1", name: "Ada Lovelace", preferred_username: "ada@example.invalid" }));
    expect(await read(f)).toBe("Ada Lovelace");
    expect(f).toHaveBeenCalledTimes(1);
    const [url, init] = f.mock.calls[0]!;
    expect(url).toBe(`${ISSUER}/userinfo`);
    expect(init).toMatchObject({ headers: { Authorization: "Bearer access-token" }, cache: "no-store", credentials: "omit", redirect: "error" });
    expect(init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("falls back to `preferred_username` when there is no `name`, or an empty one", async () => {
    expect(await read(asking(() => json({ sub: "sub-1", preferred_username: "ada" })))).toBe("ada");
    expect(await read(asking(() => json({ sub: "sub-1", name: "", preferred_username: "ada" })))).toBe("ada");
  });

  test.each([
    ["neither claim", { sub: "sub-1" }],
    ["a claim that is not text", { sub: "sub-1", name: 7, preferred_username: ["x"] }],
    ["another subject", { sub: "sub-2", name: "Someone Else" }],
    ["no subject", { name: "Someone Else" }],
    ["a body that is not an object", ["Ada"]],
  ])("%s gives no name", async (_what, body) => {
    expect(await read(asking(() => json(body)))).toBeUndefined();
  });

  test.each([401, 403, 500, 503])("a %i answer gives no name", async (status) => {
    expect(await read(asking(() => json({ sub: "sub-1", name: "Ada" }, status)))).toBeUndefined();
  });

  test("a network error, a body that does not parse, and a slow answer give no name and never throw", async () => {
    expect(await read(asking(() => Promise.reject(new TypeError("offline"))))).toBeUndefined();
    expect(await read(asking(() => new Response("not json", { status: 200 })))).toBeUndefined();
    const slow = vi.fn((_url: string | URL | Request, init?: RequestInit) => new Promise<Response>((_, reject) => init?.signal?.addEventListener("abort", () => reject(init.signal!.reason))));
    expect(await readName(provider, "access-token", "sub-1", slow as unknown as typeof fetch, 20)).toBeUndefined();
    expect(slow).toHaveBeenCalledTimes(1);
  });

  test("asks nothing without an endpoint, without a token, or for an endpoint on another origin than the issuer's", async () => {
    const f = asking(() => json({ sub: "sub-1", name: "Ada" }));
    expect(await read(f, { ...provider, userinfoEndpoint: undefined })).toBeUndefined();
    expect(await readName(provider, undefined, "sub-1", f as unknown as typeof fetch, 50)).toBeUndefined();
    expect(await read(f, { ...provider, userinfoEndpoint: "https://other.example.invalid/userinfo" })).toBeUndefined();
    expect(await read(f, { ...provider, userinfoEndpoint: `http://idp.example.invalid/userinfo` })).toBeUndefined();
    expect(f).not.toHaveBeenCalled();
  });
});

describe("the header's sentence", () => {
  test("a person with a name that is safe to show is named", () => {
    expect(signedInAs("human", undefined, "Ada Lovelace")).toBe("Signed in as Ada Lovelace");
  });

  test.each([
    ["no name yet, or none given", undefined],
    ["an empty name", ""],
    ["a name with a control character", "Ada\u0007Lovelace"],
    ["a name with a bidirectional control", "Ada‮Lovelace"],
    ["a name of more than 300 characters", "A".repeat(301)],
  ])("%s reads as a person", (_what, name) => {
    expect(signedInAs("human", undefined, name)).toBe("Signed in as a person");
  });

  test("an automation identity is named by its service, whatever name was read", () => {
    expect(signedInAs("service", "backup-bot", undefined)).toBe("Signed in as the automation backup-bot");
    expect(signedInAs("service", "backup-bot", "Ada")).toBe("Signed in as the automation backup-bot");
  });
});
