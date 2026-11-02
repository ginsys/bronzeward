// T5's competing update (#367): while the participant edits node label bw-team in the web
// interface, h-all saves another value of it into the participant's open draft, so that the
// participant's next save is refused for a stale ETag. h-all signs in to Zitadel by the device
// grant, approved in headless Chrome; its access token stays in this process's memory. Run by
// `fixtures/participants/session t5 collide`, through `session passwords --only h-all --exec`, so
// that the password is in this process's environment only.
//
// Afterwards a leak scan reads the fixture state, the provider and participant state and the
// fixture's and provider's container logs for the token. It first plants a marker of its own and
// must find it, so that a scan that reads nothing cannot pass. It prints addresses, statuses,
// counts and page titles, never a token, a code or a password.
//
// Usage: node fixtures/participants/collide.mjs <A's port> <value> <state dir> <container prefix>
import { spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const [port, value, stateDir, prefix] = process.argv.slice(2);
if (!port || !value || !stateDir || !prefix) throw new Error("usage: collide.mjs <A's port> <value> <state dir> <container prefix>");
const human = "h-all";
const password = process.env.BW_PW_H_ALL;
if (!password) throw new Error("no password for h-all in the environment (run through session passwords --only h-all --exec)");
// As rehearse-signin.mjs: the password leaves this process's environment at once, so that no child
// (ChromeDriver, Chrome, docker) gets it, and none of the caller's WebdriverIO settings reaches it.
for (const k of Object.keys(process.env)) if (k.startsWith("BW_PW_") || k.startsWith("WDIO_")) delete process.env[k];
if (process.env.NODE_DEBUG) throw new Error("NODE_DEBUG is set; unset it, because its output bypasses the redaction");
// A file path, not the URL's path, which keeps percent-encoding (a space as %20).
const repo = fileURLToPath(new URL("../../", import.meta.url));
const part = join(repo, ".dev/participants");
const idpDir = join(repo, ".dev/idp");
const client = JSON.parse(readFileSync(join(part, "client.json"), "utf8"));
const require = createRequire(join(repo, "web/package.json"));
const { remote } = require("webdriverio");
const api = `http://127.0.0.1:${port}/api/v1`;
const fragment = "participants-labels";

// Every line printed goes through out, which removes the password, the device grant's codes once
// known, and anything shaped like a session password: a WebDriver error can name the failed
// command's arguments (setValue).
const hidden = [password];
const out = (line) => {
  let s = String(line);
  for (const h of hidden) s = s.replaceAll(h, "[redacted]");
  console.log(s.replace(/BWSYNTH-pw-[a-z0-9]{8}-[0-9]{4}/g, "[redacted]"));
};

// signedIn: h-all's access token, by the device grant. The code is entered and approved in headless
// Chrome, in a new profile in the participant state, which session scan reads and session stop
// removes. The browser stays open until the token poll ends: the consent's Allow comes some seconds
// after the password on a loaded host, and closing the browser earlier can cut the approval off.
async function signedIn() {
  const disc = await (await fetch(`${client.issuer}/.well-known/openid-configuration`)).json();
  const form = (o) => new URLSearchParams(o);
  const dev = await fetch(disc.device_authorization_endpoint, {
    method: "POST",
    body: form({ client_id: client.cliClientId, scope: `openid ${client.scopes}` }),
  });
  if (!dev.ok) throw new Error(`device authorization: ${dev.status}`);
  const d = await dev.json();
  for (const c of [d.user_code, d.device_code]) if (c) hidden.push(c);
  // The abort ends the poll's wait, its request and its body read, so that no token request
  // follows the browser's end.
  const ctl = new AbortController();
  const poll = (async () => {
    let wait = (d.interval ?? 5) * 1000;
    const until = Date.now() + 120000;
    while (!ctl.signal.aborted && Date.now() < until) {
      // The wait ends at the deadline at the latest, and no request starts after it.
      await new Promise((r) => {
        const t = setTimeout(r, Math.max(0, Math.min(wait, until - Date.now())));
        ctl.signal.addEventListener("abort", () => (clearTimeout(t), r()), { once: true });
      });
      if (ctl.signal.aborted || Date.now() >= until) break;
      // The deadline bounds the request and its body read too, so a stalled endpoint ends the poll.
      const t = await fetch(disc.token_endpoint, {
        method: "POST",
        body: form({ grant_type: "urn:ietf:params:oauth:grant-type:device_code", device_code: d.device_code, client_id: client.cliClientId }),
        signal: AbortSignal.any([ctl.signal, AbortSignal.timeout(Math.max(1, until - Date.now()))]),
      });
      const b = await t.json();
      if (t.ok && b.access_token) return b.access_token;
      if (b.error === "slow_down") wait += 5000;
      else if (b.error !== "authorization_pending") throw new Error(`device token: ${t.status} ${b.error ?? ""}`);
    }
    throw new Error(ctl.signal.aborted ? "device token: the poll was stopped" : "device token: no approval within two minutes");
  })();
  // Read below; a failure before then must not end the process as an unhandled rejection.
  poll.catch(() => {});
  const profile = join(part, "collide-profile");
  rmSync(profile, { recursive: true, force: true });
  mkdirSync(profile, { recursive: true });
  const browser = await remote({
    // No WebDriver log of its own: a command log would carry the password.
    logLevel: "silent",
    capabilities: { browserName: "chrome", "goog:chromeOptions": { args: ["--headless=new", `--user-data-dir=${profile}`] } },
  });
  const say = async (what) => out(`${what}: ${await browser.getTitle()} <${new URL(await browser.getUrl()).pathname}>`);
  try {
    const submit = async (selector, v) => {
      const field = await browser.$(selector);
      await field.waitForDisplayed({ timeout: 15000 });
      await field.setValue(v);
      await (await browser.$('button[type="submit"]')).click();
    };
    await browser.url(d.verification_uri);
    await say("device page");
    await submit('input[name="user_code"]', d.user_code);
    await submit('input[name="loginName"], input#loginName, input[autocomplete="username"]', human);
    await submit('input[type="password"]', password);
    await say("after the password");
    // The device grant asks for consent after the password (Device Authorization, Allow).
    const allow = await browser.$("button*=Allow");
    await allow.waitForClickable({ timeout: 15000 });
    await allow.click();
    await say("after consent");
    return await poll;
  } catch (e) {
    await say("page at the failure").catch(() => {});
    throw e;
  } finally {
    // The poll ends before the browser does: aborted, then awaited, its failure read above.
    ctl.abort();
    await poll.catch(() => {});
    await browser.deleteSession();
  }
}

// collide: the one open draft, read for its ETag, and h-all's bw-team value saved into it.
async function collide(token) {
  const auth = { Authorization: `Bearer ${token}` };
  const list = await fetch(`${api}/drafts?limit=500`, { headers: auth });
  if (!list.ok) throw new Error(`GET /drafts: ${list.status}`);
  const open = (await list.json()).items.filter((x) => x.state === "open");
  if (open.length !== 1) throw new Error(`${open.length} open drafts, want 1: the participant has saved no draft yet, or another is open`);
  const draft = open[0].id;
  const got = await fetch(`${api}/drafts/${draft}`, { headers: auth });
  const etag = got.headers.get("etag");
  if (!got.ok || !etag) throw new Error(`GET the draft: ${got.status}, ETag ${etag ? "present" : "missing"}`);
  const put = await fetch(`${api}/drafts/${draft}/fragments/${fragment}`, {
    method: "PUT",
    headers: { ...auth, "Content-Type": "application/json", "If-Match": etag, "Idempotency-Key": `participants-collide-${Date.now()}` },
    body: JSON.stringify({ layer: "role", document: `machine:\n  nodeLabels:\n    bw-team: "${value}"\n`, marks: [] }),
  });
  if (put.status !== 200) throw new Error(`PUT ${fragment}: ${put.status}`);
  out(`collide: h-all saved bw-team: ${value} into draft ${draft}: 200; the participant's next save is refused for its ETag`);
}

// files <dir>: every regular file under the directory.
function files(dir) {
  const found = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) found.push(...files(p));
    else if (e.isFile()) found.push(p);
  }
  return found;
}

