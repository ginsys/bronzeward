// C2: alternative 2, as an upper bound for an own tokenizer. The scanner on main with its line by
// line comment masking replaced by the comments of the `yaml` package's concrete syntax tree
// (Parser). The tree keeps comments as tokens, also for text the package refuses; everything else
// is main's tags().
import { references, setCommentsFromCst } from "./c1-state.ts";

setCommentsFromCst(true);
export { references };
