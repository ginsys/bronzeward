// The web shell every screen shares (ginsys/bronzeward#333): the header wireframe's identity, roles,
// epoch and sign-out from GET /identity, the navigation, the footer's versions, the client
// contracts §7 compatibility check, which GET /server answers before any page reads data, and the
// recovery banner, start mode and epoch-change notice of §8.5.

import { createContext, type ReactNode, useCallback, useContext, useEffect, useId, useReducer, useRef, useState } from "react";
import { leaving, signingOut, unsaved, WriteNotices } from "./forms.tsx";
import { type Load, ReadsProvider } from "./list.tsx";
import { PreEntry, type Recovery, RecoveryBanner, RecoveryRead, type StartMode, startModeOf, startRefusal } from "./recovery.tsx";
import { Client, type ClientOptions, EPOCH_CHANGED, type Problem, type ReadResult } from "./request.ts";
import { view } from "./resources.tsx";
import { follow, PAGES, usePath } from "./router.ts";
import { Disconnected, HumanOnly, Loading, Offline, ProblemFields, problemFields, Refused, Status } from "./states.tsx";
import { type Ending, EPOCH_CHANGED_ASIDE, type Writes } from "./write.ts";
import type { components } from "./api/v1";

export type Server = components["schemas"]["ServerBody"];
export type Identity = components["schemas"]["IdentityBody"];
export type Role = Identity["roles"][number];

// §7 item 3: revision 2 added Bronzeward-Expected-Epoch, which every write but the recovery entry
// sends (§8.1); revision 3 the timeline's order and tail, which W3 reads with (§8.6 item 6).
export const MIN_API_REVISION = 3;

declare const __BW_VERSION__: string;
export const clientVersion = () => __BW_VERSION__;

export const incompatible = (served: number) =>
  `This server serves API revision ${served}; this client needs API revision ${MIN_API_REVISION} or later.`;
export const deprecated = (route: string) => `The server marks ${route} as deprecated: it will be removed in a later release.`;

// What an action needs to know: the roles GET /identity served, whether the page is offline, and
// whether it is a page kept across an epoch change (keepsPage).
const Shell = createContext<{ roles: readonly Role[]; offline: boolean; kept?: boolean }>({ roles: [], offline: false });
export const ShellContext = Shell.Provider;
// The offline banner shows: no action sends anything (journeys §8 Offline row).
export const useOffline = () => useContext(Shell).offline;

export const KEPT_PAGE = "The epoch changed: copy your text, then open the page again";

// An action the roles do not allow is disabled with its reason; the server's refusal stays
// authoritative (journeys §7), so `needs` only names what the route accepts. A page kept across an
// epoch change sends nothing (ginsys/bronzeward#517 H10).
export function useGate(needs: readonly Role[]): string | undefined {
  const { roles, offline, kept = false } = useContext(Shell);
  return offline ? "Offline" : kept ? KEPT_PAGE : needs.some((r) => roles.includes(r)) ? undefined : `Needs ${needs.join(" or ")}`;
}
export function Action({ label, needs, onClick, submit = false, busy = false }: { label: string; needs: readonly Role[]; onClick?: () => void; submit?: boolean; busy?: boolean }) {
  const reason = useGate(needs);
  const id = useId();
  return (
    <span>
      <button type={submit ? "submit" : "button"} onClick={onClick} disabled={reason !== undefined || busy} aria-describedby={reason !== undefined ? id : undefined}>{label}</button>
      {reason !== undefined && <span id={id}> {reason}</span>}
    </span>
  );
}

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

// A read that did not answer with data.
export type Unanswered = Exclude<ReadResult<unknown>, { kind: "ok" | "discarded" }>;

