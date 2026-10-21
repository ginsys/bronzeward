"""Secure loopback cookie regression without starting an IdP or using secrets."""

import http.cookiejar
import os
import unittest
from urllib.request import Request

os.environ["BW_IDP_URL"] = "http://127.0.0.1:18080"
os.environ["BW_CALLBACK_URL"] = "http://127.0.0.1:18081/callback"
import browser  # noqa: E402


def request_cookie(policy, url):
    jar = http.cookiejar.CookieJar(policy)
    cookie = http.cookiejar.Cookie(
        version=0, name="AUTH_SESSION_ID", value="synthetic", port=None,
        port_specified=False, domain="127.0.0.1", domain_specified=False,
        domain_initial_dot=False, path="/", path_specified=True, secure=True,
        expires=None, discard=True, comment=None, comment_url=None, rest={})
    jar.set_cookie_if_ok(cookie, Request(os.environ["BW_IDP_URL"]))
    request = Request(url)
    jar.add_cookie_header(request)
    return request.get_header("Cookie")


class CookiesTest(unittest.TestCase):
    def test_default_jar_reproduces_missing_cookie(self):
        self.assertIsNone(request_cookie(http.cookiejar.DefaultCookiePolicy(), os.environ["BW_IDP_URL"]))

    def test_fixture_idp_receives_secure_cookie(self):
        self.assertEqual(request_cookie(browser.LoopbackCookies(), os.environ["BW_IDP_URL"]),
                         "AUTH_SESSION_ID=synthetic")

    def test_other_origins_do_not_receive_cookie(self):
        for url in [os.environ["BW_CALLBACK_URL"], "http://127.0.0.1:18082/", "https://127.0.0.1:18080/"]:
            with self.subTest(url=url):
                self.assertIsNone(request_cookie(browser.LoopbackCookies(), url))

    def test_external_navigation_is_refused(self):
        with self.assertRaises(ValueError):
            browser.check("https://example.test/")
        with self.assertRaises(ValueError):
            browser.check("http://127.0.0.1:18082/")


if __name__ == "__main__":
    unittest.main()
