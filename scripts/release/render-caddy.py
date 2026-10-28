#!/usr/bin/env python3
"""Render the reference Caddy configuration from web/headers.json (ginsys/bronzeward#262 P5).

The configuration is client contracts §10.1-§10.2 for Caddy: TLS for the site, /api/v1/ and /livez
to the server, every other path from the web files with the application as fallback, and the
header set of headers.json on every page. Deployment values stay Caddy environment placeholders,
so one rendering serves every deployment. Usage: render-caddy.py <headers.json> > Caddyfile
"""

import json
import re
import sys

HEAD = """\
# Rendered from web/headers.json by scripts/release/render-caddy.py; render again rather than edit.
# The reference hosting of client contracts §10.1-§10.2. Caddy reads these environment variables:
#   BW_SITE_ADDRESS     the site, as https://bronzeward.example.com; Caddy obtains its certificate
#   BW_SERVER           the Bronzeward server's listen address, as 127.0.0.1:8080
#   BW_WEB_ROOT         the directory holding this archive's dist/ files
#   BW_ISSUER_ORIGIN    the identity provider's issuer origin, as https://idp.example.com
#   BW_PROVIDER_ORIGINS the issuer's origin and those of the token and revocation endpoints its
#                       discovery document names, space-separated (client contracts §10.2)
{$BW_SITE_ADDRESS} {
\t@server path /api/v1/* /livez
\thandle @server {
\t\treverse_proxy {$BW_SERVER}
\t}
\thandle {
\t\theader {
"""

TAIL = """\
\t\t}
\t\t# Whole files only, as the test host: a refused range would answer without these headers.
\t\trequest_header -Range
\t\troot * {$BW_WEB_ROOT}
\t\ttry_files {path} /index.html
\t\tfile_server
\t}
}
"""

PLACEHOLDERS = {
    "{providerOrigins}": "{$BW_PROVIDER_ORIGINS}",
    "{issuerOrigin}": "{$BW_ISSUER_ORIGIN}",
}


def render(headers):
    lines = []
    for name, value in headers.items():
        if not re.fullmatch(r"[A-Za-z0-9-]+", name):
            raise ValueError(f"header name {name!r} is not a plain token")
        if not isinstance(value, str) or re.search(r'["\\\n\r`]', value):
            raise ValueError(f"{name}: the value must be a string without quotes, backslashes or newlines")
        for placeholder, caddy in PLACEHOLDERS.items():
            value = value.replace(placeholder, caddy)
        # Anything else in braces would be a Caddy placeholder, filled at request time.
        if re.search(r"\{(?!\$BW_ISSUER_ORIGIN\}|\$BW_PROVIDER_ORIGINS\})[^}]*\}", value):
            raise ValueError(f"{name}: unknown placeholder in {value!r}")
        lines.append(f'\t\t\t{name} "{value}"\n')
    return HEAD + "".join(lines) + TAIL


def main(argv):
    if len(argv) != 2:
        print("usage: render-caddy.py <headers.json>", file=sys.stderr)
        return 2
    with open(argv[1], encoding="utf-8") as f:
        headers = json.load(f)
    try:
        sys.stdout.write(render(headers))
    except ValueError as e:
        print(f"render-caddy: {argv[1]}: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
