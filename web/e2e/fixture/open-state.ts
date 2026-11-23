// The state a freshly opened fixture page ends in, for the acts of acts.e2e.ts: a signed-in principal, the entry form of a recovery
// start (which reads no identity), or a Sign in button that stays. A button seen for an instant is not the end: the shell can draw one
// before the entry form takes its place, so a button counts only once it has stayed for `settleMs`.
export type OpenState = "principal" | "entry" | "signin";

export interface Probe {
  principal(): Promise<boolean>;
  entry(): Promise<boolean>;
  signin(): Promise<boolean>;
}

export interface Options {
  entryOk: boolean;
  signinOk: boolean;
  timeoutMs: number;
  settleMs: number;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
}

export async function openState(p: Probe, o: Options): Promise<OpenState> {
  const now = o.now ?? Date.now;
  const sleep = o.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
  const deadline = now() + o.timeoutMs;
  let seen: number | undefined;
  while (now() < deadline) {
    if (await p.principal()) return "principal";
    if (o.entryOk && (await p.entry())) return "entry";
    if (o.signinOk && (await p.signin())) {
      seen ??= now();
      if (now() - seen >= o.settleMs) return "signin";
    } else seen = undefined;
    await sleep(100);
  }
  throw new Error("the page did not reach a state");
}
