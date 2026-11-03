// ginsys/bronzeward#578 second sweep: the line context the flow depth carries through. Each
// document is a context (lines before) + a probe line at an indentation + an optional tail.
// Synthetic input only. Usage: node gen2.ts <docs.json>
import { writeFileSync } from "node:fs";

const contexts = [
  "",
  "a: x\n",
  "a: x\n  y\n",
  "a: |\n  [x\n",
  "a: |\n  [x\n  ]\n",
  "a: |#c\n  [x\n",
  "a: !t >-\n  {x\n",
  "- run: |\n    echo\n",
  "- run: |\n    [echo\n",
  'a: "x\n  [y"\n',
  "a: 'x\n  {y'\n",
  "- a: 1\n",
  "- x\n",
  "- - x\n",
  "? k\n",
  "---\n",
  "--- [x]\n",
  "a: [x,\n  y]\n",
  "a: {x: 1,\n  y: 2}\n",
  "a:\n",
  "- \n",
  "a: !t\n",
  "a: x,[y\n",
  "a: x # [c\n",
  "a: 'x' # {c\n",
];
const indents = ["", "  ", "    "];
const probes = [
  'b: ?"z # !bwref ghost',
  'b: [ "q # r", !bwref x/foo]',
  '[ "q # r", !bwref x/foo]',
  '"b # c": !bwref x/foo',
  '- "q # r"',
  'with: [a,\n  b, "c # d", !bwref x/foo]',
  'b: {"k":!t "x # y", r: !bwref x/foo}',
  'b: {"k" :"x # y", r: !bwref x/foo}',
  'b: {[k]:"x # y", r: !bwref x/foo}',
  'b: {"k":&a "x # y", r: !bwref x/foo}',
  'b: [? "x # y", !bwref x/foo]',
  'b: {?!t "x # y": 1, r: !bwref x/foo}',
  'x ?"p: ["q # r", !bwref x/foo]',
  '"y # !bwref ghost',
  "[y\nb: ?\"z # !bwref ghost",
  "{y\nb: ?\"z # !bwref ghost",
  "b: !bwref x/foo",
  "--- [\"a # b\", !bwref x/foo]",
  "--- {\"k # c\": !bwref x/foo}",
  "... [\"a # b\", !bwref x/foo]",
];
const tails = ["", "c: !bwref x/end\n"];

const docs = new Set<string>();
for (const c of contexts)
  for (const ind of indents)
    for (const p of probes)
      for (const t of tails) {
        const probe = p.split("\n").map((l) => ind + l).join("\n");
        docs.add(`${c}${probe}\n${t}`);
      }
writeFileSync(process.argv[2]!, JSON.stringify([...docs]));
console.log(`docs=${docs.size}`);
