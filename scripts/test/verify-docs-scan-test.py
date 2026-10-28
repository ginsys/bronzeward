#!/usr/bin/env python3
"""Tests for the example and credential scan of scripts/verify-docs.py (ginsys/bronzeward#262 P12):
it passes on a copy of the tree, fails on one planted sentinel per pattern and code form, checks
hosts only in code an operator copies, allows synthetic values by exact value only, and never
quotes what it found. The sentinels are built from pieces so that this file holds none of them
whole."""

import importlib.util
import json
import shutil
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent
spec = importlib.util.spec_from_file_location('verify_docs', ROOT / 'scripts' / 'verify-docs.py')
vd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vd)

failures = []
DOC = 'docs/spec/README.md'
OPERATOR = 'docs/operator/README.md'
HOST = 'a host name that is not an example host'


def run(root, check=vd.scan):
    try:
        check(root)
    except ValueError as error:
        return str(error)
    return None


def planted(root, relative, text, check=vd.scan):
    """Append text to one file of the copy and return the check's refusal, then restore the file."""
    path = root / relative
    before = path.read_text(encoding='utf-8')
    path.write_text(before + text, encoding='utf-8')
    try:
        return run(root, check)
    finally:
        path.write_text(before, encoding='utf-8')


def fails(name, relative, text, want, hidden=None):
    """Want a refusal naming the file and the reason, and quoting none of hidden."""
    error = planted(root, relative, text)
    if error is None or f'{relative}:' not in error or want not in error:
        failures.append(f'{name}: want a refusal naming {relative} and {want!r}, got {error!r}')
    elif hidden and hidden in error:
        failures.append(f'{name}: the refusal quotes what it found')


def passes(name, relative, text):
    error = planted(root, relative, text)
    if error is not None:
        failures.append(f'{name}: want a pass, got {error!r}')


