// ginsys/bronzeward#578 differential sweep: `?` explicit keys holding a quoted
// scalar with `#`, in block, flow-sequence and flow-map context, single-line and multi-line, with a
// reference after the key. Synthetic input only. Usage: node gen.ts <docs.json>
import { writeFileSync } from "node:fs";

// The quoted scalars: a `#` after a blank, right after the quote, at the end, with an escaped quote
// before it, and one without a `#` as a control.
const scalars = [
  `"x # y"`, `'x # y'`, `"# y"`, `'# y'`, `"x #"`, `'x #'`, `"x#y"`, `"a\\" # b"`, `'a'' # b'`, `"bar"`,
];
// What stands between `?` and the quote, and the node properties a key may carry.
const seps = ["", " ", "  ", "\t"];
const props = ["", "!t ", "&a ", "!!str "];
// The key's value: none, an empty value, or one.
const vals = ["", ":", ": 1", " : 1"];

const flowOpen = ["[", "[ ", "[x, ", "[x,", "[\n  ", "[x,\n  ", "[[", "[{a: 1}, "];
const mapOpen = ["{", "{ ", "{k: 1, ", "{k: 1,", "{\n  ", "{k: 1,\n  "];
const closeFor = (open: string) => (open.startsWith("[[") ? "]" : "");

const docs = new Set<string>();
for (const s of scalars)
  for (const sep of seps)
    for (const p of props)
      for (const v of vals) {
        const k = `?${sep}${p}${s}`;
        // Flow sequence: the reference after the key on the same line, and on the next line.
        for (const o of flowOpen) {
          const c = closeFor(o);
          docs.add(`a: ${o}${k}${v}${c}, !bwref x/foo]\n`);
          docs.add(`a: ${o}${k}${v}${c},\n  !bwref x/foo]\n`);
        }
        // Flow map: the reference as a later value, same line and next line; the key's own value
        // as the reference.
        for (const o of mapOpen) {
          docs.add(`a: ${o}${k}${v}, b: !bwref x/foo}\n`);
          docs.add(`a: ${o}${k}${v},\n  b: !bwref x/foo}\n`);
          docs.add(`a: ${o}${k}: !bwref x/foo, b: 1}\n`);
        }
        // Block: the key at the top level and nested, the reference as the key's value and as a
        // later entry, with and without a comment after the key.
        const bk = `?${sep === "" ? " " : sep}${p}${s}`;
        docs.add(`${bk}\n: !bwref x/foo\n`);
        docs.add(`${bk} # c\n: !bwref x/foo\n`);
        docs.add(`${bk}\n: 1\nb: !bwref x/foo\n`);
        docs.add(`x:\n  ${bk}\n  : 1\n  b: !bwref x/foo\n`);
        docs.add(`- ${bk}\n  : 1\n- !bwref x/foo\n`);
        docs.add(`${bk}${v}\nb: !bwref x/foo\n`);
      }
writeFileSync(process.argv[2]!, JSON.stringify([...docs]));
console.log(`docs=${docs.size}`);
