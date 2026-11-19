// A check that something is absent passes when its read comes before the thing shows, is sent or is cached
// (ginsys/bronzeward#911, ginsys/bronzeward#916, ginsys/bronzeward#921, ginsys/bronzeward#926 and ginsys/bronzeward#928
// each met it). `absent` in auth/helpers.ts reads, pauses and
// reads again. So in an auth spec a single `expect(x).toBe(false)`, `expect(x).not.toContain(...)` or
// `expect(x).not.toMatch(...)` must carry a note saying why it cannot pass early: a comment `once: <reason>`,
// trailing on its line or as the first line of the comment block directly above it. The guard checks that a note
// exists, not that it is true. List reads and wdio's own negated matchers are not covered
// (ginsys/bronzeward#934). Every read has a positive control: the same walk must flag a planted bare read in each
// shape, and the real files are read byte for byte.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { expect, test } from "vitest";

// Parentheses and type-only wrappers change no meaning: `(expect(x)).toBe((false as const))` is the same read.
const unwrap = (node: ts.Expression): ts.Expression =>
  ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isNonNullExpression(node) || ts.isSatisfiesExpression(node) || ts.isTypeAssertionExpression(node)
    ? unwrap(node.expression)
    : node;

// `expect(x)` or `expect(promise).resolves`, which asserts the same value once the promise settles.
const isExpect = (node: ts.Expression) => {
  let call = unwrap(node);
  if (ts.isPropertyAccessExpression(call) && call.name.text === "resolves") call = unwrap(call.expression);
  if (!ts.isCallExpression(call)) return false;
  const name = unwrap(call.expression);
  return ts.isIdentifier(name) && name.text === "expect";
};

// `expect(...).toBe(false)`, `expect(...).not.toContain(...)` and `expect(...).not.toMatch(...)`.
const isAbsenceRead = (node: ts.CallExpression): boolean => {
  const callee = unwrap(node.expression);
  if (!ts.isPropertyAccessExpression(callee)) return false;
  const matcher = callee.name.text;
  if (matcher === "toBe") return node.arguments.length === 1 && unwrap(node.arguments[0]!).kind === ts.SyntaxKind.FalseKeyword && isExpect(callee.expression);
  if (matcher !== "toContain" && matcher !== "toMatch") return false;
  const negation = unwrap(callee.expression);
  return ts.isPropertyAccessExpression(negation) && negation.name.text === "not" && isExpect(negation.expression);
};

const NOTE = /^\/\/\s*once:\s*\S/;

type Comment = { text: string; line: number; ownLine: boolean; pos: number };

// Every `//` comment of the file as the parser holds it, so a string, a template or a block comment that merely
// contains `// once:` is no note. `ownLine` is a comment with only blanks before it on its line.
const lineComments = (file: ts.SourceFile): Comment[] => {
  const text = file.text;
  const seen = new Map<number, Comment>();
  const take = (ranges: ts.CommentRange[] | undefined) => {
    for (const r of ranges ?? []) {
      if (r.kind !== ts.SyntaxKind.SingleLineCommentTrivia || seen.has(r.pos)) continue;
      const line = file.getLineAndCharacterOfPosition(r.pos).line;
      const before = text.slice(file.getPositionOfLineAndCharacter(line, 0), r.pos);
      seen.set(r.pos, { text: text.slice(r.pos, r.end), line, ownLine: before.trim() === "", pos: r.pos });
    }
  };
  const visit = (node: ts.Node): void => {
    take(ts.getLeadingCommentRanges(text, node.pos));
    take(ts.getTrailingCommentRanges(text, node.end));
    ts.forEachChild(node, visit);
  };
  visit(file);
  return [...seen.values()];
};

// Whether the read carries a note: a trailing comment after its end on the line it ends, or a block of own-line
// `//` comments directly above its first line whose first line is the note.
const noted = (file: ts.SourceFile, comments: Comment[], node: ts.Node): boolean => {
  const first = file.getLineAndCharacterOfPosition(node.getStart(file)).line;
  const last = file.getLineAndCharacterOfPosition(node.getEnd()).line;
  if (comments.some((c) => !c.ownLine && c.line === last && c.pos >= node.getEnd() && NOTE.test(c.text))) return true;
  const above = new Map(comments.filter((c) => c.ownLine).map((c) => [c.line, c.text]));
  let top = first - 1;
  if (!above.has(top)) return false;
  while (above.has(top - 1)) top--;
  return NOTE.test(above.get(top)!);
};

