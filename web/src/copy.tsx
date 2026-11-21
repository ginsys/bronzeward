// One control that copies a text and says whether it did. The browser may have no clipboard (a page on
// plain http) or may refuse the write: either says so, in the sentence the caller gives for its own text.

import { useState } from "react";

export const COPY = "Copy";
export const COPIED = "Copied.";

// One copy at a time: the button is off while a write is pending, so writes end in the order they began and the
// line is always about the last one. The line says what happened to the text it names and is shown only beside that
// same text, so a reference that changes under the page (an automatic retry) never shows "Copied." for text the
// clipboard does not hold.
export function CopyButton({ text, byHand }: { text: string; byHand: string }) {
  const [pending, setPending] = useState(false);
  const [said, say] = useState<{ of: string; line: string }>();
  const copy = () => {
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (clipboard === undefined) return say({ of: text, line: byHand });
    setPending(true);
    clipboard.writeText(text).then(
      () => say({ of: text, line: COPIED }),
      () => say({ of: text, line: byHand }),
    ).finally(() => setPending(false));
  };
  return (
    <>
      <button type="button" onClick={copy} disabled={pending}>{COPY}</button>
      {said?.of === text && <span role="status"> {said.line}</span>}
    </>
  );
}
