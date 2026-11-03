// Generates the three families of the narrow plan (ginsys/bronzeward#578 plan v2/v3) as a JSON list of documents.
// Usage: node gen3.ts <out.json>
import { writeFileSync } from "node:fs";

const docs = new Set<string>();
const tail = 'b: ?"y # !bwref ghost\nc: !bwref x/foo\n';

// A. Block scalar header family.
for (const h of ["|", ">"])
  for (const ind of ["", "1", "2", "4"])
    for (const ch of ["", "-", "+"])
      for (const order of ind !== "" && ch !== "" ? [0, 1] : [0]) {
        const mods = order === 0 ? ind + ch : ch + ind;
        // The last three comments hold a digit: it is no indentation indicator (ginsys/bronzeward#601).
        for (const cm of ["", " # c", "# c", " # c: [?\"x", "# c: [x", " # c: [x", " # 4", "# 2", " # c 1-"])
          for (const place of ["inline", "own", "own0"])
            for (const body of ["  text\n", "    text\n  [x\n", "  [x\n", "  {x\n", '  ?"y\n', "  p: [x\n", "    first\n  b: [x\n", "[x\n", "{x\n", ""]) {
              const head = place === "inline" ? `a: ${h}${mods}${cm}\n` : place === "own" ? `a:\n  ${h}${mods}${cm}\n` : `a:\n${h}${mods}${cm}\n`;
              docs.add(head + body + tail);
              docs.add(head + body + "d: [x\n" + tail);
            }
      }

// B. Bare scalar family.
for (const pre of ["a:\n", "a: x\n", "a:\n    x\n", "a: x\n  y\n", "- a\n", "a:\n  - x\n", "a: |\n  t\n", "? k\n", "? k\n:\n"])
  for (const line of ['[x', '{x', '- [x', '? [x', '?"y', '[?"y # !bwref ghost', '{? "q # !bwref ghost": 1', '"y # !bwref ghost'])
    for (const ind of ["", "  ", "    ", "      "])
      for (const post of ["", "  [y\n", "[y\n", "  z\n"]) docs.add(`${pre}${ind}${line}\n${post}${tail}`);

// C. Multi-line flow and column-0 family.
const real = "!bwref x/foo";
for (const d of [
  `a: [x,\n  b, "y # z", ${real}]\n`,
  `a: [x,\n  ? "q # r", ${real}]\n`,
  `a: {k: 1,\n  ? "q # r": 1, b: ${real}}\n`,
  `a: [x,\n  "q # r", ${real}]\n`,
  `a: [\n  b, "y # z", ${real}]\n`,
  `[? "x # y", ${real}]\n`,
  `{? "x # y": 1, b: ${real}}\n`,
  `--- [? "x # y", ${real}]\n`,
  `---\n[? "x # y", ${real}]\n`,
  `---\n{? "x # y": 1, b: ${real}}\n`,
  `a: [? "x # y", ${real}]\n`,
  `a: {? "x # y": 1, b: ${real}}\n`,
  `- [? "x # y", ${real}]\n`,
  `- a: [? "x # y", ${real}]\n`,
  `x\n[?"y # !bwref ghost\nb: ${real}\n`,
  `x\n{?"y # !bwref ghost\nb: ${real}\n`,
  `x\n[?"y # !bwref ghost\n${real}\n`,
  `--- |\n[?"y # !bwref ghost\n---\nb: ${real}\n`,
  `--- >\n{?"y # !bwref ghost\n---\nb: ${real}\n`,
  `--- |\n[x\n${tail}`,
  `a:\n[?"y # !bwref ghost\nb: ${real}\n`,
  `a: x\n[?"y # !bwref ghost\nb: ${real}\n`,
  `a:\n[? "y # q", ${real}]\n`,
  `a: |\n  t\n[x\n${tail}`,
  `"a":\n[x\n${tail}`,
  `? [x\n: ${real}\n`,
  `? [? "x # y", ${real}]\n: v\n`,
  `a: &x [? "q # r", ${real}]\n`,
  `a: !t [? "q # r", ${real}]\n`,
  `a: [!t "q # r", ${real}]\n`,
  `a: x, [?"y # !bwref ghost\nb: ${real}\n`,
  `a: x [?"y # !bwref ghost\nb: ${real}\n`,
  `a: x: [?"y # !bwref ghost\nb: ${real}\n`,
]) docs.add(d);

writeFileSync(process.argv[2]!, JSON.stringify([...docs]));
console.log(docs.size);
