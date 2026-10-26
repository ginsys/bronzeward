// The page until the shell lands (#333): sign-in (W1), the callback, the signed-out page, and one
// read of GET /server while signed in. Client contracts §9.3, §9.5-§9.7, §10.3.

import { type ReactNode, useEffect, useState } from "react";
import { flushSync } from "react-dom";
import { beginSignIn, CALLBACK_PATH, completeSignIn, discover, type Failure, localReading, PENDING_KEY, type Provider, renewDelay, Session, SIGNED_OUT_PATH, type Tokens } from "./auth.ts";
import { type ClientConfig, loadClientConfig } from "./config.ts";
import { type Message, Renewer } from "./renewer.ts";
import { Client, type ReadResult, TRANSPORT_FAILURE } from "./request.ts";
import type { components } from "./api/v1";

type Server = components["schemas"]["ServerBody"];

// §9.6 texts. The server says only `401`, so the page never names a cause it cannot read.
export const NOT_ACCEPTED = "Not accepted by this installation. Sign in again.";
export const REVOKED = "Your access was revoked or is denied by this installation; contact a recovery administrator.";
export const SIGNED_OUT = "You are signed out of Bronzeward.";
export const PRINCIPAL_CHANGED = "The identity provider renewed the sign-in for another account. Sign in again to continue.";
export const SIGNING_OUT = "Signing out";
const CHANNEL = "bronzeward";

export type View =
  | { kind: "loading" }
  // Nothing at all: after pagehide, so a restored page shows no data (§10.3 items 3-4).
  | { kind: "blank" }
  | { kind: "signed-out" }
  // Between the sign-out button and the redirect: nothing read before stays on the page (§9.7, §10.3).
  | { kind: "signing-out" }
  // `problem`: the answer's §8.2 fields, under a message of its own.
  | { kind: "sign-in"; message: string; reading?: string | undefined; problem?: string | undefined; config: ClientConfig; provider: Provider }
  | { kind: "failed"; message: string; problem?: string | undefined }
  | { kind: "signed-in"; sub: string; status: string; problem?: string | undefined; signOut: () => void };

const failure = (f: Failure) => (f.kind === "transport" ? TRANSPORT_FAILURE : f.reason);

export function App() {
  const [view, setView] = useState<View>({ kind: "loading" });

  useEffect(() => {
    let session: Session | undefined;
    let renewing: ReturnType<typeof renewals> | undefined;
    // Set when this page signs out, is hidden or unmounts: a sign-in still awaiting stops there.
    let ended = false;
    const channel = new BroadcastChannel(CHANNEL);
    // §9.5 item 2, set once signed in.
    let changed: (() => void) | undefined;
    // B1: another tab's pair is a new sign-in for another principal, else taken when newer.
    const renewer = new Renewer(channel, navigator.locks, () => session, (t) => {
      const p = session?.principal;
      if (p === undefined) return;
      if (p.iss !== t.iss || p.sub !== t.sub) return changed?.();
      if (session?.adopt(t) === true) renewing?.schedule();
    });
    const forget = () => {
      renewing?.stop();
      renewer.release();
      session?.clear();
      session = undefined;
    };
    const end = () => {
      ended = true;
      forget();
    };
    // §10.3 items 3-4: cleared on pagehide; a page restored from the back-forward cache starts over.
    const onHide = () => {
      end();
      flushSync(() => setView({ kind: "blank" }));
    };
    const onShow = (e: PageTransitionEvent) => {
      if (e.persisted) location.reload();
    };
    addEventListener("pagehide", onHide);
    addEventListener("pageshow", onShow);
    // §9.7 item 2: another tab signed out.
    channel.onmessage = (e: MessageEvent) => {
      const m = e.data as Message | null;
      if (typeof m !== "object" || m === null) return;
      if (m.type !== "signed-out") return renewer.onMessage(m);
      end();
      // Nothing read before stays on screen, even if the navigation stalls or is cancelled.
      flushSync(() => setView({ kind: "signing-out" }));
      location.replace(SIGNED_OUT_PATH);
    };

    const signIn = async (config: ClientConfig, provider: Provider) => {
      location.assign(await beginSignIn(config, provider, location.pathname + location.search + location.hash, sessionStorage, location.origin));
    };

    const signedIn = async (config: ClientConfig, provider: Provider, tokens: Tokens) => {
      // §9.5 item 2: a renewal for another account is a new sign-in, and is shown, not adopted.
      changed = () => {
        forget();
        setView({ kind: "sign-in", message: PRINCIPAL_CHANGED, config, provider });
      };
      const s = new Session(config, provider, tokens, { onPrincipalChange: () => changed?.(), ...renewer.hooks });
      session = s;
      renewer.hold();
      // B1: the renewal goes through the lock holder.
      const timer = renewals(() => session && { expiresAt: session.expiresAt, renew: () => renewer.renew() });
      renewing = timer;
      timer.schedule();
      const client = new Client({
        token: () => session?.token(),
        renew: timer.renew,
        // The tokens go, but the session stays this read's, so the refusal below is still shown.
        onRevoked: () => {
          timer.stop();
          renewer.release();
          s.clear();
        },
      });
      client.setView({ principal: `${s.principal?.iss} ${s.principal?.sub}`, route: location.pathname, query: location.search });
      const signOut = () => {
        // signOut takes the tokens before its first await, so the revocation still has the refresh token.
        const to = session?.signOut(location.origin) ?? Promise.resolve(SIGNED_OUT_PATH);
        end();
        setView({ kind: "signing-out" });
        channel.postMessage({ type: "signed-out" });
        void to.then((url) => location.assign(url));
      };
      const res = await client.read<Server>("/server");
      if (session !== s) return;
      if (res.kind === "unauthenticated") {
        // Signing in again is the operator's (W1).
        timer.stop();
        const reading = localReading(s.token() ?? "", config, Date.now());
        return setView({ kind: "sign-in", message: NOT_ACCEPTED, reading, problem: problemFields(res), config, provider });
      }
      if (res.kind === "revoked") return setView({ kind: "failed", message: REVOKED, problem: problemFields(res) });
      const sub = s.principal?.sub ?? "";
      const status = describe(res, sub);
      const problem = problemFields(res);
      setView({ kind: "signed-in", sub, status, problem: problem === status ? undefined : problem, signOut });
    };

    void (async () => {
      if (location.pathname === SIGNED_OUT_PATH) return setView({ kind: "signed-out" });
      // The callback's code and state leave the address bar and the history entry before anything
      // can fail, and its pending entry goes on every path that does not reach completeSignIn.
      const callback = location.pathname === CALLBACK_PATH ? location.search : undefined;
      if (callback !== undefined) history.replaceState(null, "", "/");
      const abandon = () => {
        if (callback !== undefined) sessionStorage.removeItem(PENDING_KEY);
      };
      const fail = (message: string) => {
        abandon();
        setView({ kind: "failed", message });
      };
      const config = await loadClientConfig();
      if (ended) return abandon();
      if (config.kind !== "ok") return fail(config.kind === "transport" ? TRANSPORT_FAILURE : config.reason);
      const found = await discover(config.config.issuer);
      if (ended) return abandon();
      if (found.kind !== "ok") return fail(failure(found));
      if (callback === undefined) {
        // B1: an open tab's pair, else the redirect.
        const tokens = await renewer.join();
        if (ended) return;
        if (tokens === undefined) return signIn(config.config, found.provider);
        return signedIn(config.config, found.provider, tokens);
      }
      const done = await completeSignIn(config.config, found.provider, callback, { storage: sessionStorage, origin: location.origin });
      if (ended) return;
      if (done.kind !== "ok") return setView({ kind: "sign-in", message: failure(done), config: config.config, provider: found.provider });
      history.replaceState(null, "", done.returnTo);
      renewer.post(done.tokens);
      await signedIn(config.config, found.provider, done.tokens);
    })();

    return () => {
      end();
      channel.close();
      removeEventListener("pagehide", onHide);
      removeEventListener("pageshow", onShow);
    };
  }, []);

  return <Screen view={view} />;
}

