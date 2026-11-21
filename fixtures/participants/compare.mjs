// The comparison of the operation page with the server's events (ginsys/bronzeward#359), pure so
// that web-watch.mjs and the unit cases (scripts/test/watch-compare-test.mjs) share it.
//
// At the end of a watch the page replaces every row it received by the server's rows, so a
// comparison made then compares the server with itself. The driver therefore logs each row the page
// adds while the watch runs (an observer on the events list), and the rules read that log as well as
// the final rows:
//   log   entries {op: "add", id, text, received, dateTime}, {op: "clear"}, {op: "mark", name: "stop"|"restart"}
//   final the rows shown after the watch, {id, text, received, dateTime}
//         (dateTime is the row's <time datetime> attribute as written: a received row carries the
//         browser's arrival instant, a final row the server's `at`)
//   server the events the server serves, {id, type, at, data}, in order
// Every function returns rows {rule, ok, detail}.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const row = (rule, ok, detail) => ({ rule, ok, detail: ok ? "" : detail });

// compare: R1 no event shown twice without a clear between; R2 what was received is, in order, a
// subsequence of what the server sent; R3 after the end the page shows exactly the server's events,
// each row's <time dateTime> equal to the event's `at`, and no receipt prefix; R4 (an outage run only) rows were received both
// before the stop and after the restart; R5 each shown line names what its event holds.
export function compare({ server, log, final, outage = false }) {
  const out = [];
  const ids = server.map((e) => e.id);
  const seen = new Set();
  let twice;
  let next = 0;
  let stray;
  // A clear lets the page show the list again from the start, so each rule restarts there; a violation
  // before a clear stays found.
  for (const x of log) {
    if (x.op === "clear") {
      seen.clear();
      next = 0;
    } else if (x.op === "add") {
      if (seen.has(x.id) && twice === undefined) twice = x.id;
      seen.add(x.id);
      if (!x.received) continue;
      const at = ids.indexOf(x.id, next);
      if (at < 0 && stray === undefined) stray = x.id;
      if (at >= 0) next = at + 1;
    }
  }
  out.push(row("R1", twice === undefined, `${twice} was shown twice`));
  out.push(row("R2", stray === undefined, `${stray} is not a later event the server sent`));

  const wrong = [];
  if (final.length !== server.length) wrong.push(`${final.length} rows for ${server.length} events`);
  server.forEach((e, i) => {
    const f = final[i];
    if (f === undefined) return;
    if (f.id !== e.id) wrong.push(`row ${i} is ${f.id}, not ${e.id}`);
    else if (f.received || f.dateTime !== e.at) wrong.push(`${e.id} is not shown with the server's time`);
  });
  out.push(row("R3", wrong.length === 0, wrong.join("; ")));

  if (outage) {
    const at = (name) => log.findIndex((x) => x.op === "mark" && x.name === name);
    const [stop, restart] = [at("stop"), at("restart")];
    const got = (a, b) => log.slice(a, b).filter((x) => x.op === "add" && x.received).length;
    const [before, after] = [stop < 0 ? 0 : got(0, stop), restart < 0 ? 0 : got(restart + 1)];
    out.push(row("R4", before > 0 && after > 0, `${before} rows received before the stop and ${after} after the restart`));
  }

  // A state change line names its transition as the page words it, `from → to`, and its cause; any
  // other line names its type. Every line the page showed counts, a received one before its replacement
  // included.
  const need = (e) => {
    const d = e.data ?? {};
    if (e.type !== "operation-state" || typeof d.to !== "string") return [e.type];
    return [typeof d.from === "string" ? `${d.from} → ${d.to}` : d.to, ...(typeof d.cause === "string" ? [d.cause.trim()] : [])];
  };
  const byId = new Map(server.map((e) => [e.id, e]));
  const shown = [...final, ...log.filter((x) => x.op === "add")];
  const unnamed = new Set();
  for (const f of shown) {
    const e = byId.get(f.id);
    if (e !== undefined && !need(e).every((s) => f.text.includes(s))) unnamed.add(e.id);
  }
  for (const e of server) if (!final.some((f) => f.id === e.id)) unnamed.add(e.id);
  out.push(row("R5", unnamed.size === 0, `${[...unnamed].join(", ")} not named in the line shown`));
  return out;
}

// flow: the S4 flow's counts, read from the server. F1 one commitment among the operation's events;
// F2 the expected number of attempts; F3 one approval entry naming the plan (the approval is not
// among the operation's events); F4 the ownership transition comes before the first attempt.
export function flow({ events, approvals, attempts }) {
  const at = (type) => events.findIndex((e) => e.type === type);
  const count = (type) => events.filter((e) => e.type === type).length;
  const [transition, attempt] = [at("ownership-transition"), at("attempt")];
  return [
    row("F1", count("commitment") === 1, `${count("commitment")} commitments`),
    row("F2", count("attempt") === attempts, `${count("attempt")} attempts, expected ${attempts}`),
    row("F3", approvals === 1, `${approvals} approval entries`),
    row("F4", transition >= 0 && (attempt < 0 || transition < attempt), "no ownership transition before the first attempt"),
  ];
}

// The command line, run by `session` on web-watch.mjs's <dir>/web-watch.json:
//   node compare.mjs <web-watch.json> [--outage] [--flow <attempts> <approvals>]
// It prints one `rule<TAB>ok|FAIL<TAB>detail` line per rule and exits 1 when any failed.
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [file, ...opts] = process.argv.slice(2);
  const w = JSON.parse(readFileSync(file, "utf8"));
  const i = opts.indexOf("--flow");
  const rows = [
    ...compare({ server: w.server, log: w.log, final: w.final, outage: opts.includes("--outage") }),
    ...(i < 0 ? [] : flow({ events: w.server, approvals: Number(opts[i + 2]), attempts: Number(opts[i + 1]) })),
  ];
  for (const r of rows) console.log(`${r.rule}\t${r.ok ? "ok" : "FAIL"}\t${r.detail}`);
  process.exit(rows.every((r) => r.ok) ? 0 : 1);
}
