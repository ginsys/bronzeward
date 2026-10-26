// The AUTH cases' harness (conformance/cases/AUTH-*.md). Two disposable servers on loopback, each on
// a database of its own on mise run dev-db's PostgreSQL, each behind the test static host:
//   - A trusts mise run dev-idp's provider; its page signs in through the provider's Session API
//     (AUTH-01, AUTH-08).
//   - B trusts the synthetic provider (provider.ts), which hands out tokens the fixture issuer mints
//     on the spec's request, defects included (AUTH-02, AUTH-03, AUTH-05, AUTH-07, AUTH-10,
//     AUTH-11); its page reaches it through the injector (injector.ts; COMP-01, COMP-02 and the
//     shell's states).
// It removes the databases with the processes, and fails the run when it cannot; then the state
// directory, which names them, is kept, and the next run refuses to start until it is removed.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { randomBytes } from "node:crypto";
import { fileURLToPath } from "node:url";
import { SevereServiceError } from "webdriverio";
import { config as base } from "./wdio.conf.ts";
import { type Faults, type Injector, startInjector } from "./injector.ts";
import { type Counts, type Grant, type Provider, startProvider } from "./provider.ts";

export const IDP_PAGE = "http://localhost:58443";
export const FIXTURE_PAGE = "http://localhost:58448";
export const FIXTURE_ISSUER = "http://127.0.0.1:58446";
export const CONTROL = "http://127.0.0.1:58449";
export const FIXTURE_API = "http://127.0.0.1:58451";
const INJECTOR = "http://127.0.0.1:58452";
const IDP_API = "http://127.0.0.1:58450";
const KEYS = "127.0.0.1:58447";
const PG = ["-h", "127.0.0.1", "-p", "55433", "-U", "bronzeward"];
// The subject the fixture deployment lists in deniedSubjects (AUTH-10).
export const DENIED = "h-approver";

const root = fileURLToPath(new URL("../..", import.meta.url));
const state = `${root}.dev/browser-auth`;
// wdio.conf.ts has checked it.
const canary = process.env["BW_WEB_CANARY"]!;

// The test provider's variables, as mise run dev-idp writes them.
export function idpEnv(): Record<string, string> {
  const file = `${root}.dev/idp/idp.env`;
  return Object.fromEntries(readFileSync(file, "utf8").trim().split("\n").map((l) => {
    const i = l.indexOf("=");
    return [l.slice(0, i), l.slice(i + 1).replace(/^'(.*)'$/, "$1")];
  }));
}

const procs: ChildProcess[] = [];
const servers: Server[] = [];
const databases: string[] = [];
// Set once this run has made the state directory; before that it may be another run's.
let ownState = false;

function start(bin: string, args: string[]): ChildProcess {
  const p = spawn(`${root}bin/${bin}`, args, { stdio: "inherit" });
  procs.push(p);
  return p;
}

async function ready(url: string, p?: ChildProcess) {
  for (let i = 0; ; i++) {
    if (await fetch(url).then((r) => r.ok, () => false)) return;
    if (i === 75 || p?.exitCode != null) throw new SevereServiceError(`${url} did not answer within 15 s`);
    await new Promise((r) => setTimeout(r, 200));
  }
}

// One server on a fresh, migrated database.
function server(name: string, listen: string, oidc: string, extra: string) {
  const db = `bw_auth_${name}_${randomBytes(4).toString("hex")}`;
  execFileSync("createdb", [...PG, db]);
  databases.push(db);
  const file = `${state}/server-${name}.yaml`;
  writeFileSync(file, [
    `listen: ${listen}`,
    "database:",
    `  dsn: postgres://bronzeward@127.0.0.1:55433/${db}?sslmode=disable`,
    "auth:",
    "  oidc:",
    ...oidc.split("\n").map((l) => `    ${l}`),
    "    groupsClaim: groups",
    "    maxTokenLifetime: 15m",
    "  roles: {viewer: [bw-viewers], author: [bw-authors], publisher: [bw-publishers], approver: [bw-approvers], recovery-admin: [bw-recovery]}",
    extra,
    "execution:",
    "  maxTransportDeadline: 1m",
    "",
  ].join("\n"));
  execFileSync(`${root}bin/bronzeward`, ["migrate", "-config", file], { stdio: "inherit" });
  return start("bronzeward", ["serve", "-config", file]);
}

