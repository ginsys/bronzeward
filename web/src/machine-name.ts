// The grammar of a machine's name (persistence and API §3.5), in the one place the console reads it: the
// views show a received name only when it matches (client contracts A9 item 1), and onboarding names a machine
// by its node's hostname only when it matches. A hostname of one or more labels joined by a dot, 253 characters
// at most; a label is 1 to 63 lowercase letters, digits and hyphens, neither end a hyphen; with a dot, the last
// label is not all digits, so that an address is no name. No `m` flag, so `$` refuses a trailing line feed.
export const MACHINE_NAME = /^(?=.{1,253}$)(?!.*\.[0-9]+$)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;
