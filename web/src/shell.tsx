// The web shell every screen shares (ginsys/bronzeward#333): the header wireframe's identity, roles,
// epoch and sign-out from GET /identity, the navigation, the footer's versions, and the client
// contracts §7 compatibility check, which GET /server answers before any page reads data.

import { createContext, type ReactNode, useCallback, useContext, useEffect, useId, useReducer, useRef, useState } from "react";
import { Client, type ClientOptions, type ReadResult } from "./request.ts";
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
// Offline since the earlier cause, undefined while neither holds.
export const offlineSince = ({ down, lost }: Connection) => (down === undefined || (lost !== undefined && lost < down) ? lost : down);

// §7 item 4: each deprecated route, once per page load.
export const Deprecations = ({ routes }: { routes: readonly string[] }) =>
  routes.map((route) => <Status key={route} testid="deprecation">{deprecated(route)}</Status>);

export function SignedIn({ auth, principal, sub, onUnauthenticated, onRevoked, onDeprecation, signOut, notices = [] }: SignedInProps & { notices?: readonly string[] }) {
  const [path, go] = usePath();
  const [client] = useState(() => {
    const c = new Client({ ...auth, onDeprecation });
    c.setView({ principal, route: location.pathname, query: location.search });
    return c;
  });
  const [server, setServer] = useState<Load<Server>>({ kind: "loading" });
  const [identity, setIdentity] = useState<Load<Identity>>({ kind: "loading" });
  const [conn, observe] = useReducer((c: Connection, [e, at]: [ConnectionEvent, Date]) => connection(c, e, at), {});
  const offline = offlineSince(conn);
  const [online, setOnline] = useState(0);
  const readAt = useRef<Date | undefined>(undefined);
  const shown = useRef({ server: false, identity: false });

  // A transport failure keeps the data this resource showed under the offline banner; without any,
  // it is shown as the disconnected state.
  const settle = useCallback(<T,>(res: ReadResult<T>, set: (r: Load<T>) => void, what: "server" | "identity") => {
    if (res.kind === "discarded") return;
    if (res.kind === "unauthenticated") return onUnauthenticated(res);
    if (res.kind === "revoked") return onRevoked(res);
    if (res.kind === "transport" && shown.current[what]) return observe(["lost", new Date()]);
    if (res.kind === "ok") {
      shown.current[what] = true;
      readAt.current = new Date();
      observe(["answered", readAt.current]);
    }
    set(res);
  }, [onUnauthenticated, onRevoked]);

  // Reads still in flight when the shell goes (a refusal, sign-out) are discarded, and renew no more.
  useEffect(() => () => client.setView({ principal: "", route: "", query: "" }), [client]);

  // Read until it answers with data; again when the browser is back online.
  const known = server.kind === "ok";
  useEffect(() => {
    if (!known) void client.read<Server>("/server").then((res) => settle(res, setServer, "server"));
  }, [client, known, online, settle]);

  // The header's identity, read again on every page and when the browser is back online.
  const compatible = known && server.data.apiRevision >= MIN_API_REVISION;
  useEffect(() => {
    if (!compatible) return;
    client.setView({ principal, route: path, query: location.search });
    void client.read<Identity>("/identity").then((res) => settle(res, setIdentity, "identity"));
  }, [client, compatible, path, principal, online, settle]);

  useEffect(() => {
    const down = () => observe(["offline", new Date()]);
    const up = () => {
      observe(["online", new Date()]);
      setOnline((n) => n + 1);
    };
    addEventListener("offline", down);
    addEventListener("online", up);
    return () => {
      removeEventListener("offline", down);
      removeEventListener("online", up);
    };
  }, []);

  let main: ReactNode;
  if (server.kind === "loading") main = <Loading what="the server's version" />;
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
      <footer>
        Client {clientVersion()}.{" "}
        <span data-testid="server">{server.kind === "ok" ? `Server ${server.data.version}, API revision ${server.data.apiRevision}` : "Server unknown"}</span>
      </footer>
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
