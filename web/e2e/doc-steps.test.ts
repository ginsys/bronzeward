// Each numbered web step of the operator pages has a browser case that covers it (ginsys/bronzeward#405). A page marks a
// list with `<!-- bw-web-steps: <list> -->` on the line above its "1."; a case covers a step by carrying
// `[step <list>.<n>]` in its title, once per step. This guard joins the two, both ways: a step with no case is missing,
// and a tag naming a step no page has is extra. The list is `<page>` for a page with one list and `<page>-<n>` for a page
// with several. A numbered list in a web section (a `## Web...` heading, the whole of quick-start-web, or the paragraph
// `**Web.** Steps:`) with no marker is refused too, so a new list cannot be left out.
//
// What it does not see, stated because the check reads sources and not a run: "passing" is that the case exists, is not
// skipped and sits in a spec the required browser-auth job runs (the reporter writes no result), so a case that a
// browser branch skips at run time passes it. A tag says a case exercises the step; it does not say the case asserts
// every sentence of the step. Every read has a positive control: each defect is planted below and must be named.
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { expect, test } from "vitest";

export const PAGES = ["quick-start-web", "sign-in-and-renewal", "inventory", "editing", "protected-import", "plan-and-approval", "publication", "operation-tracking"];

const MARKER = /^<!-- bw-web-steps: (\S+) -->\s*$/;
// A list item of the list itself: up to two spaces before the number (an item's own text starts at the third column, so a
// line indented further is that item's content, a nested list included).
const SIBLING = /^ {0,2}(\d+)\. /;
const FIRST = /^ {0,2}1\. /;
const TAG = /\[step ([a-z0-9-]+)\.(\d+)\]/g;

type List = { id: string; steps: number };

