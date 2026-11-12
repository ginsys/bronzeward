// The AUTH cases' harness (conformance/cases/AUTH-*.md). Disposable servers on loopback, each on
// a database of its own on mise run dev-db's PostgreSQL, each behind the test static host:
//   - A trusts mise run dev-idp's provider; its page signs in through the provider's Session API
//     (AUTH-01, AUTH-08).
//   - B trusts the synthetic provider (provider.ts), which hands out tokens the fixture issuer mints
//     on the spec's request, defects included (AUTH-02, AUTH-03, AUTH-05, AUTH-07, AUTH-10,
//     AUTH-11); its page reaches it through the injector (injector.ts; COMP-01, COMP-02 and the
//     shell's states).
//   - On a case's request, B's deployment on a fresh database, which the injector then passes B's
//     page on to: started normally for the inventory cases (PAGE-01), with -recovery-start (REC-01),
//     or restarted with it on the same database (PAGE-02). Its provider is mise run dev-bao's
//     OpenBao behind a proxy (bao.ts), so the draft cases (#339) can ingest fragment documents.
// It removes the databases with the processes, and fails the run when it cannot; then the state
// directory, which names them, is kept, and the next run refuses to start until it is removed.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { randomBytes } from "node:crypto";
import { fileURLToPath } from "node:url";
import { SevereServiceError } from "webdriverio";
import { printFailure } from "./print-failure.ts";
import { config as base, removeProfiles } from "./wdio.conf.ts";
import { type Faults, type Injector, startInjector } from "./injector.ts";
import {
  BAO_PROXY, CONTROL, DISPOSABLE_API, FIXTURE_API, FIXTURE_ISSUER, FIXTURE_LISTEN, FIXTURE_PAGE, IDP_API, INJECTOR, KEYS,
  PAGE_LISTEN, PAGE_ORIGIN as IDP_PAGE,
} from "./ports.ts";
import { type Counts, type Grant, type Provider, startProvider } from "./provider.ts";
import { type Bao, cleanBao, startBao, teardownFailure } from "./bao.ts";

export { CONTROL, FIXTURE_API, FIXTURE_ISSUER, FIXTURE_PAGE, IDP_PAGE };
// mise run browser-auth sets BW_DEV_PG_PORT and BW_DEV_IDP_DIR from BW_DEV_INSTANCE
// (scripts/dev/instance.sh); the defaults are the default instance's.
const PG_PORT = process.env["BW_DEV_PG_PORT"] ?? "11432";
const PG = ["-h", "127.0.0.1", "-p", PG_PORT, "-U", "bronzeward"];
// The subject the fixture deployment lists in deniedSubjects (AUTH-10).
export const DENIED = "h-approver";

// RFC 4648 base32 in lower case without padding, as internal/id encodes an identifier's body.
const base32 = (b: Buffer) => {
  let bits = "";
  for (const x of b) bits += x.toString(2).padStart(8, "0");
  return (bits.match(/.{1,5}/g) ?? []).map((s) => "abcdefghijklmnopqrstuvwxyz234567"[parseInt(s.padEnd(5, "0"), 2)]).join("");
};

const root = fileURLToPath(new URL("../..", import.meta.url));
const state = `${root}.dev/browser-auth`;
// wdio.conf.ts has checked it.
const canary = process.env["BW_WEB_CANARY"]!;

// A draft case's fragment document (#339): its machine token and its tier label carry the run's canary,
// so the artifact scan finds the document in any retained file. A review shows sanitized documents and
// can replace the token by a reference. The label is no secret, so protected.e2e.ts asks the page, as a
// boolean, whether its review text holds the label. Never typed or read over WebDriver.
export const syntheticDocument = (name: string) => `machine:\n  token: ${canary}-doc-${name}\n  nodeLabels:\n    example.test/tier: ${canary}-tier-${name}\n`;

