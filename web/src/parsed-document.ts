// The parsed view of an opened imported configuration (docs/spec/configuration-editing.md §5, ginsys/bronzeward#1270): the
// contract of the machine's cluster, read through the page's own reads of the machine and its cluster (no route and no
// field of the API is new), then the schema of that contract, then the text read as fields.
//
// The view holds the stored values, so it is derived from the text and from nothing else: a memo keyed on the text, which
// is undefined as soon as the text is (Close, pagehide, sign-out, another import). The schema is all that stays loaded,
// and it holds no value of any document. Until the text is open nothing is read and the schema chunk is not fetched.

import { useEffect, useMemo, useState } from "react";
import type { components } from "./api/v1";
import { buildConfigView, noFieldsWords, noParseWords, type DocView, type NoFieldsReason } from "./config-tree.ts";
import { schemaFor, type FormSchema, type NoForm } from "./talos-schema.ts";
import { useBody } from "./titles.tsx";

type S = components["schemas"];
const enc = encodeURIComponent;

/** Reading the fields; the text shown with one sentence that says why there are no fields; or the fields. */
export type Parsed =
  | { readonly state: "reading" }
  | { readonly state: "none"; readonly sentence: string }
  | { readonly state: "fields"; readonly docs: readonly DocView[] };

/**
 * The contract of the machine's cluster, as the cluster records it: undefined while a read is pending, null where the
 * machine or its cluster gave no body. With `wanted` false nothing is read.
 */
export function useContract(machine: string | null | undefined, wanted: boolean): string | null | undefined {
  const m = useBody<S["MachineBody"]>(wanted && machine != null ? `/machines/${enc(machine)}` : undefined);
  const c = useBody<S["ClusterBody"]>(typeof m?.cluster === "string" ? `/clusters/${enc(m.cluster)}` : undefined);
  if (machine == null) return null;
  if (m === undefined || m === null) return m;
  return c === undefined || c === null ? c : c.contract;
}

type Found = FormSchema | NoForm | { readonly form: false; readonly reason: NoFieldsReason };

/** The parsed view of `text` for the cluster of `machine`; undefined while there is no text. */
export function useParsedDocument(text: string | undefined, machine: string | null | undefined): Parsed | undefined {
  const open = text !== undefined;
  const contract = useContract(machine, open);
  const [found, setFound] = useState<Found>();
  useEffect(() => {
    setFound(undefined);
    if (!open || contract === undefined) return;
    if (contract === null) return setFound({ form: false, reason: "contract-unread" });
    let live = true;
    void schemaFor(contract).then((r) => live && setFound(r));
    return () => {
      live = false;
    };
  }, [open, contract]);
  return useMemo((): Parsed | undefined => {
    if (text === undefined) return undefined;
    if (found === undefined) return { state: "reading" };
    if (!found.form) return { state: "none", sentence: noFieldsWords(found.reason) };
    const v = buildConfigView(text, found);
    return v.ok ? { state: "fields", docs: v.docs } : { state: "none", sentence: noParseWords(v) };
  }, [text, found]);
}
