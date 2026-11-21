// A check that something is absent passes when its read comes before the thing shows, is sent or is cached
// (ginsys/bronzeward#911, ginsys/bronzeward#916, ginsys/bronzeward#921, ginsys/bronzeward#926 and ginsys/bronzeward#928
// each met it). `absent` in auth/helpers.ts reads, pauses and
// reads again. So in an auth spec a single `expect(x).toBe(false)`, `expect(x).not.toContain(...)`,
// `expect(x).not.toMatch(...)`, a negated wdio matcher (`await expect(el).not.toBeExisting()`, any matcher
// expect-webdriverio 8.0.0 exports, network ones included), or an empty list read (`toEqual([])`, `toHaveLength(0)`, a literal
// zero on `toBeElementsArrayOfSize`, `toHaveChildren` or `toBeRequestedTimes`) must carry a note saying why it cannot pass
// early: a comment `once: <reason>`, trailing on its line or as the first line of the comment block directly above it. The
// guard checks that a note exists, not that it is true. The protected specs may not call the one-read `storageEmpty` at all
// (ginsys/bronzeward#934). Every read has a positive control: the same walk must flag a planted bare read in each
// shape, and the real files are read byte for byte.
// A member named by a string key (`expect(x)["toEqual"]([])`, `helpers["storageEmpty"]`, `import { "storageEmpty" as e }`)
// is the same read as the property form. Stated limits, because the parser cannot see them: a computed key (`x[name]`), a
// key built at run time (`"storage" + "Empty"`), a read through an alias (`const e = expect`), a string-keyed destructuring
// assignment target (`({ "storageEmpty": e } = helpers)`), a count that means zero without a literal `0` (`expect.oneOf(0)`,
// a fractional bound such as `{ lte: 0.5 }`), and a matcher that a later expect-webdriverio adds to the list.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { expect, test } from "vitest";

// Parentheses and type-only wrappers change no meaning: `(expect(x)).toBe((false as const))` is the same read.
const unwrap = (node: ts.Expression): ts.Expression =>
  ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isNonNullExpression(node) || ts.isSatisfiesExpression(node) || ts.isTypeAssertionExpression(node)
    ? unwrap(node.expression)
    : node;

// `x.name` or `x["name"]`, with the key a string or a template without substitutions: the same member either way.
// A computed key (`x[name]`) is a stated limit of this guard; so is a read through an alias (`const e = expect`).
const member = (node: ts.Expression): { object: ts.Expression; name: string } | undefined => {
  const n = unwrap(node);
  if (ts.isPropertyAccessExpression(n)) return { object: n.expression, name: n.name.text };
  if (!ts.isElementAccessExpression(n)) return undefined;
  const key = unwrap(n.argumentExpression);
  return ts.isStringLiteralLike(key) ? { object: n.expression, name: key.text } : undefined;
};

// `expect(x)` or `expect(promise).resolves`, which asserts the same value once the promise settles.
const isExpect = (node: ts.Expression) => {
  let call = unwrap(node);
  const resolves = member(call);
  if (resolves?.name === "resolves") call = unwrap(resolves.object);
  if (!ts.isCallExpression(call)) return false;
  const name = unwrap(call.expression);
  return ts.isIdentifier(name) && name.text === "expect";
};

// Every matcher expect-webdriverio 8.0.0 exports (element, elements, browser, network and snapshot). Negated, each
// retries until the negation holds (the snapshot ones compare once), so it passes at the first read that finds the thing
// absent, before the state it must not show has had its turn. A matcher from a later version is not here until the
// list is extended.
const ELEMENT_MATCHERS = new Set([
  "toBeClickable", "toBeDisabled", "toBeDisplayed", "toBeDisplayedInViewport", "toBeEnabled", "toExist", "toBeExisting", "toBePresent",
  "toBeFocused", "toBeSelected", "toBeChecked", "toBeStable", "toBeRequired", "toBeReadOnly", "toHaveAttribute", "toHaveChildren",
  "toHaveComputedLabel", "toHaveComputedRole", "toHaveElementClass", "toHaveElementProperty", "toHaveHeight", "toHaveHref",
  "toHaveLink", "toHaveHTML", "toHaveId", "toHaveSize", "toHaveStyle", "toHaveTagName", "toHaveText", "toHaveValue", "toHaveWidth",
  "toBeElementsArrayOfSize",
  "toHaveClipboardText", "toHaveCookie", "toHaveLocalStorageItem", "toHaveSessionStorageItem", "toHaveTitle", "toHaveUrl", "toHaveWindowCount",
  "toBeRequested", "toBeRequestedTimes", "toBeRequestedWith",
  "toMatchSnapshot", "toMatchInlineSnapshot",
]);

