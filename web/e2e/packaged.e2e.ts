// The web client's footer when the server cannot be read (client contracts §7 item 5), on the build the page was served
// from: the client's own version, and no server. The host answers every API request with a redirect the page refuses, so the
// page's first read, GET /server, has no answer. BW_EXPECT_VERSION is the version a release bundle was built at (the
// packaged-web CI job serves that bundle through its Caddyfile, BW_PACKAGED_WEB in wdio.conf.ts); unset, the page is the
// source build, which prints (devel). A bundle that is not the release's, or a page that does not print its build, fails here.
import { $, browser, expect } from "@wdio/globals";
import { settled } from "./settle.ts";

const version = process.env["BW_EXPECT_VERSION"] ?? "(devel)";

describe("the footer of a page whose server cannot be read", () => {
  it(`names the build's own version, ${version}, and no server`, async () => {
    await browser.url("/machines?cluster=c1");
    await settled("/machines?cluster=c1");
    expect(await $("footer").getText()).toBe(`Client ${version}. Server unknown`);
  });
});
