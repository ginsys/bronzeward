// One control that copies a text and says whether it did. The browser may have no clipboard (a page on
// plain http) or may refuse the write: either says so, in the sentence the caller gives for its own text.

import { useRef, useState } from "react";

export const COPY = "Copy";
export const COPIED = "Copied.";

export function CopyButton({ text, byHand }: { text: string; byHand: string }) {
  // What was said belongs to the text it was said about: a reference that changes under the page (an automatic
  // retry) leaves no "Copied." for text the clipboard does not hold. Only the latest copy may say anything, so a
  // slow copy of an older text, whether it succeeds or fails, never replaces or hides the newer one's line.
  const [said, say] = useState<{ of: string; line: string }>();
  const latest = useRef(0);
  const copy = () => {
    const attempt = ++latest.current;
    const speak = (line: string) => {
      if (latest.current === attempt) say({ of: text, line });
    };
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (clipboard === undefined) return speak(byHand);
    clipboard.writeText(text).then(() => speak(COPIED), () => speak(byHand));
  };
  return (
    <>
      <button type="button" onClick={copy}>{COPY}</button>
      {said?.of === text && <span role="status"> {said.line}</span>}
    </>
  );
}
