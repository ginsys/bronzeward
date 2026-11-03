// C3b: a variant of C3. The references are read off the AST of `yaml` parseAllDocuments even when
// the library reports errors (it keeps a partial tree). Only a text whose tree holds no reference
// at all, and that has errors, falls back to C0.
//
// `yaml` is resolved from web/node_modules without a link: createRequire anchored at web/.
import { createRequire } from "node:module";
import { references as scanner } from "../../../web/src/position.ts";

// The name grammar of the server (internal/ingest/declare.go nameSyntax): a tagged value outside it names nothing.
const NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;
const { parseAllDocuments, visit } = createRequire(new URL("../../../web/package.json", import.meta.url))("yaml");

export function references(text) {
  const docs = parseAllDocuments(text, { uniqueKeys: false });
  const found = new Set();
  for (const doc of docs) {
    visit(doc, {
      Alias: () => visit.SKIP,
      Scalar(_, node) {
        if (node.tag === "!bwref" && NAME.test(String(node.value))) found.add(String(node.value));
      },
    });
  }
  if (found.size === 0 && docs.some((d) => d.errors.length > 0)) return scanner(text);
  return [...found];
}
