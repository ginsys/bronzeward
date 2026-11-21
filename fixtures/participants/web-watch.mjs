// The operation page of the web interface followed against the server (ginsys/bronzeward#359), not a
// participant session: headless Chrome signs in as a seeded human, opens /operations/<id> and logs every
// timeline row the page adds while the watch runs, then reads the server's events and the operation as
// the automation token and writes <dir>/web-watch.json for compare.mjs. Run by
// `fixtures/participants/session passwords --only <human> --exec node <this file> ...` (the password is
// in this process's environment only; it prints addresses and titles, never a password).
// Usage: node fixtures/participants/web-watch.mjs <human> <dir> <operation id> <server base URL> <token file>
//   <dir>/mark-stop and <dir>/mark-restart: when either appears, the log gets that mark (an outage run)
//   and <dir>/mark-<name>.ack is written. <dir>/web.pid holds this process's pid.
//   <dir>/web-ready: written once the page shows the live notice and one received row.
// Run from the repository root; the file ends the run with exit 0 once the page shows the watch ended.
import { mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const [human, dir, op, base, tokenFile] = process.argv.slice(2);
const password = process.env[`BW_PW_${(human ?? "").toUpperCase().replaceAll("-", "_")}`];
if (!password) throw new Error(`no password for ${human} in the environment (run through session passwords --exec)`);
for (const k of Object.keys(process.env)) if (k.startsWith("BW_PW_") || k.startsWith("WDIO_")) delete process.env[k];
if (process.env.NODE_DEBUG) throw new Error("NODE_DEBUG is set; unset it, because its output bypasses the redaction");
const repo = fileURLToPath(new URL("../../", import.meta.url));
const { remote } = createRequire(join(repo, "web/package.json"))("webdriverio");
const web = "http://localhost:58443";
// A WebDriver error can name the failed command's arguments, and the password is one of them. The
// fixtures' leak scan reads the marker of a session password in any file of the tree, so no pattern
// for it is written here.
const out = (line) => console.log(String(line).replaceAll(password, "[password]"));

// The log lives in the page: one entry per row the page adds, a clear when the list empties, and the
// marks the driver adds. A row's text and its <time datetime> attribute, as written (a received row's is
// the browser's arrival instant, a final row's the server's `at`), are read when it is added; a row kept
// under the same key is updated in place, so the server's replacement adds nothing.
function observe() {
  const log = (window.__bw = []);
  const rows = (n) => (n.nodeType === 1 ? [...(n.matches("li[data-event-id]") ? [n] : []), ...n.querySelectorAll("li[data-event-id]")] : []);
  new MutationObserver((records) => {
    let removed = false;
    for (const r of records) {
      for (const n of r.addedNodes) for (const li of rows(n)) log.push({ op: "add", id: li.dataset.eventId, text: li.textContent, received: li.textContent.startsWith("received "), dateTime: li.querySelector("time")?.getAttribute("datetime") ?? null });
      for (const n of r.removedNodes) removed ||= rows(n).length > 0;
    }
    if (removed && document.querySelector("li[data-event-id]") === null) log.push({ op: "clear" });
  }).observe(document, { childList: true, subtree: true });
}

mkdirSync(dir, { recursive: true });
const profile = join(dir, "web-profile");
rmSync(profile, { recursive: true, force: true });
mkdirSync(profile, { recursive: true });
// The session ends a driver it must stop by this pid (the wrapper that started it does not pass the signal on), and the browser goes
// with it. Both are in place before the browser starts, so a stop during the start ends this process too.
writeFileSync(join(dir, "web.pid"), `${process.pid}\n`);
const starting = remote({
  logLevel: "silent",
  capabilities: { browserName: "chrome", "goog:chromeOptions": { args: ["--headless=new", `--user-data-dir=${profile}`] } },
});
process.on("SIGTERM", () => starting.then((b) => b.deleteSession(), () => {}).finally(() => process.exit(143)));
const browser = await starting;
const say = async (what) => out(`${what}: ${await browser.getTitle()} <${new URL(await browser.getUrl()).pathname}>`);
async function submit(selector, value) {
  const field = await browser.$(selector);
  await field.waitForDisplayed({ timeout: 15000 });
  await field.setValue(value);
  await (await browser.$('button[type="submit"]')).click();
}
async function signIn() {
  const signin = await browser.$("button*=Sign in");
  if (await signin.isExisting()) await signin.click();
  await submit('input[name="loginName"], input#loginName, input[autocomplete="username"]', human);
  await submit('input[type="password"]', password);
  await browser.pause(2000);
  const allow = await browser.$("button*=Allow");
  if (await allow.isExisting()) await allow.click();
}

let failed = false;
try {
  await browser.url(`${web}/`);
  await browser.pause(2000);
  await signIn();
  await browser.pause(3000);
  const principal = await browser.$('[data-testid="principal"]');
  if (!(await principal.isExisting())) throw new Error("not signed in");
  out(`signed in as ${await principal.getText()}`);
  // The observer is in place before the page's stream opens; an operation page that asks for the
  // sign-in again is signed in once more (the provider's session answers it).
  await browser.addInitScript(observe);
  await browser.url(`${web}/operations/${encodeURIComponent(op)}`);
  const events = await browser.$('[data-testid="events"]');
  if (!(await events.waitForExist({ timeout: 20000 }).then(() => true, () => false))) {
    await signIn();
    await events.waitForExist({ timeout: 20000 });
  }
  await say("operation page");
  const marks = new Set();
  let ready = false;
  const deadline = Date.now() + 14 * 60 * 1000;
  for (;;) {
    for (const name of ["stop", "restart"]) {
      if (!marks.has(name) && existsSync(join(dir, `mark-${name}`))) {
        marks.add(name);
        await browser.execute((n) => window.__bw.push({ op: "mark", name: n }), name);
        // The session waits for this acknowledgement before it goes on, so that no row arrives between the event and its mark.
        writeFileSync(join(dir, `mark-${name}.ack`), "ok\n");
        out(`mark ${name}`);
      }
    }
    const [text, received] = await browser.execute(() => [document.body.innerText, window.__bw.filter((x) => x.op === "add" && x.received).length]);
    if (!ready && text.includes("Live: connected") && received > 0) {
      ready = true;
      writeFileSync(join(dir, "web-ready"), "ready\n");
      out("ready: live notice and a received row");
    }
    if (text.includes("Live: ended.")) break;
    if (Date.now() > deadline) throw new Error("the watch did not end within 14 minutes");
    await browser.pause(250);
  }
  const log = await browser.execute(() => window.__bw);
  const final = await browser.execute(() => [...document.querySelectorAll('[data-testid="events"] li[data-event-id]')].map((li) => ({ id: li.dataset.eventId, text: li.textContent, received: li.textContent.startsWith("received "), dateTime: li.querySelector("time")?.getAttribute("datetime") ?? null })));
  // The server's side, as the automation token: the events in order, and the operation's attempts.
  const auth = { authorization: `Bearer ${readFileSync(tokenFile, "utf8").trim()}`, accept: "application/json" };
  const get = async (path) => {
    const res = await fetch(`${base}/api/v1${path}`, { headers: auth });
    if (res.status !== 200) throw new Error(`${path}: HTTP ${res.status}`);
    return res.json();
  };
  const server = [];
  for (let cursor; ; ) {
    const page = await get(`/operations/${encodeURIComponent(op)}/events${cursor === undefined ? "" : `?cursor=${encodeURIComponent(cursor)}`}`);
    server.push(...page.items);
    if ((cursor = page.next) === undefined) break;
  }
  const attempts = ((await get(`/operations/${encodeURIComponent(op)}`)).attempts ?? []).length;
  writeFileSync(join(dir, "web-watch.json"), `${JSON.stringify({ op, log, final, server, attempts }, null, 1)}\n`);
  out(`web-watch.json: ${log.length} log entries, ${final.length} final rows, ${server.length} server events, ${attempts} attempts`);
} catch (e) {
  failed = true;
  out(`failed: ${e.message}`);
  await say("page at the failure").catch(() => {});
} finally {
  await browser.deleteSession();
}
process.exit(failed ? 1 : 0);
