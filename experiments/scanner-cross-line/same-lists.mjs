// Checks that two scanners list the same references for every document with no anchor between a
// `!bwref` tag and its value.
//
// Usage: node same-lists.mjs <set.json> <scanner before> <scanner after>
//
// <set.json> is a JSON array of strings (or of objects with the stream in "doc"). Each scanner
// module exports `references(text)` as web/src/position.ts does. A document counts as anchored when
// the text holds `!bwref`, blanks, `&name` and a blank. Any other document must give the same list
// from both scanners. Issue 605 changes the reading of anchored values only. The script prints the
// counts and the ids of up to 20 documents that differ, and exits 1 if one differs.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const [setPath, beforePath, afterPath] = process.argv.slice(2);
if (afterPath === undefined) {
  console.error("usage: node same-lists.mjs <set.json> <scanner before> <scanner after>");
  process.exit(2);
}
const set = JSON.parse(readFileSync(setPath, "utf8")).map((e) => (typeof e === "string" ? e : e.doc));
const before = await import(pathToFileURL(resolve(beforePath)).href);
const after = await import(pathToFileURL(resolve(afterPath)).href);
const anchored = /!bwref[ \t\r\n]+&[0-9A-Za-z_-]+[ \t\r\n]/;
let plain = 0;
let same = 0;
const differ = [];
set.forEach((doc, id) => {
  if (anchored.test(doc)) return;
  plain++;
  if (JSON.stringify(before.references(doc)) === JSON.stringify(after.references(doc))) same++;
  else differ.push(id);
});
console.log(`documents ${set.length}, without an anchor after a tag ${plain}, same list ${same}, different ${differ.length}`);
if (differ.length > 0) console.log(`different ids: ${differ.slice(0, 20).join(",")}`);
process.exit(differ.length > 0 ? 1 : 0);
