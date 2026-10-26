// AUTH-02, AUTH-03, AUTH-05 and AUTH-10 (conformance/cases/): the fixture page signed in with
// tokens the fixture issuer mints, against a server that trusts it.
import { randomUUID } from "node:crypto";
import { $, browser, expect } from "@wdio/globals";
import { NOT_ACCEPTED, REVOKED } from "../../src/app.tsx";
import { FIXTURE_API, FIXTURE_ISSUER } from "../auth.conf.ts";
import { counts, fixtureSignIn, mint, storageEmpty } from "./helpers.ts";

const reading = () => $('[data-testid="reading"]');
const ALL_ROLES = "viewer or author or publisher or approver or recovery-admin";

// No new sign-in follows a refusal: the provider sees no second authorization request.
async function noLoop() {
  await browser.pause(2000);
  expect((await counts()).authorize).toBe(1);
}

describe("a token the server does not accept", () => {
  const cases: [string, string | undefined][] = [
    ["expired", "This browser's own reading: the token expired at "],
    ["wrong-issuer", "This browser's own reading: the token is from "],
    ["wrong-audience", "This browser's own reading: the token is not issued for bronzeward; the identity provider needs an audience mapping."],
    ["over-lifetime", undefined],
    ["alg-none", undefined],
    ["groups-not-array", undefined],
  ];
  for (const [defect, read] of cases) {
    it(`AUTH-02 ${defect}: 401, one renewal attempt, the browser's own reading`, async () => {
      expect(await fixtureSignIn("h-viewer", { defect })).toBe(NOT_ACCEPTED);
      if (read === undefined) expect(await reading().isExisting()).toBe(false);
      else expect(await reading().getText()).toContain(read);
      expect((await counts()).refresh).toBe(1);
      await noLoop();
    });
  }
});

describe("a token the server refuses with 403", () => {
  it("AUTH-03: without the needed group, the route's roles are named", async () => {
    expect(await fixtureSignIn("h-viewer", { "without-group": "bw-viewers" })).toBe(`Needs ${ALL_ROLES}; you are signed in as h-viewer`);
  });

  it("AUTH-05: a revoked identity is told so, and is not signed in again", async () => {
    const res = await fetch(`${FIXTURE_API}/api/v1/identity-revocations`, {
      method: "POST",
      headers: { Authorization: `Bearer ${mint("h-recovery")}`, "Idempotency-Key": randomUUID(), "Content-Type": "application/json" },
      body: JSON.stringify({ iss: FIXTURE_ISSUER, sub: "h-author", reason: "AUTH-05" }),
    });
    // A revocation is permanent; the second browser of the run meets the first one's.
    expect([201, 409]).toContain(res.status);
    expect(await fixtureSignIn("h-author")).toBe(REVOKED);
    await noLoop();
  });

  it("AUTH-10: a subject in deniedSubjects is handled as AUTH-05", async () => {
    expect(await fixtureSignIn("h-approver")).toBe(REVOKED);
    await noLoop();
  });
});

afterEach(storageEmpty);