// The test provider's variables, as mise run dev-idp writes them.
export function idpEnv(): Record<string, string> {
  const file = `${root}${process.env["BW_DEV_IDP_DIR"] ?? ".dev/idp"}/idp.env`;
  return Object.fromEntries(readFileSync(file, "utf8").trim().split("\n").map((l) => {
    const i = l.indexOf("=");
    return [l.slice(0, i), l.slice(i + 1).replace(/^'(.*)'$/, "$1")];
  }));
}

const procs: ChildProcess[] = [];
const servers: Server[] = [];
const databases: string[] = [];
// This run's OpenBao records, removed after the processes stop.
let bao: Bao | undefined;
// Set once this run has made the state directory; before that it may be another run's.
let ownState = false;

function start(bin: string, args: string[], env: Record<string, string> = {}): ChildProcess {
  const p = spawn(`${root}bin/${bin}`, args, { stdio: "inherit", env: { ...process.env, ...env } });
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

// The file the interrupt build reads at every step (internal/seam): one line, "<step> <action>".
const interruptFile = `${state}/interrupt`;

// One server, started with the serve flags given, on the database named or a fresh, migrated one.
// `interrupt` runs the build that holds the fixture interrupt seam, reading interruptFile.
function server(name: string, listen: string, oidc: string, extra: string, flags: string[] = [], on?: string, interrupt = false) {
  const db = on ?? `bw_auth_${name}_${randomBytes(4).toString("hex")}`;
  if (on === undefined) {
    execFileSync("createdb", [...PG, db]);
    databases.push(db);
  }
  const file = `${state}/server-${name}.yaml`;
  writeFileSync(file, [
    `listen: ${listen}`,
    "database:",
    `  dsn: postgres://bronzeward@127.0.0.1:${PG_PORT}/${db}?sslmode=disable`,
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
  if (on === undefined) execFileSync(`${root}bin/bronzeward`, ["migrate", "-config", file], { stdio: "inherit" });
  return { db, p: interrupt ? start("bronzeward-interrupt", ["serve", "-config", file, ...flags], { BW_INTERRUPT_CONTROL: interruptFile }) : start("bronzeward", ["serve", "-config", file, ...flags]) };
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
// POST /faults sets the injector's faults, GET /seen lists the API paths it passed on and GET
// /answered the server's answers to them, GET /held the paths whose answers it holds now, POST
// /release lets its held answers go and answers once they are sent, GET /writes lists the writes
// it received, GET /redirected counts the requests that followed its redirects, and POST /epoch commits a new epoch where they go; the next sign-in clears all. POST /disposable answers the URL of B's
// deployment just started on a fresh database, replacing any earlier one: ?start=recovery with
// -recovery-start, its entry not yet made, and with &keep on the earlier one's database. POST
// /bao/hold?ms= holds each KV write of that deployment's provider this long (AMB-02), and POST
// /bao/retire retires the versions of its digest key (AMB-06); both answer 500 unless the faults'
// upstream is that deployment, and the next sign-in ends the hold.
// The protected import cases (ginsys/bronzeward#341) add three routes, all for this run's disposable
// deployment only: POST /bao/fault?mode=none|kv|decrypt makes its OpenBao proxy answer 503 to the KV
// writes of a generation or to the Transit decrypt of the staged envelope, POST /bao/decrypt-hold?ms=
// holds each such decrypt, and POST /disposable/kill ends its process at once with no restart; POST
// /disposable?expiry=short starts it with an absolute expiry of 45 s, for an expired claim.
// The disposable deployment runs the interrupt build (ginsys/bronzeward#366): POST /interrupt?point=
// "<step> <action>" writes the file that build reads at every step (recovery-entry-commit lose-reply,
// draft-commit stall), and POST /interrupt with no point removes it, which ends a stall. The next
// sign-in removes it too.
// The seeding step (ginsys/bronzeward#822): POST /disposable?seed, once the deployment answers, runs
// bin/e2eseed.test (mise run browser-auth builds it from internal/api's TestE2ESeed) against its fresh
// database. That test writes with the helpers the API tests use (internal/api/e2eseed_test.go): a
// release, two machines, and one plan in each state a plan page shows (proposed, approved, committed,
// expired, cancelled, revoked), the committed one with an operation and its first attempt. GET /seeded
// answers their identifiers as JSON, and 404 when the deployment was not started with ?seed. Both
// routes belong to this harness process, not to the bronzeward server, which has no route for tests;
// a plain POST /disposable seeds nothing, and ?keep reuses the earlier seed with the database.
// The seed is written after the deployment's controller has started, never before: its first pass
// takes over every operation another process owned, and TestE2ESeedSurvivesARunningController proves
// that a running controller then leaves the seeded records as they are.
export interface Chaos {
  fault: (mode: string) => void;
  decryptHold: (ms: number) => void;
  kill: () => Promise<void>;
  interrupt: (line: string) => void;
}
function control(provider: Provider, next: { grant: Grant }, injector: Injector, disposable: (recovery: boolean, keep: boolean, short: boolean, seed: boolean) => Promise<string>, seeded: () => string | undefined, kv: (ms: number) => void, chaos: Chaos): Server {
  let go: string | undefined;
  return createServer((req, res) => {
    const url = new URL(req.url ?? "/", CONTROL);
    if (url.pathname === "/counts") return res.end(JSON.stringify(provider.counts));
    if (url.pathname === "/seen") return res.end(JSON.stringify(injector.seen));
    if (url.pathname === "/answered") return res.end(JSON.stringify(injector.answered));
    if (url.pathname === "/held") return res.end(JSON.stringify(injector.holding));
    if (url.pathname === "/writes") return res.end(JSON.stringify(injector.writes));
    if (url.pathname === "/redirected") return res.end(JSON.stringify(injector.redirected));
    if (url.pathname === "/epoch" && req.method === "POST") {
      try {
        injector.epoch();
      } catch (e) {
        return res.writeHead(500).end(String(e));
      }
      return res.end();
    }
    if (url.pathname === "/bao/hold" && req.method === "POST") {
      try {
        kv(Number(url.searchParams.get("ms") ?? 0));
      } catch (e) {
        return res.writeHead(500).end(String(e));
      }
      return res.end();
    }
    if (url.pathname === "/bao/fault" && req.method === "POST") {
      try {
        chaos.fault(url.searchParams.get("mode") ?? "");
      } catch (e) {
        return res.writeHead(500).end(String(e));
      }
      return res.end();
    }
    if (url.pathname === "/bao/decrypt-hold" && req.method === "POST") {
      try {
        chaos.decryptHold(Number(url.searchParams.get("ms") ?? 0));
      } catch (e) {
        return res.writeHead(500).end(String(e));
      }
      return res.end();
    }
    if (url.pathname === "/interrupt" && req.method === "POST") {
      try {
        chaos.interrupt(url.searchParams.get("point") ?? "");
      } catch (e) {
        return res.writeHead(500).end(String(e));
      }
      return res.end();
    }
    if (url.pathname === "/disposable/kill" && req.method === "POST") return void chaos.kill().then(() => res.end(), (e: unknown) => res.writeHead(500).end(String(e)));
    if (url.pathname === "/bao/retire" && req.method === "POST") return void injector.retire().then(() => res.end(), (e: unknown) => res.writeHead(500).end(String(e)));
    if (url.pathname === "/release" && req.method === "POST") return void injector.release().then(() => res.end());
    if (url.pathname === "/disposable" && req.method === "POST") {
      const started = disposable(url.searchParams.get("start") === "recovery", url.searchParams.has("keep"), url.searchParams.get("expiry") === "short", url.searchParams.has("seed"));
      return void started.then((at) => res.end(at), (e: unknown) => res.writeHead(500).end(String(e)));
    }
    if (url.pathname === "/seeded" && req.method === "GET") {
      const doc = seeded();
      return doc === undefined ? res.writeHead(404).end() : res.writeHead(200, { "Content-Type": "application/json" }).end(doc);
    }
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
    // An answer an earlier case left held goes on and is sent before the logs are cleared, so
    // nothing of that case reaches the next one's.
    void injector.release().then(() => {
      for (const k of Object.keys(provider.counts) as (keyof Counts)[]) provider.counts[k] = 0;
      injector.faults = {};
      injector.seen.length = 0;
      injector.answered.length = 0;
      injector.writes.length = 0;
      injector.redirected = 0;
      if (bao !== undefined) (bao.hold = 0), (bao.fail = "none"), (bao.decryptHold = 0);
      rmSync(interruptFile, { force: true });
      res.end();
    });
  });
}

async function prepare() {
  // Anything left on these ports from an earlier run would answer the readiness checks below.
  for (const at of [PAGE_LISTEN, FIXTURE_LISTEN, FIXTURE_ISSUER.slice(7), CONTROL.slice(7), FIXTURE_API.slice(7), IDP_API.slice(7), INJECTOR.slice(7), DISPOSABLE_API.slice(7), KEYS, BAO_PROXY]) {
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
  // The provider sends the browser back only to the web origin dev-idp registered.
  if (idp["BW_TEST_IDP_WEB_ORIGIN"] !== IDP_PAGE) {
    throw new SevereServiceError(`dev-idp's web origin is ${idp["BW_TEST_IDP_WEB_ORIGIN"]}, not this run's page ${IDP_PAGE}: start dev-idp with the same BW_E2E_PORT_BASE, or with BW_DEV_IDP_WEB_ORIGIN=${IDP_PAGE}`);
  }
  execFileSync(`${root}bin/oidc-fixture`, ["keygen", "-out", `${state}/fixture.key`]);
  const keys = start("oidc-fixture", ["serve", "-key", `${state}/fixture.key`, "-issuer", FIXTURE_ISSUER, "-listen", KEYS]);
  await ready(`http://${KEYS}/jwks`, keys);
  const next = { grant: { access: "", sub: "" } };
  const provider = await startProvider({ issuer: FIXTURE_ISSUER, pageOrigin: FIXTURE_PAGE, clientId: "synthetic-web-client", next: () => next.grant, keys: `http://${KEYS}`, refresh: canary, document: syntheticDocument });
  servers.push(provider.server);
  const injector = await startInjector(FIXTURE_API, Number(new URL(INJECTOR).port));
  servers.push(injector.server);
  // B's deployment, which the recovery starts share.
  const fixtureOidc = `issuer: ${FIXTURE_ISSUER}\naudience: bronzeward`;
  const fixtureDenied = `  deniedSubjects: [{iss: "${FIXTURE_ISSUER}", sub: ${DENIED}}]`;
  // A recovery entry takes operations over as this process's owner identity, and a fragment write
  // ingests its document (#339): both need a provider and ingestion block. The provider is mise run
  // dev-bao's OpenBao, through this run's proxy, with this run's own digest key and tokens (bao.ts).
  bao = await startBao(root, state, Number(BAO_PROXY.split(":")[1]));
  servers.push(bao.server);
  // The short variant is for the expired claim of ginsys/bronzeward#341: a claim passes its absolute
  // expiry 45 s after it starts, the lease being shorter than that as the server requires.
  const providerBlock = bao.block;
  const deploymentWith = (expiry: string) => [
    fixtureDenied,
    providerBlock,
    `ingestion: {instance: rec-01, heartbeat: 5s, lease: 15s, absoluteExpiry: ${expiry}, sweep: 15s}`,
  ].join("\n");
  const disposableDeployment = deploymentWith("10m");
  let disposable: { db: string; p: ChildProcess } | undefined;
  // Only the deployment this run started on a database it created, with the faults' upstream on it:
  // the epoch, the KV hold and the key retirement act on nothing else.
  const owned = (what: string) => {
    const db = disposable?.db;
    if (injector.faults.upstream !== DISPOSABLE_API || db === undefined || !db.startsWith("bw_auth_disposable_") || !databases.includes(db)) {
      throw new Error(`${what} only in this run's disposable deployment, with the faults' upstream on it`);
    }
    return db;
  };
  const kv = (ms: number) => {
    owned("a KV write is held");
    if (!Number.isInteger(ms) || ms < 0 || ms > 60_000) throw new Error(`a KV hold of ${ms} ms: give 0 to 60000`);
    bao!.hold = ms;
  };
  const chaos: Chaos = {
    fault: (mode) => {
      owned("an OpenBao fault is set");
      if (mode !== "none" && mode !== "kv" && mode !== "decrypt") throw new Error(`an OpenBao fault of mode ${mode}: give none, kv or decrypt`);
      bao!.fail = mode;
    },
    decryptHold: (ms) => {
      owned("a decrypt is held");
      if (!Number.isInteger(ms) || ms < 0 || ms > 120_000) throw new Error(`a decrypt hold of ${ms} ms: give 0 to 120000`);
      bao!.decryptHold = ms;
    },
    interrupt: (line) => {
      owned("an interrupt is set");
      if (line === "") return rmSync(interruptFile, { force: true });
      if (!/^[a-z-]+ (kill|stall|lose-reply)$/.test(line)) throw new Error(`an interrupt of "${line}": give "<step> kill", "<step> stall" or "<step> lose-reply"`);
      writeFileSync(interruptFile, `${line}\n`);
    },
    // The process ends by SIGKILL, as a crash does, so nothing it held is released; the database stays,
    // and POST /disposable?keep starts the deployment again on it.
    kill: async () => {
      owned("the process is killed");
      const p = disposable!.p;
      if (p.exitCode !== null || p.signalCode !== null) return;
      await new Promise<void>((resolve) => {
        p.once("exit", () => resolve());
        p.kill("SIGKILL");
      });
    },
  };
  // The identifiers of the records bin/e2eseed.test wrote into the disposable deployment's database.
  let seeded: string | undefined;
  const ctl = control(provider, next, injector, async (recovery, keep, short, seed) => {
    if (seed && keep) throw new Error("a seed is written once, into a fresh database: give ?seed or ?keep, not both");
    if (disposable !== undefined) await stop(disposable.p);
    if (!keep) seeded = undefined;
    disposable = server("disposable", DISPOSABLE_API.slice(7), fixtureOidc, short ? deploymentWith("45s") : disposableDeployment, recovery ? ["-recovery-start"] : [], keep ? disposable?.db : undefined, true);
    await ready(`${DISPOSABLE_API}/livez`, disposable.p);
    if (seed) {
      const out = `${state}/seeded.json`;
      execFileSync(`${root}bin/e2eseed.test`, ["-test.run", "^TestE2ESeed$", "-test.count=1"], {
        stdio: "inherit",
        env: { ...process.env, BW_E2E_SEED_DSN: `postgres://bronzeward@127.0.0.1:${PG_PORT}/${disposable.db}?sslmode=disable`, BW_E2E_SEED_OUT: out },
      });
      seeded = readFileSync(out, "utf8");
      rmSync(out);
    }
    return DISPOSABLE_API;
  }, () => seeded, kv, chaos);
  await new Promise<void>((resolve, reject) => {
    ctl.once("error", reject);
    ctl.listen(Number(new URL(CONTROL).port), "127.0.0.1", resolve);
  });
  servers.push(ctl);

  const a = server("idp", IDP_API.slice(7), `issuer: ${idp["BW_TEST_IDP_ISSUER"]}\naudience: "${idp["BW_TEST_IDP_PROJECT"]}"`, "  deniedSubjects: []").p;
  const b = server("fixture", FIXTURE_API.slice(7), fixtureOidc, fixtureDenied).p;
  // A new epoch in the disposable deployment's database, as a recovery entry commits it; the server
  // reads the epoch on each request. Only a database this run created for that deployment: any
  // other, B's included, is refused, so no case changes a database it does not own.
  injector.epoch = () => {
    const db = owned("a new epoch is committed");
    const ep = `ep_${base32(randomBytes(16))}`;
    execFileSync("psql", [...PG, "-v", "ON_ERROR_STOP=1", "-q", "-d", db, "-c", `INSERT INTO recovery_epoch (epoch, entered_at) VALUES ('${ep}', now())`, "-c", `UPDATE installation_state SET epoch = '${ep}'`]);
  };
  // This run's digest key only (bao.ts), and only while the writes go to the disposable deployment.
  injector.retire = async () => {
    owned("a digest key is retired");
    await bao!.retire();
  };
  await ready(`${IDP_API}/livez`, a);
  await ready(`${FIXTURE_API}/livez`, b);
  const ha = host(PAGE_LISTEN, {
    issuer: idp["BW_TEST_IDP_ISSUER"], audience: idp["BW_TEST_IDP_PROJECT"], cliClientId: idp["BW_TEST_IDP_CLI_CLIENT"], webClientId: idp["BW_TEST_IDP_WEB_CLIENT"],
    // Zitadel puts the project in aud only when this scope asks for it (client contracts §5 item 4).
    scopes: `urn:zitadel:iam:org:project:id:${idp["BW_TEST_IDP_PROJECT"]}:aud`,
  }, idp["BW_TEST_IDP_ISSUER"]!, IDP_API);
  const hb = host(FIXTURE_LISTEN, { issuer: FIXTURE_ISSUER, audience: "bronzeward", cliClientId: "synthetic-cli-client", webClientId: "synthetic-web-client" }, FIXTURE_ISSUER, INJECTOR);
  await ready(`http://${PAGE_LISTEN}/`, ha);
  await ready(`http://${FIXTURE_LISTEN}/`, hb);
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
  let failed = false;
  let failure: unknown;
  try {
    // Each server must be gone before its database can be dropped.
    await Promise.all(procs.splice(0).map(stop));
    await Promise.all(servers.splice(0).map((s) => new Promise((r) => (s.closeAllConnections(), s.close(r)))));
    // A failure is reported after the databases are dropped, and keeps `bao`, so a later cleanup
    // tries its records again (#517 H19).
    const { kept, failed: baoFailed } = await cleanBao(bao);
    bao = kept;
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
    const failure = teardownFailure(left.length > 0 ? new Error(`databases left behind: ${left.join(", ")} (named in ${state})`) : undefined, baoFailed);
    if (failure !== undefined) throw failure;
    if (ownState) rmSync(state, { recursive: true, force: true });
    ownState = false;
  } catch (e) {
    failed = true;
    failure = e;
  }
  // The browsers' profiles go last and whatever the teardown left, so that failing to remove them
  // leaves nothing else undone; a teardown failure stays the error, with this one logged.
  try {
    removeProfiles();
  } catch (e) {
    if (!failed) throw e;
    console.error(`browser profiles: ${String(e)}`);
  }
  if (failed) throw failure;
}

export const config: WebdriverIO.Config = {
  ...base,
  // Every spec file unless BW_SPECS names some (comma-separated, e.g. "inventory,shell"), for a local
  // rerun; CI sets none.
  specs: process.env["BW_SPECS"] ? process.env["BW_SPECS"].split(",").map((s) => `./auth/${s}.e2e.ts`) : ["./auth/*.e2e.ts"],
  baseUrl: IDP_PAGE,
  async onPrepare() {
    try {
      await prepare();
    } catch (e) {
      await cleanup().catch((c: unknown) => console.error(String(c)));
      throw e instanceof SevereServiceError ? e : new SevereServiceError(String(e));
    }
  },
  onComplete: () => printFailure(cleanup),
};
