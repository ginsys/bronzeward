// The protected import review and the claim's actions (W5, ginsys/bronzeward#341; client contracts
// §11.3 and §11.4): the review fetched on demand and held in this page's memory only, and Continue,
// Take over and Abandon as writes on the ingestion. The marks are the next change's.

import { type ReactNode, useCallback, useEffect, useState } from "react";
import type { components } from "./api/v1";
import { NO_ASSIST } from "./drafts.tsx";
import { useSend, useWrites, Result } from "./forms.tsx";
import { type Load, short, useReads, useSettled } from "./list.tsx";
import { Action } from "./shell.tsx";
import { Status, when } from "./states.tsx";
import type { WriteSpec } from "./write.ts";

type Ingestion = components["schemas"]["IngestionBody"];
type Verb = "continue" | "takeover" | "abandon";
const enc = encodeURIComponent;

// What an ingestion's claim lets the operator do now: the server's refusal stays authoritative, so
// this only keeps a button from offering what its state never allows (compilation §3.6, journeys §8).
// A takeover waits for the lease to lapse and is encrypted staging only (the CLI's import status).
export function offers(b: Pick<Ingestion, "state" | "mode" | "leaseUntil">, now: number): { review: boolean; verbs: Verb[] } {
  if (b.state === "paused") return { review: true, verbs: ["continue", "abandon"] };
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

// One action: a second click while it runs, or while its outcome is unknown, sends nothing new (B10).
// Once it is done the ingestion is read again, and the progress watch follows the new claim generation.
function ClaimAction({ id, verb, again }: { id: string; verb: Verb; again: () => void }) {
  const [w, send] = useSend();
  const done = w?.outcome?.kind === "done";
  useEffect(() => {
    if (done) again();
  }, [done, again]);
  return (
    <>
      <Action label={LABEL[verb]} needs={["author"]} onClick={() => send(claimSpec(id, verb))} busy={w !== undefined && w.outcome === undefined} />
      <Result w={w} done={(d) => WHAT[verb](d as { operation?: string })} />
    </>
  );
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

// The review view (W5): read on request, `no-store`, shown read-only as served, with no copy, download
// or print action. A failed or dropped read keeps nothing, and the review is read again on request.
function Review({ b }: { b: Ingestion }) {
  const read = useSettled();
  const { failure } = useReads();
  const [text, setText] = useState<string>();
  const [why, setWhy] = useState<ReactNode>();
  useEffect(() => discardOn(window, () => setText(undefined)), []);
  // Its state moved on (continued, abandoned, expired): the review goes with it.
  useEffect(() => {
    if (b.state !== "paused") setText(undefined);
  }, [b.state]);
  const open = useCallback(async () => {
    setWhy(undefined);
    const r = reviewed(await read<components["schemas"]["IngestionReviewBody"]>(`/ingestions/${enc(b.id)}/review`));
    if (r === undefined) return;
    if ("text" in r) setText(r.text);
    else setWhy(failure(r.why));
  }, [read, failure, b.id]);
  return (
    <section aria-label="Protected review">
      {text === undefined ? <Action label="Review import" needs={["author"]} onClick={() => void open()} /> : (
        <>
          <Status testid="review-notice">PROTECTED REVIEW. This may contain secrets nothing has recognised yet. This page stores none of it. Screen capture and copying by hand are outside its control, and browser extensions can read this page.</Status>
          <p><label>Review of import {short(b.id)}<br /><textarea readOnly data-testid="review-text" value={text} rows={24} cols={100} {...NO_ASSIST} /></label></p>
          <p><Action label="Close review" needs={[]} onClick={() => setText(undefined)} /></p>
        </>
      )}
      {why}
    </section>
  );
}

// The claim's actions under the import page's state line (W5).
export function Claim({ b, again, now }: { b: Ingestion; again: () => void; now: number }) {
  const o = offers(b, now);
  const writes = useWrites();
  if (writes === undefined || (!o.review && o.verbs.length === 0)) return null;
  return (
    <section aria-label="Claim">
      <p>{b.state === "paused" ? expiry(b.expiresAt, now) : null}</p>
      {o.review && <Review b={b} />}
      {o.verbs.map((v) => <ClaimAction key={v} id={b.id} verb={v} again={again} />)}
    </section>
  );
}
