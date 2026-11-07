// The protected import review and the claim's actions (W5, ginsys/bronzeward#341; client contracts
// §11.3 and §11.4): the review fetched on demand and held in this page's memory only, and Continue,
// Take over and Abandon as writes on the ingestion, and the marks (marks.tsx) sent from the review.

import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { NO_ASSIST } from "./drafts.tsx";
import { useSend, useWrites, Result } from "./forms.tsx";
import { type Load, short, useReads, useSettled } from "./list.tsx";
import { markSent, MarksPanel, markSpec } from "./marks.tsx";
import { Action } from "./shell.tsx";
import { Status, when } from "./states.tsx";
import type { Outcome, Write, WriteSpec } from "./write.ts";

type Ingestion = components["schemas"]["IngestionBody"];
type Verb = "continue" | "takeover" | "abandon";
const enc = encodeURIComponent;

// What an ingestion's claim lets the operator do now: the server's refusal stays authoritative, so
// this only keeps a button from offering what its state never allows (compilation §3.6, journeys §8).
// A takeover waits for the lease to lapse and is encrypted staging only (the CLI's import status).
export function offers(b: Pick<Ingestion, "state" | "mode" | "leaseUntil" | "expiresAt">, now: number): { review: boolean; verbs: Verb[] } {
  // A paused claim whose review time passed offers no review: the server may already have abandoned it.
  if (b.state === "paused") return { review: now <= Date.parse(b.expiresAt), verbs: ["continue", "abandon"] };
  if (b.state !== "held" && b.state !== "resumed") return { review: false, verbs: [] };
  const lapsed = now > Date.parse(b.leaseUntil);
  return { review: false, verbs: lapsed && b.mode !== "transient" ? ["takeover", "abandon"] : ["abandon"] };
}

// The write of one action: no body, the ingestion as the resource to read when its outcome is unknown.
export function claimSpec(id: string, verb: Verb): WriteSpec {
  const route = { continue: "continuations", takeover: "takeovers", abandon: "abandonments" }[verb];
  return { method: "POST", path: `/ingestions/${enc(id)}/${route}`, resource: `/ingestions/${enc(id)}`, body: {} };
}

const LABEL: Record<Verb, string> = { continue: "Continue import", takeover: "Take over import", abandon: "Abandon import" };
const WHAT: Record<Verb, (d: { operation?: string }) => ReactNode> = {
  continue: (d) => `The import continues in operation ${short(d.operation ?? "")}.`,
  takeover: (d) => `The import was taken over and resumes in operation ${short(d.operation ?? "")}.`,
  abandon: () => "The import was abandoned. Its staged input is discarded.",
};

const ORDER: Verb[] = ["continue", "takeover", "abandon"];

// The verbs the section shows: those offered now, and those this page sent. The read that follows an
// action moves the claim on and drops its verb from the offered ones, and the outcome of what was sent
// must stay on the page after that, also when no verb is left (Abandon).
export const shownVerbs = (offered: Verb[], sent: Verb[]): Verb[] => ORDER.filter((v) => offered.includes(v) || sent.includes(v));

// When an action's answer sends the page to read the ingestion again: its success, and a 409, which
// says the claim moved on under another operator (Continue after another Continue or an Abandon). A
// 409 is read for and never sent again.
export const rereads = (o: Outcome | undefined): boolean => o?.kind === "done" || (o?.kind === "refused" && o.status === 409);

// One read per write that ended so. `last` is the write already read for; the write to read for next
// is returned. This sends nothing.
export function rereadOnce(w: Write | undefined, last: Write | undefined, again: () => void): Write | undefined {
  if (w === undefined || w === last || !rereads(w.outcome)) return last;
  again();
  return w;
}

// An action's button, when its verb is offered, and its outcome line, as long as the page shows it.
export function ClaimView({ verb, offered, w, send }: { verb: Verb; offered: boolean; w: Write | undefined; send: () => void }) {
  return (
    <>
      {offered && <Action label={LABEL[verb]} needs={["author"]} onClick={send} busy={w !== undefined && w.outcome === undefined} />}
      <Result w={w} done={(d) => WHAT[verb](d as { operation?: string })} />
    </>
  );
}

