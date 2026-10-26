// The client-configuration file served next to the web files (client contracts §5 item 4, D3 (b)),
// read from the page's own origin before anything can authenticate.

import { CUT, readJson } from "./request.ts";

export const CLIENT_CONFIG_PATH = "/bronzeward-client.json";

export interface ClientConfig {
  issuer: string;
  audience: string;
  cliClientId: string;
  webClientId: string;
  // The optional `scopes` member, split: requested after `openid offline_access`; none if absent.
  scopes: string[];
}

const MEMBERS = ["issuer", "audience", "cliClientId", "webClientId"] as const;
// RFC 6749 §3.3 scope-token characters.
const SCOPE_TOKEN = /^[\x21\x23-\x5b\x5d-\x7e]+$/;
export const BASE_SCOPES = ["openid", "offline_access"];

export type ConfigResult = { kind: "ok"; config: ClientConfig } | { kind: "transport" } | { kind: "invalid"; reason: string };

export async function loadClientConfig(fetchImpl: typeof fetch = fetch): Promise<ConfigResult> {
  let res: Response;
  try {
    res = await fetchImpl(CLIENT_CONFIG_PATH, { cache: "no-store", redirect: "error", credentials: "omit" });
  } catch {
    return { kind: "transport" };
  }
  if (!res.ok) return { kind: "invalid", reason: `${CLIENT_CONFIG_PATH} answered ${res.status}` };
  const body = await readJson(res);
  if (body === CUT) return { kind: "transport" };
  if (typeof body !== "object" || body === null) return { kind: "invalid", reason: `${CLIENT_CONFIG_PATH} is not a JSON object` };
  const record = body as Record<string, unknown>;
  const missing = MEMBERS.find((m) => typeof record[m] !== "string" || record[m] === "");
  if (missing !== undefined) return { kind: "invalid", reason: `${CLIENT_CONFIG_PATH} has no ${missing}` };
  const scopes = readScopes(record["scopes"]);
  if (typeof scopes === "string") return { kind: "invalid", reason: `${CLIENT_CONFIG_PATH}: ${scopes}` };
  const config = { ...Object.fromEntries(MEMBERS.map((m) => [m, record[m]])), scopes } as unknown as ClientConfig;
  if (!trustedIssuer(config.issuer)) return { kind: "invalid", reason: "the issuer must be https, or http to a loopback address" };
  return { kind: "ok", config };
}

// The scopes, or the reason the member is malformed.
function readScopes(value: unknown): string[] | string {
  if (value === undefined) return [];
  if (typeof value !== "string") return "scopes is not a string";
  const scopes = value.split(" ").filter((s) => s !== "");
  if (scopes.length === 0) return "scopes is empty";
  const bad = scopes.find((s) => !SCOPE_TOKEN.test(s) || BASE_SCOPES.includes(s));
  if (bad !== undefined) return BASE_SCOPES.includes(bad) ? `scopes repeats ${bad}, which is always requested` : "scopes holds a character a scope cannot";
  return scopes;
}

// §6 item 1: https, or plain http only to a loopback address.
export function trustedIssuer(issuer: string): boolean {
  try {
    const url = new URL(issuer);
    return url.protocol === "https:" || (url.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname));
  } catch {
    return false;
  }
}
