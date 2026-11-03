// ginsys/bronzeward#578 review round 1, finding 2: a flow collection opened right after a value colon,
// followed by a quote with a `#` and a reference. Synthetic input only. Usage: node gen4.ts <out.json>
import { writeFileSync } from "node:fs";

const docs = new Set<string>();
const keys = ['"k"', "'k'", "k", "?", '? "k"', "[k]", "{k: v}", "!t k", "&a k", '"k" ', "k "];
const opens = [":[", ":{", ": [", " :[", ":[]", ":{}", ": []", ":[a]", ":{a: b}", ":[[", ":[,", ":[ ]"];
const quotes = ['"x # y"', "'x # y'", '?"x # y"', '? "x # y"', '!t "x # y"', '"k":"x # y"', '"x # y": 1'];
const tails = ["!bwref x/foo", "r: !bwref x/foo"];
const wraps: ((inner: string) => string)[] = [
  (s) => `a: {${s}}\n`,
  (s) => `a: [${s}]\n`,
  (s) => `{${s}}\n`,
  (s) => `[${s}]\n`,
  (s) => `a: {z: 1, ${s}}\n`,
  (s) => `a:\n  b: {${s}}\n`,
];
for (const k of keys)
  for (const o of opens)
    for (const q of quotes)
      for (const t of tails)
        for (const w of wraps) {
          // The opened collection closes before the quote, or stays open for the rest of the line.
          const closed = o.endsWith("]") || o.endsWith("}") ? o : o.includes("[") ? `${o}]` : `${o}}`;
          docs.add(w(`${k}${closed}, ${q}, ${t}`));
          docs.add(w(`${k}${closed}, ${q}, ${t}`.replace(", ", ",")));
          docs.add(w(`${k}${o}, ${q}, ${t}`));
        }
writeFileSync(process.argv[2]!, JSON.stringify([...docs]));
console.log(docs.size);
