// The web shell every screen shares (ginsys/bronzeward#333): the header wireframe's identity, roles,
// epoch and sign-out from GET /identity, the navigation, the footer's versions, the client
// contracts §7 compatibility check, which GET /server answers before any page reads data, and the
// recovery banner, start mode and epoch-change notice of §8.5.

import { createContext, type ReactNode, useCallback, useContext, useEffect, useId, useReducer, useRef, useState } from "react";
import { PreEntry, type Recovery, RecoveryBanner, RecoveryRead, type StartMode, startModeOf, startRefusal } from "./recovery.tsx";
import { Client, type ClientOptions, EPOCH_CHANGED, type ReadResult } from "./request.ts";
import { follow, PAGES, usePath } from "./router.ts";
import { Disconnected, HumanOnly, Loading, Offline, ProblemFields, problemFields, Refused, Status } from "./states.tsx";
import type { components } from "./api/v1";

export type Server = components["schemas"]["ServerBody"];
export type Identity = components["schemas"]["IdentityBody"];
export type Role = Identity["roles"][number];

// §7 item 3: revision 2 added Bronzeward-Expected-Epoch, which every write but the recovery entry
// sends (§8.1).
export const MIN_API_REVISION = 2;

declare const __BW_VERSION__: string;
export const clientVersion = () => __BW_VERSION__;

export const incompatible = (served: number) =>
  `This server serves API revision ${served}; this client needs API revision ${MIN_API_REVISION} or later.`;
export const deprecated = (route: string) => `The server marks ${route} as deprecated: it will be removed in a later release.`;

// What an action needs to know: the roles GET /identity served, and whether the page is offline.
const Shell = createContext<{ roles: readonly Role[]; offline: boolean }>({ roles: [], offline: false });
export const ShellContext = Shell.Provider;

// An action the roles do not allow is disabled with its reason; the server's refusal stays
// authoritative (journeys §7), so `needs` only names what the route accepts.
export function Action({ label, needs, onClick }: { label: string; needs: readonly Role[]; onClick: () => void }) {
  const { roles, offline } = useContext(Shell);
  const id = useId();
  const reason = offline ? "Offline" : needs.some((r) => roles.includes(r)) ? undefined : `Needs ${needs.join(" or ")}`;
  return (
    <span>
      <button type="button" onClick={onClick} disabled={reason !== undefined} aria-describedby={reason !== undefined ? id : undefined}>{label}</button>
      {reason !== undefined && <span id={id}> {reason}</span>}
    </span>
  );
}

type Load<T> = { kind: "loading" } | ReadResult<T>;

// A read that did not answer with data, as the catalogue shows it; a 403 names what the route
// accepts, from the server's own answer, with its problem fields under it (§8.2, §9.6).
export function Failure({ res, sub }: { res: Exclude<ReadResult<unknown>, { kind: "ok" | "discarded" }>; sub: string }) {
  if (res.kind === "transport") return <Disconnected />;
  if (res.kind === "defect") return <Status>Not read: {res.message}.</Status>;
  if (res.kind === "refused" && res.status === 403) {
    const p = res.problem;
    const row = (text: ReactNode) => <>{text}<ProblemFields text={problemFields(res)} /></>;
    if (p?.["humanOnly"] === true) return row(<HumanOnly />);
    if (p?.["plan"] !== undefined && typeof p.detail === "string") return row(<Status>{p.detail}; you are signed in as {sub}</Status>);
    const roles = p?.["roles"];
    if (Array.isArray(roles) && roles.every((r) => typeof r === "string")) return row(<Status>Needs {roles.join(" or ")}; you are signed in as {sub}</Status>);
  }
  return <Refused problem={"problem" in res ? res.problem : null} status={"status" in res ? res.status : 0} verb="read" />;
}

export interface SignedInProps {
  auth: Pick<ClientOptions, "token" | "renew" | "onRevoked">;
  // The token's principal, for discarding another sign-in's answers, and its subject for texts.
  principal: string;
  sub: string;
  // The refusal that ends the signed-in page, with its answer for the problem fields (§8.2).
  onUnauthenticated: (res: ReadResult<unknown>) => void;
  onRevoked: (res: ReadResult<unknown>) => void;
  // §7 item 4: the page keeps the notices, so one on a refusal's answer outlives the shell.
  onDeprecation: (route: string) => void;
  signOut: () => void;
}