// The marked lists of a page, and what is wrong with its lists. A list is the numbered items after its marker: an item's
// indented lines, a blank line and a lazy continuation belong to it, and it ends at the first blank-separated line that
// is neither indented nor the next number. Fenced text outside a list is skipped, a fence inside one is refused, and a fence
// that is never closed is a problem.
export const lists = (page: string, markdown: string): { lists: List[]; problems: string[] } => {
  const lines = markdown.split("\n");
  const found: List[] = [];
  const problems: string[] = [];
  const marked = new Set<number>();
  let open = ""; // the fence that is open, as its opener's run of backticks or tildes
  let web = false;
  let webLevel = 0;
  let steps = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;
    // A fence closes only by a run of its own character, at least as long as its opener's, with nothing after it.
    const run = /^ {0,3}(`{3,}|~{3,})/.exec(line)?.[1];
    const was = open !== "";
    if (!was && run !== undefined) open = run;
    else if (was && run !== undefined && run[0] === open[0] && run.length >= open.length && /^ {0,3}(`+|~+)\s*$/.test(line)) open = "";
    if (was || open !== "") continue;
    const heading = /^(#{1,6}) (.*)$/.exec(line);
    if (heading !== null) {
      steps = false;
      if (/^Web\b/.test(heading[2]!)) [web, webLevel] = [true, heading[1]!.length];
      else if (web && heading[1]!.length <= webLevel) web = false;
      continue;
    }
    if (line.startsWith("**Web.** Steps:")) steps = true;
    const marker = MARKER.exec(line);
    if (marker === null) {
      if (FIRST.test(line) && !marked.has(i) && (page === "quick-start-web" || web || steps)) {
        problems.push(`${page}:${i + 1}: a numbered list in a web section has no <!-- bw-web-steps: ... --> line above it`);
      }
      continue;
    }
    const id = marker[1]!;
    if (id !== page && !new RegExp(`^${page}-[0-9]+$`).test(id)) problems.push(`${page}:${i + 1}: the list name ${id} is neither ${page} nor ${page}-<n>`);
    if (found.some((l) => l.id === id)) problems.push(`${page}:${i + 1}: the list name ${id} is used twice`);
    let j = i + 1;
    while (j < lines.length && lines[j]!.trim() === "") j++;
    if (!FIRST.test(lines[j] ?? "")) {
      problems.push(`${page}:${i + 1}: the marker ${id} is not followed by a numbered list that starts at 1`);
      continue;
    }
    marked.add(j);
    let n = 1;
    let blank = false;
    let k = j + 1;
    for (; k < lines.length; k++) {
      const next = lines[k]!;
      if (/^\s*(```|~~~)/.test(next)) {
        // The check reads no fenced text, so a fence inside a list is refused where it could hide a step: put it outside.
        problems.push(`${page}:${k + 1}: the list ${id} holds a fenced block, which this check does not read; put it outside the list`);
        break;
      }
      if (next.trim() === "") { blank = true; continue; }
      const item = SIBLING.exec(next);
      if (item !== null) {
        // An item with another number is a step the pages would show and no tag can name: refuse it, blank line or not.
        if (Number(item[1]) === n + 1) n++;
        else problems.push(`${page}:${k + 1}: the list ${id} has item ${item[1]} where ${n + 1} is next`);
        blank = false;
        continue;
      }
      if (/^\s/.test(next)) { blank = false; continue; }
      if (blank) break;
    }
    found.push({ id, steps: n });
    i = k - 1;
  }
  if (open !== "") problems.push(`${page}: a fenced block is never closed`);
  if (found.length === 0) problems.push(`${page}: no <!-- bw-web-steps: ... --> line`);
  return { lists: found, problems };
};

type Tag = { id: string; step: number; at: string; unrun: boolean };

const kind = (call: ts.CallExpression): { base: string; modifier: string } | undefined => {
  const callee = call.expression;
  if (ts.isIdentifier(callee)) return { base: callee.text, modifier: "" };
  if (ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.expression)) return { base: callee.expression.text, modifier: callee.name.text };
  return undefined;
};

// The fixed parts of a title: one string, or a template's literal segments. A tag is read inside one segment: text joined
// across a substitution (`[step a.${n}1]`) is no tag.
const title = (node: ts.Expression | undefined): string[] => {
  if (node === undefined) return [];
  if (ts.isStringLiteralLike(node)) return [node.text];
  if (ts.isTemplateExpression(node)) return [node.head.text, ...node.templateSpans.map((s) => s.literal.text)];
  return [];
};

// The tags in the titles of a spec's cases, with whether the case is not run normally: `xit`, `.skip`, `.todo` or `.only`
// on the case or on a describe around it. A tag in a describe title is no tag: it would cover every case under it.
export const tags = (source: string, name: string): Tag[] => {
  const file = ts.createSourceFile(name, source, ts.ScriptTarget.Latest, true);
  const found: Tag[] = [];
  const unrun = (c: { base: string; modifier: string }) => ["xit", "xtest", "xdescribe"].includes(c.base) || ["skip", "todo", "only"].includes(c.modifier);
  const visit = (node: ts.Node): void => {
    const k = ts.isCallExpression(node) ? kind(node) : undefined;
    if (ts.isCallExpression(node) && k !== undefined && ["it", "test", "xit", "xtest", "specify"].includes(k.base)) {
      let skipped = unrun(k);
      for (let p: ts.Node | undefined = node.parent; p !== undefined && !skipped; p = p.parent) {
        const outer = ts.isCallExpression(p) ? kind(p) : undefined;
        if (outer !== undefined && ["describe", "xdescribe", "context"].includes(outer.base) && unrun(outer)) skipped = true;
      }
      const at = `${name}:${file.getLineAndCharacterOfPosition(node.getStart(file)).line + 1}`;
      for (const segment of title(node.arguments[0])) for (const m of segment.matchAll(TAG)) found.push({ id: m[1]!, step: Number(m[2]), at, unrun: skipped });
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return found;
};

// Every problem between the pages (name to text) and the specs (file name to text); none means each step has a case and
// each tag a step.
export const check = (pages: Record<string, string>, specs: Record<string, string>): string[] => {
  const problems: string[] = [];
  const want = new Map<string, string>();
  for (const [page, text] of Object.entries(pages)) {
    const read = lists(page, text);
    problems.push(...read.problems);
    for (const l of read.lists) for (let n = 1; n <= l.steps; n++) want.set(`${l.id}.${n}`, page);
  }
  const covered = new Set<string>();
  for (const [name, text] of Object.entries(specs)) {
    for (const t of tags(text, name)) {
      const id = `${t.id}.${t.step}`;
      if (!want.has(id)) problems.push(`${t.at}: [step ${id}] names a step no page has`);
      else if (t.unrun) problems.push(`${t.at}: [step ${id}] is on a case that is skipped, todo or only`);
      else covered.add(id);
    }
  }
  for (const [id, page] of want) if (!covered.has(id)) problems.push(`${page}: step ${id} has no browser case tagged [step ${id}]`);
  return problems;
};

// The specs the browser-auth job runs (auth.conf.ts `specs`), and the pages.
const e2e = fileURLToPath(new URL(".", import.meta.url));
const docs = fileURLToPath(new URL("../../docs/operator/clients/", import.meta.url));
const SPECS = ["driver-controls.e2e.ts", ...readdirSync(`${e2e}auth`).filter((f) => f.endsWith(".e2e.ts")).sort().map((f) => `auth/${f}`)];
const read = (path: string) => readFileSync(path, "utf8");
const realPages = () => Object.fromEntries(PAGES.map((p) => [p, read(`${docs}${p}.md`)]));
const realSpecs = () => Object.fromEntries(SPECS.map((s) => [s, read(`${e2e}${s}`)]));

test("every numbered web step of the eight pages has a tagged browser case, and every tag a step", () => {
  expect(check(realPages(), realSpecs())).toEqual([]);
});

test("the specs read are the ones the browser-auth job runs", () => {
  expect(read(`${e2e}auth.conf.ts`)).toContain('["./driver-controls.e2e.ts", "./auth/*.e2e.ts"]');
});

const page = (...lines: string[]) => lines.join("\n");
const list = (id: string, n: number) => ["", `<!-- bw-web-steps: ${id} -->`, "", ...Array.from({ length: n }, (_, i) => `${i + 1}. Step ${i + 1}.`)];
const spec = (...ids: string[]) => `describe("d", () => { it("a case ${ids.map((i) => `[step ${i}]`).join(" ")}", () => {}); });`;

test("the negative control: a page and a spec that agree pass, and each step may be covered by several cases", () => {
  expect(check({ a: page("# A", ...list("a", 2)) }, { "s.ts": spec("a.1", "a.2") })).toEqual([]);
  expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": spec("a.1"), "t.ts": spec("a.1") })).toEqual([]);
});

test("the positive control: a step with no case, a tag with no step, and a case that is not run are each named", () => {
  expect(check({ a: page("# A", ...list("a", 2)) }, { "s.ts": spec("a.1") })).toEqual(["a: step a.2 has no browser case tagged [step a.2]"]);
  expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": spec("a.1", "a.2", "b.1") })).toEqual([
    "s.ts:1: [step a.2] names a step no page has",
    "s.ts:1: [step b.1] names a step no page has",
  ]);
  for (const skipped of ['it.skip("c [step a.1]", f)', 'xit("c [step a.1]", f)', 'it.only("c [step a.1]", f)', 'it.todo("c [step a.1]")', 'describe.skip("d", () => { it("c [step a.1]", f); })', 'xdescribe("d", () => { it("c [step a.1]", f); })']) {
    expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": skipped }), skipped).toEqual([
      "s.ts:1: [step a.1] is on a case that is skipped, todo or only",
      "a: step a.1 has no browser case tagged [step a.1]",
    ]);
  }
});

test("the positive control: a tag in a describe title, or in a comment or a string that is no title, covers nothing", () => {
  const sources = ['describe("d [step a.1]", () => { it("c", f); });', '// it("c [step a.1]", f)', 'const s = "it(\'c [step a.1]\')";'];
  for (const source of sources) expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": source }), source).toEqual(["a: step a.1 has no browser case tagged [step a.1]"]);
});

test("the positive control: a tag in a template title counts, and one in a substituted part does not", () => {
  expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": "it(`c ${defect} [step a.1]`, f);" })).toEqual([]);
  expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": "it(`c ${'[step a.1]'}`, f);" })).toEqual(["a: step a.1 has no browser case tagged [step a.1]"]);
  // Text on both sides of a substitution is two segments, never one tag: `[step a.${2}1]` is not `[step a.21]` and not `[step a.1]`.
  expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": "it(`c [step a.${2}1]`, f);" })).toEqual(["a: step a.1 has no browser case tagged [step a.1]"]);
  expect(check({ a: page("# A", ...list("a", 1)) }, { "s.ts": "it(`c [step a.1${2}]`, f);" })).toEqual(["a: step a.1 has no browser case tagged [step a.1]"]);
});

test("the positive control: a list is found with its indented lines, images, blank lines and lazy lines, and a fence is no list", () => {
  const body = ["", "<!-- bw-web-steps: a -->", "", "1. One,", "lazy line.", "", "   ![image](x.png)", "", "2. Two:", "", "   1. not a step", "", "3. Three.", "", "Next paragraph.", "", "1. Another list, not web."];
  expect(lists("a", page("# A", ...body))).toEqual({ lists: [{ id: "a", steps: 3 }], problems: [] });
  expect(lists("a", page("# A", "```", "1. in a fence", "```", ...list("a", 1)))).toEqual({ lists: [{ id: "a", steps: 1 }], problems: [] });
});

test("the positive control: a marker with no list, a repeated name, a name from another page and an item out of order are each named", () => {
  expect(lists("a", page("# A", "", "<!-- bw-web-steps: a -->", "", "Text."))).toEqual({ lists: [], problems: ["a:3: the marker a is not followed by a numbered list that starts at 1", "a: no <!-- bw-web-steps: ... --> line"] });
  expect(lists("a", page("# A", ...list("a", 1), ...list("a", 1))).problems).toEqual(["a:7: the list name a is used twice"]);
  expect(lists("a", page("# A", ...list("b", 1))).problems).toEqual(["a:3: the list name b is neither a nor a-<n>"]);
  expect(lists("a", page("# A", ...list("a-1", 2), ...list("a-2", 1))).problems).toEqual([]);
  expect(lists("a", page("# A", "", "<!-- bw-web-steps: a -->", "", "1. One.", "2. Two.", "", "4. Four.")).problems).toEqual(["a:8: the list a has item 4 where 3 is next"]);
});

test("the positive control: an item with the wrong number is named with no blank line before it, and at any sibling indent", () => {
  const marked = (...body: string[]) => lists("a", page("# A", "", "<!-- bw-web-steps: a -->", "", ...body));
  expect(marked("1. One.", "3. Three.").problems).toEqual(["a:6: the list a has item 3 where 2 is next"]);
  expect(marked("1. One.", "  3. Three.").problems).toEqual(["a:6: the list a has item 3 where 2 is next"]);
  expect(marked("1. One.", " 2. Two.", "  3. Three.")).toEqual({ lists: [{ id: "a", steps: 3 }], problems: [] });
});

const FENCE_REFUSED = (line: number) => `a:${line}: the list a-1 holds a fenced block, which this check does not read; put it outside the list`;

test("the positive control: a fence inside a marked list is refused wherever it stands, and the next marked list is still read", () => {
  const two = (...first: string[]) => lists("a", page("# A", "", "<!-- bw-web-steps: a-1 -->", "", ...first, "", "<!-- bw-web-steps: a-2 -->", "", "1. Other.", "2. Other."));
  // Right after an item, indented inside one, and after a blank line: each ends list a-1 with the same named problem.
  expect(two("1. One.", "```text", "  2. code only", "```")).toEqual({ lists: [{ id: "a-1", steps: 1 }, { id: "a-2", steps: 2 }], problems: [FENCE_REFUSED(6)] });
  expect(two("1. One.", "   ```text", "   2. code only", "   ```")).toEqual({ lists: [{ id: "a-1", steps: 1 }, { id: "a-2", steps: 2 }], problems: [FENCE_REFUSED(6)] });
  expect(two("1. One.", "", "~~~text", "2. code", "~~~")).toEqual({ lists: [{ id: "a-1", steps: 1 }, { id: "a-2", steps: 2 }], problems: [FENCE_REFUSED(7)] });
});

test("the positive control: a fence is closed only by its own kind and length, and one never closed is a problem", () => {
  const after = (...fenced: string[]) => lists("a", page("# A", ...fenced, ...list("a", 2)));
  const found = { lists: [{ id: "a", steps: 2 }], problems: [] };
  expect(after("```text", "~~~", "```")).toEqual(found);
  expect(after("````text", "```", "````")).toEqual(found);
  expect(after("~~~text", "```", "~~~")).toEqual(found);
  // Never closed: the rest of the page is fenced text, so the problem is named and the list after it is not found.
  expect(after("```text", "~~~")).toEqual({ lists: [], problems: ["a: a fenced block is never closed", "a: no <!-- bw-web-steps: ... --> line"] });
  expect(after("````text", "```")).toEqual({ lists: [], problems: ["a: a fenced block is never closed", "a: no <!-- bw-web-steps: ... --> line"] });
});

test("the negative control: an item nested under a step, indented to its text, is that step's content and no step", () => {
  const marked = (...body: string[]) => lists("a", page("# A", "", "<!-- bw-web-steps: a -->", "", ...body));
  expect(marked("1. One:", "   1. nested", "   2. nested", "2. Two.")).toEqual({ lists: [{ id: "a", steps: 2 }], problems: [] });
});

test("the positive control: a numbered list in a web section with no marker is named, in each of the three ways a section is found", () => {
  const unmarked = (p: string, ...lines: string[]) => lists(p, page(...lines)).problems.filter((x) => x.includes("has no <!--"));
  expect(unmarked("a", "# A", "## Web", "", "Steps:", "", "1. One.")).toEqual(["a:6: a numbered list in a web section has no <!-- bw-web-steps: ... --> line above it"]);
  expect(unmarked("a", "# A", "## Web client", "", "### Several", "", "1. One.")).toHaveLength(1);
  expect(unmarked("a", "# A", "", "**Web.** Steps:", "", "1. One.")).toHaveLength(1);
  expect(unmarked("quick-start-web", "# Q", "", "1. One.")).toHaveLength(1);
  // Up to two spaces before the number is still a list that starts there.
  expect(unmarked("a", "# A", "## Web", "", "  1. One.")).toHaveLength(1);
  expect(unmarked("a", "# A", "## Web", "", " 1. One.")).toHaveLength(1);
});

test("the negative control: a numbered list outside a web section needs no marker, and a web section ends at the next heading of its level", () => {
  const unmarked = (p: string, ...lines: string[]) => lists(p, page(...lines)).problems.filter((x) => x.includes("has no <!--"));
  expect(unmarked("a", "# A", "## Command line", "", "1. One.", "## Web", "", "Text only.", "## Next", "", "1. One.")).toEqual([]);
  expect(unmarked("a", "# A", "", "**Web.** Steps:", "## Other", "1. One.")).toEqual([]);
});

test("the mutation: with the only tag of a real step taken out, the guard names that step and nothing new else", () => {
  const pages = realPages();
  const specs = realSpecs();
  const held = new Map<string, string[]>();
  for (const [name, text] of Object.entries(specs)) for (const t of tags(text, name)) held.set(`${t.id}.${t.step}`, [...(held.get(`${t.id}.${t.step}`) ?? []), name]);
  const only = [...held].find(([, files]) => files.length === 1);
  expect(only, "no real step has exactly one tag").toBeDefined();
  const [id, [file]] = only!;
  const before = check(pages, specs);
  // The same step may be tagged twice in one file: every occurrence goes.
  const after = check(pages, { ...specs, [file!]: specs[file!]!.replaceAll(`[step ${id}]`, "") });
  expect(after.filter((p) => !before.includes(p))).toEqual([expect.stringContaining(`step ${id} has no browser case`)]);
});