// scan <token>: the paths whose content holds the token, its signature or the planted marker.
// The marker must be found in its file alone.
function scan(token) {
  const marker = `BWSYNTH-collide-control-${randomBytes(12).toString("hex")}`;
  const control = join(part, "collide-control");
  writeFileSync(control, `planted ${marker}\n`, { mode: 0o600 });
  try {
    // A docker command that fails is a scan that read nothing there: it fails the scan.
    const docker = (args, encoding) => {
      const r = spawnSync("docker", args, { encoding, maxBuffer: 1 << 30 });
      if (r.error || r.status !== 0) throw new Error(`leak scan: docker ${args.join(" ")} failed: ${r.error?.message ?? `exit ${r.status}`}`);
      return r;
    };
    const names = docker(["ps", "-a", "--format", "{{.Names}}"], "utf8").stdout.split("\n");
    const logText = names
      // As session scan: the fixture's containers, the default dev-idp's, the login page and the forwarder.
      .filter((n) => n.startsWith(`${prefix}-`) || n.startsWith("bw-dev-idp") || n === "bw-dev-login" || n === "bw-dev-forward")
      .map((n) => [n, docker(["logs", n], "latin1")])
      .map(([n, r]) => [`docker logs ${n}`, `${r.stdout}${r.stderr}`]);
    const needles = [token, token.split(".").pop(), marker].map((s) => Buffer.from(s));
    const hits = [];
    let read = 0;
    const look = (where, buf) => {
      read++;
      needles.forEach((n, i) => buf.includes(n) && hits.push([where, i === 2 ? "marker" : "token"]));
    };
    for (const dir of [stateDir, idpDir, part]) {
      for (const f of files(dir)) {
        if (statSync(f).size > 512 * 1024 * 1024) {
          hits.push([f, "unread: over 512 MiB"]);
          continue;
        }
        look(f, readFileSync(f));
      }
    }
    for (const [where, text] of logText) look(where, Buffer.from(text, "latin1"));
    return { read, hits, control };
  } finally {
    rmSync(control, { force: true });
  }
}

let failed = false;
let token = "";
try {
  token = await signedIn();
  await collide(token);
} catch (e) {
  failed = true;
  out(`collide failed: ${e.message}`);
}
let scanned = null;
if (token) {
  try {
    scanned = scan(token);
  } catch (e) {
    failed = true;
    out(e.message);
  }
}
if (scanned) {
  const { read, hits, control } = scanned;
  const found = hits.filter(([w, k]) => k === "marker" && w === control).length === 1;
  const leaks = hits.filter(([w, k]) => !(k === "marker" && w === control));
  for (const [w, k] of leaks) out(`leak scan: ${k} in ${w}`);
  if (!found) out("leak scan: the planted marker was not found: the scan read nothing it was meant to");
  if (found && leaks.length === 0) out(`leak scan: ${read} files and logs read, the planted marker found, the token in none`);
  failed ||= !found || leaks.length > 0;
}
token = "";
process.exit(failed ? 1 : 0);