// The offline banner's two causes: the browser's offline event, and a read that found no answer
// after its data was shown. Each ends only on its own event (an online event; a read that answers),
// so a late answer while the browser is offline keeps the actions disabled.
export interface Connection {
  down?: Date | undefined;
  lost?: Date | undefined;
}
export type ConnectionEvent = "offline" | "online" | "lost" | "answered";
export function connection(c: Connection, e: ConnectionEvent, at: Date): Connection {
  switch (e) {
    case "offline":
      return { ...c, down: c.down ?? at };
    case "online":
      return { ...c, down: undefined };
    case "lost":
      return { ...c, lost: c.lost ?? at };
    case "answered":
      return { ...c, lost: undefined };
  }
}
// What a read's result says about the connection: any answer the server gave (data, a refusal, a
// problem) is "answered"; no answer is "lost" only once this resource's data was shown, which stays
// under the banner (otherwise the read shows the disconnected state); a discarded read says nothing.
export function observed(res: ReadResult<unknown>, shown: boolean): ConnectionEvent | undefined {
  if (res.kind === "discarded") return undefined;
  if (res.kind !== "transport") return "answered";
  return shown ? "lost" : undefined;
}
// Offline since the earlier cause, undefined while neither holds.
export const offlineSince = ({ down, lost }: Connection) => (down === undefined || (lost !== undefined && lost < down) ? lost : down);

// §7 item 5: the client's version and the server's, "unknown" while its read is refused; the views
// a refusal ends the shell with show it too.
export const VersionFooter = ({ server }: { server?: Server | undefined }) => (
  <footer>
    Client {clientVersion()}.{" "}
    <span data-testid="server">{server !== undefined ? `Server ${server.version}, API revision ${server.apiRevision}` : "Server unknown"}</span>
  </footer>
);

// §7 item 4: each deprecated route, once per page load.
export const Deprecations = ({ routes }: { routes: readonly string[] }) =>
  routes.map((route) => <Status key={route} testid="deprecation">{deprecated(route)}</Status>);

