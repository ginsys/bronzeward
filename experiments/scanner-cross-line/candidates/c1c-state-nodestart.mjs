// C1c: C1 that also lists a `!bwref` only where the scan reads it as a node's property. A `!bwref`
// in the text of a plain, quoted or block scalar names no reference (it is part of a string).
import { references, setOnlyAtNodeStart } from "./c1-state.ts";

setOnlyAtNodeStart(true);
export { references };
