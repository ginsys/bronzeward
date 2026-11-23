// Finding a control on a page of a fixture run (ginsys/bronzeward#365). A control is found by the forms the e2e cases already use:
// button=<name>, a=<name>, [data-testid=...], form[aria-label=...], section[aria-label=...], input[name=...][value=...]. It is
// returned only when exactly one element matches inside its scope. With none or several, the error lists every control of the
// scope by kind and name, so a run that stops says what the page offered and needs no second run to find out.
import { browser } from "@wdio/globals";

type Scope = WebdriverIO.Element | ChainablePromiseElement;

const offered = async (scope: WebdriverIO.Element): Promise<string[]> =>
  browser.execute((s: HTMLElement) =>
    [...s.querySelectorAll<HTMLElement>("button, a, input, textarea, select, form[aria-label], section[aria-label], [data-testid]")].map((e) => {
      const t = e.tagName.toLowerCase();
      const off = (e as HTMLInputElement).disabled === true ? " (off)" : "";
      if (t === "input") return `input name=${e.getAttribute("name")} value=${e.getAttribute("value")}${off}`;
      if (e.getAttribute("data-testid") !== null) return `${t} testid=${e.getAttribute("data-testid")}`;
      return `${t} "${(e.getAttribute("aria-label") ?? e.innerText).replace(/\s+/g, " ").slice(0, 80)}"${off}`;
    }), scope as unknown as HTMLElement);

export async function control(scope: Scope, how: string, what: string): Promise<WebdriverIO.Element> {
  const from = await scope;
  const found = await from.$$(how);
  if (found.length === 1) return found[0]!;
  throw new Error(`${what}: ${found.length} elements match ${how}, expected exactly one. The scope offers:\n  ${(await offered(from)).join("\n  ")}`);
}

// The named kinds of control, so a case reads as what a person does.
export const button = (scope: Scope, name: string) => control(scope, `button=${name}`, `button "${name}"`);
export const field = (scope: Scope, name: string) => control(scope, `input[name="${name}"]`, `field ${name}`);
export const choice = (scope: Scope, group: string, value: string) => control(scope, `input[type="radio"][name="${group}"][value="${value}"]`, `choice ${group}=${value}`);
export const region = (scope: Scope, label: string) => control(scope, `section[aria-label="${label}"]`, `section "${label}"`);
// An answer exists only after the server's response settles, and a press returns before that: wait until one is drawn, and only then
// insist on exactly one.
export async function shown(scope: Scope, testid: string): Promise<WebdriverIO.Element> {
  const how = `[data-testid="${testid}"]`;
  const from = await scope;
  await browser.waitUntil(async () => (await from.$$(how)).length > 0, { timeout: 60_000, interval: 100, timeoutMsg: `element ${testid} was not drawn within 60 s` });
  return control(scope, how, `element ${testid}`);
}
