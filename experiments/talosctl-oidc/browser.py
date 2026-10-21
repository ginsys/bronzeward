#!/usr/bin/env python3
"""Submit one synthetic Keycloak login form for the CLI's real PKCE flow."""

from html.parser import HTMLParser
import http.cookiejar
import os
import sys
from urllib.parse import urlencode, urljoin, urlsplit
from urllib.request import build_opener, HTTPRedirectHandler, HTTPCookieProcessor, Request


def origin(url):
    parsed = urlsplit(url)
    if parsed.username or parsed.password or parsed.hostname != "127.0.0.1":
        raise ValueError("fixture browser permits only explicit loopback origins")
    return parsed.scheme, parsed.hostname, parsed.port


allowed = {origin(os.environ["BW_IDP_URL"]), origin(os.environ["BW_CALLBACK_URL"])}


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


opener = build_opener(Redirects(), HTTPCookieProcessor(http.cookiejar.CookieJar()))
with opener.open(check(sys.argv[1]), timeout=15) as response:
    form = LoginForm()
    form.feed(response.read().decode())
    page_url = response.url
if not form.action:
    raise SystemExit("synthetic login form not found")
form.fields.update(username=os.environ["BW_LOGIN_USER"], password=os.environ["BW_LOGIN_PASSWORD"])
request = Request(check(urljoin(page_url, form.action)), data=urlencode(form.fields).encode())
with opener.open(request, timeout=15) as response:
    response.read()
    if origin(response.url) != origin(os.environ["BW_CALLBACK_URL"]):
        raise SystemExit("login did not reach the CLI callback")
print("Synthetic browser completed the loopback callback; no token printed.")
