// The parsed fields of a configuration as a read-only tree (docs/spec/configuration-editing.md §4 item 2, §5, §7;
// ginsys/bronzeward#1270): a heading per object, a labelled item per field with the schema's description as its help
// text, a list as rows, a map as rows of key and value. Every item is text, never an input, so nothing here edits:
// the editing build (ginsys/bronzeward#1271) draws its controls from the same tree. A value never goes into an
// attribute, an id or a title: ids come from the position in the tree, and the values are text children only.
//
// Group headings are real headings in order: the panel's title is an h3, so a document is an h4, its groups h5 and
// h6 (a deeper object is shown as a row of its parent's group, not as a heading beyond h6).

import { useId, type ReactNode, type Ref } from "react";
import type { DocView, Field, Node } from "./config-tree.ts";

const OBJECT_LEVELS = ["h5", "h6"] as const;

function Value({ node }: { node: Node }): ReactNode {
  switch (node.kind) {
    case "text":
      return <span className="value">{node.text}</span>;
    case "ref":
      return <>Reference <code>{node.name}</code></>;
    case "object":
      return <Items fields={node.fields} />;
    case "list":
      return <ol className="rows">{node.rows.map((r, i) => <li key={i}><Value node={r} /></li>)}</ol>;
    case "map":
      return (
        <dl className="rows">
          {node.rows.map((r, i) => <div key={i}><dt>{r.key}</dt><dd><Value node={r.node} /></dd></div>)}
        </dl>
      );
  }
}

// The fields of an object inside a row, or a run of plain fields of a group: a label, its value, its help text tied to the value.
function Items({ fields, prefix }: { fields: readonly Field[]; prefix?: string }) {
  const id = useId();
  return (
    <dl>
      {fields.map((f, i) => {
        const help = `${prefix ?? id}-h${i}`;
        return (
          <div key={i}>
            <dt>{f.name}</dt>
            <dd aria-describedby={f.help === undefined ? undefined : help}><Value node={f.node} /></dd>
            {f.help !== undefined && <dd id={help} className="help">{f.help}</dd>}
          </div>
        );
      })}
    </dl>
  );
}

// A group: its plain fields in one list, an object field as a group of its own with a heading, in document order.
function Group({ fields, depth }: { fields: readonly Field[]; depth: number }) {
  const id = useId();
  const runs: (readonly Field[] | Field)[] = [];
  for (const f of fields) {
    const last = runs[runs.length - 1];
    if (f.node.kind === "object" && depth < OBJECT_LEVELS.length) runs.push(f);
    else if (Array.isArray(last)) (last as Field[]).push(f);
    else runs.push([f]);
  }
  return (
    <>
      {runs.map((run, i) => {
        if (Array.isArray(run)) return <Items key={i} fields={run as readonly Field[]} prefix={`${id}-${i}`} />;
        const f = run as Field;
        const Heading = OBJECT_LEVELS[depth]!;
        const head = `${id}-${i}`;
        return (
          <section key={i} aria-labelledby={head}>
            <Heading id={head}>{f.name}</Heading>
            {f.help !== undefined && <p className="help" id={`${head}-help`}>{f.help}</p>}
            <Group fields={(f.node as Extract<Node, { kind: "object" }>).fields} depth={depth + 1} />
          </section>
        );
      })}
    </>
  );
}

/** The documents of a parsed view, one section each, then each one's Other settings with their raw lines. */
export function ConfigFields({ docs, label, ref }: { docs: readonly DocView[]; label: string; ref?: Ref<HTMLDivElement> }) {
  const id = useId();
  // `ref` is where focus lands when the fields appear in place of a button that took it away (tabIndex -1: reachable by a move, not by Tab).
  return (
    <div ref={ref} tabIndex={-1} className="config-fields" data-testid="config-fields" role="group" aria-label={label}>
      {docs.length === 0 && <p>There are no settings in this text: it holds comments only, or nothing.</p>}
      {docs.map((d, i) => (
        <section key={i} aria-labelledby={`${id}-d${i}`}>
          <h4 id={`${id}-d${i}`}>{d.heading}</h4>
          <Group fields={d.fields} depth={0} />
          {d.other.length > 0 && (
            <section aria-labelledby={`${id}-o${i}`}>
              <h5 id={`${id}-o${i}`}>Other settings</h5>
              <ul className="other">
                {d.other.map((o, j) => <li key={j}><code>{o.path}</code><pre>{o.lines}</pre></li>)}
              </ul>
            </section>
          )}
        </section>
      ))}
    </div>
  );
}