// Every absence read of a source, by 1-based line, with whether it carries a note. Parsed from the TypeScript tree,
// so a comment or a string makes no read.
export const absenceReads = (source: string, fileName = "source.e2e.ts"): { line: number; noted: boolean }[] => {
  const file = ts.createSourceFile(fileName, `${source}\nexport {};\n`, ts.ScriptTarget.Latest, true);
  const comments = lineComments(file);
  const found: { line: number; noted: boolean }[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && isAbsenceRead(node)) {
      found.push({ line: file.getLineAndCharacterOfPosition(node.getStart(file)).line + 1, noted: noted(file, comments, node) });
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return found;
};

const bare = (source: string) => absenceReads(source).filter((r) => !r.noted).map((r) => r.line);

test("the positive control: the read flags a bare false read, a bare negated include and a bare negated match", () => {
  const sample = [
    'expect(await $("a").isExisting()).toBe(false);',
    'expect(await $("a").isEnabled()).toBe(false);',
    "expect(shown).not.toContain(CANARY);",
    'expect(await $("body").getText()).not.toMatch(/canary/);',
    "if (id !== \"\") expect(await cacheHolds(id)).toBe(false);",
  ];
  expect(bare(sample.join("\n"))).toEqual([1, 2, 3, 4, 5]);
});

test("the negative control: absent, a true read, a count, a plain include and an async matcher are not reads of this kind", () => {
  const sample = [
    'await absent(() => $("a").isExisting(), "a");',
    'expect(await $("a").isExisting()).toBe(true);',
    "expect(sent).toBe(0);",
    "expect(shown).toContain(CANARY);",
    'await expect($("a")).not.toBeExisting();',
    'await expect($("a")).not.toHaveText("x");',
    '// expect(await $("a").isExisting()).toBe(false);',
    'const text = "expect(x).toBe(false)";',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([]);
});

test("the positive control: a trailing note and a block note above count, and the walk says which read each one is", () => {
  const sample = [
    'expect(await $("a").isEnabled()).toBe(false); // once: the gate follows the typed field in the same render',
    "// once: drawn disabled in the same element as its text",
    "// (web/src/states.tsx), so it cannot render later.",
    'expect(await $("b").isEnabled()).toBe(false);',
    'expect(await $("c").isEnabled()).toBe(false);',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([
    { line: 1, noted: true },
    { line: 4, noted: true },
    { line: 5, noted: false },
  ]);
});

test("the negative control: an empty note, a note that is not the block's first line, a note past a blank line and another comment do not count", () => {
  const sample = [
    "// once:",
    'expect(await $("a").isEnabled()).toBe(false);',
    "// The gate follows the typed field.",
    "// once: the same render",
    'expect(await $("b").isEnabled()).toBe(false);',
    "// once: the same render",
    "",
    'expect(await $("c").isEnabled()).toBe(false);',
    "// the page is read once, so it cannot pass early",
    'expect(await $("d").isEnabled()).toBe(false);',
    'expect(await $("e").isEnabled()).toBe(false); // the gate',
  ];
  expect(bare(sample.join("\n"))).toEqual([2, 5, 8, 10, 11]);
});

test("the negative control: text that only contains a note (a string, a template, a block comment) is no note", () => {
  const sample = [
    'expect("// once: synthetic text").not.toContain("canary");',
    "expect(`// once: synthetic text`).not.toContain(\"canary\");",
    "expect(value).toBe(false); /* // once: reason */",
    "expect(value).toBe(false); /* once: reason */",
    'const s = "// once: reason"; expect(value).toBe(false);',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([
    { line: 1, noted: false },
    { line: 2, noted: false },
    { line: 3, noted: false },
    { line: 4, noted: false },
    { line: 5, noted: false },
  ]);
});

test("the positive control: a read wrapped in parentheses or a type-only wrapper is the same read", () => {
  const sample = [
    "expect(value).toBe((false));",
    "(expect(value)).toBe(false);",
    "(expect(value).not).toContain(CANARY);",
    "expect(value).not.toMatch(/canary/);",
    "expect(value).toBe(false as const);",
    "expect(value).toBe(false!);",
    "expect(value).toBe(false satisfies boolean);",
    "(expect)(value).toBe(false);",
    '(expect)(value).not.toContain("x");',
    "((expect))(value).not.toMatch(/x/);",
    "await expect(cacheHolds(path)).resolves.toBe(false);",
    'await expect(text()).resolves.not.toContain("x");',
    "await expect(text()).resolves.not.toMatch(/x/);",
  ];
  expect(bare(sample.join("\n"))).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13]);
});

test("the positive control: a read over several lines takes a trailing note at its end and a block note above its start", () => {
  const sample = [
    "expect(",
    "  value,",
    ").toBe(false); // once: the text is drawn with the state the case waited for",
    "// once: the same render",
    "expect(",
    "  other,",
    ").toBe(false);",
    "expect(",
    "  third,",
    ").toBe(false);",
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([
    { line: 1, noted: true },
    { line: 5, noted: true },
    { line: 8, noted: false },
  ]);
});

test("the positive control: a read inside a loop, a closure and a call argument is found", () => {
  const sample = [
    'for (const line of lines) expect(line).not.toContain("Not saved");',
    'await check(async () => { expect(await $("a").isExisting()).toBe(false); });',
    'expect(await Promise.all([$("a").isExisting()])).not.toMatch(/true/);',
  ];
  expect(bare(sample.join("\n"))).toEqual([1, 2, 3]);
});

const dir = fileURLToPath(new URL("./auth/", import.meta.url));

test("the e2e auth specs carry no bare false read without a once note", () => {
  const specs = readdirSync(dir).filter((name) => name.endsWith(".e2e.ts")).sort();
  // The control for the listing: it holds the specs this guard exists for, so an empty or wrong directory fails here.
  expect(specs).toEqual(expect.arrayContaining(["protected.e2e.ts", "a11y-import.e2e.ts", "drafts.e2e.ts", "writes.e2e.ts", "recovery.e2e.ts"]));
  let seen = 0;
  const found: string[] = [];
  for (const name of specs) {
    const file = `${dir}${name}`;
    const source = readFileSync(file, "utf8");
    // The control for this file: the read returned every byte of it, so a read near the end cannot hide.
    expect(Buffer.byteLength(source), name).toBe(statSync(file).size);
    expect(source.trim().length, name).toBeGreaterThan(0);
    const reads = absenceReads(source, file);
    seen += reads.length;
    for (const r of reads) if (!r.noted) found.push(`${name}:${r.line}`);
  }
  // The control for the walk: the specs hold many such reads, noted ones included, so a walk that saw none would pass here.
  expect(seen).toBeGreaterThan(20);
  expect(found).toEqual([]);
});
