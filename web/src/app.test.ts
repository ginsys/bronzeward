import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, test } from "vitest";
import { Screen, type View } from "./app.tsx";
import { WriteNotices, WritesProvider } from "./forms.tsx";
import type { Client } from "./request.ts";
import { problemFields, problemText, REPEATING, REPLAYED, unknown } from "./states.tsx";
import { Write, Writes } from "./write.ts";

const shown = (view: View) => renderToStaticMarkup(createElement(Screen, { view }));

const problem = (code: string, status: number, detail?: string) => ({
  type: `urn:bronzeward:problem:${code}`,
  title: "A dependency is unavailable",
  status,
  instance: "req-1",
  ...(detail === undefined ? {} : { detail }),
});

test("§8.2: a problem shows its title, its detail when present and its instance", () => {
  const auth = problem("dependency-unavailable", 503, "authentication could not reach the identity provider or the database; nothing was committed");
  expect(problemText({ kind: "unavailable", status: 503, problem: auth })).toBe(
    "Not read: A dependency is unavailable (authentication could not reach the identity provider or the database; nothing was committed). Request req-1.",
  );
  expect(problemText({ kind: "refused", status: 401, problem: problem("other", 401) })).toBe("Not read: A dependency is unavailable. Request req-1.");
  expect(problemText({ kind: "refused", status: 502, problem: null })).toBe("Not read: the server answered 502.");
});

test("§8.2: the problem fields shown under a 403 row's, a 401's or a revocation's own message", () => {
  const forbidden = { ...problem("forbidden", 403, "needs viewer"), title: "Forbidden", roles: ["viewer"] };
  expect(problemFields({ kind: "refused", status: 403, problem: forbidden })).toBe("Not read: Forbidden (needs viewer). Request req-1.");
  expect(problemFields({ kind: "revoked", problem: { ...problem("identity-revoked", 403), title: "Identity revoked" } })).toBe("Not read: Identity revoked. Request req-1.");
  expect(problemFields({ kind: "unauthenticated", problem: null })).toBeUndefined();
  expect(problemFields({ kind: "transport" })).toBeUndefined();
});

// The shell's 403 rows: shell.test.ts.
test("§8.2: each refusal view renders the problem fields under its own message", () => {
  const fields = "Not read: Forbidden (needs viewer). Request req-1.";
  const paragraph = `<p data-testid="problem">${fields}</p>`;
  expect(shown({ kind: "sign-in", message: "W1", problem: fields, signIn: () => {} })).toContain(paragraph);
  expect(shown({ kind: "failed", message: "revoked", problem: fields })).toContain(paragraph);
  expect(shown({ kind: "failed", message: "revoked" })).not.toContain('data-testid="problem"');
});

test("B11: a write whose outcome is unknown keeps its notice on the views that end the shell", async () => {
  const writes = new Writes(() => "https://idp.invalid SYNTH-sub-1", async () => {});
  writes.client = { currentEpoch: "e1", attempt: async () => undefined } as unknown as Client;
  await writes.submit(new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" }));
  const on = (view: View) => renderToStaticMarkup(createElement(WritesProvider, { value: writes }, createElement(Screen, { view })));
  expect(on({ kind: "failed", message: "revoked" })).toContain('data-testid="write-notice"');
  expect(on({ kind: "sign-in", message: "W1", signIn: () => {} })).toContain('data-testid="write-notice"');
  // Sign out, then the unload guard's dialog cancelled: the page stays on this view.
  expect(on({ kind: "signing-out" })).toContain('data-testid="write-notice"');
});

test("B11: a dropped attempt shows the unknown notice at once, while the automatic repeats still run", async () => {
  const writes = new Writes(() => "https://idp.invalid SYNTH-sub-1", async () => {});
  let sends = 0;
  let answer: (a: unknown) => void = () => {};
  writes.client = {
    currentEpoch: "e1",
    attempt: (_m: string, path: string) => {
      if (path === "/recovery") return Promise.resolve({ status: 200, headers: new Headers({ "Bronzeward-Epoch": "e1" }), data: {} });
      // The first send drops; the repeat gets no answer until the test gives one.
      return ++sends === 1 ? Promise.resolve(undefined) : new Promise((r) => (answer = r));
    },
  } as unknown as Client;
  const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });
  const done = writes.submit(w);
  const notices = () => renderToStaticMarkup(createElement(WritesProvider, { value: writes }, createElement(WriteNotices, { go: () => {} })));
  for (let i = 0; i < 20 && sends < 2; i++) await Promise.resolve();
  expect(sends).toBe(2);
  expect(w.outcome).toBeUndefined();
  const html = notices();
  expect(html).toContain(unknown("the connection dropped"));
  expect(html).toContain(REPEATING);
  expect(html).toContain(`Key ${w.key}`);
  expect(html).not.toContain("Dismiss");
  answer({ status: 201, headers: new Headers(), data: { id: "c1" } });
  expect((await done).kind).toBe("done");
  // The operator saw the notice, so it shows the result until dismissed.
  expect(notices()).toContain(`Saved (the server answered 201). Key ${w.key}`);
  writes.dismiss(w);
  expect(notices()).toBe("");
});

test("B11: a write with no answer yet and no attempt in doubt shows no notice", async () => {
  const writes = new Writes(() => "https://idp.invalid SYNTH-sub-1", async () => {});
  let answer: (a: unknown) => void = () => {};
  writes.client = { currentEpoch: "e1", attempt: () => new Promise((r) => (answer = r)) } as unknown as Client;
  const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });
  const done = writes.submit(w);
  await Promise.resolve();
  expect(renderToStaticMarkup(createElement(WritesProvider, { value: writes }, createElement(WriteNotices, { go: () => {} })))).toBe("");
  answer({ status: 201, headers: new Headers(), data: { id: "c1" } });
  await done;
});

test("journeys §8 Replayed: a [Repeat exactly] from the notice shows its result there, with no form shown", async () => {
  const writes = new Writes(() => "https://idp.invalid SYNTH-sub-1", async () => {});
  let answer: unknown;
  writes.client = {
    currentEpoch: "e1",
    attempt: async (_m: string, path: string) =>
      path === "/recovery" ? { status: 200, headers: new Headers({ "Bronzeward-Epoch": "e1" }), data: {} } : answer,
  } as unknown as Client;
  const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });
  await writes.submit(w);
  answer = { status: 201, headers: new Headers({ "Idempotent-Replayed": "true" }), data: { id: "c1" } };
  await writes.submit(w);
  const html = renderToStaticMarkup(createElement(WritesProvider, { value: writes }, createElement(WriteNotices, { go: () => {} })));
  expect(html).toContain(`${REPLAYED} Saved (the server answered 201).`);
  expect(html).toContain("Dismiss");
  expect(html).not.toContain("Repeat exactly");
});

test("§7 item 4: a deprecation notice stays on the refusal that ends the shell", () => {
  const notice = 'data-testid="deprecation"';
  const with_ = (view: View) => renderToStaticMarkup(createElement(Screen, { view, notices: ["/api/v1/identity"] }));
  expect(with_({ kind: "sign-in", message: "W1", signIn: () => {} })).toContain(notice);
  expect(with_({ kind: "failed", message: "revoked" })).toContain(notice);
  expect(with_({ kind: "signing-out" })).not.toContain(notice);
});
