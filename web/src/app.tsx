// The page until the shell lands (#333): sign-in (W1), the callback, the signed-out page, and one
// read of GET /server while signed in. Client contracts §9.3, §9.5-§9.7, §10.3.

import { type ReactNode, useEffect, useState } from "react";
import { flushSync } from "react-dom";
import { beginSignIn, CALLBACK_PATH, completeSignIn, discover, type Failure, localReading, type Provider, RENEW_BEFORE_MS, Session, SIGNED_OUT_PATH } from "./auth.ts";
import { type ClientConfig, loadClientConfig } from "./config.ts";
import { Client, type ReadResult, TRANSPORT_FAILURE } from "./request.ts";
import type { components } from "./api/v1";

type Server = components["schemas"]["ServerBody"];

// §9.6 texts. The server says only `401`, so the page never names a cause it cannot read.
export const NOT_ACCEPTED = "Not accepted by this installation. Sign in again.";
export const REVOKED = "Your access was revoked or is denied by this installation; contact a recovery administrator.";
export const SIGNED_OUT = "You are signed out of Bronzeward.";
export const PRINCIPAL_CHANGED = "The identity provider renewed the sign-in for another account. Sign in again to continue.";
const CHANNEL = "bronzeward";

type View =
  | { kind: "loading" }
  // Nothing at all: after pagehide, so a restored page shows no data (§10.3 items 3-4).
  | { kind: "blank" }
  | { kind: "signed-out" }
  | { kind: "sign-in"; message: string; reading?: string | undefined; config: ClientConfig; provider: Provider }
  | { kind: "failed"; message: string }
  | { kind: "signed-in"; sub: string; status: string; signOut: () => void };

const failure = (f: Failure) => (f.kind === "transport" ? TRANSPORT_FAILURE : f.reason);