// wdio's count matchers take a number or `{ eq, lte, ... }` first, then an options object: a literal zero there is an
// empty-list read, so the options argument is allowed (unlike `toHaveLength`, which takes only the count).
const COUNT_MATCHERS = new Set(["toBeElementsArrayOfSize", "toHaveChildren", "toBeRequestedTimes"]);

const isLiteral = (node: ts.Expression | undefined, test: (n: ts.Expression) => boolean) => node !== undefined && test(unwrap(node));

const isZero = (n: ts.Expression) => ts.isNumericLiteral(n) && n.text === "0";

// `{ eq: 0 }` or `{ lte: 0 }`; where both are written `eq` decides, as in wdio's NumberMatcher.
const isZeroCount = (n: ts.Expression) => {
  if (isZero(n)) return true;
  if (!ts.isObjectLiteralExpression(n)) return false;
  const prop = (key: string) =>
    n.properties.find((p): p is ts.PropertyAssignment => ts.isPropertyAssignment(p) && (ts.isIdentifier(p.name) || ts.isStringLiteralLike(p.name)) && p.name.text === key);
  const decisive = prop("eq") ?? prop("lte");
  return decisive !== undefined && isZero(unwrap(decisive.initializer));
};

// `expect(...).toBe(false)`, `.toEqual([])`, `.toStrictEqual([])`, `.toHaveLength(0)`, a literal zero count on a wdio count
// matcher, and the negated `.not.toContain(...)`, `.not.toMatch(...)` and `.not.<wdio matcher>(...)`.
const isAbsenceRead = (node: ts.CallExpression): boolean => {
  const callee = member(node.expression);
  if (callee === undefined) return false;
  const matcher = callee.name;
  const [arg] = node.arguments;
  if (matcher === "toBe") return node.arguments.length === 1 && isLiteral(arg, (n) => n.kind === ts.SyntaxKind.FalseKeyword) && isExpect(callee.object);
  if (matcher === "toEqual" || matcher === "toStrictEqual") return node.arguments.length === 1 && isLiteral(arg, (n) => ts.isArrayLiteralExpression(n) && n.elements.length === 0) && isExpect(callee.object);
  if (matcher === "toHaveLength") return node.arguments.length === 1 && isLiteral(arg, isZero) && isExpect(callee.object);
  if (COUNT_MATCHERS.has(matcher) && isLiteral(arg, isZeroCount) && isExpect(callee.object)) return true;
  if (matcher !== "toContain" && matcher !== "toMatch" && !ELEMENT_MATCHERS.has(matcher)) return false;
  const negation = member(callee.object);
  return negation?.name === "not" && isExpect(negation.object);
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

// The specs where a late write hides a leak. `storageEmpty` reads once, so these use `storageEmptyAfterPause`
// (auth/helpers.ts), which reads, pauses and reads again. No note lifts this: after the swap the one-read name has no
// legitimate use in them, and a note on an import specifier would only be a way round. Any reference to it, an import,
// an alias or a call, is refused by line. The other specs keep the one read.
export const PROTECTED_SPECS = ["protected.e2e.ts", "a11y-import.e2e.ts", "publish.e2e.ts"];

export const protectedStorage = (source: string, fileName: string): number[] => {
  if (!PROTECTED_SPECS.includes(fileName.split("/").pop()!)) return [];
  const file = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true);
  const lines: number[] = [];
  const visit = (node: ts.Node): void => {
    if ((ts.isIdentifier(node) && node.text === "storageEmpty") || (ts.isStringLiteralLike(node) && node.text === "storageEmpty" && namesMember(node))) {
      lines.push(file.getLineAndCharacterOfPosition(node.getStart(file)).line + 1);
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return lines;
};

// A string that names a binding or a member, as `import { "x" as y }`, `export { "x" }`, `a["x"]` and `const { "x": y } = a`,
// is the same reference as the identifier. A string anywhere else (an argument, a value) is only text.
const namesMember = (node: ts.StringLiteralLike): boolean => {
  // Parentheses and type-only wrappers around an element-access key change no meaning (`a[("x" as const)]`).
  let key: ts.Node = node;
  while (ts.isParenthesizedExpression(key.parent) || ts.isAsExpression(key.parent) || ts.isNonNullExpression(key.parent) || ts.isSatisfiesExpression(key.parent) || ts.isTypeAssertionExpression(key.parent)) key = key.parent;
  const p = key.parent;
  return (
    ts.isImportSpecifier(p) ||
    ts.isExportSpecifier(p) ||
    (ts.isElementAccessExpression(p) && p.argumentExpression === key) ||
    (ts.isBindingElement(p) && p.propertyName === node)
  );
};

// How many times the two-read check is called or handed over as a callback (`afterEach(check)`), under its own name or
// the name it is imported as. An unused import, a type reference (`typeof check`), a comment and a string do not count.
export const afterPauseUses = (source: string): number => {
  const file = ts.createSourceFile("spec.ts", source, ts.ScriptTarget.Latest, true);
  const names = new Set(["storageEmptyAfterPause"]);
  for (const s of file.statements) {
    const named = ts.isImportDeclaration(s) ? s.importClause?.namedBindings : undefined;
    if (named !== undefined && ts.isNamedImports(named)) for (const e of named.elements) if ((e.propertyName ?? e.name).text === "storageEmptyAfterPause") names.add(e.name.text);
  }
  let uses = 0;
  const visit = (node: ts.Node): void => {
    if (ts.isImportDeclaration(node)) return;
    if (ts.isCallExpression(node)) for (const part of [node.expression, ...node.arguments]) if (ts.isIdentifier(part) && names.has(part.text)) uses++;
    ts.forEachChild(node, visit);
  };
  visit(file);
  return uses;
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

test("the positive control: a bare negated wdio matcher, with or without a wait option, is a read of this kind", () => {
  const sample = [
    'await expect($("a")).not.toBeExisting();',
    'await expect($("a")).not.toBeExisting({ wait: 10_000 });',
    'await expect($("a")).not.toHaveText("x");',
    'await expect($("a")).not.toHaveText(expect.stringContaining("x"), { wait: 20_000 });',
    'await expect($("a")).not.toBeDisplayed();',
    'await expect($("a")).not.toBeEnabled();',
    'await expect($("a")).not.toBeDisabled();',
    'await expect($("a")).not.toBeClickable();',
    'await (expect($("a"))).not.toBeExisting();',
    'await expect($("a")).not.toBeExisting(); // once: drawn in the same render as the field',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([
    ...[1, 2, 3, 4, 5, 6, 7, 8, 9].map((line) => ({ line, noted: false })),
    { line: 10, noted: true },
  ]);
});

test("the positive control: an empty list read is a read of this kind, however it is written", () => {
  const sample = [
    "expect(await posts()).toEqual([]);",
    "expect(await posts()).toStrictEqual([]);",
    "expect(await posts()).toHaveLength(0);",
    "expect((await posts())).toEqual(([]));",
    "await expect(posts()).resolves.toEqual([]);",
    "expect(w.lost).toEqual([]); // once: appended only by awaited after() calls",
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([
    ...[1, 2, 3, 4, 5].map((line) => ({ line, noted: false })),
    { line: 6, noted: true },
  ]);
});

test("the negative control: absent, a true read, a count, a plain include, a plain inequality, a list that is not empty and a positive matcher are not reads of this kind", () => {
  const sample = [
    'await absent(() => $("a").isExisting(), "a");',
    'expect(await $("a").isExisting()).toBe(true);',
    "expect(sent).toBe(0);",
    "expect(shown).toContain(CANARY);",
    "expect(puts[0]![2]).not.toBe(puts[1]![2]);",
    "expect(await posts()).toEqual([1]);",
    "expect(await posts()).toEqual(expected);",
    "expect(await posts()).toHaveLength(1);",
    'await expect($("a")).toHaveText("x");',
    'await expect($("a")).toBeExisting();',
    '// expect(await $("a").isExisting()).toBe(false);',
    'const text = "expect(x).toBe(false)";',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([]);
});

const STORAGE = ['import { absent, storageEmpty } from "./helpers.ts";', "afterEach(storageEmpty);", "  await storageEmpty();", "  await storageEmptyAfterPause();"];

test("the positive control: in a protected spec every reference to the one-read storage check is refused, whatever it is imported or called as", () => {
  for (const name of PROTECTED_SPECS) expect(protectedStorage(STORAGE.join("\n"), name), name).toEqual([1, 2, 3]);
  expect(protectedStorage('import { storageEmpty as empty } from "./helpers.ts";\nafterEach(empty);', "publish.e2e.ts")).toEqual([1]);
  // No note makes it pass: a note on an import specifier means nothing.
  expect(protectedStorage("afterEach(storageEmpty); // once: a case that writes nothing", "protected.e2e.ts")).toEqual([1]);
});

test("the negative control: the same references in a spec that is not protected, and the two-read check anywhere, are not refused", () => {
  for (const name of ["tabs.e2e.ts", "provider.e2e.ts", "drafts.e2e.ts"]) expect(protectedStorage(STORAGE.join("\n"), name), name).toEqual([]);
  expect(protectedStorage("afterEach(storageEmptyAfterPause);\n// storageEmpty is one read", "protected.e2e.ts")).toEqual([]);
});

test("the two-read check counts as used only where it is called or registered, never for an import or a comment", () => {
  expect(afterPauseUses(STORAGE.join("\n"))).toBe(1);
  expect(afterPauseUses("afterEach(storageEmptyAfterPause);")).toBe(1);
  expect(afterPauseUses('import { storageEmptyAfterPause } from "./helpers.ts";')).toBe(0);
  expect(afterPauseUses('import { storageEmptyAfterPause } from "./helpers.ts";\n// storageEmptyAfterPause is used here\nconst s = "storageEmptyAfterPause";')).toBe(0);
  // A type reference is no use, and an imported alias is one.
  expect(afterPauseUses('import { storageEmptyAfterPause } from "./helpers.ts";\ntype Check = typeof storageEmptyAfterPause;\nconst f = storageEmptyAfterPause;')).toBe(0);
  expect(afterPauseUses('import { storageEmptyAfterPause as check } from "./helpers.ts";\nafterEach(check);')).toBe(1);
  expect(afterPauseUses('import { storageEmptyAfterPause as check } from "./helpers.ts";\nawait check();')).toBe(1);
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

test("the positive control: a matcher, `not` or `resolves` named by a string key is the same read as a property access", () => {
  const sample = [
    'expect(await posts())["toEqual"]([]);',
    'expect($("a")).not["toBeExisting"]();',
    'expect($("a"))["not"].toHaveText("x");',
    'expect(value)["toBe"](false);',
    'await expect(posts())["resolves"].toEqual([]);',
    "expect(value).not[`toContain`](CANARY);",
    'expect(await posts())["toHaveLength"](0);',
    'expect(value)["not"]["toMatch"](/x/);',
    'expect(value)["toBe"](false); // once: drawn in the same render',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([
    ...[1, 2, 3, 4, 5, 6, 7, 8].map((line) => ({ line, noted: false })),
    { line: 9, noted: true },
  ]);
});

test("the negative control: a string key that names no absence read, and a computed key (a stated limit), are not flagged", () => {
  const sample = [
    'expect(value)["toBe"](true);',
    'expect(await posts())["toEqual"]([1]);',
    'expect($("a"))["toHaveText"]("x");',
    "expect(value)[matcher](false);",
    "expect(value).not[matcher]();",
    'expect(value)["not"][matcher]();',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([]);
});

// The expect-webdriverio 8.0.0 matchers the guard's list used to miss.
const ADDED_MATCHERS = [
  "toBeDisabled", "toBeDisplayedInViewport", "toBeSelected", "toBeChecked", "toBeStable", "toBeRequired", "toBeReadOnly",
  "toHaveChildren", "toHaveComputedLabel", "toHaveComputedRole", "toHaveElementProperty", "toHaveHeight", "toHaveHref",
  "toHaveLink", "toHaveHTML", "toHaveId", "toHaveSize", "toHaveStyle", "toHaveTagName", "toHaveValue", "toHaveWidth",
  "toBeElementsArrayOfSize", "toHaveClipboardText", "toHaveCookie", "toHaveLocalStorageItem", "toHaveSessionStorageItem",
  "toHaveTitle", "toHaveUrl", "toHaveWindowCount", "toBeRequested", "toBeRequestedTimes", "toBeRequestedWith",
];

test("the positive control: every wdio matcher the list used to miss is a read of this kind when negated, noted or not", () => {
  const sample = ADDED_MATCHERS.map((m) => `await expect(subject).not.${m}(arg);`);
  sample.push(`await expect(subject).not.toBeDisabled(); // once: drawn with the form`);
  expect(absenceReads(sample.join("\n"))).toEqual([
    ...ADDED_MATCHERS.map((_, i) => ({ line: i + 1, noted: false })),
    { line: ADDED_MATCHERS.length + 1, noted: true },
  ]);
});

test("the negative control: toBeVisible, which wdio 8.0.0 does not export, is off the list, and the positive form of each added matcher is not a read", () => {
  const sample = [
    'await expect($("a")).not.toBeVisible();',
    ...ADDED_MATCHERS.filter((m) => m !== "toBeElementsArrayOfSize" && m !== "toHaveChildren" && m !== "toBeRequestedTimes").map((m) => `await expect(subject).${m}(arg);`),
  ];
  expect(bare(sample.join("\n"))).toEqual([]);
});

test("the positive control: a literal zero is an absence read on every count matcher, with or without an options argument", () => {
  const sample = [
    'await expect($$("a")).toBeElementsArrayOfSize(0);',
    'await expect($$("a")).toBeElementsArrayOfSize({ eq: 0 });',
    'await expect($$("a")).toBeElementsArrayOfSize({ lte: 0 });',
    'await expect($$("a")).toBeElementsArrayOfSize(0, { wait: 500 });',
    'await expect($("ul")).toHaveChildren(0);',
    'await expect($("ul")).toHaveChildren({ eq: 0 }, { wait: 500 });',
    "await expect(mock).toBeRequestedTimes(0);",
    "await expect(mock).toBeRequestedTimes({ lte: 0 });",
    "await expect(mock).toBeRequestedTimes((0));",
    "await expect(mock).toBeRequestedTimes({ eq: 0, lte: 1 });",
    'await expect($$("a")).not.toBeElementsArrayOfSize(2);',
    'await expect($$("a")).toBeElementsArrayOfSize(0); // once: the list is drawn in the same render',
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([
    ...[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11].map((line) => ({ line, noted: false })),
    { line: 12, noted: true },
  ]);
});

test("the negative control: a non-zero count, a range above zero and a computed count are not absence reads", () => {
  const sample = [
    'await expect($$("a")).toBeElementsArrayOfSize(1);',
    'await expect($$("a")).toBeElementsArrayOfSize(2);',
    'await expect($$("a")).toBeElementsArrayOfSize({ gte: 1 });',
    'await expect($$("a")).toBeElementsArrayOfSize({ eq: 3 });',
    'await expect($("ul")).toHaveChildren({ gte: 1 });',
    "await expect(mock).toBeRequestedTimes(1);",
    // wdio's NumberMatcher lets `eq` override `lte`, so these ask for one request.
    "await expect(mock).toBeRequestedTimes({ eq: 1, lte: 0 });",
    "await expect(mock).toBeRequestedTimes({ lte: 0, eq: 1 });",
    "await expect(mock).toBeRequestedTimes(count);",
    "await expect(mock).toBeRequested();",
  ];
  expect(absenceReads(sample.join("\n"))).toEqual([]);
});

test("the positive control: a string-named storageEmpty (import, export, element access, destructuring key) is refused in a protected spec", () => {
  const forms = [
    'import { "storageEmpty" as empty } from "./helpers.ts";\nafterEach(empty);',
    'afterEach(helpers["storageEmpty"]);',
    "afterEach(helpers[`storageEmpty`]);",
    'const { "storageEmpty": empty } = helpers;',
    'export { "storageEmpty" } from "./helpers.ts";',
  ];
  for (const form of forms) expect(protectedStorage(form, "protected.e2e.ts"), form).toEqual([1]);
  // A type-only wrapper or parentheses around the key change no meaning.
  for (const form of ['afterEach(helpers["storageEmpty" as const]);', 'afterEach(helpers[("storageEmpty")]);', 'afterEach(helpers["storageEmpty"!]);']) {
    expect(protectedStorage(form, "protected.e2e.ts"), form).toEqual([1]);
  }
  expect(protectedStorage('import { "storageEmpty" as empty } from "./helpers.ts";', "tabs.e2e.ts")).toEqual([]);
});

test("the negative control: a string that merely contains the name, the two-read check by a string key and a built key (a stated limit) are not refused", () => {
  const forms = [
    'log("storageEmpty");',
    'const s = "storageEmpty";',
    'afterEach(helpers["storageEmptyAfterPause"]);',
    'import { "storageEmptyAfterPause" as check } from "./helpers.ts";',
    'afterEach(helpers["storage" + "Empty"]);',
    "afterEach(helpers[name]);",
  ];
  for (const form of forms) expect(protectedStorage(form, "protected.e2e.ts"), form).toEqual([]);
});

const dir = fileURLToPath(new URL("./auth/", import.meta.url));

test("the e2e auth specs carry no unnoted absence read", () => {
  const specs = readdirSync(dir).filter((name) => name.endsWith(".e2e.ts")).sort();
  // The control for the listing: it holds the specs this guard exists for, so an empty or wrong directory fails here.
  // The protected specs are named: a spec renamed away from the storage rule fails here instead of dropping out of it.
  expect(specs).toEqual(expect.arrayContaining(["protected.e2e.ts", "a11y-import.e2e.ts", "drafts.e2e.ts", "writes.e2e.ts", "recovery.e2e.ts", ...PROTECTED_SPECS]));
  let seen = 0;
  const found: string[] = [];
  const checked: string[] = [];
  for (const name of specs) {
    const file = `${dir}${name}`;
    const source = readFileSync(file, "utf8");
    // The control for this file: the read returned every byte of it, so a read near the end cannot hide.
    expect(Buffer.byteLength(source), name).toBe(statSync(file).size);
    expect(source.trim().length, name).toBeGreaterThan(0);
    const reads = absenceReads(source, file);
    seen += reads.length;
    for (const r of reads) if (!r.noted) found.push(`${name}:${r.line}`);
    for (const line of protectedStorage(source, file)) found.push(`${name}:${line} storageEmpty (one read) in a protected spec`);
    if (PROTECTED_SPECS.includes(name)) {
      checked.push(name);
      // The control for the storage rule: the two-read check that replaces the one-read name is called or registered in the file.
      if (afterPauseUses(source) === 0) found.push(`${name}: no storageEmptyAfterPause call`);
    }
  }
  expect(checked).toEqual([...PROTECTED_SPECS].sort());
  // The control for the walk: the specs hold many such reads, noted ones included, so a walk that saw none would pass here.
  expect(seen).toBeGreaterThan(20);
  expect(found).toEqual([]);
});
