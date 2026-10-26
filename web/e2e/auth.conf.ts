// The AUTH cases' harness (conformance/cases/AUTH-*.md). Two disposable servers on loopback, each on
// a database of its own on mise run dev-db's PostgreSQL, each behind the test static host:
//   - A trusts mise run dev-idp's provider; its page signs in through the provider's Session API
//     (AUTH-01, AUTH-08).
//   - B trusts the synthetic provider (provider.ts), which hands out tokens the fixture issuer mints
//     on the spec's request, defects included (AUTH-02, AUTH-03, AUTH-05, AUTH-10).
// It removes the databases with the processes, and fails the run when it cannot; then the state
// directory, which names them, is kept.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { randomBytes } from "node:crypto";
import { fileURLToPath } from "node:url";
import { SevereServiceError } from "webdriverio";
import { config as base } from "./wdio.conf.ts";
import { type Grant, type Provider, startProvider } from "./provider.ts";

export const IDP_PAGE = "http://localhost:58443";
export const FIXTURE_PAGE = "http://localhost:58448";
export const FIXTURE_ISSUER = "http://127.0.0.1:58446";
export const CONTROL = "http://127.0.0.1:58449";
export const FIXTURE_API = "http://127.0.0.1:58451";
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
function control(provider: Provider, next: { grant: Grant }): Server {
  let go: string | undefined;
  return createServer((req, res) => {
    const url = new URL(req.url ?? "/", CONTROL);
    if (url.pathname === "/counts") return res.end(JSON.stringify(provider.counts));
    if (url.pathname === "/go" && req.method === "POST") {
      let body = "";
      req.on("data", (c: Buffer) => (body += c.toString()));
      req.on("end", () => ((go = body), res.end()));
      return;
    }
    if (url.pathname === "/go") {
      const to = go;
      go = undefined;
      return to === undefined ? res.writeHead(404).end() : res.writeHead(302, { Location: to, "Cache-Control": "no-store" }).end();
    }
    if (url.pathname !== "/next") return res.writeHead(404).end();
    const human = url.searchParams.get("human") ?? "";
    const args = ["mint", "-key", `${state}/fixture.key`, "-issuer", FIXTURE_ISSUER, "-human", human];
    for (const f of ["defect", "without-group"]) if (url.searchParams.has(f)) args.push(`-${f}`, url.searchParams.get(f)!);
    next.grant = { access: execFileSync(`${root}bin/oidc-fixture`, args).toString().trim(), sub: human };
    provider.counts.authorize = provider.counts.refresh = provider.counts.revoke = 0;
    res.end();
  });
}

async function prepare() {
  // Anything left on these ports from an earlier run would answer the readiness checks below.
  for (const at of ["127.0.0.1:58443", "127.0.0.1:58448", FIXTURE_ISSUER.slice(7), CONTROL.slice(7), FIXTURE_API.slice(7), IDP_API.slice(7), KEYS]) {
    if (await fetch(`http://${at}/`).then(() => true, () => false)) throw new SevereServiceError(`something already answers on ${at}`);
  }
  rmSync(state, { recursive: true, force: true });
  mkdirSync(state, { recursive: true });
  const idp = idpEnv();
  execFileSync(`${root}bin/oidc-fixture`, ["keygen", "-out", `${state}/fixture.key`]);
  const keys = start("oidc-fixture", ["serve", "-key", `${state}/fixture.key`, "-issuer", FIXTURE_ISSUER, "-listen", KEYS]);
  await ready(`http://${KEYS}/jwks`, keys);
  const next = { grant: { access: "", sub: "" } };
  const provider = await startProvider({ issuer: FIXTURE_ISSUER, pageOrigin: FIXTURE_PAGE, clientId: "synthetic-web-client", next: () => next.grant, keys: `http://${KEYS}`, refresh: canary });
  servers.push(provider.server);
  const ctl = control(provider, next);
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
  const hb = host("127.0.0.1:58448", { issuer: FIXTURE_ISSUER, audience: "bronzeward", cliClientId: "synthetic-cli-client", webClientId: "synthetic-web-client" }, FIXTURE_ISSUER, FIXTURE_API);
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
  databases.length = 0;
  if (left.length > 0) throw new Error(`databases left behind: ${left.join(", ")} (named in ${state})`);
  rmSync(state, { recursive: true, force: true });
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
