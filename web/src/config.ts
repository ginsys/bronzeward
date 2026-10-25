// The client-configuration file served next to the web files (client contracts §5 item 4, D3 (b)),
// read from the page's own origin before anything can authenticate.

export const CLIENT_CONFIG_PATH = "/bronzeward-client.json";

export interface ClientConfig {
  issuer: string;
  audience: string;
  cliClientId: string;
  webClientId: string;
}

const MEMBERS = ["issuer", "audience", "cliClientId", "webClientId"] as const;

export type ConfigResult = { kind: "ok"; config: ClientConfig } | { kind: "transport" } | { kind: "invalid"; reason: string };

export async function loadClientConfig(fetchImpl: typeof fetch = fetch): Promise<ConfigResult> {
  let res: Response;
  try {
    res = await fetchImpl(CLIENT_CONFIG_PATH, { cache: "no-store", redirect: "error", credentials: "omit" });
  } catch {
    return { kind: "transport" };
  }
  if (!res.ok) return { kind: "invalid", reason: `${CLIENT_CONFIG_PATH} answered ${res.status}` };
  const body: unknown = await res.json().catch(() => null);
  if (typeof body !== "object" || body === null) return { kind: "invalid", reason: `${CLIENT_CONFIG_PATH} is not a JSON object` };
  const record = body as Record<string, unknown>;
  const missing = MEMBERS.find((m) => typeof record[m] !== "string" || record[m] === "");
  if (missing !== undefined) return { kind: "invalid", reason: `${CLIENT_CONFIG_PATH} has no ${missing}` };
  const config = Object.fromEntries(MEMBERS.map((m) => [m, record[m]])) as unknown as ClientConfig;
  if (!trustedIssuer(config.issuer)) return { kind: "invalid", reason: "the issuer must be https, or http to a loopback address" };
  return { kind: "ok", config };
}

// §6 item 1: https, or plain http only to a loopback address.
function trustedIssuer(issuer: string): boolean {
  try {
    const url = new URL(issuer);
    return url.protocol === "https:" || (url.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname));
  } catch {
    return false;
  }
}