// One action: a second click while it runs, or while its outcome is unknown, sends nothing new (B10).
// Once it is done, or refused with a 409, the ingestion is read again, and the progress watch follows
// the new claim generation. `sent` tells the section this verb was sent, so that its outcome stays.
function ClaimAction({ id, verb, offered, again, sent }: { id: string; verb: Verb; offered: boolean; again: () => void; sent: (v: Verb) => void }) {
  const [w, send] = useSend();
  const read = useRef<Write>(undefined);
  const settled = rereads(w?.outcome);
  useEffect(() => {
    read.current = rereadOnce(w, read.current, again);
  }, [w, settled, again]);
  return <ClaimView verb={verb} offered={offered} w={w} send={() => { sent(verb); send(claimSpec(id, verb)); }} />;
}

// The page reads a paused ingestion again once its review time has ended. The progress watch stops at
// the pause and has no timer for it, so nothing else would show the expiry on an idle page. The check
// is a short interval against the clock, not one timeout for the whole wait: it follows a clock
// change and has no cap on the wait. Only a paused claim before its end runs one.
export const EXPIRY_TICK = 5000;
export const watchesExpiry = (b: Pick<Ingestion, "state" | "expiresAt">, now: number): boolean => b.state === "paused" && now <= Date.parse(b.expiresAt);
export function watchExpiry(expiresAt: string, again: () => void): () => void {
  const end = Date.parse(expiresAt);
  const t = setInterval(() => {
    if (Date.now() <= end) return;
    clearInterval(t);
    again();
  }, EXPIRY_TICK);
  return () => clearInterval(t);
}

// How long a review has left, as W5 words it.
export function expiry(expiresAt: string, now: number): string {
  const left = Date.parse(expiresAt) - now;
  if (left <= 0) return `Review expired ${when(expiresAt)}. The server may already have abandoned this import: read it again.`;
  return `Review expires ${when(expiresAt)} (in ${Math.max(1, Math.round(left / 60_000))} min).`;
}

// The review's text is held in this page's memory only (§11.4 item 1): dropped on leaving the page,
// on pagehide and, since sign-out unmounts the signed-in page, on sign-out. Returns the cleanup.
export function discardOn(target: Pick<EventTarget, "addEventListener" | "removeEventListener">, discard: () => void): () => void {
  target.addEventListener("pagehide", discard);
  return () => {
    target.removeEventListener("pagehide", discard);
    discard();
  };
}

// The review's answer as the page keeps it: the document text, or why there is none.
export function reviewed(res: Load<components["schemas"]["IngestionReviewBody"]> | undefined): { text: string } | { why: Exclude<Load<unknown>, { kind: "ok" | "loading" | "discarded" }> } | undefined {
  if (res === undefined || res.kind === "loading" || res.kind === "discarded") return undefined;
  return res.kind === "ok" ? { text: res.data.document } : { why: res };
}

// Which read may still set the review: the newest one, and none after a drop. A response that was
// superseded, or that arrives after the review was closed, is dropped unread.
export function newest(): { start: () => number; drop: () => void; live: (n: number) => boolean } {
  let n = 0;
  return { start: () => ++n, drop: () => void ++n, live: (k) => k === n };
}

// The review's read. `live` goes to the shell's settle, so the result of a superseded or dropped read
// changes nothing there: its late failure shows no Offline banner and disables no action.
type ReviewBody = components["schemas"]["IngestionReviewBody"];
export const readReview = (read: <T>(path: string, live?: () => boolean) => Promise<Load<T> | undefined>, id: string, live: () => boolean) =>
  read<ReviewBody>(`/ingestions/${enc(id)}/review`, live);

