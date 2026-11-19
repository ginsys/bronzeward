// How Safari's keys command sends a sequence (the override in wdio.conf.ts): the calls the driver gets, in
// order. A function of its input alone, so that safari-keys.test.ts runs it without a browser.
import { Key } from "webdriverio";

const SPECIAL = /[-]/; // WebDriver's key codes: Key.Shift, Key.Tab, ...
const graphemes = (s: string) => Array.from(new Intl.Segmenter().segment(s), (g) => g.segment);
const text = (p: string) => !SPECIAL.test(p) && graphemes(p).length === 1;

export function safariKeys(value: string | string[]): (string | string[])[] {
  // Safari's Tab skips links; its Option+Tab reaches every control, as the other browsers' Tab does.
  if (value === "Tab" || value === Key.Tab) return [["Alt", "Tab"]];
  if (Array.isArray(value) && value.length === 2 && ["Shift", Key.Shift].includes(value[0]!) && ["Tab", Key.Tab].includes(value[1]!)) return [["Shift", "Alt", "Tab"]];
  // safaridriver presses every key of one action before it releases any, so it drops a repeated character
  // and a space ("seeded" arrives as "seded"). Text goes one character at a time, a space and a line break
  // as the keys they are. A key name, a chord and a special key go as they are, as the driver's own
  // command reads them (a string that is a name of Key is one key).
  // A name of Key and a value of Key (Key.Ctrl is "WDIO_CONTROL", twelve characters) are one key.
  const parts = typeof value === "string" ? (Object.hasOwn(Key, value) || (Object.values(Key) as string[]).includes(value) ? [] : graphemes(value)) : value;
  if (parts.length > 1 && parts.every(text)) return parts.map((p) => (p === " " ? Key.Space : p === "\n" ? Key.Enter : p));
  return [value];
}
