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
expect("bound: a reached bound hides no misplaced verdict", model(bugged, { maxSteps: 2 }).verdict, "guard-exhausted");
expect("bound: the document is misplaced under the default bound", model(bugged).verdict, "misplaced");

console.log(`pins: ${total - bad} of ${total} ok`);
process.exit(bad > 0 ? 1 : 0);