// §9.5 item 1: renew before less than 60 s remains; a failed renewal keeps the current token.
// After stop() (the server refused the session, or the page forgot it) nothing re-arms the timer,
// not even a renewal still in flight.
export function renewals(session: () => Pick<Session, "expiresAt" | "renew"> | undefined) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  const schedule = () => {
    clearTimeout(timer);
    const at = session()?.expiresAt;
    if (stopped || at === undefined) return;
    timer = setTimeout(() => void session()?.renew().then((ok) => ok && schedule()), renewDelay(at, Date.now()));
  };
  return {
    schedule,
    renew: async () => (await session()?.renew()) === true && (schedule(), true),
    stop: () => {
      stopped = true;
      clearTimeout(timer);
    },
  };
}

// What each view shows; exported for the rendered-view tests.
export function Screen({ view }: { view: View }) {
  switch (view.kind) {
    case "blank":
      return null;
    case "loading":
      return <Page><p role="status" data-testid="status">Loading</p></Page>;
    case "signing-out":
      return <Page><p role="status" data-testid="status">{SIGNING_OUT}</p></Page>;
    case "signed-out":
      return (
        <Page>
          <p role="status" data-testid="status">{SIGNED_OUT}</p>
          <p><a href="/">Sign in</a></p>
        </Page>
      );
    case "failed":
      return <Page><p role="alert" data-testid="status">{view.message}</p><Problem text={view.problem} /></Page>;
    case "sign-in":
      return (
        <Page>
          <p role="alert" data-testid="status">{view.message}</p>
          <Problem text={view.problem} />
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
          <Problem text={view.problem} />
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
  if (res.kind === "defect") return `The server read failed: ${res.message}`;
  return problemText(res);
}

// The answer's problem, when it carries one.
export function problemFields(res: ReadResult<unknown>): string | undefined {
  return "problem" in res && res.problem !== null ? problemText(res) : undefined;
}

const Problem = ({ text }: { text: string | undefined }) => (text === undefined ? null : <p data-testid="problem">{text}</p>);

// §8.2: a problem's title, its detail when present and its instance, never a request body. The
// authentication 503's detail is W1's text.
export function problemText(res: ReadResult<unknown>): string {
  const p = "problem" in res ? res.problem : null;
  if (p === null) return `Not read: the server answered ${"status" in res ? res.status : res.kind}.`;
  return `Not read: ${p.title}${p.detail !== undefined ? ` (${p.detail})` : ""}.${p.instance !== undefined ? ` Request ${p.instance}.` : ""}`;
}
