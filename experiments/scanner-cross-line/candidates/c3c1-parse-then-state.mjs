// C3+C1: alternative 3a with C1c (state in tags(), a !bwref only at a node start) as the fallback,
// instead of the scanner on main. The question it answers: is the parse worth its cost once the
// scanner is right on its own?
//
// `yaml` is resolved from web/node_modules without a link: createRequire anchored at web/.
import { createRequire } from "node:module";
import { references as scanner } from "./c1c-state-nodestart.mjs";

// The name grammar of the server (internal/ingest/declare.go nameSyntax): a tagged value outside it names nothing.
const NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;
const { parseAllDocuments, visit } = createRequire(new URL("../../../web/package.json", import.meta.url))("yaml");

export function references(text) {
  const docs = parseAllDocuments(text, { uniqueKeys: false });
  if (docs.some((d) => d.errors.length > 0)) return scanner(text);
  const found = new Set();
  for (const doc of docs) {
    visit(doc, {
      Alias: () => visit.SKIP,
      Scalar(_, node) {
        if (node.tag === "!bwref" && NAME.test(String(node.value))) found.add(String(node.value));
      },
    });
  }
  return [...found];
}
