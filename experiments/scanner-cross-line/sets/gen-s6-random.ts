// Held-out sweep for ginsys/bronzeward#598: random documents of two to six lines, each line an
// indentation, a key or an indicator, and a value drawn from a pool of YAML forms (quotes with and
// without a closing quote, flow openers and closers, block scalar headers, node properties, plain
// text, real and ghost references). The pool and the scanner under test were written apart: no
// candidate was tuned on this set. Most documents are not valid YAML, and go-yaml's answer
// settles which ones count. Synthetic input only. The generator is seeded, so the set is the same
// on every run. Usage: node gen-s6-random.ts <docs.json> [count]
import { writeFileSync } from "node:fs";

// mulberry32, seeded with the issue number.
let state = 598;
const rnd = () => {
  state = (state + 0x6d2b79f5) | 0;
  let t = Math.imul(state ^ (state >>> 15), 1 | state);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = <T>(xs: readonly T[]): T => xs[Math.floor(rnd() * xs.length)]!;

const indents = ["", "", "  ", "  ", "    ", " "];
const lead = ["", "", "", "a: ", "b: ", "- ", "- a: ", "? ", ": ", "- - ", "\"k\": ", "'k': ", "x ", "[", "{", "[a, ", "{k: ", "- ? "];
const value = [
  "x", "x y", "!bwref x/foo", "!bwref \"x/foo\"", "!bwref 'x/foo'", "!bwref x/foo # c", "!t !bwref x/foo", "&a !bwref x/foo",
  "\"q # r\"", "'q # r'", "\"q # r", "'q # r", "q\" # r", "\"\"", "''", "\"a\\\" # b\"", "'a'' # b'",
  "\"y # !bwref ghost", "'y # !bwref ghost", "x # !bwref ghost", "# !bwref ghost", "\"z\" # !bwref ghost",
  "[", "[x, ", "{k: ", "{", "[\"q # r\", !bwref x/foo]", "{\"k\": !bwref x/foo}", "[?\"y # !bwref ghost",
  "]", "}", ",", "[x]", "{k: v}", "x]", "x}", "x,", "x, ",
  "|", ">", "|-", ">+", "|2", "|1-", "|# c", "| # c",
  "!t", "!t \"q # r\"", "&a \"q # r\"", "!!str", "*a",
  "?\"y # !bwref ghost", "-", "? ", ": x", "x: y", "\"k\": !bwref x/foo", "k: !bwref x/foo, ",
  "---", "...", "--- [x]", "--- |", "--- \"q # r\"",
];

const count = Number(process.argv[3] ?? 100000);
const docs = new Set<string>();
for (let n = 0; n < count; n++) {
  const lines: string[] = [];
  const size = 2 + Math.floor(rnd() * 5);
  for (let k = 0; k < size; k++) lines.push(`${pick(indents)}${pick(lead)}${pick(value)}`);
  docs.add(`${lines.join("\n")}\n`);
}
writeFileSync(process.argv[2]!, JSON.stringify([...docs]));
console.log(`docs=${docs.size}`);
