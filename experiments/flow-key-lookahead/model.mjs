// A pure model of the token lookahead of go-yaml v3.0.5 (scannerc.go), written to predict one
// refusal: a flow key that go-yaml consumes before it reads the ':' that follows it.
//
// model(doc) returns { verdict, events, error }.
//   verdict "misplaced": a KEY or BLOCK-MAPPING-START token was inserted at a negative queue
//                        position (the token it belongs before was already consumed). go-yaml then
//                        appends it at the queue end, and its parser refuses the document.
//   verdict "error":     the scanner raised an error (the same error in the patched go-yaml).
//   verdict "ok":        no misplaced insert and no scanner error.
//   verdict "unsupported": the document uses a construct the model does not port (directives).
// The model scans the whole stream. It does not model the parser, so a refusal by the parser for a
// reason that is not a misplaced token (for example "[,]") is "ok" here.
//
// opts.guardDelete = true applies the one-line patch to yaml_parser_decrease_flow_level that
// ./patched-oracle.sh builds. The patched model must never report "misplaced".
//
// The driver follows the parser: before it consumes a token it runs yaml_parser_fetch_more_tokens
// once (peek_token), then skips the head token (skip_token). Comments only attach to tokens in
// go-yaml, so the model skips a comment to the end of its line.
//
// Not ported: directives ("unsupported"), and the characters that the go-yaml reader refuses
// (control characters, readerc.go) or that are not valid UTF-8. The model does not check them.

class ScanError extends Error {}
class Unsupported extends Error {}

