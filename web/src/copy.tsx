// One control that copies a text and says whether it did. The browser may have no clipboard (a page on
// plain http) or may refuse the write: either says so, in the sentence the caller gives for its own text.

import { useState } from "react";

export const COPY = "Copy";
export const COPIED = "Copied.";
export const WRITE_WAIT_MS = 5000;

// One copy at a time: the button is off while a write is pending, so writes end in the order they began and the
// line is always about the last one. The line says what happened to the text it names and is shown only beside that
// same text, so a reference that changes under the page (an automatic retry) never shows "Copied." for text the
// clipboard does not hold.
export function CopyButton({ text, byHand }: { text: string; byHand: string }) {
  const [pending, setPending] = useState(false);
  const [said, say] = useState<{ of: string; line: string }>();
  const copy = async () => {
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (clipboard === undefined) return say({ of: text, line: byHand });
    setPending(true);
    // A write that throws before it returns a promise, or never settles (a permission prompt nobody answers),
    // is a refused write like any other: after WRITE_WAIT_MS the by-hand sentence shows and the button is back.
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      await Promise.race([
        Promise.resolve().then(() => clipboard.writeText(text)),
        new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error("timeout")), WRITE_WAIT_MS); }),
      ]);
      say({ of: text, line: COPIED });
    } catch {
      say({ of: text, line: byHand });
    } finally {
      clearTimeout(timer);
      setPending(false);
    }
  };
  return (
    <>
      <button type="button" onClick={copy} disabled={pending}>{COPY}</button>
      {said?.of === text && <span role="status"> {said.line}</span>}
    </>
  );
}