export function App() {
  const [view, setView] = useState<View>({ kind: "loading" });

  useEffect(() => {
    let session: Session | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const channel = new BroadcastChannel(CHANNEL);
    const forget = () => {
      clearTimeout(timer);
      session?.clear();
      session = undefined;
    };
    // §10.3 items 3-4: cleared on pagehide; a page restored from the back-forward cache starts over.
    const onHide = () => {
      forget();
      flushSync(() => setView({ kind: "blank" }));
    };
    const onShow = (e: PageTransitionEvent) => {
      if (e.persisted) location.reload();
    };
    addEventListener("pagehide", onHide);
    addEventListener("pageshow", onShow);
    // §9.7 item 2: another tab signed out.
    channel.onmessage = (e: MessageEvent) => {
      if ((e.data as { type?: unknown } | null)?.type !== "signed-out") return;
      forget();
      location.replace(SIGNED_OUT_PATH);
    };

    const signIn = async (config: ClientConfig, provider: Provider) => {
      location.assign(await beginSignIn(config, provider, location.pathname + location.search + location.hash, sessionStorage, location.origin));
    };

    // §9.5 item 1: renew before less than 60 s remains; a failed renewal keeps the current token.
    const schedule = () => {
      clearTimeout(timer);
      const at = session?.expiresAt;
      if (at === undefined) return;
      timer = setTimeout(() => void session?.renew().then((ok) => ok && schedule()), Math.max(0, at - RENEW_BEFORE_MS - Date.now()));
    };

    const signedIn = async (config: ClientConfig, provider: Provider, s: Session) => {
      session = s;
      schedule();
      const client = new Client({
        token: () => session?.token(),
        renew: async () => (await session?.renew()) === true && (schedule(), true),
        // The tokens go, but the session stays this read's, so the refusal below is still shown.
        onRevoked: () => {
          clearTimeout(timer);
          s.clear();
        },
      });
      client.setView({ principal: `${s.principal?.iss} ${s.principal?.sub}`, route: location.pathname, query: location.search });
      const signOut = () => {
        const current = session;
        forget();
        channel.postMessage({ type: "signed-out" });
        void (current?.signOut(location.origin) ?? Promise.resolve(SIGNED_OUT_PATH)).then((to) => location.assign(to));
      };
      const res = await client.read<Server>("/server");
      if (session !== s) return;
      if (res.kind === "unauthenticated") {
        const reading = localReading(s.token() ?? "", config, Date.now());
        return setView({ kind: "sign-in", message: NOT_ACCEPTED, reading, config, provider });
      }
      if (res.kind === "revoked") return setView({ kind: "failed", message: REVOKED });
      const sub = s.principal?.sub ?? "";
      setView({ kind: "signed-in", sub, status: describe(res, sub), signOut });
    };

    void (async () => {
      if (location.pathname === SIGNED_OUT_PATH) return setView({ kind: "signed-out" });
      const config = await loadClientConfig();
      if (config.kind !== "ok") return setView({ kind: "failed", message: config.kind === "transport" ? TRANSPORT_FAILURE : config.reason });
      const found = await discover(config.config.issuer);
      if (found.kind !== "ok") return setView({ kind: "failed", message: failure(found) });
      if (location.pathname !== CALLBACK_PATH) return signIn(config.config, found.provider);
      const done = await completeSignIn(config.config, found.provider, location.search, { storage: sessionStorage, origin: location.origin });
      // The callback's code and state leave the address bar and the history entry either way.
      history.replaceState(null, "", done.kind === "ok" ? done.returnTo : "/");
      if (done.kind !== "ok") return setView({ kind: "sign-in", message: failure(done), config: config.config, provider: found.provider });
      // §9.5 item 2: a renewal for another account is a new sign-in, and is shown, not adopted.
      const onPrincipalChange = () => {
        forget();
        setView({ kind: "sign-in", message: PRINCIPAL_CHANGED, config: config.config, provider: found.provider });
      };
      await signedIn(config.config, found.provider, new Session(config.config, found.provider, done.tokens, { onPrincipalChange }));
    })();

    return () => {
      forget();
      channel.close();
      removeEventListener("pagehide", onHide);
      removeEventListener("pageshow", onShow);
    };
  }, []);

  switch (view.kind) {
    case "blank":
      return null;
    case "loading":
      return <Page><p role="status" data-testid="status">Loading</p></Page>;
    case "signed-out":
      return (
        <Page>
          <p role="status" data-testid="status">{SIGNED_OUT}</p>
          <p><a href="/">Sign in</a></p>
        </Page>
      );
    case "failed":
      return <Page><p role="alert" data-testid="status">{view.message}</p></Page>;
    case "sign-in":
      return (
        <Page>
          <p role="alert" data-testid="status">{view.message}</p>
          {view.reading !== undefined && <p data-testid="reading">This browser's own reading: {view.reading}.</p>}
          <button type="button" onClick={() => void beginSignIn(view.config, view.provider, location.pathname + location.search, sessionStorage, location.origin).then((u) => location.assign(u))}>
            Sign in with the identity provider
          </button>
        </Page>
      );
    case "signed-in":
      return (
        <Page>
          <p data-testid="principal">Signed in as {view.sub}</p>
          <p role="status" data-testid="status">{view.status}</p>
          <button type="button" onClick={view.signOut}>Sign out</button>
        </Page>
      );
  }
}

function Page({ children }: { children: ReactNode }) {
  return (
    <main>
      <h1>Bronzeward</h1>
      {children}
    </main>
  );
}

function describe(res: ReadResult<Server>, sub: string): string {
  if (res.kind === "ok") return `Server ${res.data.version}, API revision ${res.data.apiRevision}`;
  if (res.kind === "transport") return TRANSPORT_FAILURE;
  // §9.6 403 rows, from the server's own answer.
  const p = res.kind === "refused" && res.status === 403 ? res.problem : null;
  if (p?.["humanOnly"] === true) return "This needs a person signed in, not an automation token";
  if (p?.["plan"] !== undefined && typeof p.detail === "string") return `${p.detail}; you are signed in as ${sub}`;
  const roles = p?.["roles"];
  if (Array.isArray(roles) && roles.every((r) => typeof r === "string")) return `Needs ${roles.join(" or ")}; you are signed in as ${sub}`;
  return `The server read failed (${res.kind})`;
}
