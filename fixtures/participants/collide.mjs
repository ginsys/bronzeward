// T5's competing update (#367): while the participant edits node label bw-team in the web
// interface, h-all saves another value of it into the participant's open draft, so that the
// participant's next save is refused for a stale ETag. h-all signs in to Zitadel by the device
// grant, approved in headless Chrome; its access token stays in this process's memory. Run by
// `fixtures/participants/session t5 collide`, through `session passwords --only h-all --exec`, so
// that the password is in this process's environment only.
//
// Afterwards a leak scan reads the fixture state, the provider and participant state and the
// fixture's and provider's container logs for the token. It first plants a marker of its own and
// must find it, so that a scan that reads nothing cannot pass. It prints addresses, statuses and
// counts, never a token, a code or a password.
//
// Usage: node fixtures/participants/collide.mjs <A's port> <value> <state dir> <container prefix>
import { spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

const [port, value, stateDir, prefix] = process.argv.slice(2);
if (!port || !value || !stateDir || !prefix) throw new Error("usage: collide.mjs <A's port> <value> <state dir> <container prefix>");
const human = "h-all";
const password = process.env.BW_PW_H_ALL;
if (!password) throw new Error("no password for h-all in the environment (run through session passwords --only h-all --exec)");
const repo = new URL("../../", import.meta.url).pathname;
const part = join(repo, ".dev/participants");
const idpDir = join(repo, ".dev/idp");
const client = JSON.parse(readFileSync(join(part, "client.json"), "utf8"));
const require = createRequire(join(repo, "web/package.json"));
const { remote } = require("webdriverio");
const api = `http://127.0.0.1:${port}/api/v1`;
const fragment = "participants-labels";

// signedIn: h-all's access token, by the device grant, the code entered and approved in a
// WebDriver session (always a fresh profile).
async function signedIn() {
  const disc = await (await fetch(`${client.issuer}/.well-known/openid-configuration`)).json();
  const form = (o) => new URLSearchParams(o);
  const dev = await fetch(disc.device_authorization_endpoint, {
    method: "POST",
    body: form({ client_id: client.cliClientId, scope: `openid ${client.scopes}` }),
  });
  if (!dev.ok) throw new Error(`device authorization: ${dev.status}`);
  const d = await dev.json();
  const browser = await remote({
    logLevel: "error",
    capabilities: { browserName: "chrome", "goog:chromeOptions": { args: ["--headless=new"] } },
  });
  try {
    const submit = async (selector, v) => {
      const field = await browser.$(selector);
      await field.waitForDisplayed({ timeout: 15000 });
      await field.setValue(v);
      await (await browser.$('button[type="submit"]')).click();
    };
    await browser.url(d.verification_uri);
    await submit('input[name="user_code"]', d.user_code);
    await submit('input[name="loginName"], input#loginName, input[autocomplete="username"]', human);
    await submit('input[type="password"]', password);
    await browser.pause(2000);
    const allow = await browser.$("button*=Allow");
    if (await allow.isExisting()) await allow.click();
  } finally {
    await browser.deleteSession();
  }
  let wait = (d.interval ?? 5) * 1000;
  const until = Date.now() + 120000;
  while (Date.now() < until) {
    await new Promise((r) => setTimeout(r, wait));
    const t = await fetch(disc.token_endpoint, {
      method: "POST",
      body: form({ grant_type: "urn:ietf:params:oauth:grant-type:device_code", device_code: d.device_code, client_id: client.cliClientId }),
    });
    const b = await t.json();
    if (t.ok && b.access_token) return b.access_token;
    if (b.error === "slow_down") wait += 5000;
    else if (b.error !== "authorization_pending") throw new Error(`device token: ${t.status} ${b.error ?? ""}`);
  }
  throw new Error("device token: no approval within two minutes");
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
  console.log(`collide: h-all saved bw-team: ${value} into draft ${draft}: 200; the participant's next save is refused for its ETag`);
}

// files <dir>: every regular file under the directory.
function files(dir) {
  const out = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...files(p));
    else if (e.isFile()) out.push(p);
  }
  return out;
}

// scan <token>: the paths whose content holds the token, its signature or the planted marker.
// The marker must be found in its file alone.
function scan(token) {
  const marker = `BWSYNTH-collide-control-${randomBytes(12).toString("hex")}`;
  const control = join(part, "collide-control");
  writeFileSync(control, `planted ${marker}\n`, { mode: 0o600 });
  try {
    const names = spawnSync("docker", ["ps", "-a", "--format", "{{.Names}}"], { encoding: "utf8" }).stdout.split("\n");
    const logText = names
      // As session scan: the fixture's containers, the default dev-idp's, the login page and the forwarder.
      .filter((n) => n.startsWith(`${prefix}-`) || n.startsWith("bw-dev-idp") || n === "bw-dev-login" || n === "bw-dev-forward")
      .map((n) => [n, spawnSync("docker", ["logs", n], { encoding: "latin1", maxBuffer: 1 << 30 })])
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
  console.log(`collide failed: ${e.message}`);
}
if (token) {
  const { read, hits, control } = scan(token);
  const found = hits.filter(([w, k]) => k === "marker" && w === control).length === 1;
  const leaks = hits.filter(([w, k]) => !(k === "marker" && w === control));
  for (const [w, k] of leaks) console.log(`leak scan: ${k} in ${w}`);
  if (!found) console.log("leak scan: the planted marker was not found: the scan read nothing it was meant to");
  if (found && leaks.length === 0) console.log(`leak scan: ${read} files and logs read, the planted marker found, the token in none`);
  failed ||= !found || leaks.length > 0;
}
token = "";
process.exit(failed ? 1 : 0);
