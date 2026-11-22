// Remote text that is shown as it is (client contracts A8 rules 3 and 6). A leaf module: the states and the
// protected e2e specs import it without the API types.
import { NOT_SHOWN } from "./words.ts";

// The rule of an operation's cause: a control or format character (a line feed, a bidirectional control) and
// a long text stay out of the page.
export const SAFE = /^[^\p{Cc}\p{Cf}]{1,300}$/u;

// A name an author typed, shown only when it is safe text (A8 rule 3), else undefined.
export const safeText = (s: string): string | undefined => (SAFE.test(s) ? s : undefined);
// A word the server sets (a state, kind, class or reason), shown only when it is safe text, else "(not shown)"; none stays none.
export function safeWord(s: string): string;
export function safeWord(s: string | null): string | undefined;
export function safeWord(s: string | null): string | undefined {
  return s == null ? undefined : safeText(s) ?? NOT_SHOWN;
}
