// C3: alternative 3a. The references are read off the AST of the `yaml` package (web's own
// dependency) with parseAllDocuments. A text it reports errors for (a draft in progress) falls back
// to C0, the scanner on main.
//
// `yaml` is resolved from web/node_modules without a link: createRequire anchored at web/.
import { createRequire } from "node:module";
import { references as scanner } from "../../../web/src/position.ts";

// The name grammar of the server (internal/ingest/declare.go nameSyntax): a tagged value outside it names nothing.
const NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;
const { parseAllDocuments, visit } =createRequire(new URL("../../../web/package.json", import.meta.url))("yaml");

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
// Whether `references` fell back to the scanner on main for this text.
export const fellBack = (text) => parseAllDocuments(text, { uniqueKeys: false }).some((d) => d.errors.length > 0);
