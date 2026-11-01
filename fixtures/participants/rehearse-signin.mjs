// The agent's rehearsal of T1's sign-ins (#367), not a participant session: one CLI device-grant
// sign-in and one web authorization-code sign-in, each through Zitadel's login page in headless
// Chrome. Run by `fixtures/participants/session passwords --exec node <this file> <human>`,
// so that the human's password is in this process's environment only. It prints page addresses
// and titles, never a password or a code.
// Usage: node fixtures/participants/rehearse-signin.mjs <human>   (from web/, for webdriverio)
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { join } from "node:path";

const human = process.argv[2] ?? "h-viewer";
const password = process.env[`BW_PW_${human.toUpperCase().replaceAll("-", "_")}`];
if (!password) throw new Error(`no password for ${human} in the environment (run through session passwords --exec)`);
// The passwords leave this process's environment at once, so that no child (ChromeDriver, the CLI)
// gets them. NODE_DEBUG, read at Node's start, would print what a child is given and more.
for (const k of Object.keys(process.env)) if (k.startsWith("BW_PW_")) delete process.env[k];
if (process.env.NODE_DEBUG) throw new Error("NODE_DEBUG is set; unset it, because its output bypasses the redaction");
const repo = new URL("../../", import.meta.url).pathname;
const part = join(repo, ".dev/participants");
const require = createRequire(join(repo, "web/package.json"));
// WDIO_LOG_LEVEL overrides logLevel below, and WDIO_LOG_PATH would keep that log outside the scan's
// places: none of the caller's WebdriverIO settings reaches it.
for (const k of Object.keys(process.env)) if (k.startsWith("WDIO_")) delete process.env[k];
const { remote } = require("webdriverio");
const web = "http://localhost:58443";

// Every line this driver prints goes through out, which removes the password and anything shaped
// like a session password. A WebDriver error can name the failed command's arguments, and the
// password is one of them (setValue).
const out = (line) => console.log(String(line).replaceAll(password, "[password]").replace(/BWSYNTH-pw-[a-z0-9]{8}-[0-9]{4}/g, "[password]"));

const browser = await remote({
  // No WebDriver log of its own: a command log would carry the password.
  logLevel: "silent",
  // A WebDriver session is always a fresh profile, so nothing a page stores outlives it.
  capabilities: { browserName: "chrome", "goog:chromeOptions": { args: ["--headless=new"] } },
});
const say = async (what) => out(`${what}: ${await browser.getTitle()} <${new URL(await browser.getUrl()).pathname}>`);

// fill <selector> <value>, then the page's submit button.
async function submit(selector, value) {
  const field = await browser.$(selector);
  await field.waitForDisplayed({ timeout: 15000 });
  await field.setValue(value);
  await (await browser.$('button[type="submit"]')).click();
}

// signIn: the login page's two steps, the login name, then the password; then any consent.
async function signIn() {
  await submit('input[name="loginName"], input#loginName, input[autocomplete="username"]', human);
  await say("after the login name");
  await submit('input[type="password"]', password);
  await browser.pause(2000);
  await say("after the password");
  const allow = await browser.$("button*=Allow");
  if (await allow.isExisting()) {
    await allow.click();
    await say("after consent");
  }
}

const env = { ...process.env, XDG_CONFIG_HOME: join(part, "home/config"), XDG_STATE_HOME: join(part, "home/state") };
const ctl = join(part, "bin/bronzewardctl");
let failed = false;

try {
  // 1. The CLI, device grant: the address and code it prints, approved in the browser.
  const login = spawn(ctl, ["login", "--device"], { env, stdio: ["ignore", "pipe", "pipe"] });
  let cliOut = "";
  const done = new Promise((resolve) => login.on("exit", resolve));
  login.stdout.on("data", (d) => (cliOut += d));
  login.stderr.on("data", (d) => (cliOut += d));
  for (let i = 0; i < 30 && !/open (\S+) on any device/.test(cliOut); i++) await browser.pause(500);
  const url = cliOut.match(/open (\S+) on any device/)?.[1];
  const code = cliOut.match(/enter the code (\S+?)\.\s/)?.[1];
  if (!url || !code) throw new Error("the CLI printed no device address and code");
  out(`CLI device address: ${new URL(url).pathname}`);
  await browser.url(url);
  await say("device page");
  // Zitadel's own device page (/ui/login/device), which then hands over to the login page.
  await submit('input[name="user_code"]', code);
  await signIn();
  const rc = await done;
  out(`CLI login exit: ${rc}`);
  failed ||= rc !== 0;
  const who = spawn(ctl, ["--no-input", "whoami"], { env, stdio: ["ignore", "pipe", "inherit"] });
  let whoOut = "";
  who.stdout.on("data", (d) => (whoOut += d));
  const whoRc = await new Promise((resolve) => who.on("exit", resolve));
  out(`CLI whoami exit ${whoRc}:\n${whoOut}`);
  failed ||= whoRc !== 0;

  // 2. The web interface, authorization code. Signed in means back on the web host's origin, with
  // the shell's "Signed in as" line, which shows only after the server answered the identity read.
  await browser.reloadSession();
  await browser.url(`${web}/`);
  await browser.pause(2000);
  await say("web start");
  const signin = await browser.$("button*=Sign in");
  if (await signin.isExisting()) await signin.click();
  await signIn();
  await browser.pause(3000);
  await say("web after sign-in");
  const origin = new URL(await browser.getUrl()).origin;
  const principal = await browser.$('[data-testid="principal"]');
  const signedIn = await principal.isExisting();
  out(`web origin ${origin}, ${signedIn ? await principal.getText() : "not signed in"}`);
  failed ||= origin !== web || !signedIn;
} catch (e) {
  failed = true;
  out(`failed: ${e.message}`);
  await say("page at the failure").catch(() => {});
} finally {
  await browser.deleteSession();
}
process.exit(failed ? 1 : 0);
