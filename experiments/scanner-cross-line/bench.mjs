// What one call costs on a large draft: the time of references(text) per candidate and document size.
// A draft is read again on every edit, so this is the cost per keystroke.
//
// Usage: node bench.mjs <candidate module> [<candidate module> ...]
//
// The document is synthetic: a list of steps, each with plain, quoted, flow and block scalar
// values, a comment, and one !bwref every ten steps. Valid YAML; it is the same on every run.
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const step = (i) =>
  [
    `- name: step ${i} # a comment, with "quotes" and 'apostrophes'`,
    `  with: [a, b, "c # d", { k: ${i % 10 === 0 ? "!bwref x/ref-" + i : "v"} }]`,
    `  run: |`,
    `    echo "line one"`,
    `    echo '# not a comment' [x`,
    `  text: "a quoted value`,
    `    on two lines"`,
    `  plain: a plain value`,
    `    on two lines`,
  ].join("\n");
const document = (steps) => Array.from({ length: steps }, (_, i) => step(i)).join("\n") + "\n";

for (const path of process.argv.slice(2)) {
  const { references } = await import(pathToFileURL(resolve(path)).href);
  for (const steps of [10, 100, 1000]) {
    const text = document(steps);
    references(text);
    const runs = steps >= 1000 ? 5 : 20;
    const started = performance.now();
    let found = 0;
    for (let i = 0; i < runs; i++) found = references(text).length;
    const ms = (performance.now() - started) / runs;
    console.log(`${path.split("/").pop()}: ${text.split("\n").length - 1} lines, ${text.length} bytes, ${found} references, ${ms.toFixed(2)} ms per call`);
  }
}