const ALPHA = /[0-9A-Za-z_-]/;
const URI = /[0-9A-Za-z_\-;/?:@&=+$,.!~*'()[\]%]/;
// is_break in yamlprivateh.go (124-130): CR, LF, NEL (U+0085), LS (U+2028) and PS (U+2029).
const BREAKS = new Set(["\n", "\r", ...[0x85, 0x2028, 0x2029].map((c) => String.fromCodePoint(c))]);
const BOM = String.fromCodePoint(0xfeff);
const isBreak = (c) => BREAKS.has(c);
const isZ = (c) => c === "\0";
const isBlank = (c) => c === " " || c === "\t";
const isBlankz = (c) => isBlank(c) || isBreak(c) || isZ(c);
const isBreakz = (c) => isBreak(c) || isZ(c);

export function model(doc, opts = {}) {
  const guardDelete = opts.guardDelete === true;
  // go-yaml counts characters (mark.index, mark.column), not UTF-16 units: work on code points. An initial
  // byte order mark is dropped by yaml_parser_determine_encoding (readerc.go 47-73) before any mark moves.
  const chars = Array.from(doc);
  let idx = chars[0] === BOM ? 1 : 0;
  let line = 0;
  let col = 0;
  const at = (k = 0) => (idx + k < chars.length ? chars[idx + k] : "\0");
  const mark = () => ({ index: idx, line, col });
  const skip = () => {
    idx++;
    col++;
  };
  const skipLine = () => {
    idx += at() === "\r" && at(1) === "\n" ? 2 : 1;
    col = 0;
    line++;
  };

  const tokens = [];
  let head = 0;
  let parsed = 0;
  let flow = 0;
  let indent = -1;
  const indents = [];
  let allowed = false;
  let started = false;
  const simpleKeys = [];
  const byTok = new Map();
  const events = [];

  const fail = (msg) => {
    throw new ScanError(msg);
  };
  const insert = (pos, typ) => {
    tokens.push({ typ });
    if (pos < 0) return;
    tokens.splice(head + pos, 0, tokens.pop());
  };
  const queued = () => parsed + (tokens.length - head);

  function isValid(key) {
    if (!key.possible) return false;
    if (key.mark.line < line || key.mark.index + 1024 < idx) {
      if (key.required) fail("could not find expected ':'");
      key.possible = false;
      return false;
    }
    return true;
  }
  function saveSimpleKey() {
    const required = flow === 0 && indent === col;
    if (!allowed) return;
    removeSimpleKey();
    const key = { possible: true, required, token_number: queued(), mark: mark() };
    simpleKeys[simpleKeys.length - 1] = key;
    byTok.set(key.token_number, simpleKeys.length - 1);
  }
  function removeSimpleKey() {
    const key = simpleKeys[simpleKeys.length - 1];
    if (!key.possible) return;
    if (key.required) fail("could not find expected ':'");
    key.possible = false;
    byTok.delete(key.token_number);
  }
  function increaseFlowLevel() {
    simpleKeys.push({ possible: false, required: false, token_number: queued(), mark: mark() });
    flow++;
  }
  function decreaseFlowLevel() {
    if (flow === 0) return;
    flow--;
    const last = simpleKeys.length - 1;
    const num = simpleKeys[last].token_number;
    if (!guardDelete || byTok.get(num) === last) byTok.delete(num);
    simpleKeys.pop();
  }
  function rollIndent(column, number, typ) {
    if (flow > 0 || indent >= column) return;
    indents.push(indent);
    indent = column;
    let pos = number;
    if (number > -1) {
      pos = number - parsed;
      if (pos < 0) events.push({ kind: "indent", number, parsed, line, col });
    }
    insert(pos, typ);
  }
  function unrollIndent(column) {
    if (flow > 0) return;
    while (indent > column) {
      insert(-1, "BLOCK-END");
      indent = indents.pop();
    }
  }

  function scanToNextToken() {
    for (;;) {
      if (col === 0 && at() === BOM) skip();
      while (at() === " " || ((flow > 0 || !allowed) && at() === "\t")) skip();
      if (at() === "#") while (!isBreakz(at())) skip();
      if (isBreak(at())) {
        skipLine();
        if (flow === 0) allowed = true;
      } else break;
    }
  }

  function docIndicator() {
    return col === 0 && ((at() === "-" && at(1) === "-" && at(2) === "-") || (at() === "." && at(1) === "." && at(2) === ".")) && isBlankz(at(3));
  }

  function scanAnchor() {
    skip();
    let n = 0;
    while (ALPHA.test(at())) {
      skip();
      n++;
    }
    const c = at();
    if (n === 0 || !(isBlankz(c) || "?:,]}%@`".includes(c))) fail("did not find expected alphabetic or numeric character");
  }
  function scanUri(headLen) {
    let n = 0;
    while (URI.test(at())) {
      if (at() === "%") {
        if (!/[0-9A-Fa-f]/.test(at(1)) || !/[0-9A-Fa-f]/.test(at(2))) fail("did not find URI escaped octet");
        skip();
        skip();
        skip();
      } else skip();
      n++;
    }
    if (headLen === 0 && n === 0) fail("did not find expected tag URI");
  }
  function scanTag() {
    if (at(1) === "<") {
      skip();
      skip();
      scanUri(0);
      if (at() !== ">") fail("did not find the expected '>'");
      skip();
    } else {
      let handle = at();
      skip();
      while (ALPHA.test(at())) {
        handle += at();
        skip();
      }
      if (at() === "!") {
        handle += "!";
        skip();
      }
      if (handle.length > 1 && handle.endsWith("!")) scanUri(0);
      else scanUri(handle.length);
    }
    if (!isBlankz(at())) fail("did not find expected whitespace or line break");
  }
  function scanFlowScalar(single) {
    const q = single ? "'" : '"';
    skip();
    for (;;) {
      if (docIndicator()) fail("found unexpected document indicator");
      if (isZ(at())) fail("found unexpected end of stream");
      while (!isBlankz(at())) {
        if (single && at() === "'" && at(1) === "'") {
          skip();
          skip();
        } else if (at() === q) break;
        else if (!single && at() === "\\" && isBreak(at(1))) {
          skip();
          skipLine();
        } else if (!single && at() === "\\") {
          const e = at(1);
          skip();
          skip();
          const w = e === "x" ? 2 : e === "u" ? 4 : e === "U" ? 8 : 0;
          for (let i = 0; i < w; i++) skip();
        } else skip();
      }
      if (at() === q) break;
      while (isBlank(at()) || isBreak(at())) {
        if (isBlank(at())) skip();
        else skipLine();
      }
    }
    skip();
  }
  function scanBlockScalar() {
    skip();
    let increment = 0;
    for (let i = 0; i < 2; i++) {
      if (at() === "+" || at() === "-") skip();
      else if (/[1-9]/.test(at())) {
        increment = Number(at());
        skip();
      } else if (at() === "0") fail("found an indentation indicator equal to 0");
    }
    while (isBlank(at())) skip();
    if (at() === "#") while (!isBreakz(at())) skip();
    if (!isBreakz(at())) fail("did not find expected comment or line break");
    if (isBreak(at())) skipLine();
    let blockIndent = increment > 0 ? (indent >= 0 ? indent + increment : increment) : 0;
    const breaks = () => {
      let max = 0;
      for (;;) {
        while ((blockIndent === 0 || col < blockIndent) && at() === " ") skip();
        if (col > max) max = col;
        if (!isBreak(at())) break;
        skipLine();
      }
      if (blockIndent === 0) {
        blockIndent = Math.max(max, indent + 1, 1);
      }
    };
    breaks();
    while (col === blockIndent && !isZ(at())) {
      while (!isBreakz(at())) skip();
      if (isBreak(at())) skipLine();
      breaks();
    }
  }
  function scanPlainScalar() {
    const need = indent + 1;
    let leading = false;
    for (;;) {
      if (docIndicator()) break;
      if (at() === "#") break;
      while (!isBlankz(at())) {
        const c = at();
        if ((c === ":" && isBlankz(at(1))) || (flow > 0 && ",?[]{}".includes(c))) break;
        skip();
      }
      if (!(isBlank(at()) || isBreak(at()))) break;
      while (isBlank(at()) || isBreak(at())) {
        if (isBlank(at())) {
          if (leading && col < need && at() === "\t") fail("found a tab character that violates indentation");
          skip();
        } else {
          leading = true;
          skipLine();
        }
      }
      if (flow === 0 && col < need) break;
    }
    if (leading) allowed = true;
  }

  function fetchValue() {
    const key = simpleKeys[simpleKeys.length - 1];
    if (isValid(key)) {
      const pos = key.token_number - parsed;
      if (pos < 0) events.push({ kind: "key", number: key.token_number, parsed, line, col });
      insert(pos, "KEY");
      rollIndent(key.mark.col, key.token_number, "BLOCK-MAPPING-START");
      key.possible = false;
      byTok.delete(key.token_number);
      allowed = false;
    } else {
      if (flow === 0) {
        if (!allowed) fail("mapping values are not allowed in this context");
        rollIndent(col, -1, "BLOCK-MAPPING-START");
      }
      allowed = flow === 0;
    }
    skip();
    insert(-1, "VALUE");
  }

  function fetchNextToken() {
    if (!started) {
      started = true;
      indent = -1;
      simpleKeys.push({ possible: false, required: false, token_number: 0, mark: mark() });
      allowed = true;
      insert(-1, "STREAM-START");
      return;
    }
    scanToNextToken();
    unrollIndent(col);
    const c = at();
    if (isZ(c)) {
      if (col !== 0) {
        col = 0;
        line++;
      }
      unrollIndent(-1);
      removeSimpleKey();
      allowed = false;
      insert(-1, "STREAM-END");
    } else if (col === 0 && c === "%") {
      throw new Unsupported("directive");
    } else if (col === 0 && c === "-" && at(1) === "-" && at(2) === "-" && isBlankz(at(3))) {
      unrollIndent(-1);
      removeSimpleKey();
      allowed = false;
      skip(), skip(), skip();
      insert(-1, "DOCUMENT-START");
    } else if (col === 0 && c === "." && at(1) === "." && at(2) === "." && isBlankz(at(3))) {
      unrollIndent(-1);
      removeSimpleKey();
      allowed = false;
      skip(), skip(), skip();
      insert(-1, "DOCUMENT-END");
    } else if (c === "[" || c === "{") {
      saveSimpleKey();
      increaseFlowLevel();
      allowed = true;
      skip();
      insert(-1, c === "[" ? "FLOW-SEQUENCE-START" : "FLOW-MAPPING-START");
    } else if (c === "]" || c === "}") {
      removeSimpleKey();
      decreaseFlowLevel();
      allowed = false;
      skip();
      insert(-1, c === "]" ? "FLOW-SEQUENCE-END" : "FLOW-MAPPING-END");
    } else if (c === ",") {
      removeSimpleKey();
      allowed = true;
      skip();
      insert(-1, "FLOW-ENTRY");
    } else if (c === "-" && isBlankz(at(1))) {
      if (flow === 0) {
        if (!allowed) fail("block sequence entries are not allowed in this context");
        rollIndent(col, -1, "BLOCK-SEQUENCE-START");
      }
      removeSimpleKey();
      allowed = true;
      skip();
      insert(-1, "BLOCK-ENTRY");
    } else if (c === "?" && (flow > 0 || isBlankz(at(1)))) {
      if (flow === 0) {
        if (!allowed) fail("mapping keys are not allowed in this context");
        rollIndent(col, -1, "BLOCK-MAPPING-START");
      }
      removeSimpleKey();
      allowed = flow === 0;
      skip();
      insert(-1, "KEY");
    } else if (c === ":" && (flow > 0 || isBlankz(at(1)))) {
      fetchValue();
    } else if (c === "*" || c === "&") {
      saveSimpleKey();
      allowed = false;
      scanAnchor();
      insert(-1, c === "*" ? "ALIAS" : "ANCHOR");
    } else if (c === "!") {
      saveSimpleKey();
      allowed = false;
      scanTag();
      insert(-1, "TAG");
    } else if ((c === "|" || c === ">") && flow === 0) {
      removeSimpleKey();
      allowed = true;
      scanBlockScalar();
      insert(-1, "SCALAR");
    } else if (c === "'" || c === '"') {
      saveSimpleKey();
      allowed = false;
      scanFlowScalar(c === "'");
      insert(-1, "SCALAR");
    } else if (
      !(isBlankz(c) || "-?:,[]{}#&*!|>'\"%@`".includes(c)) ||
      (c === "-" && !isBlank(at(1))) ||
      (flow === 0 && (c === "?" || c === ":") && !isBlankz(at(1)))
    ) {
      saveSimpleKey();
      allowed = false;
      scanPlainScalar();
      insert(-1, "SCALAR");
    } else fail("found character that cannot start any token");
  }

  // yaml_parser_fetch_more_tokens
  function fetchMoreTokens() {
    for (;;) {
      if (head < tokens.length - 2) {
        const i = byTok.get(parsed);
        if (i === undefined) break;
        if (!isValid(simpleKeys[i])) break;
      }
      fetchNextToken();
    }
  }

  try {
    for (let guard = 0; guard < 4 * chars.length + 64; guard++) {
      fetchMoreTokens();
      const tok = tokens[head++];
      parsed++;
      if (tok.typ === "STREAM-END") break;
    }
  } catch (e) {
    if (e instanceof Unsupported) return { verdict: "unsupported", events, error: e.message };
    if (e instanceof ScanError) return { verdict: "error", events, error: e.message };
    throw e;
  }
  return { verdict: events.length > 0 ? "misplaced" : "ok", events, error: "" };
}
