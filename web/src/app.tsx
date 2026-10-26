// The page: sign-in (W1), the callback, the signed-out page, and the shell while signed in. Client
// contracts §9.3, §9.5-§9.7, §10.3.

import { type ReactNode, useEffect, useState } from "react";
import { flushSync } from "react-dom";
import { beginSignIn, CALLBACK_PATH, completeSignIn, discover, type Failure, localReading, PENDING_KEY, type Provider, Session, SIGNED_OUT_PATH, type Tokens } from "./auth.ts";
import { type ClientConfig, loadClientConfig } from "./config.ts";
import { navigate, PageState, renewals } from "./page.ts";
import { type Channel, Renewer } from "./renewer.ts";
import { TRANSPORT_FAILURE } from "./request.ts";
import { Deprecations, SignedIn, type SignedInProps, VersionFooter } from "./shell.tsx";
import { NOT_ACCEPTED, ProblemFields, problemFields } from "./states.tsx";

// §9.6 texts. The server says only `401`, so the page never names a cause it cannot read.
export { NOT_ACCEPTED };
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
  | { kind: "sign-in"; message: string; reading?: string | undefined; problem?: string | undefined; signIn: () => void }
  | { kind: "failed"; message: string; problem?: string | undefined }
  | { kind: "signed-in"; props: SignedInProps };

const failure = (f: Failure) => (f.kind === "transport" ? TRANSPORT_FAILURE : f.reason);

