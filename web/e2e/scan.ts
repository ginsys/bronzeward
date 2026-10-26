// Client contracts §11.5 item 2: every retained artifact of the browser run is scanned for the
// run's synthetic values. A positive control planted in a nested directory inside the artifact
// directory must be found by the same walk, and is removed before anything is uploaded. Any
// JWT-shaped text is a finding too: the AUTH cases' tokens are minted during the run.
// Usage: node e2e/scan.ts <artifact dir> <synthetic value | @file holding one>...
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, sep } from "node:path";

const JWT = /eyJ[\w-]{8,}\.eyJ[\w-]{8,}\./;
// Built at run time, so the source holds no JWT-shaped literal for a secret scanner to flag.
const part = (claims: object): string => Buffer.from(JSON.stringify(claims)).toString("base64url");
const PLANTED_JWT = `${part({ alg: "none" })}.${part({ sub: "planted" })}.`;

function scan(dir: string, values: string[]): { files: number; found: string[]; unscannable: string[] } {
  const found: string[] = [];
  const unscannable: string[] = [];
  let files = 0;
  for (const entry of readdirSync(dir, { recursive: true, withFileTypes: true })) {
    if (entry.isDirectory()) continue;
    // The upload follows symlinks, so a link (or any other non-regular entry) would ship bytes
    // this walk never read: it fails the scan instead of being skipped.
    if (!entry.isFile()) {
      unscannable.push(join(entry.parentPath, entry.name));
      continue;
    }
    files++;
    const path = join(entry.parentPath, entry.name);
    const bytes = readFileSync(path);
    // The value's position, never the value itself, goes to the log.
    for (const [i, v] of values.entries()) {
      if (bytes.includes(v) || bytes.includes(Buffer.from(v, "utf16le"))) found.push(`${path}: value ${i + 1}`);
    }
    if (JWT.test(bytes.toString("latin1")) || JWT.test(bytes.toString("utf16le"))) found.push(`${path}: a JWT`);
  }
  return { files, found, unscannable };
}

const [dir, ...args] = process.argv.slice(2);
// A value from a file stays out of the process's arguments.
const values = args.map((a) => (a.startsWith("@") ? readFileSync(a.slice(1), "utf8").trim() : a));
if (dir === undefined || values.length === 0 || values.some((v) => v.length < 8)) {
  console.error("usage: node e2e/scan.ts <artifact dir> <synthetic value of 8 or more characters>...");
  process.exit(2);
}
const control = mkdtempSync(join(dir, "scan-control-"));
let result: ReturnType<typeof scan>;
try {
  mkdirSync(join(control, "nested"));
  for (const [i, v] of values.entries()) writeFileSync(join(control, "nested", `planted-${i}.log`), `before ${v} after`);
  writeFileSync(join(control, "nested", "planted-jwt.log"), `before ${PLANTED_JWT}sig after`);
  result = scan(dir, values);
} finally {
  rmSync(control, { recursive: true, force: true });
}
const planted = result.found.filter((f) => f.startsWith(control + sep));
const leaks = result.found.filter((f) => !f.startsWith(control + sep));
const files = result.files - values.length - 1;
if (planted.length !== values.length + 1) {
  console.error(`scan: the positive control found ${planted.length} of ${values.length + 1} planted values and JWTs`);
  process.exitCode = 1;
} else if (result.unscannable.length > 0) {
  console.error(`scan: entries that are not regular files cannot be scanned:\n${result.unscannable.join("\n")}`);
  process.exitCode = 1;
} else if (files === 0) {
  console.error(`scan: ${dir} holds no artifact to scan`);
  process.exitCode = 1;
} else if (leaks.length > 0) {
  console.error(`scan: synthetic values in retained artifacts:\n${leaks.join("\n")}`);
  process.exitCode = 1;
} else {
  console.log(`scan: ${files} artifacts clean of ${values.length} synthetic values and of JWTs; the positive control found each planted one`);
}
