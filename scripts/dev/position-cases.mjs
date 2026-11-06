// Writes web/src/position-cases.json: YAML documents and the references go-yaml lists in each, for
// web/src/position.test.ts. Run it with `mise run web:position-cases`. The expectations come from
// the go-yaml oracle of experiments/scanner-cross-line (main.go, the server's go-yaml pin) and are
// never typed or edited by hand: add a document here and run the task again.
//
// A document goes in the file only when go-yaml accepts the stream: the server reads no reference
// from one it refuses. The names of the refused documents are printed so the loss shows.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const experiment = join(root, "experiments", "scanner-cross-line");
const out = join(root, "web", "src", "position-cases.json");
const NBSP = "\u00a0";

// [source, name, document]. The sets of the experiment follow these (cases.json and review.json).
const own = [
  // The documents of the first fix of ginsys/bronzeward#578 (a # in a quoted key of a flow collection)
  // and of the hosted findings on it.
  ...[
    'a: [? "x # y", !bwref x/foo]\n',
    '{? "x # y": 1, b: !bwref x/foo}\n',
    'a: {? "x # y": 1, b: !bwref x/foo}\n',
    'a: [?"x # y", !bwref x/foo]\n',
    'a: {?!t "x # y", b: !bwref x/foo}\n',
    'a: [!t "x # y", !bwref x/foo]\n',
    'a: {"k":"x # y", r: !bwref x/foo}\n',
    "a: [\"k\":'x # y', !bwref x/foo]\n",
    'a: {"k":!t "x # y", r: !bwref x/foo}\n',
    'a: {"k":&a "x # y", r: !bwref x/foo}\n',
    'a: {"k" :"x # y", r: !bwref x/foo}\n',
    'a: {[k]:"x # y", r: !bwref x/foo}\n',
    '[? "x # y", !bwref x/foo]\n',
    '--- ["a # b", !bwref x/foo]\n',
    'a: [x,\n  b, "y # z", !bwref x/foo]\n',
    'a: {"k":[], ?"x # y": 1, r: !bwref x/foo}\n',
    'a: {"k":{}, ?"x # y": 1, r: !bwref x/foo}\n',
    'a: ["k":[a], ? "x # y", !bwref x/foo]\n',
    '{"k":[],"x # y":1, r: !bwref x/foo}\n',
    "a: {\"k\":[ ], '?x # y': 1, r: !bwref x/foo}\n",
    'a: |2\n    text\n  p: [x\nb: ?"y # !bwref ghost\nc: !bwref x/foo\n',
    'a: |2\n    first\n  b: [x\nc: ?"y # !bwref ghost\nd: !bwref x/foo\n',
    'a:\n  |\n  [x\nb: ?"y # !bwref ghost\nc: !bwref x/foo\n',
    'a:\n    x\n  [y\nb: ?"z # !bwref ghost\nc: !bwref x/foo\n',
    'a: |# comment: [x\n  text\nb: ?"y # !bwref ghost\nc: !bwref x/foo\n',
    'a: ?"x # !bwref ghost\nb: !bwref x/foo\n',
    'a: x[?"y # !bwref ghost\nb: !bwref x/foo\n',
    'a: x!t "y # !bwref ghost\nb: !bwref x/foo\n',
    'a: x\n  [?"y # !bwref ghost\nb: !bwref x/foo\n',
    'a: |\n  [x\nb: ?"y # !bwref ghost\nc: !bwref x/foo\n',
    'a: |# comment: [?"y # !bwref ghost, ignored\n  text\nb: !bwref x/foo\n',
    'a: [x :"y # !bwref ghost\n  , !bwref x/foo]\n',
    "a: [!t,?'u v, 'y # z', !bwref x/foo]\n",
    '- [? "x # y", !bwref x/foo]\n',
  ].map((d, i) => ["ginsys/bronzeward#578", `flow-key-${i + 1}`, d]),
  // The documents of ginsys/bronzeward#595: a tag with a comma, a verbatim tag, an empty flow
  // sequence and an alias that a colon follows.
  ...[
    'a: [!t,u "x # y", !bwref x/foo]\n',
    'a: [!<tag:yaml.org,2002:str> "x # y", !bwref x/foo]\n',
    'a: [!t [], ?"x # y", !bwref x/foo]\n',
    'a: [&k key, *k:"x # y", !bwref x/foo]\n',
    'a: {k: !t,u "x # y", j: !bwref x/foo}\n',
    "a: [!t,u 'x # y', !bwref x/foo]\n",
    'a: [\n  !t,u "x # y",\n  !bwref x/foo]\n',
    'a: [!t [], "x # y", !bwref x/foo]\n',
  ].map((d, i) => ["ginsys/bronzeward#595", i === 3 ? "alias-then-colon" : `flow-tag-${i + 1}`, d]),
  // The block scalar header: go-yaml reads the indicators only directly after | or >, and a digit in
  // the header comment is no indentation indicator (found in the hosted review of
  // ginsys/bronzeward#601).
  ["ginsys/bronzeward#601", "header-comment-digit", 'a: | # 4\n  "q # r\nb: !bwref x/foo\n'],
  ["ginsys/bronzeward#601", "header-digit-then-comment", "a: |4 # x\n    t\nb: !bwref x/foo\n"],
  ["ginsys/bronzeward#601", "header-digit-then-sign", "a: |2-\n  t\nb: !bwref x/foo\n"],
  ["ginsys/bronzeward#601", "header-sign-then-digit", "a: |-2\n  t\nb: !bwref x/foo\n"],
  ["ginsys/bronzeward#601", "header-comment-digit-sign", "a: | # c 1-\n  t\nb: !bwref x/foo\n"],
  // A document marker ends every node, also the column of the entry above (the local review of
  // ginsys/bronzeward#600).
  ["ginsys/bronzeward#600", "marker-resets-entry-column", 'a:\n  b: x\n--- text\n "x\n---\nb: !bwref x/foo\n'],
  // Indentation counts U+0020 only, and an empty line ends at a line break after spaces only.
  ["ginsys/bronzeward#601", "nbsp-key-after-block", `a: |\n x\n${NBSP}b: !bwref x/foo\n`],
  // go-yaml refuses a tab, or a line of U+00A0 alone, in the block's indentation column: the
  // streams `a: |`, ` x`, then a line with a tab before `b:`, or a line of U+00A0 alone, are left out
  // by the refusal rule below. Past the indentation both are block text.
  ["ginsys/bronzeward#601", "nbsp-only-line-in-block", `a: |\n  x\n  ${NBSP}\n  y\nb: !bwref x/foo\n`],
  ["ginsys/bronzeward#601", "nbsp-only-last-line-in-block", `a: |\n  x\n  ${NBSP}\nb: !bwref x/foo\n`],
  ["ginsys/bronzeward#601", "tab-only-line-in-block", "a: |\n  x\n  \t\n  y\nb: !bwref x/foo\n"],
  ["ginsys/bronzeward#601", "nbsp-before-tag-in-block", `a: |\n  x\n  ${NBSP}!bwref x/ghost\nb: !bwref x/foo\n`],
  // The hosted review of ginsys/bronzeward#602: the edges of the state, each with the sweep family
  // (experiments/scanner-cross-line/sets/gen-s8-state-edges.ts) that holds more of its kind.
  // (B) a colon after properties alone closes a key that starts at the first property.
  ["ginsys/bronzeward#602", "edge-properties-only-key", '- &e: |\n   "x\n- !bwref x/foo\n'],
  // (A) a blank line, or a comment-only line, closes no block entry.
  ["ginsys/bronzeward#602", "edge-blank-before-header", "outer:\n  a:\n\n    |2\n    text\n  b: !bwref x/foo\n"],
  ["ginsys/bronzeward#602", "edge-comment-before-header", "outer:\n  a:\n# c\n    |2\n    text\n  b: !bwref x/foo\n"],
  // (C) a key that closed at the end of a line takes the colon that starts the next one.
  ["ginsys/bronzeward#602", "edge-colon-on-next-line", 'a: {? "k"\n  :!bwref x/foo}\n'],
  // (D) a comment starts right after any token that is not a plain scalar.
  ["ginsys/bronzeward#602", "edge-comment-after-bracket", 'a: [[]# , "\n  , !bwref x/foo]\n'],
  ["ginsys/bronzeward#602", "edge-comment-after-quote", 'a: ["q"# , "\n  , !bwref x/foo]\n'],
  // (E) a comment line ends a plain key, so a `:` that starts the next line is the value indicator.
  ["ginsys/bronzeward#602", "edge-colon-after-comment", "a: [? k\n  # c\n  :!bwref x/bar, !bwref x/foo]\n"],
  ["ginsys/bronzeward#602", "edge-colon-after-tab-comment", "a: [? k\t# c\n  :!bwref x/bar, !bwref x/foo]\n"],
  // (F) a Unicode space after a document marker is text, not a blank.
  ["ginsys/bronzeward#602", "edge-marker-nbsp", '--- a: |\n  "x\nb: !bwref x/foo\n'],
  ["ginsys/bronzeward#602", "edge-dots-nbsp", '... a: |\n  "x\nb: !bwref x/foo\n'],
  // (G) tag-like text in a declared literal's comment is no tag, and does not hide the real one after it.
  ["ginsys/bronzeward#602", "edge-fake-tag-in-comment", "cfg: |\n  # !bwref \"\n  a: 1\nb: !bwref x/foo\nc: '\"'\n"],
  // (I) the carriage return of a CRLF is part of the line break: a marker, a header's comment and a
  // literal's text end at it.
  ["ginsys/bronzeward#602", "edge-crlf-marker", "---\r\n!bwref x/foo\r\n"],
  ["ginsys/bronzeward#602", "edge-crlf-marker-in-literal", "cfg: |\r\n  ---\r\n  !bwref x/foo\r\n"],
  ["ginsys/bronzeward#602", "edge-crlf-header-comment", 'a: | # c\r\n  [x\r\nb: ?"y # !bwref ghost\r\nc: !bwref x/foo\r\n'],
  // (J) a tag spelled through a declared handle is read as main read it, by its `!bwref`.
  ["ginsys/bronzeward#602", "edge-handle-tag", "%TAG !r! !\n---\na: !r!bwref x/foo\n"],
  // (H) a flow collection used as a block key keeps its first column across its members.
  ["ginsys/bronzeward#602", "edge-flow-key-holder", '[a, b]: |\n  "x\nfoo: !bwref x/foo\n'],
  // ginsys/bronzeward#637: the server refuses a tag on a key (tag-placement), so the oracle lists no
  // name for an explicit key, an implicit key or an alias key of a tagged key. The controls list the
  // name. The alias key of a tagged value is a known gap: the oracle lists the value's name.
  ...[
    ["tagged-explicit-key", "? !bwref x/foo\n: v\n"],
    ["tagged-explicit-key-nested", "a:\n  ? !bwref x/foo\n  : v\n"],
    ["tagged-explicit-key-anchor", "? &e !bwref x/foo\n: v\n"],
    ["tagged-explicit-key-next-line", "?\n  !bwref x/foo\n: v\n"],
    ["tagged-explicit-key-in-sequence", "- ? !bwref x/foo\n  : v\n"],
    ["tagged-explicit-key-flow", "{? !bwref x/foo : v}\n"],
    ["tagged-explicit-key-flow-sequence", "[? !bwref x/foo]\n"],
    ["tagged-implicit-key", "!bwref x/foo: v\n"],
    ["tagged-alias-key-of-tagged-key", "? &k !bwref x/foo\n: v\n*k : w\n"],
    ["tagged-alias-key-of-tagged-value", "a: &k !bwref x/foo\n*k : v\n"],
    ["explicit-key-value-control", "? a\n: !bwref x/foo\n"],
    ["sequence-entry-control", "- !bwref x/foo\n"],
  ].map(([name, doc]) => ["ginsys/bronzeward#637", name, doc]),
];

