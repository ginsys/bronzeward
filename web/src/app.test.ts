import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, test } from "vitest";
import { problemFields, problemText, Screen, type View } from "./app.tsx";

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

test("§8.2: each refusal view renders the problem fields under its own message", () => {
  const fields = "Not read: Forbidden (needs viewer). Request req-1.";
  const paragraph = `<p data-testid="problem">${fields}</p>`;
  expect(shown({ kind: "signed-in", sub: "u", status: "Needs viewer; you are signed in as u", problem: fields, signOut: () => {} })).toContain(paragraph);
  expect(shown({ kind: "sign-in", message: "W1", problem: fields, config: undefined as never, provider: undefined as never })).toContain(paragraph);
  expect(shown({ kind: "failed", message: "revoked", problem: fields })).toContain(paragraph);
  expect(shown({ kind: "failed", message: "revoked" })).not.toContain('data-testid="problem"');
});