with tempfile.TemporaryDirectory() as work:
    root = Path(work)
    shutil.copytree(ROOT / 'docs', root / 'docs')
    for relative in ('web/headers.json', 'scripts/release/render-caddy.py'):
        (root / relative).parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(ROOT / relative, root / relative)
    (root / 'packaging').mkdir()
    for path in (ROOT / 'packaging').glob('*.example.*'):
        shutil.copy(path, root / 'packaging' / path.name)

    error = run(root)
    if error is not None:
        failures.append(f'the copied tree: want a pass, got {error!r}')

    # One sentinel per credential and path pattern, anywhere under docs/.
    pem = '-----BEGIN ' + 'RSA PRIVATE ' + 'KEY-----'
    jwt = 'eyJ' + 'hbGciOiJub25lIn0' + '.eyJ' + 'zdWIiOiJ4In0' + '.'
    token = 'bwt' + '_tok_0123456789abcdef.' + 'A' * 43
    key = 'client' + '_secret'
    fails('PEM block', DOC, f'\n{pem}\n', 'PEM private-key block')
    fails('JWT', DOC, f'\n{jwt}\n', 'JWT-shaped string', jwt)
    fails('automation token', 'docs/design/research/evidence/20261019-poc-coverage.txt', f'\n{token}\n',
          'automation token', token)
    fails('client_secret', DOC, f'\n"{key}": "s3cr3t-value"\n', 'client_secret value', 's3cr3t-value')
    fails('client_secret on the next JSON line', DOC, f'\n{{"{key}":\n  "s3cr3t-value"}}\n',
          'client_secret value', 's3cr3t-value')
    fails('client_secret on the next YAML line', DOC, f'\n{key}:\n  s3cr3t-value\n', 'client_secret value',
          's3cr3t-value')
    fails('a host-shaped client_secret', OPERATOR, f'\n`{key}: 10.23.45.67`\n', 'client_secret value',
          '10.23.45.67')
    fails('Linux home', DOC, '\n/home/' + 'alice/notes\n', 'home-directory path', 'alice')
    fails('macOS home', DOC, '\n/Users/' + 'alice/notes\n', 'home-directory path', 'alice')
    fails('Windows home', DOC, '\nC:\\' + 'Users\\alice\n', 'home-directory path', 'alice')
    fails('Windows home in lower case', DOC, '\nc:\\' + 'users\\alice\n', 'home-directory path', 'alice')
    fails('Windows home JSON-escaped', DOC, '\n"C:\\\\' + 'Users\\\\alice"\n', 'home-directory path', 'alice')
    passes('the redaction placeholder', DOC, '\n/home/<user>/tmp and `bwt_<token id>.<secret>`\n')
    # The line of a match that spans lines is the line it starts on.
    before = (root / DOC).read_text(encoding='utf-8')
    error = planted(root, DOC, f'\n{key}:\n  s3cr3t-value\n') or ''
    if f'{DOC}:{before.count(chr(10)) + 2}: client_secret value' not in error:
        failures.append(f'a multiline match: want the line of its key, got {error!r}')

    # Hosts: in an operator page's code, not in its prose.
    host = 'https://idp.corp.test/'
    fails('host in inline code', OPERATOR, f'\nSet `{host}`.\n', HOST, 'corp.test')
    fails('host in a fenced block', OPERATOR, f'\n```sh\ncurl {host}\n```\n', HOST, 'corp.test')
    fails('host in a span over two lines', OPERATOR, f'\nSet `curl\n{host}` first.\n', HOST)
    fails('host in a blockquoted fence', OPERATOR, f'\n> ~~~sh\n> curl {host}\n> ~~~\n', HOST)
    fails('a fence-like line inside a fence', OPERATOR, f'\n```sh\n~~~ {host}\n```\n', HOST)
    fails('host in indented code', OPERATOR, f'\nRun:\n\n    curl {host}\n', HOST)
    fails('host in a fence inside a list item', OPERATOR,
          f'\n1. Configure the client:\n\n    ```sh\n    export BW_ISSUER={host}\n\n    bronzeward login\n    ```\n', HOST)
    fails('host in indented code in an ordered list item', OPERATOR, f'\n1. Run:\n\n       curl {host}\n', HOST)
    fails('host in indented code in a bullet list item', OPERATOR, f'\n- Run:\n\n      curl {host}\n', HOST)
    fails('host in tab-indented code', OPERATOR, f'\nRun:\n\n\tcurl {host}\n', HOST)
    fails('host in a tab-indented fence in a list item', OPERATOR,
          f'\n1. Configure:\n\n\t```sh\n\texport BW_ISSUER={host}\n\n\tbronzeward login\n\t```\n', HOST)
    passes('host in a list continuation', OPERATOR, f'\n- Step one\n\n    see {host} later.\n')
    # ginsys/bronzeward#419: an item a dedent or a fence closes, and code on the marker's own line.
    fails('host in indented code after a nested list ends', OPERATOR,
          f'\n- Outer\n  - Inner\n\n  Run:\n\n      curl {host}\n', HOST)
    fails('host in code dedented straight from a nested item', OPERATOR,
          f'\n- Outer\n     - Inner\n\n      curl {host}\n', HOST)
    fails('host in indented code after a fence ends a list', OPERATOR,
          f'\n- Step one\n\n```sh\nbronzeward version\n```\n\n    curl {host}\n', HOST)
    fails('host in indented code on a bullet marker line', OPERATOR, f'\n-     curl {host}\n', HOST)
    fails('host in indented code on an ordered marker line', OPERATOR, f'\n1.     curl {host}\n', HOST)
    passes('host in a nested list continuation', OPERATOR, f'\n- Outer\n  - Inner\n\n      see {host} later.\n')
    passes('host in operator prose', OPERATOR, f'\nSee [the provider]({host}) or {host}.\n')
    passes('host in a specification code block', DOC, f'\n```sh\ncurl {host}\n```\n')
    fails('mail domain', OPERATOR, '\n`h-author@corp.test`\n', HOST)
    fails('name and port', OPERATOR, '\n`listen: bw.corp.test:8080`\n', HOST)
    fails('IPv4 address', OPERATOR, '\n`10.1.2.3`\n', HOST, '10.1.2.3')
    fails('bracketed IPv6 and port', OPERATOR, '\n`[2001:db8::1]:443`\n', HOST)
    fails('a URL with a bracketed IPv6 host', OPERATOR, '\n`https://[2001:db8::1]/`\n', HOST)
    fails('a query that looks like userinfo', OPERATOR, '\n`https://corp.test?account=a@example.com`\n', HOST)
    for allowed in ('https://bronzeward.example.com/', 'https://idp.example.invalid', 'http://localhost:8080',
                    'http://127.0.0.1:28080', 'https://github.com/ginsys/bronzeward/releases', 'https://$HOST/x',
                    'https://${HOST}/x', 'https://{$BW_SITE_ADDRESS}/x', 'https://user@idp.example.com/'):
        passes(f'allowed {allowed}', OPERATOR, f'\n`{allowed}`\n')
    fails('another project on GitHub', OPERATOR, '\n`https://github.com/ginsys/bronzeward-fork/`\n', HOST)
    fails('look-alike domain', OPERATOR, '\n`https://example.com.corp.test/`\n', HOST)
    fails('a host only prefixed by a placeholder', OPERATOR, '\n`https://$HOST.corp.test/`\n', HOST)
    # Synthetic values by exact value only, the whole host compared.
    passes('synthetic mirror', OPERATOR, '\n`https://mirror.s5.invalid`\n')
    fails('a sibling of the synthetic mirror', OPERATOR, '\n`https://mirror2.s5.invalid`\n', HOST)
    fails('a subdomain of the synthetic mirror', OPERATOR, '\n`https://a.mirror.s5.invalid`\n', HOST)
    fails('a host the synthetic mirror only starts', OPERATOR, '\n`https://mirror.s5.invalid_extra.corp.test/`\n',
          HOST)
    fails('a fixture address it does not list', OPERATOR, '\n`10.55.0.11`\n', HOST)

    # The rendered Caddyfile and packaging's example files.
    real = (root / 'web/headers.json').read_text(encoding='utf-8')
    for value, want in (('https://evil.corp.test', HOST), (f'{{nope}} {token}', 'does not render')):
        headers = json.loads(real)
        headers['X-Sentinel'] = value
        (root / 'web/headers.json').write_text(json.dumps(headers), encoding='utf-8')
        error = run(root) or ''
        (root / 'web/headers.json').write_text(real, encoding='utf-8')
        if 'Caddyfile' not in error or want not in error or 'corp.test' in error or token in error:
            failures.append(f'the rendered Caddyfile with {want!r}: want a refusal, got {error!r}')
    example = 'packaging/bronzeward-client.example.json'
    fails('host in a packaging example', example, '\n"https://idp.corp.test"\n', HOST, 'corp.test')
    fails('secret in a packaging example', example, f'\n"{key}": "s3cr3t-value"\n', 'client_secret value')

    # The whole entrypoint: the scan refuses before a link check could quote the token, and a link
    # check outside the scanned files names a credential-shaped destination without quoting it.
    error = planted(root, OPERATOR, f'\n[a link]({token})\n', vd.main) or ''
    if 'P12 scan' not in error or token in error:
        failures.append(f'the entrypoint with a token link: want the scan refusal alone, got {error!r}')
    (root / 'README.md').write_text(f'[a link]({token})\n', encoding='utf-8')
    error = run(root, lambda root: vd.check_markdown(root / 'README.md', root)) or ''
    if 'missing link target' not in error or token in error:
        failures.append(f'a link check on a token destination: want a refusal without it, got {error!r}')
    # A destination too long for a file name fails in the file system, not in the check.
    long_jwt = jwt + 'x' * 300
    (root / 'README.md').write_text(f'[a link]({long_jwt})\n', encoding='utf-8')
    error = run(root, lambda root: vd.check_markdown(root / 'README.md', root)) or ''
    if 'unusable link target' not in error or long_jwt[:40] in error:
        failures.append(f'a link check on a long token destination: want a refusal without it, got {error!r}')

for failure in failures:
    print(f'FAIL: {failure}', file=sys.stderr)
if failures:
    sys.exit(1)
print('PASS: the P12 scan passes on the tree and refuses each planted sentinel without quoting it')
