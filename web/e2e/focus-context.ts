// What the keyboard-only case of the operation page (auth/operation.e2e.ts) adds to a failure. A focus on the body, or
// one control too few reached, has two causes the message alone cannot tell apart: the page was mounted anew by a new
// epoch (the marks the case put on the controls are gone, and the epoch moved), or it was not (marks and epoch are
// as they were). Functions of their input alone, so that focus-context.test.ts runs them without a browser.

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
export function withFocusContext(error: unknown, marks: number | "unread", total: number, epochThen: string, epochNow: string): Error {
  const text = error instanceof Error ? error.message : String(error);
  return new Error(`${text}; [data-t9] marks ${marks} of ${total}; epoch at marking ${epochThen}, now ${epochNow}`, { cause: error });
}

// withFocusContext after reading the marks and the banner now. A read that fails reads "unread", and the original
// failure is the message's start and the `cause` either way.
export async function failedWith(
  error: unknown, marksNow: () => Promise<number>, bannerNow: () => Promise<string | null | undefined>, total: number, epochThen: string,
): Promise<Error> {
  const marks = await marksNow().catch(() => "unread" as const);
  return withFocusContext(error, marks, total, epochThen, await epochRead(bannerNow));
}
