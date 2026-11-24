// What the keyboard-only case of the operation page (auth/operation.e2e.ts) adds to a failure. A focus on the body, or
// one control too few reached, has two causes the message alone cannot tell apart: the page was mounted anew by a new
// epoch (the marks the case put on the controls are gone, and the epoch moved), or it was not (marks and epoch are
// as they were) and one control was replaced by a new element while its data arrived (changesText names it).
// Functions of their input alone, so that focus-context.test.ts runs them without a browser.

// The epoch the recovery banner names ("... epoch ep_<id> ..."), or "none": the page's own text, no new hook.
export function bannerEpoch(banner: string | null | undefined): string {
  return /epoch (ep_[a-z0-9]+)/.exec(banner ?? "")?.[1] ?? "none";
}

// The epoch read from the page by `read`, "unread" where the read itself failed (a lost session, a navigation): a read
// made to explain a failure must never be the failure, so it never throws.
export async function epochRead(read: () => Promise<string | null | undefined>): Promise<string> {
  try {
    return bannerEpoch(await read());
  } catch {
    return "unread";
  }
}

// The failure with the count of marked controls now against the count marked, and the epoch at the marking against now.
// Counts and the synthetic epoch id only: nothing the failure did not already print.
export function withFocusContext(error: unknown, marks: number | "unread", total: number, epochThen: string, epochNow: string, detail = ""): Error {
  const text = error instanceof Error ? error.message : String(error);
  return new Error(`${text}; [data-t9] marks ${marks} of ${total}; epoch at marking ${epochThen}, now ${epochNow}${detail === "" ? "" : `; ${detail}`}`, { cause: error });
}

// withFocusContext after reading the marks and the banner now, and `detail` (changesText of what the page changed). A read
// that fails reads "unread" (the detail is then left out), and the original failure is the message's start and the `cause`
// either way.
export async function failedWith(
  error: unknown, marksNow: () => Promise<number>, bannerNow: () => Promise<string | null | undefined>, total: number, epochThen: string,
  detail?: () => Promise<string>,
): Promise<Error> {
  const marks = await marksNow().catch(() => "unread" as const);
  const epochNow = await epochRead(bannerNow);
  return withFocusContext(error, marks, total, epochThen, epochNow, await detail?.().catch(() => ""));
}

// A change to the page's controls after the marking, as the page's MutationObserver saw it: a marked control that left
// the page (its mark, and what it was), or a control that joined it (it has no mark).
export interface Change { kind: "removed" | "added"; ms: number; mark: string | null; tag: string; href: string | null; text: string }

const SHOWN = 8;
const node = (c: Change) => `${c.tag} ${c.href ?? "-"} "${c.text}"`;

// What those changes say about a focus that fell to the body: which marked control held focus last, which marked
// controls left, and which control that joined has the href of one that left (the same link made again, so a
// replacement and not a removal). Synthetic ids and link text only; at most SHOWN changes are named.
export function changesText(changes: readonly Change[], lastFocus: number | null): string {
  const head = `last focus on ${lastFocus === null ? "no control" : `control ${lastFocus}`}`;
  if (changes.length === 0) return `${head}; no marked control removed and no control added since the marking`;
  const parts = changes.slice(0, SHOWN).map((c) => {
    if (c.kind === "removed") return `control ${c.mark} (${node(c)}) removed at ${c.ms} ms${String(lastFocus) === c.mark ? ", held focus last" : ""}`;
    const same = c.href === null ? undefined : changes.find((r) => r.kind === "removed" && r.href === c.href);
    return `added at ${c.ms} ms (${node(c)})${same === undefined ? "" : `, the href of removed control ${same.mark}`}`;
  });
  return `${head}; ${[...parts, ...(changes.length > SHOWN ? [`and ${changes.length - SHOWN} more`] : [])].join("; ")}`;
}
