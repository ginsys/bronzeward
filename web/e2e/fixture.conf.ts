// The browser driver of the fixture scenarios (fixtures/scenarios/s6 mode web, ginsys/bronzeward#365): one act of the
// recovery-admin made in a real browser against a running fixture server, as a person signed in through the page.
// The scenario mints one fixture token for the human and hands it over in this process's environment only (BW_FIXTURE_TOKEN),
// never on a command line, in a log or over WebDriver. The synthetic provider (provider.ts) gives it to the page from its token
// endpoint, under the provider's own issuer: the page checks the id token against that issuer and the server checks the access
// token against the fixture issuer it trusts, so the two need not agree. A web host (fixtures/webhost) serves web/dist and passes the
// API to the server. The provider and the host live for one run, so nothing persists across a restart of the server.
// The acts are cases of fixture/acts.e2e.ts, chosen by BW_FIXTURE_ACT. The run fails if the token is found in web/e2e/out afterwards.
import { type ChildProcess } from "node:child_process";
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { type Server } from "node:http";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { SevereServiceError } from "webdriverio";
import { binPath, died, missingBinaries, spawnChild } from "./children.ts";
import { FIXTURE_ISSUER, FIXTURE_LISTEN, FIXTURE_PAGE } from "./ports.ts";
import { printFailure } from "./print-failure.ts";
import { startProvider } from "./provider.ts";
import { config as base, removeProfiles } from "./wdio.conf.ts";

const root = fileURLToPath(new URL("../..", import.meta.url));
const web = `${root}web/`;
const out = `${web}e2e/out`;
const children: ChildProcess[] = [];
const servers: Server[] = [];
let signature = "";
// The directory of the client configuration the web host reads (issuer, audience and synthetic client ids, no token), removed at the end.
let clientDir = "";

// The files under dir that hold sig, the token's last 24 characters.
function holding(dir: string, sig: string): string[] {
  return readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile())
    .map((e) => `${e.parentPath}/${e.name}`)
    .filter((f) => readFileSync(f).includes(sig));
}

async function prepare() {
  const token = process.env["BW_FIXTURE_TOKEN"];
  const api = process.env["BW_FIXTURE_API"];
  if (token === undefined || token === "" || api === undefined || api === "") throw new SevereServiceError("BW_FIXTURE_TOKEN and BW_FIXTURE_API must be set by the scenario");
  // The worker processes start after this and must not inherit the token.
  delete process.env["BW_FIXTURE_TOKEN"];
  // The human the page signs in as: one value, named by the scenario beside the token it minted for that human (a synthetic fixture
  // name, never a credential), so the page's sign-in and the token cannot name two.
  const human = process.env["BW_FIXTURE_HUMAN"];
  if (human === undefined || human === "") throw new SevereServiceError("BW_FIXTURE_HUMAN must be set by the scenario");
  signature = token.slice(-24);
  const missing = missingBinaries(root, ["webhost"]);
  if (missing.length > 0) throw new SevereServiceError(`missing ${missing.join(", ")} under ${root}bin: go build -o bin/webhost ./fixtures/webhost`);
  const provider = await startProvider({ issuer: FIXTURE_ISSUER, pageOrigin: FIXTURE_PAGE, clientId: "synthetic-web-client", next: () => ({ access: token, sub: human, bare: true }) });
  servers.push(provider.server);
  clientDir = mkdtempSync(`${tmpdir()}/bw-fixture-client-`);
  const clientConfig = `${clientDir}/client.json`;
  writeFileSync(clientConfig, JSON.stringify({ issuer: FIXTURE_ISSUER, audience: "bronzeward", cliClientId: "synthetic-cli-client", webClientId: "synthetic-web-client" }));
  const host = spawnChild(binPath(root, "webhost"), ["-listen", FIXTURE_LISTEN, "-dist", `${web}dist`, "-headers", `${web}headers.json`, "-client-config", clientConfig, "-issuer", FIXTURE_ISSUER, "-api", api], { stdio: "inherit" });
  children.push(host);
  for (let i = 0; ; i++) {
    const answered = await fetch(`${FIXTURE_PAGE}/bronzeward-client.json`).then((r) => r.ok, () => false);
    const why = died(host);
    if (why !== undefined) throw new SevereServiceError(`the web host could not start: ${why}`);
    if (answered) break;
    if (i === 75 || host.exitCode != null) throw new SevereServiceError(`the web host on ${FIXTURE_LISTEN} did not answer within 15 s`);
    await new Promise((r) => setTimeout(r, 200));
  }
}

async function cleanup() {
  for (const p of children.splice(0)) p.kill();
  await Promise.all(servers.splice(0).map((s) => new Promise((r) => (s.closeAllConnections(), s.close(r)))));
  removeProfiles();
  if (clientDir !== "") rmSync(clientDir, { recursive: true, force: true });
  if (signature === "") return;
  // The scan with its control: a file that holds the signature must be found, and the output must hold none.
  const control = mkdtempSync(`${tmpdir()}/bw-fixture-control-`);
  writeFileSync(`${control}/control.txt`, `control ${signature}\n`);
  const found = holding(control, signature).length;
  rmSync(control, { recursive: true, force: true });
  const hits = holding(out, signature);
  console.log(`token scan of ${out}: ${hits.length} files hold the token (expect 0), control finds ${found} (expect 1)`);
  if (found !== 1 || hits.length > 0) throw new Error(`the token scan failed: ${hits.length} files hold the token, control ${found}`);
}

export const config: WebdriverIO.Config = {
  ...base,
  specs: ["./fixture/acts.e2e.ts"],
  baseUrl: FIXTURE_PAGE,
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
