// Generates the ginsys/bronzeward#560 differential sweep: each indicator and node-property form right
// before a quote, in block, flow-sequence and flow-map context, with a reference on the next line.
// Synthetic input only. Usage: node gen.ts <docs.json>
import { writeFileSync } from "node:fs";

const forms = [
  "", "x", "!", "!t", "!!str", "!<tag:yaml.org,2002:str>", "!foo,", "!a]", "!a}", "&a", "*a",
  "?", ":", "-", ",", "[", "]", "{", "}", "#", "|", ">", "%", "@", "`",
];
const seps = ["", " "];
const pres = ["", "foo ", "x, ", "!t ", "&a ", "&a !t "];
const quotes = ['"', "'"];
const bodies = (q: string) => ["bar", `x # y${q}`];
const templates = [
  (s: string) => `a: [${s},\n  !bwref x/foo # c\n]\n`,
  (s: string) => `a: [x,\n  ${s},\n  !bwref x/foo # c\n]\n`,
  (s: string) => `a: {k: ${s},\n  b: !bwref x/foo # c\n}\n`,
  (s: string) => `a: {${s}: 1,\n  b: !bwref x/foo # c\n}\n`,
  (s: string) => `a: ${s}\nb: !bwref x/foo # c\n`,
  (s: string) => `- ${s}\n- !bwref x/foo # c\n`,
  (s: string) => `x:\n  a: ${s}\n  b: !bwref x/foo # c\n`,
];

const docs = new Set<string>();
for (const f of forms)
  for (const sep of seps)
    for (const pre of pres)
      for (const q of quotes)
        for (const body of bodies(q))
          for (const t of templates) docs.add(t(`${pre}${f}${sep}${q}${body}`));
writeFileSync(process.argv[2]!, JSON.stringify([...docs]));
console.log(`docs=${docs.size}`);