// The review view (W5): read on request, `no-store`, shown read-only as served, with no copy, download
// or print action. A failed or dropped read keeps nothing, and the review is read again on request.
function Review({ b, marking }: { b: Ingestion; marking: { send: (marks: string[]) => void; busy: boolean } }) {
  const read = useSettled();
  const { failure } = useReads();
  const [text, setText] = useState<string>();
  const [why, setWhy] = useState<ReactNode>();
  const [reading, setReading] = useState(false);
  const [reads] = useState(newest);
  // Every drop of the text also drops a read in flight.
  const drop = useCallback(() => {
    reads.drop();
    setReading(false);
    setText(undefined);
  }, [reads]);
  useEffect(() => discardOn(window, drop), [drop]);
  // Its state moved on (continued, abandoned, expired): the review goes with it.
  useEffect(() => {
    if (b.state !== "paused") drop();
  }, [b.state, drop]);
  const open = useCallback(async () => {
    const mine = reads.start();
    setWhy(undefined);
    setText(undefined);
    setReading(true);
    const r = reviewed(await readReview(read, b.id, () => reads.live(mine)));
    if (!reads.live(mine)) return;
    setReading(false);
    if (r === undefined) return;
    if ("text" in r) setText(r.text);
    else setWhy(failure(r.why));
  }, [reads, read, failure, b.id]);
  return (
    <section aria-label="Protected review">
      {text === undefined ? <Action label="Review import" needs={["author"]} onClick={() => void open()} busy={reading} /> : <Shown id={b.id} text={text} close={drop} marking={marking} />}
      {why}
    </section>
  );
}

// The open review: the notice, the read-only text and the close button. Closing is a plain button, not a
// gated action: it only drops what the page holds, so no role, connection or epoch state may disable it.
export function Shown({ id, text, close, marking }: { id: string; text: string; close: () => void; marking?: { send: (marks: string[]) => void; busy: boolean } }) {
  return (
    <>
      <Status testid="review-notice">PROTECTED REVIEW. This may contain secrets nothing has recognised yet. This page stores none of it. Screen capture and copying by hand are outside its control, and browser extensions can read this page.</Status>
      <p><label>Review of import {short(id)}<br /><textarea readOnly data-testid="review-text" value={text} rows={24} cols={100} {...NO_ASSIST} /></label></p>
      {marking && <MarksPanel text={text} send={marking.send} busy={marking.busy} />}
      <p><button type="button" onClick={close}>Close review</button></p>
    </>
  );
}

// The claim's actions under the import page's state line (W5).
export function Claim(p: { b: Ingestion; again: () => void; now: number }) {
  return useWrites() === undefined ? null : <ClaimSection {...p} />;
}

// The marks are one write of the claim, not of the review: a done send moves the claim out of `paused`,
// which closes the review, and the send's notice stays (an unknown outcome is never hidden by that).
function ClaimSection({ b, again, now }: { b: Ingestion; again: () => void; now: number }) {
  const o = offers(b, now);
  const watching = watchesExpiry(b, now);
  const { expiresAt } = b;
  useEffect(() => (watching ? watchExpiry(expiresAt, again) : undefined), [watching, expiresAt, again]);
  // The verbs this page sent stay shown with their outcome, also once the read that follows has moved
  // the claim on and no verb is offered.
  const [sent, setSent] = useState<Verb[]>([]);
  const sentVerb = useCallback((v: Verb) => setSent((s) => (s.includes(v) ? s : [...s, v])), []);
  const shown = shownVerbs(o.verbs, sent);
  const [mw, msend] = useSend();
  const done = mw?.outcome?.kind === "done";
  useEffect(() => {
    if (done) again();
  }, [done, again]);
  if (!o.review && shown.length === 0 && mw === undefined) return null;
  return (
    <section aria-label="Claim">
      <p>{b.state === "paused" ? expiry(b.expiresAt, now) : null}</p>
      {o.review && <Review b={b} marking={{ send: (marks) => msend(markSpec(b.id, marks)), busy: mw !== undefined && mw.outcome === undefined }} />}
      <Result w={mw} done={markSent} />
      {shown.map((v) => <ClaimAction key={v} id={b.id} verb={v} offered={o.verbs.includes(v)} again={again} sent={sentVerb} />)}
    </section>
  );
}
