// One page-level state (client contracts §9.3, §9.5-§9.7, §10.3). Every continuation that outlives
// an await (the renewal timer, a renewal's answer, a navigation after the PKCE digest) takes a mark
// before it and goes on afterwards only while the page has not moved since:
//
//   phase      entered when                                          moves to
//   starting   the page loads                                        signed-in, ended
//   signed-in  it has a pair (callback, an open tab)                 refused, changed, ended
//   refused    the server refused it: a 401 its one renewal does     ended
//              not clear (W1), or identity-revoked
//   changed    a renewal or another tab's pair names another         ended
//              account (§9.5 item 2)
//   ended      it signs out, another tab signs out, pagehide or      (none)
//              unmount
//
// refused and changed renew and redirect no more; only the operator's sign-in button leaves them,
// by a navigation of its own. Nothing at all goes on in ended.

import { renewDelay, type Session } from "./auth.ts";

export type Phase = "starting" | "signed-in" | "refused" | "changed" | "ended";

export class PageState {
  #phase: Phase = "starting";
  #moves = 0;

  get phase(): Phase {
    return this.#phase;
  }

  // False, with no move, out of refused or changed except into ended, and out of ended at all.
  to(next: Phase): boolean {
    const p = this.#phase;
    if (p === "ended" || ((p === "refused" || p === "changed") && next !== "ended")) return false;
    this.#phase = next;
    this.#moves++;
    return true;
  }

  // Taken before an await: afterwards true only while the page has not moved since.
  mark(): () => boolean {
    const at = this.#moves;
    return () => at === this.#moves;
  }
}

// A top-level navigation to the provider once the PKCE digest is done (§9.3 item 1): a first tab's
// sign-in, the sign-in button and the renewal by the redirect. It goes only if the page has not
// moved since `still` was taken; no URL means the sign-in was not begun.
export async function navigate(url: Promise<string | undefined>, still: () => boolean, assign: (url: string) => void): Promise<boolean> {
  const to = await url;
  if (to === undefined || !still()) return false;
  assign(to);
  return true;
}

export interface RenewalDeps {
  session: () => Pick<Session, "expiresAt" | "byRedirect"> | undefined;
  // B1: a renewal through the lock holder (Renewer.renew).
  renew: () => Promise<boolean>;
  holder: () => boolean;
  // §9.3 item 3: the sign-in redirect, which must still hold `still` after the PKCE digest.
  redirect: (still: () => boolean) => Promise<unknown>;
}

// §9.5 item 1: renew before less than 60 s remains, and once after a 401; a failed renewal keeps
// the current token (item 3). Only a signed-in page renews, and neither the timer nor a renewal's
// answer does anything once the page has moved.
export function renewals(page: PageState, deps: RenewalDeps) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = () => {
    clearTimeout(timer);
    const at = deps.session()?.expiresAt;
    if (page.phase !== "signed-in" || at === undefined) return;
    const still = page.mark();
    timer = setTimeout(() => {
      if (!still()) return;
      // §9.3 item 3: without a rotating refresh token the holder renews by the redirect; another
      // tab asks it, as for any renewal, and redirects itself if granted the lock while it waits.
      const byRedirect = () => deps.session()?.byRedirect === true && deps.holder();
      if (byRedirect()) return void deps.redirect(still);
      void deps.renew().then((ok) => {
        if (!still()) return;
        if (ok) schedule();
        else if (byRedirect()) void deps.redirect(still);
      });
    }, renewDelay(at, Date.now()));
  };
  return {
    schedule,
    renew: async () => {
      const still = page.mark();
      if (!(await deps.renew()) || !still()) return false;
      schedule();
      return true;
    },
    stop: () => clearTimeout(timer),
  };
}
