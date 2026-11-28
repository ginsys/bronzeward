// The layout blocks of the console layout design (docs/design/console-layout.md §2, ginsys/bronzeward#1255): a panel with a titled
// band, a page header with the page's actions, and a note drawn as one visible line with the whole note behind a native
// disclosure. The action variants are Action's (shell.tsx). The draft page, the cluster page, the Clusters and Machines lists, the
// operation page (ginsys/bronzeward#1257) and the six list pages (ginsys/bronzeward#1259) use them so far; the stylesheet is app.css,
// "Console layout blocks".

import type { ReactNode } from "react";
import { PageNote } from "./page-note.tsx";
import { ABOUT_PAGE } from "./words.ts";

// A panel holds one thing. Its band has the title on the left and the panel's actions on the right, so an action sits under the
// heading it belongs to. `label` names the section for a screen reader and a test where it differs from the title.
export function Panel({ title, actions, label = title, children }: { title: string; actions?: ReactNode; label?: string; children?: ReactNode }) {
  return (
    <section className="panel" aria-label={label}>
      <div className="panel-head">
        <h3>{title}</h3>
        {actions !== undefined && actions !== false && <div className="actions">{actions}</div>}
      </div>
      {children}
    </section>
  );
}

// The top of a page: the title and its lines on the left, the page's one primary action, if it has one, on the right. The markup
// order is the Tab order, so the action follows the title's lines and comes before the note.
export function PageHead({ heading, actions, children }: { heading: ReactNode; actions?: ReactNode; children?: ReactNode }) {
  return (
    <div className="page-head">
      <div className="titles">
        <h2>{heading}</h2>
        {children}
      </div>
      {actions !== undefined && actions !== false && <div className="actions">{actions}</div>}
    </div>
  );
}

// A page's note as one line and the whole note behind "What is this page?". The line is written from the page's own sentences
// (`firstSentence`, `lastSentence`), never new wording; the whole note is PageNote's, with the same parts and the same test id.
// A note that is one sentence is its own line: the disclosure would only repeat it, so it is drawn as the plain page note.
// A link the note carries goes in as `children`, as PageNote's does; it is then part of the whole note and never of the line.
export function Note({ line, parts, children }: { line: string; parts: readonly (string | false | undefined)[]; children?: ReactNode }) {
  if (children === undefined && parts.filter((p): p is string => typeof p === "string").join(" ") === line) return <PageNote parts={parts} />;
  return (
    <>
      <p className="lede" data-testid="page-note-line">{line}</p>
      <details className="about">
        <summary>{ABOUT_PAGE}</summary>
        <PageNote parts={parts}>{children}</PageNote>
      </details>
    </>
  );
}

// The top of a list page: the page header with the page's actions, then the first sentence of its note as the line (the whole note, and
// the link it carries as `children`, behind "What is this page?").
export const ListHead = ({ heading, actions, note, children }: { heading: string; actions?: ReactNode; note: string; children?: ReactNode }) => (
  <>
    <PageHead heading={heading} actions={actions} />
    <Note line={firstSentence(note)} parts={[note]}>{children}</Note>
  </>
);

// The first and the last sentence of a note, as literal slices of it. A note of one sentence gives that sentence for both.
export const firstSentence = (note: string): string => {
  const i = note.indexOf(". ");
  return i < 0 ? note : note.slice(0, i + 1);
};
export const lastSentence = (note: string): string => {
  const i = note.lastIndexOf(". ", note.length - 2);
  return i < 0 ? note : note.slice(i + 2);
};