export function SignedIn({ auth, principal, sub, onUnauthenticated, onRevoked, onDeprecation, signOut, notices = [] }: SignedInProps & { notices?: readonly string[] }) {
  const [path, go, query] = usePath();
  const [server, setServer] = useState<Load<Server>>({ kind: "loading" });
  const [identity, setIdentity] = useState<Load<Identity>>({ kind: "loading" });
  const [recovery, setRecovery] = useState<Load<Recovery>>({ kind: "loading" });
  const [recoveryMode, setRecoveryMode] = useState(false);
  const [mode, setMode] = useState<StartMode>("unknown");
  const [epochChanged, setEpochChanged] = useState(false);
  const [conn, observe] = useReducer((c: Connection, [e, at]: [ConnectionEvent, Date]) => connection(c, e, at), {});
  const offline = offlineSince(conn);
  // Bumped to read again: back online, asked by the operator, or after a new epoch.
  const [again, setAgain] = useState(0);
  const readAt = useRef<Date | undefined>(undefined);
  const shown = useRef({ server: false, identity: false, recovery: false });
  const [client] = useState(() => {
    const c = new Client({
      ...auth,
      onDeprecation,
      // §8.5: what was read is discarded with its epoch, and GET /server read again for the mode.
      onEpochChange: () => {
        setEpochChanged(true);
        setServer({ kind: "loading" });
        setIdentity({ kind: "loading" });
        setRecovery({ kind: "loading" });
        shown.current = { server: false, identity: false, recovery: false };
        setAgain((n) => n + 1);
      },
      onRecoveryModeChange: setRecoveryMode,
    });
    c.setView({ principal, route: location.pathname, query: location.search });
    return c;
  });

  // A transport failure keeps the data this resource showed under the offline banner; without any,
  // it is shown as the disconnected state.
  // A refusal ends the page whichever read carried it, a superseded one too: the client already
  // dropped the credentials it refused. Any other answer of a superseded read says nothing more.
  const settle = useCallback(<T,>(res: ReadResult<T>, set: (r: Load<T>) => void, what: "server" | "identity" | "recovery", live: () => boolean) => {
    if (res.kind === "discarded") return;
    if (res.kind === "unauthenticated") return onUnauthenticated(res);
    if (res.kind === "revoked") return onRevoked(res);
    if (!live()) return;
    if (startRefusal(res)) setMode("recovery");
    const now = new Date();
    const e = observed(res, shown.current[what]);
    if (e !== undefined) observe([e, now]);
    if (e === "lost") return;
    // A refusal replaces the data, so a later read without answer has nothing to keep shown.
    shown.current[what] = res.kind === "ok";
    if (res.kind === "ok") readAt.current = now;
    set(res);
  }, [onUnauthenticated, onRevoked]);

  // Reads still in flight when the shell goes (a refusal, sign-out) are discarded, and renew no more.
  useEffect(() => () => client.setView({ principal: "", route: "", query: "" }), [client]);

  // Read until it answers with data, each time the page reads again. A read a newer one or a new
  // page superseded is ignored when it completes, so it cannot replace a newer answer.
  const known = server.kind === "ok";
  useEffect(() => {
    if (known) return;
    let live = true;
    void client.read<Server>("/server").then((res) => {
      const m = startModeOf(res);
      if (m !== undefined && live) setMode(m);
      settle(res, setServer, "server", () => live);
    });
    return () => void (live = false);
  }, [client, known, again, settle]);

  // The header's identity, and the recovery banner's scopes while in recovery mode, read again on
  // every page and each time the page reads again.
  const compatible = known && server.data.apiRevision >= MIN_API_REVISION;
  useEffect(() => {
    if (!compatible) return;
    client.setView({ principal, route: path, query });
    let live = true;
    void client.read<Identity>("/identity").then((res) => settle(res, setIdentity, "identity", () => live));
    return () => void (live = false);
  }, [client, compatible, path, query, principal, again, settle]);
  useEffect(() => {
    if (!compatible || !recoveryMode) return;
    let live = true;
    void client.read<Recovery>("/recovery").then((res) => settle(res, setRecovery, "recovery", () => live));
    return () => void (live = false);
  }, [client, compatible, recoveryMode, path, query, again, settle]);

  useEffect(() => {
    const down = () => observe(["offline", new Date()]);
    const up = () => {
      observe(["online", new Date()]);
      setAgain((n) => n + 1);
    };
    addEventListener("offline", down);
    addEventListener("online", up);
    // A browser already offline when the shell came sent no event.
    if (!navigator.onLine) down();
    return () => {
      removeEventListener("offline", down);
      removeEventListener("online", up);
    };
  }, []);

  let main: ReactNode;
  if (server.kind === "loading") main = <Loading what="the server's version" />;
  // GET /server is refused this way only before a recovery start's entry (§8.5 item 1).
  else if (startRefusal(server)) main = <PreEntry onRead={() => setAgain((n) => n + 1)} />;
  else if (server.kind !== "ok") main = server.kind !== "discarded" && <Failure res={server} sub={sub} />;
  else if (!compatible) main = <Status>{incompatible(server.data.apiRevision)}</Status>;
  else if (identity.kind === "loading") main = <Loading what="your identity" />;
  else if (identity.kind !== "ok") main = identity.kind !== "discarded" && <Failure res={identity} sub={sub} />;
  else main = <Page path={path} />;

  const id = identity.kind === "ok" ? identity.data : undefined;
  return (
    <ShellContext value={{ roles: id?.roles ?? [], offline: offline !== undefined }}>
      <header>
        <h1>Bronzeward</h1>
        {id !== undefined && (
          <p>
            <span data-testid="principal">Signed in as {id.subject ?? id.service}</span> ({id.kind}).{" "}
            <span data-testid="roles">Roles: {id.roles.length > 0 ? id.roles.join(", ") : "none"}</span>.{" "}
            <span data-testid="epoch">Epoch {id.epoch}</span>
          </p>
        )}
        <button type="button" onClick={signOut}>Sign out</button>
        {recoveryMode && <RecoveryBanner recovery={recovery.kind === "ok" ? recovery.data : undefined} mode={mode} />}
        {recoveryMode && recovery.kind !== "loading" && <RecoveryRead res={recovery} />}
        {epochChanged && <Status testid="epoch-changed">{EPOCH_CHANGED}.</Status>}
        <Deprecations routes={notices} />
        {offline !== undefined && <Offline since={offline} readAt={readAt.current} />}
        {id !== undefined && (
          <nav aria-label="Main">
            {PAGES.map(([to, title]) => (
              <a key={to} href={to} aria-current={current(path) === to ? "page" : undefined} onClick={(e) => follow(e, go, to)}>{title}</a>
            ))}
          </nav>
        )}
      </header>
      <main>{main}</main>
      <VersionFooter server={server.kind === "ok" ? server.data : undefined} />
    </ShellContext>
  );
}

// The root is the machine list, the hub of the journeys (§6).
const current = (path: string) => (path === "/" ? "/machines" : path);

// Each page's view comes with its own ticket (#334 onwards).
function Page({ path }: { path: string }) {
  const page = PAGES.find(([to]) => to === current(path));
  if (page === undefined) return <><h2>Not found</h2><Status>No page at {path}.</Status></>;
  return <h2>{page[1]}</h2>;
}
