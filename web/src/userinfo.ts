// The name in the header (ginsys/bronzeward#1049): the person's own name as the identity provider gives it
// at its userinfo endpoint (OpenID Connect Core §5.3), asked for with the access token the page already
// holds. The server's identity read serves no name, and a provider such as ZITADEL leaves it out of both
// the access token and the ID token. The name is shown and nothing else: never sent to the server, never
// stored, never used for a decision. Any failure leaves the sentence that says only "a person".

import type { Provider } from "./auth.ts";
import { SAFE } from "./safe.ts";

// How long the page waits for the provider before it shows "a person" and stops asking.
export const NAME_WAIT_MS = 5_000;

const originOf = (url: string): string | undefined => {
  try {
    return new URL(url).origin;
  } catch {
    return undefined;
  }
};

const text = (v: unknown): string | undefined => (typeof v === "string" && v !== "" ? v : undefined);

// The `name` the provider gives for the signed-in `sub`, else its `preferred_username`; undefined for every
// other case, never an error. Nothing is asked unless the endpoint is on the issuer's own origin, the one
// origin the page's content security policy and the access token already reach, and the answer counts only
// when it is about the same subject (§5.3.2).
export async function readName(provider: Provider, token: string | undefined, sub: string, fetchImpl: typeof fetch = fetch, wait = NAME_WAIT_MS): Promise<string | undefined> {
  const endpoint = provider.userinfoEndpoint;
  if (endpoint === undefined || token === undefined || originOf(endpoint) !== originOf(provider.issuer)) return undefined;
  try {
    const res = await fetchImpl(endpoint, { headers: { Authorization: `Bearer ${token}` }, cache: "no-store", credentials: "omit", redirect: "error", signal: AbortSignal.timeout(wait) });
    if (!res.ok) return undefined;
    const body: unknown = await res.json();
    if (typeof body !== "object" || body === null || Array.isArray(body)) return undefined;
    const claims = body as Record<string, unknown>;
    return claims["sub"] === sub ? (text(claims["name"]) ?? text(claims["preferred_username"])) : undefined;
  } catch {
    return undefined;
  }
}

// The header's sentence: the person by name when there is one that is safe to show (principals.ts SAFE keeps
// control and format characters and a long text out of the page), else "a person"; an automation identity
// by its service.
export function signedInAs(kind: "human" | "service", service: string | null | undefined, name: string | undefined): string {
  if (kind === "service") return service ? `Signed in as the automation ${service}` : "Signed in as an automation";
  return name !== undefined && SAFE.test(name) ? `Signed in as ${name}` : "Signed in as a person";
}