// A shell read and the failure of a later one (#513): a read that fails after one that answered
// keeps that answer, and the failure shows above the page instead of replacing it, so the page keeps
// its unsaved text.
export interface Reading<T> {
  load: Load<T>;
  failed?: Unanswered | undefined;
}
export function reading<T>(prev: Reading<T>, res: Load<T>, kept: boolean): Reading<T> {
  if (!kept || prev.load.kind !== "ok" || res.kind === "ok" || res.kind === "loading" || res.kind === "discarded") return { load: res };
  return { load: prev.load, failed: res };
}

// The page with the failure of a later read above it. The page keeps its place under this element
// whether or not a failure shows, so React keeps it mounted.
export function Kept({ failed, sub, children }: { failed: Unanswered | undefined; sub: string; children: ReactNode }) {
  return <>{failed !== undefined && <Failure res={failed} sub={sub} />}{children}</>;
}

// What the shell shows once GET /server is compatible: the wait for the first identity, the failure
// of that first read, or the page. The page element is the same one, at the same place, for every
// identity that answered, whatever failure shows above it.
export function identityMain(identity: Reading<Identity>, sub: string, page: ReactNode): ReactNode {
  if (identity.load.kind === "loading") return <Loading what="your identity" />;
  if (identity.load.kind !== "ok") return identity.load.kind !== "discarded" && <Failure res={identity.load} sub={sub} />;
  return <Kept failed={identity.failed} sub={sub}>{page}</Kept>;
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
  // The page's writes, which outlive the shell (B10, B11).
  writes: Writes;
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
// Whether each of the shell's three reads showed data; a page's reads each keep their own.
type Shown = Record<"server" | "identity" | "recovery", boolean>;
const noneShown = (): Shown => ({ server: false, identity: false, recovery: false });
// Offline since the earlier cause, undefined while neither holds.
export const offlineSince = ({ down, lost }: Connection) => (down === undefined || (lost !== undefined && lost < down) ? lost : down);
// The connection after `e`, and a count bumped when a read's answer ends the banner (§8.6 item 6):
// a view that kept a read without answer under it then reads that resource again once.
export function regaining([c, n]: [Connection, number], e: ConnectionEvent, at: Date): [Connection, number] {
  const next = connection(c, e, at);
  return [next, e === "answered" && offlineSince(c) !== undefined && offlineSince(next) === undefined ? n + 1 : n];
}

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

// What settling an answer changes in the shell besides the read's own state.
export interface ShellEvents {
  onUnauthenticated: (res: ReadResult<unknown>) => void;
  onRevoked: (res: ReadResult<unknown>) => void;
  // §8.5: a new epoch; what was read is discarded with it, and GET /server read again for the mode.
  onEpochChange: () => void;
  // The process's refusal on another read: the state before the entry, as GET /server's answer.
  onPreEntry: (res: Extract<ReadResult<unknown>, { kind: "refused" }>) => void;
  setMode: (mode: StartMode) => void;
  observe: (e: ConnectionEvent, at: Date) => void;
}

// The shell's client and its one invalidation point (#410). The process's recovery refusal, a new
// epoch, and a revocation or sign-out each make every read in flight stale: a read started before
// any of them is not settled into page state, whatever the client already answered it, though its
// effect's cleanup has not run yet. Credential refusals are handled before that check.
export class ShellReads {
  readonly client: Client;
  // Whether each of the shell's three reads showed data, and when a read last answered with data.
  shown = noneShown();
  readAt: Date | undefined;
  private invalidations = 0;

  constructor(opts: Omit<ClientOptions, "onEpochChange">, private readonly events: () => ShellEvents) {
    this.client = new Client({
      ...opts,
      // At once, before any answer the client gave under the old epoch is settled.
      onEpochChange: () => {
        this.invalidate(false);
        this.shown = noneShown();
        this.events().onEpochChange();
      },
      // The page ends with the revocation: nothing read before it is settled, or sent again.
      onRevoked: () => {
        this.invalidate(true);
        opts.onRevoked?.();
      },
    });
  }

  // Every read in flight is stale; with `drop`, the client sends none of them again either, not
  // even to learn an ambiguous epoch.
  invalidate(drop: boolean): void {
    if (drop) this.client.dropReads();
    this.invalidations++;
  }

  // The process's refusal of a recovery start on a page's read or on a write (§8.5 item 1): every
  // read in flight is dropped, and the page shows the state before the entry as GET /server's answer.
  preEntry(res: Extract<ReadResult<unknown>, { kind: "refused" }>): void {
    this.invalidate(true);
    this.shown = noneShown();
    const on = this.events();
    on.setMode("recovery");
    on.onPreEntry(res);
  }

  // Sign-out: nothing read before it is settled, or sent again, while the page moves on, and no
  // write is sent again, before the credential goes.
  signOut(then: () => void): void {
    this.client.endWrites();
    this.invalidate(true);
    then();
  }

  // From now, true until the reads are invalidated.
  readonly since = (): (() => boolean) => {
    const at = this.invalidations;
    return () => at === this.invalidations;
  };

  // A transport failure keeps the data this resource showed under the offline banner; without any,
  // it is shown as the disconnected state.
  // A refusal ends the page whichever read carried it, a superseded one too: the client already
  // dropped the credentials it refused. Any other answer of a superseded read says nothing more.
  // `what` names the shell's own reads; a page's read (none) says through `held` whether it shows data.
  // `set` is told, as its second argument, when the read failed after one that answered and the shell
  // keeps that answer (#513). True when the read found no answer and its data stays under the banner.
  settle<T>(res: ReadResult<T>, set: (r: Load<T>, kept?: boolean) => void, what: keyof Shown | undefined, live: () => boolean, held: () => boolean): boolean {
    const on = this.events();
    if (res.kind === "discarded") return false;
    if (res.kind === "unauthenticated") return (on.onUnauthenticated(res), false);
    if (res.kind === "revoked") return (on.onRevoked(res), false);
    if (!live()) return false;
    if (startRefusal(res)) on.setMode("recovery");
    const now = new Date();
    const e = observed(res, held());
    if (e !== undefined) on.observe(e, now);
    if (e === "lost") return true;
    // The process's refusal means the process restarted for recovery, its entry not yet made,
    // possibly under the same epoch: every read still in flight is dropped at once, its answer
    // discarded and its retries not sent, GET /server's own refusal included. On any other read the
    // page shows that state as GET /server's own answer, which it then reads again (§8.5 item 1),
    // and nothing read before it stays shown.
    if (res.kind === "refused" && startRefusal(res)) {
      if (what !== "server") return (this.preEntry(res), false);
      this.invalidate(true);
    }
    // An identity read that fails after one that answered keeps that answer, so the page below stays
    // mounted with its unsaved text and the failure shows above it (#513). A refusal that ends the
    // session was taken above, and a read without answer keeps its data under the banner. GET /server
    // is read again only while it has no data, so it never fails after a good read.
    const kept = what === "identity" && this.shown.identity && res.kind !== "ok";
    // Any other refusal replaces the data, so a later read without answer has nothing to keep shown.
    if (what !== undefined && !kept) this.shown[what] = res.kind === "ok";
    if (res.kind === "ok") this.readAt = now;
    if (kept) set(res, true);
    else set(res);
    return false;
  }
}

// A notice's [Show current state] (B11, W11): it opens the notice's resource, and reads it again
// when that resource is the view already shown, since a move to the same path reads nothing.
export function showCurrent(to: string, here: string, move: (to: string) => void, readAgain: () => void): void {
  if (to === here) readAgain();
  else move(to);
}

// The shell's go and show: the move asks its leave question (B11), then runs. While a refused Back or
// Forward returns, an in-app move does nothing, so it asks nothing either (#505): a question the
// operator accepted would still leave the page where it is.
export function moveFree(returning: () => boolean, leave: () => boolean, act: () => void): void {
  if (!returning() && leave()) act();
}

// A refusal that ends the page, a read's or a write's, stops every write through the shell's client
// at once (§8.2, §9.6): the effect cleanup that also disconnects it runs only after the page changed,
// and a repeat whose confirming read answers before that would still be sent.
export const ending = <T,>(writes: Writes, client: () => Client, f: (res: T) => void) => (res: T) => {
  writes.disconnect(client());
  f(res);
};

// §8.5: a new epoch reads the server and the identity again. A page that held unsaved text when the
// epoch changed stays shown meanwhile, so the operator's text is not dropped with what was read
// (ginsys/bronzeward#517 H10), also while a read fails in a way that a later read can mend: no
// answer, or a server error, the client's `unavailable` after its retries ran out included. Its
// actions stay disabled until the operator opens another page (useGate), so a read it makes under the
// new epoch lets it send nothing; the operator can copy the text out. A refusal, a start's pre-entry
// state or an incompatible server still replaces it. #513's kept identity does not cover this case:
// a new epoch forgets which reads answered, so its first identity read keeps nothing.
const mendable = (r: Load<unknown>) => r.kind === "loading" || r.kind === "transport" || r.kind === "discarded" || r.kind === "unavailable" || (r.kind === "refused" && r.status >= 500);
export const keepsPage = (kept: boolean, server: Load<Server>, identity: Load<Identity>) =>
  kept && (server.kind === "ok" ? server.data.apiRevision >= MIN_API_REVISION && identity.kind !== "ok" && mendable(identity) : mendable(server));

export function SignedIn({ auth, principal, sub, onUnauthenticated, onRevoked, onDeprecation, signOut, writes, notices = [] }: SignedInProps & { notices?: readonly string[] }) {
  // B11: a move to another page while a write is pending or unknown asks first, Back and Forward
  // included.
  const [path, move, query, returning] = usePath((to, from) => leaving(writes, to, false, from));
  const go = useCallback((to: string) => {
    moveFree(returning, () => leaving(writes, to), () => move(to));
  }, [move, returning, writes]);
  const show = useCallback((to: string) => {
    moveFree(returning, () => leaving(writes, to, true), () => showCurrent(to, location.pathname + location.search, move, () => setAgain((n) => n + 1)));
  }, [move, returning, writes]);
  const [server, setServer] = useState<Load<Server>>({ kind: "loading" });
  const [identity, setIdentity] = useState<Reading<Identity>>({ load: { kind: "loading" } });
  const [recovery, setRecovery] = useState<Load<Recovery>>({ kind: "loading" });
  const [recoveryMode, setRecoveryMode] = useState(false);
  const [mode, setMode] = useState<StartMode>("unknown");
  const [epochChanged, setEpochChanged] = useState(false);
  // The view (path and query) that held unsaved text when the epoch last changed: that page is kept
  // shown, and sends nothing, until the operator opens another (keepsPage, useGate).
  const viewKey = JSON.stringify([path, query]);
  const [keptView, keepView] = useState<string | undefined>();
  const kept = keptView === viewKey;
  const [[conn, regained], observe] = useReducer((s: [Connection, number], [e, at]: [ConnectionEvent, Date]) => regaining(s, e, at), [{}, 0]);
  const offline = offlineSince(conn);
  // Bumped to read again: back online, asked by the operator, or after a new epoch.
  const [again, setAgain] = useState(0);
  const events = useRef<ShellEvents>(undefined);
  events.current = {
    onUnauthenticated: ending(writes, () => reads.client, onUnauthenticated),
    onRevoked: ending(writes, () => reads.client, onRevoked),
    onEpochChange: () => {
      writes.setAside(EPOCH_CHANGED_ASIDE);
      setEpochChanged(true);
      keepView(unsaved.size > 0 ? viewKey : undefined);
      setServer({ kind: "loading" });
      setIdentity({ load: { kind: "loading" } });
      setRecovery({ kind: "loading" });
      setAgain((n) => n + 1);
    },
    onPreEntry: (res) => {
      setIdentity({ load: { kind: "loading" } });
      setRecovery({ kind: "loading" });
      setServer({ kind: "refused", status: res.status, problem: res.problem });
    },
    setMode,
    observe: (e, at) => observe([e, at]),
  };
  const [reads] = useState(() => new ShellReads({ ...auth, onDeprecation, onRecoveryModeChange: setRecoveryMode }, () => events.current!));
  const client = reads.client;
  // The page's writes go through this shell's client while it is shown, and a write's credential
  // refusal ends the page as a read's does (§8.2, §9.6).
  writes.client = client;
  const ended = useCallback(
    ending(writes, () => client, (e: Ending) => (e.kind === "unauthenticated" ? onUnauthenticated(e) : onRevoked(e))),
    [writes, client, onUnauthenticated, onRevoked],
  );
  writes.onEnded = ended;
  // A write's process refusal shows the state before the entry as a read's does (§8.5 item 1).
  const preEntry = useCallback((p: Problem) => reads.preEntry({ kind: "refused", status: 409, problem: p }), [reads]);
  writes.onPreEntry = preEntry;
  useEffect(() => () => {
    writes.disconnect(client);
    if (writes.onEnded === ended) writes.onEnded = undefined;
    if (writes.onPreEntry === preEntry) writes.onPreEntry = undefined;
  }, [writes, client, ended, preEntry]);
  // A navigation to another path moves focus to the new page, where the keyboard and a screen
  // reader continue; a filter change keeps it on the filter.
  const page = useRef<HTMLElement>(null);
  const shownPath = useRef(path);
  useEffect(() => {
    if (shownPath.current !== path) page.current?.focus();
    shownPath.current = path;
  }, [path]);
  // In render, before any page's effect sends a read for the new path or query: effects run child
  // first, so a page's read sent before this shell's effect would be discarded with the old ones.
  client.setView({ principal, route: path, query });

  const settle = useCallback(<T,>(res: ReadResult<T>, set: (r: Load<T>, kept?: boolean) => void, what: keyof Shown, live: () => boolean) => reads.settle(res, set, what, live, () => reads.shown[what]), [reads]);
  const settlePage = useCallback(<T,>(res: ReadResult<T>, set: (r: Load<T>) => void, live: () => boolean, held: () => boolean) => reads.settle(res, set, undefined, live, held), [reads]);

  // Reads still in flight when the shell goes (a refusal, sign-out) are discarded, and renew no more.
  useEffect(() => () => client.setView({ principal: "", route: "", query: "" }), [client]);

  // Read until it answers with data, each time the page reads again. A read a newer one or a new
  // page superseded is ignored when it completes, so it cannot replace a newer answer; so is one
  // the shell invalidated, whatever the client already answered it. A move to another path or
  // query discards it in the client, so the read starts again under the new view.
  const known = server.kind === "ok";
  // The write layer's fallback for its in-progress wait (ginsys/bronzeward#517 H14).
  if (server.kind === "ok") writes.lease = server.data.ingestionClaim?.leaseSeconds;
  useEffect(() => {
    if (known) return;
    const valid = reads.since();
    let live = true;
    const current = () => live && valid();
    void client.read<Server>("/server").then((res) => {
      const m = startModeOf(res);
      if (m !== undefined && current()) setMode(m);
      settle(res, setServer, "server", current);
    });
    return () => void (live = false);
  }, [client, reads, known, path, query, principal, again, settle]);

  // The header's identity, and the recovery banner's scopes while in recovery mode, read again on
  // every page and each time the page reads again.
  const compatible = known && server.data.apiRevision >= MIN_API_REVISION;
  useEffect(() => {
    if (!compatible) return;
    const valid = reads.since();
    let live = true;
    void client.read<Identity>("/identity").then((res) => settle(res, (r, kept) => setIdentity((p) => reading(p, r, kept === true)), "identity", () => live && valid()));
    return () => void (live = false);
  }, [client, reads, compatible, path, query, principal, again, settle]);
  useEffect(() => {
    if (!compatible || !recoveryMode) return;
    const valid = reads.since();
    let live = true;
    void client.read<Recovery>("/recovery").then((res) => settle(res, setRecovery, "recovery", () => live && valid()));
    return () => void (live = false);
  }, [client, reads, compatible, recoveryMode, path, query, again, settle]);

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

  const holding = keepsPage(kept, server, identity.load);
  // Once another page opens, the kept one is gone: going back to it opens it again.
  useEffect(() => {
    if (keptView !== undefined && !kept) keepView(undefined);
  }, [keptView, kept]);
  const failedRead = server.kind !== "ok" ? server : identity.load;

  let main: ReactNode;
  if (holding) main = <Page path={path} query={query} go={go} />;
  else if (server.kind === "loading") main = <Loading what="the server's version" />;
  // GET /server is refused this way only before a recovery start's entry (§8.5 item 1).
  else if (startRefusal(server)) main = <PreEntry res={server} onRead={() => setAgain((n) => n + 1)} />;
  else if (server.kind !== "ok") main = server.kind !== "discarded" && <Failure res={server} sub={sub} />;
  else if (!compatible) main = <Status>{incompatible(server.data.apiRevision)}</Status>;
  else main = identityMain(identity, sub, <Page path={path} query={query} go={go} />);

  const id = identity.load.kind === "ok" ? identity.load.data : undefined;
  return (
    <ShellContext value={{ roles: id?.roles ?? [], offline: offline !== undefined, kept }}>
      <a className="skip" href="#main">Skip to the page</a>
      <header>
        <h1>Bronzeward</h1>
        {id !== undefined && (
          <p>
            <span data-testid="principal">Signed in as {id.subject ?? id.service}</span> ({id.kind}).{" "}
            <span data-testid="roles">Roles: {id.roles.length > 0 ? id.roles.join(", ") : "none"}</span>.{" "}
            <span data-testid="epoch">Epoch {id.epoch}</span>
          </p>
        )}
        <button type="button" onClick={() => signingOut() && reads.signOut(signOut)}>Sign out</button>
        {recoveryMode && <RecoveryBanner recovery={recovery.kind === "ok" ? recovery.data : undefined} mode={mode} />}
        {recoveryMode && recovery.kind !== "loading" && <RecoveryRead res={recovery} />}
        {epochChanged && <Status testid="epoch-changed">{EPOCH_CHANGED}.</Status>}
        <Deprecations routes={notices} />
        <WriteNotices go={show} />
        {offline !== undefined && <Offline since={offline} readAt={reads.readAt} />}
        {id !== undefined && (
          <nav aria-label="Main">
            {PAGES.map(([to, title]) => (
              <a key={to} href={to} aria-current={current(path) === to ? "page" : undefined} onClick={(e) => follow(e, go, to)}>{title}</a>
            ))}
          </nav>
        )}
      </header>
      <ReadsProvider value={{ client, settle: settlePage, undropped: reads.since, view: JSON.stringify([path, query]), again, regained, go, failure: (res) => <Failure res={res} sub={sub} /> }}><main id="main" ref={page} tabIndex={-1}>{holding && (failedRead.kind === "loading" || failedRead.kind === "discarded" || failedRead.kind === "ok" ? <Loading what="the server and your identity under the new epoch" /> : <Failure res={failedRead} sub={sub} />)}{main}</main></ReadsProvider>
      <VersionFooter server={server.kind === "ok" ? server.data : undefined} />
    </ShellContext>
  );
}

// The root is the machine list, the hub of the journeys (§6).
const current = (path: string) => (path === "/" ? "/machines" : path);

function Page({ path, query, go }: { path: string; query: string; go: (to: string) => void }) {
  return view(path, query, go) ?? <><h2>Not found</h2><Status>No page at {path}.</Status></>;
}
