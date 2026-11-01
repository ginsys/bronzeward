// The real-browser runs' ports (#459). A run takes the 16 ports from BW_E2E_PORT_BASE (default
// 16000), below the Linux ephemeral range (32768-60999), so that runs with different bases can run
// on one host at the same time. The base must be 16000+16*n with n from 0 to 249: a base between two
// of them would overlap another run's block.
export function portBase(raw: string | undefined): number {
  const s = raw ?? "16000";
  const base = Number(s);
  if (!/^[0-9]+$/.test(s) || base < 16000 || base > 19984 || (base - 16000) % 16 !== 0) {
    throw new Error(`BW_E2E_PORT_BASE must be 16000+16*n with n from 0 to 249 (16000, 16016, ..., 19984), not ${s}`);
  }
  return base;
}
const base = portBase(process.env["BW_E2E_PORT_BASE"]);
const at = (offset: number) => base + offset;

// The test static host: mise run browser's page, and browser-auth's page behind the server that
// trusts mise run dev-idp's provider, which must have this origin as its web origin.
export const PAGE_ORIGIN = `http://localhost:${at(0)}`;
export const PAGE_LISTEN = `127.0.0.1:${at(0)}`;
// mise run browser: the redirect target and the synthetic provider's issuer.
export const TARGET_ORIGIN = `http://127.0.0.1:${at(1)}`;
export const FAKE_ISSUER = `http://127.0.0.1:${at(2)}`;
// mise run browser-auth (auth.conf.ts).
export const FIXTURE_ISSUER = `http://127.0.0.1:${at(3)}`;
export const KEYS = `127.0.0.1:${at(4)}`;
export const FIXTURE_PAGE = `http://localhost:${at(5)}`;
export const FIXTURE_LISTEN = `127.0.0.1:${at(5)}`;
export const CONTROL = `http://127.0.0.1:${at(6)}`;
export const IDP_API = `http://127.0.0.1:${at(7)}`;
export const FIXTURE_API = `http://127.0.0.1:${at(8)}`;
export const INJECTOR = `http://127.0.0.1:${at(9)}`;
export const DISPOSABLE_API = `http://127.0.0.1:${at(10)}`;
