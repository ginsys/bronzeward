// Usage: node pins.mjs
// Unit cases for model.mjs. Each case is a document and the verdict that the model must give. It needs no go and
// no oracle, and it writes nothing. The exit status is 1 when any case fails.
import { model } from "./model.mjs";

let bad = 0;
let total = 0;
function expect(name, got, want) {
  total++;
  if (got === want) console.log(`pins: ${name}: ${got} ok`);
  else {
    console.error(`pins: FAIL ${name}: got ${got}, want ${want}`);
    bad++;
  }
}

// The scan loop fails closed. A bound that the scan reaches gives "guard-exhausted", never "ok" or "misplaced".
// The control: the same document under the default bound reaches the end of the stream. Both call forms of compare.mjs
// are covered: model(doc) and model(doc, { guardDelete: true }).
const small = "a: b\n";
expect("bound: default bound reaches the end of the stream", model(small).verdict, "ok");
expect("bound: default bound, guard on", model(small, { guardDelete: true }).verdict, "ok");
expect("bound: a bound of 1 step is reached (guard off)", model(small, { maxSteps: 1 }).verdict, "guard-exhausted");
expect("bound: a bound of 1 step is reached (guard on)", model(small, { guardDelete: true, maxSteps: 1 }).verdict, "guard-exhausted");
expect("bound: a bound of 1000 steps is not reached", model(small, { maxSteps: 1000 }).verdict, "ok");
const bugged = "[? b]: !bwref x/foo\n";
expect("bound: the document is misplaced under the default bound", model(bugged).verdict, "misplaced");
// A reached bound hides no misplaced verdict at any bound. Find the smallest bound that reaches the end of the stream, then
// check every bound below it. The control: some of those bounds already hold events when the loop stops, so a check that
// only looks at an empty event list would pass on the early bounds and fail on these. A run with no such bound fails.
let needed = 0;
for (let n = 1; n < 1000 && needed === 0; n++) if (model(bugged, { maxSteps: n }).verdict !== "guard-exhausted") needed = n;
expect("bound: some bound reaches the end of the stream", needed > 1, true);
expect("bound: the smallest such bound gives misplaced", model(bugged, { maxSteps: needed }).verdict, "misplaced");
let belowExhausted = 0;
let belowWithEvents = 0;
for (let n = 1; n < needed; n++) {
  const m = model(bugged, { maxSteps: n });
  if (m.verdict === "guard-exhausted") belowExhausted++;
  else console.error(`pins: bound ${n} gave ${m.verdict}`);
  if (m.events.length > 0) belowWithEvents++;
}
console.log(`pins: bound: ${needed - 1} bounds below ${needed} steps, ${belowExhausted} guard-exhausted, ${belowWithEvents} already hold events`);
expect("bound: every bound below the smallest one is guard-exhausted", belowExhausted, needed - 1);
expect("bound: at least one of those bounds already holds events", belowWithEvents > 0, true);

// Escapes in a double-quoted scalar (scannerc.go 2496-2579). K is a flow key that the bug refuses: a document that the
// scan reaches the end of gives "misplaced", and a document with a bad escape gives "error" with go-yaml's own message.
// The first four are the w688-adv documents 0-3 (go-yaml and the guarded go-yaml gave the same answer on 0-2; 3 is the control).
const K = "[? b]: !bwref x/foo\n";
function verdictOf(name, doc, want, message) {
  const m = model(doc);
  expect(name, m.verdict, want);
  if (message !== undefined) expect(`${name} (message)`, m.error, message);
}
verdictOf("escape: unknown escape \\q", `k: "\\q"\n${K}`, "error", "found unknown escape character");
verdictOf("escape: non-hex digits in \\u", `k: "\\uZZZZ"\n${K}`, "error", "did not find expected hexdecimal number");
verdictOf("escape: \\x with no digits", `k: "\\x"\n${K}`, "error", "did not find expected hexdecimal number");
verdictOf("escape: control, \\x41 is complete", `k: "\\x41"\n${K}`, "misplaced");
verdictOf("escape: \\u with 3 digits then the quote", `k: "\\u123"\n${K}`, "error", "did not find expected hexdecimal number");
verdictOf("escape: \\U with 7 digits then the quote", `k: "\\U0000004"\n${K}`, "error", "did not find expected hexdecimal number");
verdictOf("escape: \\xG1", `k: "\\xG1"\n${K}`, "error", "did not find expected hexdecimal number");
verdictOf("escape: a surrogate, \\uD800", `k: "\\uD800"\n${K}`, "error", "found invalid Unicode character escape code");
verdictOf("escape: the last surrogate, \\uDFFF", `k: "\\uDFFF"\n${K}`, "error", "found invalid Unicode character escape code");
verdictOf("escape: control, \\uD7FF is below the surrogates", `k: "\\uD7FF"\n${K}`, "misplaced");
verdictOf("escape: control, \\uE000 is above the surrogates", `k: "\\uE000"\n${K}`, "misplaced");
verdictOf("escape: above the range, \\U00110000", `k: "\\U00110000"\n${K}`, "error", "found invalid Unicode character escape code");
verdictOf("escape: control, \\U0010FFFF is the last code", `k: "\\U0010FFFF"\n${K}`, "misplaced");
verdictOf("escape: \\/ is not an escape of go-yaml v3.0.5", `k: "\\/"\n${K}`, "error", "found unknown escape character");
verdictOf("escape: a backslash at the end of the stream", 'k: "\\', "error", "found unknown escape character");
verdictOf("escape: a single-quoted scalar has no escapes", `k: '\\q'\n${K}`, "misplaced");
for (const e of ["0", "a", "b", "t", "\t", "n", "v", "f", "r", "e", " ", '"', "'", "\\", "N", "_", "L", "P"]) {
  verdictOf(`escape: control, one-character escape ${JSON.stringify(e)}`, `k: "\\${e}"\n${K}`, "misplaced");
}

