// Client contracts §11.5 item 2: every retained artifact of the browser run is scanned for the
// run's synthetic values, after a positive control shows the scan finds a planted one.
// Usage: node e2e/scan.ts <artifact dir> <synthetic value>...
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

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
const control = mkdtempSync(join(dir, "..", "scan-control-"));
try {
  for (const [i, v] of values.entries()) writeFileSync(join(control, `planted-${i}.log`), `before ${v} after`);
  const planted = scan(control, values);
  if (planted.found.length !== values.length) {
    console.error(`scan: the positive control found ${planted.found.length} of ${values.length} planted values`);
    process.exit(1);
  }
} finally {
  rmSync(control, { recursive: true, force: true });
}
const result = scan(dir, values);
if (result.files === 0) {
  console.error(`scan: ${dir} holds no artifact to scan`);
  process.exit(1);
}
if (result.found.length > 0) {
  console.error(`scan: synthetic values in retained artifacts:\n${result.found.join("\n")}`);
  process.exit(1);
}
console.log(`scan: ${result.files} artifacts clean of ${values.length} synthetic values; the positive control found each planted one`);