function host(listen: string, clientConfig: object, issuer: string, api: string) {
  const file = `${state}/client-${listen.split(":")[1]}.json`;
  writeFileSync(file, JSON.stringify(clientConfig));
  const web = `${root}web/`;
  return start("webhost", ["-listen", listen, "-dist", `${web}dist`, "-headers", `${web}headers.json`, "-client-config", file, "-issuer", issuer, "-api", api]);
}

// The control routes: a spec chooses the fixture token the next sign-in receives, reads the
// provider's counts, and sends the browser to a URL holding a code without naming it over
// WebDriver, whose log the artifact scan reads (POST /go stores it, GET /go redirects there once).
// POST /faults sets the injector's faults, GET /seen lists the API paths it passed on; the next
// sign-in clears both.
function control(provider: Provider, next: { grant: Grant }, injector: Injector): Server {
  let go: string | undefined;
  return createServer((req, res) => {
    const url = new URL(req.url ?? "/", CONTROL);
    if (url.pathname === "/counts") return res.end(JSON.stringify(provider.counts));
    if (url.pathname === "/seen") return res.end(JSON.stringify(injector.seen));
    if (req.method === "POST" && (url.pathname === "/go" || url.pathname === "/faults")) {
      let body = "";
      req.on("data", (c: Buffer) => (body += c.toString()));
      req.on("end", () => {
        if (url.pathname === "/go") go = body;
        else injector.faults = JSON.parse(body) as Faults;
        res.end();
      });
      return;
    }
    if (url.pathname === "/go") {
      const to = go;
      go = undefined;
      return to === undefined ? res.writeHead(404).end() : res.writeHead(302, { Location: to, "Cache-Control": "no-store" }).end();
    }
    if (url.pathname !== "/next") return res.writeHead(404).end();
    const q = url.searchParams;
    const mint = (human: string) => {
      const args = ["mint", "-key", `${state}/fixture.key`, "-issuer", FIXTURE_ISSUER, "-human", human];
      for (const f of ["defect", "without-group"]) if (q.has(f)) args.push(`-${f}`, q.get(f)!);
      return { access: execFileSync(`${root}bin/oidc-fixture`, args).toString().trim(), sub: human };
    };
    // A failed mint fails the spec that asked for it, not the launcher, whose cleanup must still run.
    try {
      // AUTH-07 and AUTH-11: a short expires_in, rotation, a renewal as another human, held answers.
      const grant: Grant = { ...mint(q.get("human") ?? ""), rotate: q.has("rotate"), hold: Number(q.get("hold") ?? 0), bare: q.has("bare") };
      if (q.has("lifetime")) grant.lifetime = Number(q.get("lifetime"));
      if (q.has("renew-as")) grant.renewed = { ...mint(q.get("renew-as")!), lifetime: 300 };
      next.grant = grant;
    } catch (e) {
      return res.writeHead(500).end(String(e));
    }
    for (const k of Object.keys(provider.counts) as (keyof Counts)[]) provider.counts[k] = 0;
    injector.faults = {};
    injector.seen.length = 0;
    res.end();
  });
}

