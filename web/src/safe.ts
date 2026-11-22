// Remote text that is shown as it is (client contracts A8 rules 3 and 6). A leaf module: the states and the
// protected e2e specs import it without the API types.
import { CUT_SENTENCE, NOT_SHOWN } from "./words.ts";

// The characters that stay out of the page: a control or format character (a line feed, a bidirectional control).
// One class for a word and a sentence, so the two rules cannot drift.
const UNSAFE = "\\p{Cc}\\p{Cf}";
// The rule of an operation's cause: those characters and a long text stay out of the page.
export const SAFE = new RegExp(`^[^${UNSAFE}]{1,300}$`, "u");

// A sentence the server wrote (a refusal's title or detail), shown whole where it is safe: each unsafe character
// reads U+FFFD in its place, and a text over 1000 code points is cut and says so (A8 rule 6). A sentence is the
// thing a person must read, so it is never replaced by "(not shown)". For display only: a match on the text reads the raw one.
const SENTENCE_MAX = 1000;
export function safeSentence(s: string): string {
  const shown = s.replace(new RegExp(`[${UNSAFE}]`, "gu"), "�");
  const points = Array.from(shown);
  return points.length > SENTENCE_MAX ? `${points.slice(0, SENTENCE_MAX).join("")}${CUT_SENTENCE}` : shown;
}

// A name an author typed, shown only when it is safe text (A8 rule 3), else undefined.
export const safeText = (s: string): string | undefined => (SAFE.test(s) ? s : undefined);
// A word the server sets (a state, kind, class or reason), shown only when it is safe text, else "(not shown)"; none stays none.
export function safeWord(s: string): string;
export function safeWord(s: string | null): string | undefined;
export function safeWord(s: string | null): string | undefined {
  return s == null ? undefined : safeText(s) ?? NOT_SHOWN;
}