function read(name) {
  return JSON.parse(readFileSync(join(experiment, "sets", name), "utf8"));
}
const sets = [
  ...read("cases.json").map((c) => [`experiments/scanner-cross-line/sets/cases.json (${c.issue})`, c.name, c.doc]),
  ...read("review.json").map((d, i) => ["experiments/scanner-cross-line/sets/review.json", `review-${i + 1}`, d]),
];

const seen = new Set();
const all = [...own, ...sets].filter(([, , doc]) => !seen.has(doc) && seen.add(doc));

const work = mkdtempSync(join(process.env.TMPDIR ?? tmpdir(), "position-cases-"));
try {
  writeFileSync(join(work, "in.json"), JSON.stringify(all.map(([, , doc]) => doc)));
  execFileSync("go", ["-C", experiment, "run", ".", join(work, "in.json"), join(work, "out.json")], { stdio: "inherit" });
  const results = JSON.parse(readFileSync(join(work, "out.json"), "utf8"));
  if (results.length !== all.length) throw new Error(`oracle answered ${results.length} documents for ${all.length}`);
  const cases = [];
  const refused = [];
  all.forEach(([source, name, doc], i) => {
    if (results[i].err !== "") refused.push(`${name} (${source})`);
    else cases.push({ name, source, doc, want: results[i].tags });
  });
  const pin = /go\.yaml\.in\/yaml\/v3 (v\S+)/.exec(readFileSync(join(experiment, "go.mod"), "utf8"))?.[1];
  if (pin === undefined) throw new Error("go.yaml.in/yaml/v3 is not in experiments/scanner-cross-line/go.mod");
  const file = {
    generated: "mise run web:position-cases (scripts/dev/position-cases.mjs); do not edit by hand",
    oracle: `go.yaml.in/yaml/v3 ${pin}`,
    cases,
  };
  writeFileSync(out, `${JSON.stringify(file, null, 1)}\n`);
  console.log(`${cases.length} documents written to web/src/position-cases.json (oracle ${file.oracle})`);
  if (refused.length > 0) console.log(`${refused.length} refused by go-yaml, left out:\n  ${refused.join("\n  ")}`);
} finally {
  rmSync(work, { recursive: true, force: true });
}
