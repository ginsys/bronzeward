// The disposable deployment's provider: a real OpenBao (mise run dev-bao locally, a CI step in the
// browser-auth job), as internal/baotest gives the Go tests one. This run writes the fixture's
// policy files (fixtures/openbao/policies) under its own names, makes its own digest key and one
// child token per role, and deletes or revokes all of it at the end. The deployment reaches
// OpenBao through a proxy that can hold each KV write, so an ingestion holds its draft-update claim
// (AMB-02), as the CLI's case does (internal/clitest/fragment_test.go slowBao). Data is synthetic:
// dev-bao's root token and generated values only.

import { randomBytes } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { createServer, request, type Server } from "node:http";

export interface Bao {
  // The proxy's URL, the deployment's provider address.
  url: string;
  server: Server;
  // This run's digest key, the only one retire acts on.
  digest: string;
  // The deployment's provider block.
  block: string;
  // How long each KV write is held, in milliseconds.
  hold: number;
  // Makes the digest key's current versions uncomputable (AMB-06).
  retire: () => Promise<void>;
  cleanup: () => Promise<void>;
}

const KEYS = ["bw-artifact", "bw-baseline", "bw-staging", "bw-digest"];
const ROLES = ["ingestion", "compiler", "metadata", "executor"];

export async function startBao(root: string, state: string, port: number): Promise<Bao> {
  // mise run browser-auth sets BW_DEV_BAO_ADDR from BW_DEV_INSTANCE (scripts/dev/instance.sh); the
  // default is the default instance's.
  const addr = (process.env["BW_TEST_BAO_ADDR"] ?? process.env["BW_DEV_BAO_ADDR"] ?? "http://127.0.0.1:11200").replace(/\/$/, "");
  const admin = process.env["BW_TEST_BAO_TOKEN"] ?? "bw-dev-root-token";
  const prefix = `bwweb-${randomBytes(6).toString("hex")}-`;
  const undo: (() => Promise<unknown>)[] = [];
  const call = async (method: string, path: string, body?: unknown) => {
    const r = await fetch(addr + path, { method, headers: { "X-Vault-Token": admin }, body: body === undefined ? null : JSON.stringify(body), redirect: "manual" });
    return { status: r.status, body: await r.text() };
  };
  const must = async (method: string, path: string, body?: unknown) => {
    const r = await call(method, path, body).catch((e: unknown) => {
      throw new Error(`OpenBao at ${addr} did not answer ${method} ${path} (run mise run dev-bao): ${String(e)}`);
    });
    if (Math.floor(r.status / 100) !== 2) throw new Error(`${method} ${path}: status ${r.status}: ${r.body}`);
    return r.body;
  };
  const undoAll = async () => {
    const failed: string[] = [];
    for (const u of undo.splice(0).reverse()) await u().catch((e: unknown) => failed.push(String(e)));
    if (failed.length > 0) throw new Error(`OpenBao cleanup (records named ${prefix}*): ${failed.join("; ")}`);
  };

  const make = async (): Promise<Bao> => {
    const mount = async (path: string, type: string, options?: Record<string, string>) => {
      if ((await call("GET", `/v1/sys/mounts/${path}`)).status === 200) return;
      await must("POST", `/v1/sys/mounts/${path}`, { type, options });
    };
    await mount("secret", "kv", { version: "2" });
    await mount("transit", "transit");
    for (const k of KEYS) if ((await call("GET", `/v1/transit/keys/${k}`)).status === 404) await must("POST", `/v1/transit/keys/${k}`, {});
    const digest = `${prefix}digest`;
    await must("POST", `/v1/transit/keys/${digest}`, {});
    undo.push(async () => {
      await must("POST", `/v1/transit/keys/${digest}/config`, { deletion_allowed: true });
      return must("DELETE", `/v1/transit/keys/${digest}`);
    });
    const policy = async (name: string, hcl: string) => {
      await must("PUT", `/v1/sys/policies/acl/${prefix}${name}`, { policy: hcl });
      undo.push(() => must("DELETE", `/v1/sys/policies/acl/${prefix}${name}`));
    };
    const dir = `${root}fixtures/openbao/policies`;
    for (const f of readdirSync(dir).filter((f) => f.endsWith(".hcl"))) await policy(f.slice(0, -4), readFileSync(`${dir}/${f}`, "utf8"));
    await policy("digest", `path "transit/hmac/${digest}" {\n  capabilities = ["update"]\n}\n`);
    const lines = ["provider:", `  address: http://127.0.0.1:${port}`, `  keys: {baseline: bw-baseline, staging: bw-staging, digest: ${digest}, artifact: bw-artifact}`];
    for (const role of ROLES) {
      const policies = [`${prefix}bw-${role}`, ...(role === "ingestion" ? [`${prefix}digest`] : [])];
      const out = JSON.parse(await must("POST", "/v1/auth/token/create", { policies, no_default_policy: true, ttl: "2h" })) as { auth: { client_token: string } };
      const token = out.auth.client_token;
      undo.push(() => must("POST", "/v1/auth/token/revoke", { token }));
      const file = `${state}/bao-${role}.token`;
      writeFileSync(file, `${token}\n`, { mode: 0o600 });
      lines.push(`  ${role}TokenFile: ${file}`);
    }
    const target = new URL(addr);
    const bao: Bao = {
      url: `http://127.0.0.1:${port}`,
      digest,
      block: lines.join("\n"),
      hold: 0,
      server: createServer((req, res) => {
        const held = req.method !== "GET" && (req.url ?? "").startsWith("/v1/secret/data/") ? bao.hold : 0;
        setTimeout(() => {
          const up = request({ host: target.hostname, port: target.port, path: req.url, method: req.method, headers: req.headers }, (r) => {
            res.writeHead(r.statusCode ?? 502, r.headers);
            r.pipe(res);
          });
          up.on("error", () => res.writeHead(502).end());
          req.pipe(up);
        }, held);
      }),
      // A rotation, then a minimum encryption version at the new one: Transit computes no HMAC below
      // it, so a key's record made under the earlier version can no longer be compared.
      retire: async () => {
        await must("POST", `/v1/transit/keys/${digest}/rotate`, {});
        const latest = /"latest_version":(\d+)/.exec(await must("GET", `/v1/transit/keys/${digest}`))?.[1];
        if (latest === undefined) throw new Error("the digest key shows no latest version");
        await must("POST", `/v1/transit/keys/${digest}/config`, { min_encryption_version: Number(latest) });
      },
      cleanup: undoAll,
    };
    await new Promise<void>((resolve, reject) => {
      bao.server.once("error", reject);
      bao.server.listen(port, "127.0.0.1", resolve);
    });
    return bao;
  };

  // A start that fails part way removes what it made.
  try {
    return await make();
  } catch (e) {
    await undoAll().catch((c: unknown) => console.error(String(c)));
    throw e;
  }
}
