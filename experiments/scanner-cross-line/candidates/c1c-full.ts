// C1c with all of position.ts's exports, for running web's own position tests against it
// (see run/web-tests).
import { setOnlyAtNodeStart } from "./c1-state.ts";

setOnlyAtNodeStart(true);
export * from "./c1-state.ts";
