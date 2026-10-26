// AUTH-01 and AUTH-08 (conformance/cases/): the page signed in through the test provider, against a
// server that trusts it.
import { $, browser, expect } from "@wdio/globals";
import { SIGNED_OUT } from "../../src/app.tsx";
import { IDP_PAGE, idpEnv } from "../auth.conf.ts";
import { idpSignIn, param, storageEmpty, userId, where } from "./helpers.ts";

const issuer = idpEnv()["BW_TEST_IDP_ISSUER"]!;

describe("signed in through the test provider", () => {
  it("AUTH-01: sign in and read", async () => {
    expect(await idpSignIn("h-viewer")).toMatch(/^Server .+, API revision \d+$/);
    expect(await $('[data-testid="principal"]').getText()).toBe(`Signed in as ${userId("h-viewer")}`);
    await storageEmpty();
  });

  it("AUTH-08: sign out, then back navigation shows no data", async () => {
    await idpSignIn("h-viewer");
    await $("button=Sign out").click();
    // The provider's end-session answer goes to its login UI's logout page, which mise run
    // dev-idp does not run; that page would return the browser to the post-logout URI.
    await browser.waitUntil(async () => (await where()) === `${issuer}/ui/v2/login/logout`, { timeout: 20_000, timeoutMsg: "the page did not go to the provider's end-session" });
    expect(await param("post_logout_redirect")).toBe(`${IDP_PAGE}/signed-out`);
    await browser.url(`${IDP_PAGE}/signed-out`);
    await expect($('[data-testid="status"]')).toHaveText(SIGNED_OUT);
    expect(await $("body").getText()).not.toContain(userId("h-viewer"));
    await storageEmpty();
    // Back past the provider to the page that showed data: it starts a new sign-in instead.
    // Not browser.back(), which logs each page's address, the provider's logout token included.
    await browser.execute(() => history.go(-2));
    await browser.waitUntil(async () => (await where()) === `${issuer}/ui/v2/login/login`, { timeout: 20_000, timeoutMsg: "the page did not start a new sign-in" });
  });
});
