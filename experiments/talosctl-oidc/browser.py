#!/usr/bin/env python3
"""Submit one synthetic Keycloak login form for the CLI's real PKCE flow."""

from html.parser import HTMLParser
import http.cookiejar
import json
import os
from pathlib import Path
import sys
from urllib.parse import urlencode, urljoin, urlsplit
from urllib.request import build_opener, HTTPRedirectHandler, HTTPCookieProcessor, ProxyHandler, Request


def origin(url):
    parsed = urlsplit(url)
    if parsed.username or parsed.password or parsed.hostname != "127.0.0.1":
        raise ValueError("fixture browser permits only explicit loopback origins")
    return parsed.scheme, parsed.hostname, parsed.port


allowed = {origin(os.environ["BW_IDP_URL"]), origin(os.environ["BW_CALLBACK_URL"])}
idp_origin = origin(os.environ["BW_IDP_URL"])


class LoopbackCookies(http.cookiejar.DefaultCookiePolicy):
    """Model browser Secure cookies on the single disposable loopback IdP."""

    def set_ok(self, cookie, request):
        return origin(request.full_url) == idp_origin and super().set_ok(cookie, request)

    def return_ok(self, cookie, request):
        return origin(request.full_url) == idp_origin and super().return_ok(cookie, request)

    def return_ok_secure(self, cookie, request):
        # Keycloak 26.8.1 treats loopback as a secure context. Python's default
        # jar only permits Secure cookies over HTTPS/WSS. Never relax another
        # origin (including the CLI callback, whose port differs from the IdP).
        if request.type == "http" and origin(request.full_url) == idp_origin:
            return True
        return super().return_ok_secure(cookie, request)


def check(url):
    if origin(url) not in allowed:
        raise ValueError("fixture browser refused a different origin")
    return url


class Redirects(HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return super().redirect_request(request, fp, code, msg, headers, check(newurl))


class LoginForm(HTMLParser):
    def __init__(self):
        super().__init__()
        self.action = None
        self.fields = {}
        self.inside = False

    def handle_starttag(self, tag, attributes):
        a = dict(attributes)
        if tag == "form" and a.get("id") == "kc-form-login":
            if self.action is not None:
                raise ValueError("more than one login form")
            self.action, self.inside = a.get("action"), True
        if self.inside and tag == "input" and a.get("type") == "hidden" and a.get("name"):
            self.fields[a["name"]] = a.get("value", "")

    def handle_endtag(self, tag):
        if tag == "form":
            self.inside = False


def main():
    diagnostic = {"steps": []}
    jar = http.cookiejar.CookieJar(LoopbackCookies())
    opener = build_opener(ProxyHandler({}), Redirects(), HTTPCookieProcessor(jar))
    try:
        with opener.open(check(sys.argv[1]), timeout=15) as response:
            form = LoginForm()
            form.feed(response.read().decode())
            page_url = response.url
            diagnostic["steps"].append({"stage": "form", "status": response.status,
                                        "cookies": [{"name": c.name, "secure": c.secure} for c in jar]})
        if not form.action:
            raise RuntimeError("synthetic login form not found")
        action = check(urljoin(page_url, form.action))
        if origin(action) != idp_origin:
            raise ValueError("credentials may only be submitted to the IdP")
        form.fields.update(username=os.environ["BW_LOGIN_USER"], password=os.environ["BW_LOGIN_PASSWORD"])
        request = Request(action, data=urlencode(form.fields).encode())
        with opener.open(request, timeout=15) as response:
            response.read()
            reached_callback = origin(response.url) == origin(os.environ["BW_CALLBACK_URL"])
            diagnostic["steps"].append({"stage": "submit", "status": response.status,
                                        "reached_callback": reached_callback})
            if not reached_callback:
                raise RuntimeError("login did not reach the CLI callback")
        diagnostic["result"] = "pass"
    except Exception as error:
        diagnostic.update(result="failed", error_type=type(error).__name__)
        raise
    finally:
        # Upstream intentionally drops browser stdout/stderr. Retain only safe
        # metadata; no URLs, cookie values, credentials or exception payloads.
        with Path(os.environ["BW_BROWSER_DIAGNOSTIC"]).open("x") as output:
            json.dump(diagnostic, output, indent=2)


if __name__ == "__main__":
    main()
