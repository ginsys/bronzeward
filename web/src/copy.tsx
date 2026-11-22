// The one control that copies a text and says whether it did. The browser may have no clipboard (a page on
// plain http), may refuse the write, or may throw at once: each says so, in the sentence the caller gives for its own text.

import { useState } from "react";
import { COPIED, COPY } from "./words.ts";

// The line says what happened to the text it names and is shown only beside that same text, so a reference
// that changes under the page (an automatic retry) never shows "Copied" for text that was not copied.
export function CopyButton({ text, byHand }: { text: string; byHand: string }) {
  const [said, say] = useState<{ of: string; line: string }>();
  const copy = () => {
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (clipboard === undefined) return say({ of: text, line: byHand });
    // A throw and a rejection take the one path.
    void (async () => {
      try {
        await clipboard.writeText(text);
        say({ of: text, line: COPIED });
      } catch {
        say({ of: text, line: byHand });
      }
    })();
  };
  return (
    <>
      <button type="button" onClick={copy}>{COPY}</button>
      {said?.of === text && <span role="status"> {said.line}</span>}
    </>
  );
}