// An escaped line break in a double-quoted scalar (scannerc.go 2486-2494) ends the run of non-blank characters, so a
// document indicator at column 0 of the next line is refused, as after a plain line break. The first four are the documents
// 0-3 of the w701 probe (go-yaml refuses them with this message; the model of 289531b5 accepted them); the rest are controls
// that go-yaml accepts, or refuses with the same message, and that the fix leaves alone.
const indicator = "found unexpected document indicator";
verdictOf("break: escaped break then ---", `k: "a\\\n---\nb"\n`, "error", indicator);
verdictOf("break: escaped break then ...", `k: "a\\\n...\nb"\n`, "error", indicator);
verdictOf("break: escaped break then --- before a flow key", `k: "a\\\n---\nb"\n${K}`, "error", indicator);
verdictOf("break: two escaped breaks then ---", `k: "a\\\n\\\n---\nb"\n`, "error", indicator);
verdictOf("break: control, the indicator is indented", `k: "a\\\n  ---\nb"\n${K}`, "misplaced");
verdictOf("break: control, ---x is no indicator", `k: "a\\\n---x\nb"\n${K}`, "misplaced");
verdictOf("break: control, an escaped break then text", `k: "a\\\nb"\n${K}`, "misplaced");
verdictOf("break: control, a plain break then --- (double quotes)", `k: "a\n---\nb"\n`, "error", indicator);
verdictOf("break: control, a plain break then --- (single quotes)", `k: 'a\n---\nb'\n`, "error", indicator);

// Block scalar headers (scannerc.go 2192-2281): one chomping indicator and one digit, in either order, and nothing
// else before the comment or the line break. The first four are the w688-adv documents 4-7 (4 and 5 are refused by
// go-yaml and the guarded go-yaml alike; 6 and 7 are the controls).
const refusedHeader = "did not find expected comment or line break";
verdictOf("header: two plus signs", `k: |++\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: two digits", `k: |12\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: control, a digit alone", `k: |2\n  x\n${K}`, "misplaced");
verdictOf("header: control, a sign and then a digit", `k: |+2\n  x\n${K}`, "misplaced");
verdictOf("header: control, a digit and then a sign", `k: |2+\n  x\n${K}`, "misplaced");
verdictOf("header: control, a sign alone", `k: |-\n  x\n${K}`, "misplaced");
verdictOf("header: control, folded style with a sign and a digit", `k: >-1\n  x\n${K}`, "misplaced");
verdictOf("header: two minus signs", `k: |--\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: a sign and then the other sign", `k: |+-\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: a sign, a digit and a second sign", `k: |+2-\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: a sign, a digit and a second digit", `k: |-12\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: a digit, a sign and a second digit", `k: |2-3\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: a digit, a sign and a second sign", `k: |2+-\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: folded style with two signs", `k: >++\n  x\n${K}`, "error", refusedHeader);
verdictOf("header: a zero digit alone", `k: |0\n  x\n${K}`, "error", "found an indentation indicator equal to 0");
verdictOf("header: a sign and then a zero digit", `k: |+0\n  x\n${K}`, "error", "found an indentation indicator equal to 0");

console.log(`pins: ${total - bad} of ${total} ok`);
process.exit(bad > 0 ? 1 : 0);