export function App() {
  const [view, setView] = useState<View>({ kind: "loading" });
  // §7 item 4: each route a response marked deprecated, once per page load.
  const [notices, setNotices] = useState<readonly string[]>([]);

  useEffect(() => {
    // The page-level state (page.ts): every await below is followed by a check of its mark.
    const page = new PageState();
    let session: Session | undefined;
    let renewing: ReturnType<typeof renewals> | undefined;
    // §9.5 item 2, set once signed in.
    let changed: (() => void) | undefined;
    // B1: another tab's pair is a new sign-in for another principal, else taken when newer. The
    // renewer opens the channel itself, once it holds its tab lock (#388).
    const renewer = new Renewer((): Channel => new BroadcastChannel(CHANNEL), navigator.locks, () => session, (t) => {
      const p = session?.principal;
      if (page.phase !== "signed-in" || p === undefined) return;
      if (p.iss !== t.iss || p.sub !== t.sub) return changed?.();
      if (session?.adopt(t) === true) renewing?.schedule();
    }, () => {
      // §9.7 item 2: another tab signed out. A renewal in flight here revokes the pair it brings
      // before the page leaves, within SIGN_OUT_WAIT_MS.
      const s = session;
      end();
      // Nothing read before stays on screen, even if the navigation stalls or is cancelled.
      flushSync(() => setView({ kind: "signing-out" }));
      void (s?.settled() ?? Promise.resolve()).then(() => location.replace(SIGNED_OUT_PATH));
    });
    // No more renewals here, and the lock goes to another tab.
    const stop = () => {
      renewing?.stop();
      renewer.release();
    };
    const forget = () => {
      stop();
      session?.clear();
      session = undefined;
    };
    const end = () => {
      page.to("ended");
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

    // The redirect to the provider, after the PKCE digest only while `still` holds.
    const signIn = (config: ClientConfig, provider: Provider, still: () => boolean) =>
      navigate(beginSignIn(config, provider, location.pathname + location.search + location.hash, sessionStorage, location.origin, still), still, (u) => location.assign(u));
    // The sign-in view. Its button is the operator's, so its mark is taken at the click.
    const offer = (message: string, config: ClientConfig, provider: Provider, more: { reading?: string | undefined; problem?: string | undefined } = {}) =>
      setView({ kind: "sign-in", message, ...more, signIn: () => void signIn(config, provider, page.mark()) });

    const signedIn = async (config: ClientConfig, provider: Provider, tokens: Tokens) => {
      page.to("signed-in");
      // §9.5 item 2: a renewal for another account is a new sign-in, and is shown, not adopted.
      // A renewal that answers after the page was refused or ended changes nothing on it.
      changed = () => {
        if (!page.to("changed")) return;
        forget();
        offer(PRINCIPAL_CHANGED, config, provider);
      };
      const s = new Session(config, provider, tokens, { onPrincipalChange: () => changed?.(), ...renewer.hooks });
      session = s;
      renewer.hold();
      // B1: the renewal goes through the lock holder; §9.3 item 3: by the redirect without rotation.
      const timer = renewals(page, {
        session: () => session,
        renew: () => renewer.renew(),
        holder: () => renewer.holder,
        redirect: (still) => signIn(config, provider, still),
      });
      renewing = timer;
      timer.schedule();
      const signOut = () => {
        // signOut takes the tokens before its first await, so the revocation still has the refresh token.
        const to = session?.signOut(location.origin) ?? Promise.resolve(SIGNED_OUT_PATH);
        renewer.signOut();
        end();
        setView({ kind: "signing-out" });
        void to.then((url) => location.assign(url));
      };
      // The shell's reads end the signed-in page on a 401 its one renewal does not clear, or on
      // identity-revoked; an answer after the page moved changes nothing on it.
      const refused = () => {
        if (!page.to("refused")) return false;
        stop();
        return true;
      };
      setView({
        kind: "signed-in",
        props: {
          auth: {
            token: () => session?.token(),
            // A read that outlives the signed-in page asks for no renewal (renewals()).
            renew: timer.renew,
            // The tokens go at once; the page moves to refused with the answer.
            onRevoked: () => {
              stop();
              s.clear();
            },
          },
          principal: `${s.principal?.iss} ${s.principal?.sub}`,
          sub: s.principal?.sub ?? "",
          onUnauthenticated: (res) => {
            if (!refused()) return;
            // Signing in again is the operator's (W1).
            const reading = localReading(s.token() ?? "", config, Date.now());
            offer(NOT_ACCEPTED, config, provider, { reading, problem: problemFields(res) });
          },
          onRevoked: (res) => {
            if (refused()) setView({ kind: "failed", message: REVOKED, problem: problemFields(res) });
          },
          onDeprecation: (route) => setNotices((n) => [...n, route]),
          signOut,
        },
      });
    };

    void (async () => {
      // #388: the signed-out page records no other tab's refresh token and holds no lock.
      if (location.pathname === SIGNED_OUT_PATH) {
        renewer.release();
        return setView({ kind: "signed-out" });
      }
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
      const still = page.mark();
      const config = await loadClientConfig();
      if (!still()) return abandon();
      if (config.kind !== "ok") return fail(config.kind === "transport" ? TRANSPORT_FAILURE : config.reason);
      const found = await discover(config.config.issuer);
      if (!still()) return abandon();
      if (found.kind !== "ok") return fail(failure(found));
      if (callback === undefined) {
        // B1: an open tab's pair, else the redirect.
        const tokens = await renewer.join();
        if (!still()) return;
        if (tokens === undefined) return void signIn(config.config, found.provider, still);
        return signedIn(config.config, found.provider, tokens);
      }
      const done = await completeSignIn(config.config, found.provider, callback, { storage: sessionStorage, origin: location.origin });
      if (!still()) return;
      if (done.kind !== "ok") return offer(failure(done), config.config, found.provider);
      history.replaceState(null, "", done.returnTo);
      renewer.post(done.tokens);
      await signedIn(config.config, found.provider, done.tokens);
    })();

    return () => {
      end();
      renewer.close();
      removeEventListener("pagehide", onHide);
      removeEventListener("pageshow", onShow);
    };
  }, []);

  return <Screen view={view} notices={notices} />;
}

// What each view shows; exported for the rendered-view tests. The deprecation notices stay on the
// signed-in page and on its refusals.
export function Screen({ view, notices = [] }: { view: View; notices?: readonly string[] }) {
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
      return <Page><p role="alert" data-testid="status">{view.message}</p><ProblemFields text={view.problem} /><Deprecations routes={notices} /></Page>;
    case "sign-in":
      return (
        <Page>
          <p role="alert" data-testid="status">{view.message}</p>
          <ProblemFields text={view.problem} />
          <Deprecations routes={notices} />
          {view.reading !== undefined && <p data-testid="reading">This browser's own reading: {view.reading}.</p>}
          <button type="button" onClick={view.signIn}>
            Sign in with the identity provider
          </button>
        </Page>
      );
    case "signed-in":
      return <SignedIn {...view.props} notices={notices} />;
  }
}

// Every view outside the shell, a refusal that ended it included, has no server answer to show
// (client contracts §7 item 5).
function Page({ children }: { children: ReactNode }) {
  return (
    <>
      <main>
        <h1>Bronzeward</h1>
        {children}
      </main>
      <VersionFooter />
    </>
  );
}
