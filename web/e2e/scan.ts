// Client contracts §11.5 item 2: every retained artifact of the browser run is scanned for the
// run's synthetic values. A positive control planted in a nested directory inside the artifact
// directory must be found by the same walk, and is removed before anything is uploaded.
// Usage: node e2e/scan.ts <artifact dir> <synthetic value>...
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, sep } from "node:path";

function scan(dir: string, values: string[]): { files: number; found: string[] } {
  const found: string[] = [];
  let files = 0;
  for (const entry of readdirSync(dir, { recursive: true, withFileTypes: true })) {
    if (!entry.isFile()) continue;
    files++;
    const path = join(entry.parentPath, entry.name);
    const bytes = readFileSync(path);
    // The value's position, never the value itself, goes to the log.
    for (const [i, v] of values.entries()) {
      if (bytes.includes(v) || bytes.includes(Buffer.from(v, "utf16le"))) found.push(`${path}: value ${i + 1}`);
    }
  }
  return { files, found };
}

const [dir, ...values] = process.argv.slice(2);
if (dir === undefined || values.length === 0 || values.some((v) => v.length < 8)) {
  console.error("usage: node e2e/scan.ts <artifact dir> <synthetic value of 8 or more characters>...");
  process.exit(2);
}
const control = mkdtempSync(join(dir, "scan-control-"));
let result: { files: number; found: string[] };
try {
  mkdirSync(join(control, "nested"));
  for (const [i, v] of values.entries()) writeFileSync(join(control, "nested", `planted-${i}.log`), `before ${v} after`);
  result = scan(dir, values);
} finally {
  rmSync(control, { recursive: true, force: true });
}
const planted = result.found.filter((f) => f.startsWith(control + sep));
const leaks = result.found.filter((f) => !f.startsWith(control + sep));
const files = result.files - values.length;
if (planted.length !== values.length) {
  console.error(`scan: the positive control found ${planted.length} of ${values.length} planted values`);
  process.exitCode = 1;
} else if (files === 0) {
  console.error(`scan: ${dir} holds no artifact to scan`);
  process.exitCode = 1;
} else if (leaks.length > 0) {
  console.error(`scan: synthetic values in retained artifacts:\n${leaks.join("\n")}`);
  process.exitCode = 1;
} else {
  console.log(`scan: ${files} artifacts clean of ${values.length} synthetic values; the positive control found each planted one`);
}
