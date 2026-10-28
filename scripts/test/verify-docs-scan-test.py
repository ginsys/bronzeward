#!/usr/bin/env python3
"""Tests for the example and credential scan of scripts/verify-docs.py (ginsys/bronzeward#262 P12):
it passes on a copy of the tree, fails on one planted sentinel per pattern, checks hosts only in
code an operator copies, and allows synthetic values by exact value only. The sentinels are built
from pieces so that this file holds none of them whole."""

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


def run(root):
    try:
        vd.scan(root)
    except ValueError as error:
        return str(error)
    return None


def planted(root, relative, text):
    """Append text to one file of the copy and return the scan's refusal, then restore the file."""
    path = root / relative
    before = path.read_text(encoding='utf-8')
    path.write_text(before + text, encoding='utf-8')
    try:
        return run(root)
    finally:
        path.write_text(before, encoding='utf-8')


def fails(name, relative, text, want):
    error = planted(root, relative, text)
    if error is None or want not in error:
        failures.append(f'{name}: want a refusal naming {want!r}, got {error!r}')


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
    secret = '"client' + '_secret": "s3cr3t-value"'
    fails('PEM block', DOC, f'\n{pem}\n', 'PEM private-key block')
    fails('JWT', DOC, f'\n{jwt}\n', 'JWT-shaped string')
    fails('automation token', 'docs/design/research/evidence/20261019-poc-coverage.txt', f'\n{token}\n',
          'automation token')
    fails('client_secret', DOC, f'\n{secret}\n', 'client_secret value')
    fails('Linux home', DOC, '\n/home/' + 'alice/notes\n', 'home-directory path')
    fails('macOS home', DOC, '\n/Users/' + 'alice/notes\n', 'home-directory path')
    fails('Windows home', DOC, '\nC:\\' + 'Users\\alice\n', 'home-directory path')
    # A refusal names the pattern and the place, never the value.
    error = planted(root, DOC, f'\n{token}\n') or ''
    if token in error:
        failures.append('a refusal quotes the token it found')
    passes('the redaction placeholder', DOC, '\n/home/<user>/tmp and `bwt_<token id>.<secret>`\n')

    # Hosts: in an operator page's inline code and fenced block, not in its prose.
    host = 'https://idp.corp.test/'
    fails('host in inline code', OPERATOR, f'\nSet `{host}`.\n', 'host name idp.corp.test')
    fails('host in a fenced block', OPERATOR, f'\n```sh\ncurl {host}\n```\n', 'host name idp.corp.test')
    passes('host in operator prose', OPERATOR, f'\nSee [the provider]({host}) or {host}.\n')
    passes('host in a specification code block', DOC, f'\n```sh\ncurl {host}\n```\n')
    fails('mail domain', OPERATOR, '\n`h-author@corp.test`\n', 'host name corp.test')
    fails('name and port', OPERATOR, '\n`listen: bw.corp.test:8080`\n', 'host name bw.corp.test')
    fails('IPv4 address', OPERATOR, '\n`10.1.2.3`\n', 'host name 10.1.2.3')
    for allowed in ('https://bronzeward.example.com/', 'https://idp.example.invalid', 'http://localhost:8080',
                    'http://127.0.0.1:28080', 'https://github.com/ginsys/bronzeward/releases', 'https://$HOST/x'):
        passes(f'allowed {allowed}', OPERATOR, f'\n`{allowed}`\n')
    fails('another project on GitHub', OPERATOR, '\n`https://github.com/ginsys/bronzeward-fork/`\n',
          'host name github.com')
    fails('look-alike domain', OPERATOR, '\n`https://example.com.corp.test/`\n', 'host name example.com.corp.test')
    # Synthetic values by exact value only.
    passes('synthetic mirror', OPERATOR, '\n`https://mirror.s5.invalid`\n')
    fails('a sibling of the synthetic mirror', OPERATOR, '\n`https://mirror2.s5.invalid`\n',
          'host name mirror2.s5.invalid')
    fails('a subdomain of the synthetic mirror', OPERATOR, '\n`https://a.mirror.s5.invalid`\n',
          'host name a.mirror.s5.invalid')
    fails('a fixture address it does not list', OPERATOR, '\n`10.55.0.11`\n', 'host name 10.55.0.11')

    # The rendered Caddyfile and packaging's example files.
    headers = json.loads((root / 'web/headers.json').read_text(encoding='utf-8'))
    headers['X-Sentinel'] = 'https://evil.corp.test'
    real = (root / 'web/headers.json').read_text(encoding='utf-8')
    (root / 'web/headers.json').write_text(json.dumps(headers), encoding='utf-8')
    error = run(root) or ''
    (root / 'web/headers.json').write_text(real, encoding='utf-8')
    if 'Caddyfile' not in error or 'evil.corp.test' not in error:
        failures.append(f'host in the rendered Caddyfile: want a refusal, got {error!r}')
    example = 'packaging/bronzeward-client.example.json'
    fails('host in a packaging example', example, '\n"https://idp.corp.test"\n', 'host name idp.corp.test')
    fails('secret in a packaging example', example, f'\n{secret}\n', 'client_secret value')

for failure in failures:
    print(f'FAIL: {failure}', file=sys.stderr)
if failures:
    sys.exit(1)
print('PASS: the P12 scan passes on the tree and refuses each planted sentinel')
