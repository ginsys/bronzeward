// ginsys/bronzeward#616: the line after a plain `!bwref` value on a root scalar, and the controls for it.
// A deterministic cross product, no random choice. Synthetic input only. Usage: node gen-s10-root-scalar.ts <out.json>
// It writes a JSON array of {name, doc} for the go-yaml oracle. The cross product is:
//   context   : 12 places for the tag. The root contexts have no key before the tag: the line of `---`
//               ("marker"), the first text of a stream ("bare"), the line after a line that holds only `---`
//               ("marker-own-line"), after a comment line ("comment-above"), after a directive and the marker on
//               its own line or on the tag's line ("directive-marker", "directive-marker-inline"), and in a second
//               document, after `...` or after `---` ("doc2-dots", "doc2-dots-marker", "doc2-marker-own-line",
//               "doc2-marker-inline"). Two controls have a parent: "key" (`k: `) and "key-above" (`k:` on the line
//               above), whose holder is a key column.
//   properties: the tag only, an anchor and then the tag, or the tag and then an anchor;
//   separator : a space or a tab between the marker, the properties and the value;
//   next line : none, blank, column 0, column 1, U+00A0 first, tab first;
//   text      : "bar", "- y", "y: v" (what the next line says).
// A blank next line is followed by the text at column 0, so that it is a blank and then a text line. With no
// next line the text is only "bar". The go-yaml oracle decides what each document lists. The set is not in
// sets/MANIFEST and run/gen-sets does not write it: it is made on demand, and the same input gives the same file.
import { writeFileSync } from "node:fs";

const out = process.argv[2];
if (!out) throw new Error("usage: node gen-s10-root-scalar.ts <out.json>");

// Each context is [name, text before the first line of the value, text before the tag on its line].
const contexts: [string, string, (sep: string) => string][] = [
  ["marker", "", (sep) => `---${sep}`],
  ["bare", "", () => ""],
  ["key", "", () => "k: "],
  ["marker-own-line", "---\n", () => ""],
  ["comment-above", "# c\n", () => ""],
  ["directive-marker", "%YAML 1.1\n---\n", () => ""],
  ["directive-marker-inline", "%YAML 1.1\n", (sep) => `---${sep}`],
  ["doc2-dots", "a: 1\n...\n", () => ""],
  ["doc2-dots-marker", "a: 1\n...\n", (sep) => `---${sep}`],
  ["doc2-marker-own-line", "a: 1\n---\n", () => ""],
  ["doc2-marker-inline", "a: 1\n", (sep) => `---${sep}`],
  ["key-above", "k:\n", () => "  "],
];
const props: [string, (sep: string) => string][] = [
  ["tag", (sep) => `!bwref${sep}`],
  ["anchor-tag", (sep) => `&e${sep}!bwref${sep}`],
  ["tag-anchor", (sep) => `!bwref${sep}&e${sep}`],
];
const seps: [string, string][] = [
  ["space", " "],
  ["tab", "\t"],
];
const texts: [string, string][] = [
  ["word", "bar"],
  ["dash", "- y"],
  ["key", "y: v"],
];
const nexts: [string, (t: string) => string][] = [
  ["eof", () => ""],
  ["blank", (t) => `\n${t}`],
  ["col0", (t) => t],
  ["col1", (t) => ` ${t}`],
  ["nbsp", (t) => `\u00a0${t}`],
  ["tab", (t) => `\t${t}`],
];

const docs: { name: string; doc: string }[] = [];
for (const [cn, head, c] of contexts)
  for (const [pn, p] of props)
    for (const [sn, sep] of seps)
      for (const [nn, n] of nexts)
        for (const [tn, t] of texts) {
          if (nn === "eof" && tn !== "word") continue;
          const first = `${c(sep)}${p(sep)}x/foo`;
          docs.push({ name: [cn, pn, sn, nn, tn].join("."), doc: head + (nn === "eof" ? `${first}\n` : `${first}\n${n(t)}\n`) });
        }
writeFileSync(out, JSON.stringify(docs));
console.log(`${docs.length} documents written to ${out}`);