async function prepare() {
  // Anything left on these ports from an earlier run would answer the readiness checks below.
  for (const at of ["127.0.0.1:58443", "127.0.0.1:58448", FIXTURE_ISSUER.slice(7), CONTROL.slice(7), FIXTURE_API.slice(7), IDP_API.slice(7), INJECTOR.slice(7), KEYS]) {
    if (await fetch(`http://${at}/`).then(() => true, () => false)) throw new SevereServiceError(`something already answers on ${at}`);
  }
  // Made, never reused: one left behind names databases an earlier cleanup could not drop, and the
  // create fails rather than share it with a run started at the same time.
  mkdirSync(`${root}.dev`, { recursive: true });
  try {
    mkdirSync(state);
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== "EEXIST") throw e;
    throw new SevereServiceError(`${state} is left from another run: drop the databases its server-*.yaml files name, then remove it`);
  }
  ownState = true;
  const idp = idpEnv();
  execFileSync(`${root}bin/oidc-fixture`, ["keygen", "-out", `${state}/fixture.key`]);
  const keys = start("oidc-fixture", ["serve", "-key", `${state}/fixture.key`, "-issuer", FIXTURE_ISSUER, "-listen", KEYS]);
  await ready(`http://${KEYS}/jwks`, keys);
  const next = { grant: { access: "", sub: "" } };
  const provider = await startProvider({ issuer: FIXTURE_ISSUER, pageOrigin: FIXTURE_PAGE, clientId: "synthetic-web-client", next: () => next.grant, keys: `http://${KEYS}`, refresh: canary });
  servers.push(provider.server);
  const injector = await startInjector(FIXTURE_API, Number(new URL(INJECTOR).port));
  servers.push(injector.server);
  const ctl = control(provider, next, injector);
  await new Promise<void>((resolve, reject) => {
    ctl.once("error", reject);
    ctl.listen(Number(new URL(CONTROL).port), "127.0.0.1", resolve);
  });
  servers.push(ctl);

  const a = server("idp", IDP_API.slice(7), `issuer: ${idp["BW_TEST_IDP_ISSUER"]}\naudience: "${idp["BW_TEST_IDP_PROJECT"]}"`, "  deniedSubjects: []");
  const b = server("fixture", FIXTURE_API.slice(7), `issuer: ${FIXTURE_ISSUER}\naudience: bronzeward`, `  deniedSubjects: [{iss: "${FIXTURE_ISSUER}", sub: ${DENIED}}]`);
  await ready(`${IDP_API}/livez`, a);
  await ready(`${FIXTURE_API}/livez`, b);
  const ha = host("127.0.0.1:58443", {
    issuer: idp["BW_TEST_IDP_ISSUER"], audience: idp["BW_TEST_IDP_PROJECT"], cliClientId: idp["BW_TEST_IDP_CLI_CLIENT"], webClientId: idp["BW_TEST_IDP_WEB_CLIENT"],
    // Zitadel puts the project in aud only when this scope asks for it (client contracts §5 item 4).
    scopes: `urn:zitadel:iam:org:project:id:${idp["BW_TEST_IDP_PROJECT"]}:aud`,
  }, idp["BW_TEST_IDP_ISSUER"]!, IDP_API);
  const hb = host("127.0.0.1:58448", { issuer: FIXTURE_ISSUER, audience: "bronzeward", cliClientId: "synthetic-cli-client", webClientId: "synthetic-web-client" }, FIXTURE_ISSUER, INJECTOR);
  await ready("http://127.0.0.1:58443/", ha);
  await ready("http://127.0.0.1:58448/", hb);
}

// Stopped, and killed after 10 s if it is still running.
function stop(p: ChildProcess): Promise<void> {
  return new Promise((resolve) => {
    if (p.exitCode !== null || p.signalCode !== null) return resolve();
    const kill = setTimeout(() => p.kill("SIGKILL"), 10_000);
    p.once("exit", () => (clearTimeout(kill), resolve()));
    p.kill();
  });
}

async function cleanup() {
  // Each server must be gone before its database can be dropped.
  await Promise.all(procs.splice(0).map(stop));
  await Promise.all(servers.splice(0).map((s) => new Promise((r) => (s.closeAllConnections(), s.close(r)))));
  const left = databases.filter((db) => {
    try {
      execFileSync("dropdb", [...PG, "--force", db]);
      return false;
    } catch (e) {
      console.error(`dropdb ${db}: ${String(e)}`);
      return true;
    }
  });
  // A later cleanup tries them again, and keeps the state directory while any is left.
  databases.splice(0, databases.length, ...left);
  if (left.length > 0) throw new Error(`databases left behind: ${left.join(", ")} (named in ${state})`);
  if (ownState) rmSync(state, { recursive: true, force: true });
  ownState = false;
}

export const config: WebdriverIO.Config = {
  ...base,
  specs: ["./auth/*.e2e.ts"],
  baseUrl: IDP_PAGE,
  async onPrepare() {
    try {
      await prepare();
    } catch (e) {
      await cleanup().catch((c: unknown) => console.error(String(c)));
      throw e instanceof SevereServiceError ? e : new SevereServiceError(String(e));
    }
  },
  onComplete: cleanup,
};
