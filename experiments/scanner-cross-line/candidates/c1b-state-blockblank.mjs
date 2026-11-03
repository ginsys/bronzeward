// C1b: C1 that also blanks the content of block scalars before the tags are matched, so a
// `!bwref` inside the text of a block scalar names no reference (it is part of a string).
import { references, setBlankBlock } from "./c1-state.ts";

setBlankBlock(true);
export { references };
