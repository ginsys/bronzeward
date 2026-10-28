#!/usr/bin/env python3
"""Check repository guidance links/structure and the work-item form, without network access."""

import importlib.util
import json
import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

try:
    import yaml
except ImportError:
    sys.exit('Install PyYAML 6 in your Python environment, then rerun this command.')

ROOT = Path(__file__).resolve().parent.parent
MAIN_DOCUMENT = 'https://github.com/ginsys/bronzeward/blob/main/'
FIELDS = {
    'work-type': ('dropdown', 'Work type', True),
    'objective': ('textarea', 'Objective and design references', True),
    'scope': ('textarea', 'Scope and exclusions', True),
    'deliverables': ('textarea', 'Deliverables', True),
    'acceptance': ('textarea', 'Acceptance criteria', True),
    'verification': ('textarea', 'Verification method', True),
    'alternatives': ('textarea', 'Alternatives and decision enabled', False),
    'outcome-links': ('textarea', 'Outcome links', False),
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def read_text(path, root=ROOT):
    """Read repository UTF-8 without exposing checkout paths in file errors."""
    try:
        return path.read_text(encoding='utf-8')
    except OSError as error:
        raise ValueError(f'{path.relative_to(root)}: {error.strerror}') from None
    except UnicodeError:
        raise ValueError(f'{path.relative_to(root)}: invalid UTF-8') from None


BLOCKQUOTE = re.compile(r'^(?: {0,3}>)+ ?')


def markdown_lines(path, root=ROOT):
    """Yield (number, line, fenced) for every line but the markers that open or close a fence, one
    inside a blockquote included; verify fences and whitespace."""
    fence = None
    label = path.relative_to(root)
    for number, line in enumerate(read_text(path, root).splitlines(), 1):
        require(line == line.rstrip(), f'{label}:{number}: trailing whitespace')
        # At any indentation, so that a fence inside a list item counts too.
        marker = re.match(r'^ *(`{3,}|~{3,})(.*)$', BLOCKQUOTE.sub('', line).expandtabs(4))
        if marker and fence is None:
            fence = marker.group(1)
        elif marker and marker.group(1)[0] == fence[0] and len(marker.group(1)) >= len(fence) \
                and not marker.group(2).strip():
            fence = None
        else:
            yield number, line, fence is not None
    require(fence is None, f'{label}: unclosed code fence')


def prose(path, root=ROOT):
    """Exclude fenced examples from link checks."""
    return '\n'.join(line for _, line, fenced in markdown_lines(path, root) if not fenced)


def anchors(text):
    result = set()
    for heading in re.findall(r'^#{1,6}\s+(.+?)\s*#*$', text, re.M):
        slug = re.sub(r'[^\w -]', '', heading.lower()).replace(' ', '-')
        candidate = slug
        suffix = 0
        while candidate in result:
            suffix += 1
            candidate = f'{slug}-{suffix}'
        result.add(candidate)
    return result


def shown(destination):
    """A link destination for a refusal, unless it matches a credential or home-path pattern."""
    if any(pattern.search(destination) for pattern in SECRETS.values()):
        return 'a destination matching a P12 credential or home-path pattern'
    return destination


def check_markdown(path, root=ROOT):
    label = path.relative_to(root)
    for destination in re.findall(r'\[[^\]\n]+\]\(([^)\s]+)\)', prose(path, root)):
        if destination.startswith(MAIN_DOCUMENT):
            parts = urlsplit(destination[len(MAIN_DOCUMENT):])
            target = root / unquote(parts.path)
        else:
            parts = urlsplit(destination)
            if parts.scheme or parts.netloc:
                continue
            target = path.parent / unquote(parts.path) if parts.path else path
        try:
            target = target.resolve()
            exists = target.exists()
        except OSError as error:
            # Its message quotes the path, and so the destination.
            raise ValueError(f'{label}: unusable link target ({error.strerror}): {shown(destination)}') from None
        require(target.is_relative_to(root.resolve()), f'{label}: link escapes repository: {shown(destination)}')
        require(exists, f'{label}: missing link target: {shown(destination)}')
        if parts.fragment and target.suffix == '.md':
            require(unquote(parts.fragment) in anchors(prose(target, root)),
                    f'{label}: missing anchor: {shown(destination)}')


def check_form(root=ROOT):
    templates = root / '.github/ISSUE_TEMPLATE'
    form = yaml.safe_load(read_text(templates / 'work-item.yml', root))
    require(isinstance(form, dict) and form.get('name') and form.get('description'), 'Form needs a name and description')
    elements = form.get('body')
    require(isinstance(elements, list), 'Form body must be a list')
    fields = {}
    for element in elements:
        require(isinstance(element, dict), 'Form elements must be mappings')
        if element.get('type') == 'markdown':
            require(element.get('attributes', {}).get('value'), 'Markdown element needs content')
            continue
        key = element.get('id')
        require(key in FIELDS and key not in fields, f'Unknown or duplicate form field: {key}')
        fields[key] = element
    require(fields.keys() == FIELDS.keys(), 'Missing required form sections')
    for key, (kind, label, required) in FIELDS.items():
        field = fields[key]
        require(field.get('type') == kind and field.get('attributes', {}).get('label') == label,
                f'{key}: incorrect type or section label')
        require(field.get('validations', {}).get('required') is required, f'{key}: incorrect requiredness')
    require(fields['work-type']['attributes'].get('options') ==
            ['Investigation', 'Decision', 'Specification', 'Implementation', 'Validation'], 'Incorrect work types')
    require(yaml.safe_load(read_text(templates / 'config.yml', root)) == {'blank_issues_enabled': False},
            'Blank issues must remain disabled')


# The example and credential scan (ginsys/bronzeward#262 P12). A finding names the pattern and the
# place, never the matched text, so a real secret does not reach the log that reports it.
SECRETS = {
    'PEM private-key block': re.compile(r'-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----'),
    'JWT-shaped string': re.compile(r'\beyJ[\w-]{4,}\.eyJ[\w-]{4,}\.[\w-]*'),
    'automation token': re.compile(r'\bbwt_\w+\.[\w-]+'),  # internal/auth/tokens.go:21
    'client_secret value': re.compile(r'client_secret["\']?\s*[:=]\s*["\']?(?![<"\'\s])[^\s"\',}]+'),
    'home-directory path': re.compile(r'/home/(?!<)[^/\s]+/|/Users/(?!<)[^/\s]+/|(?i:\b[a-z]:\\{1,2}users\\{1,2})'),
}
# Host names in what an operator copies: URL authorities, mail domains, name:port (a dotted name or a
# bracketed IPv6 address) and IPv4. A URL's host is taken whole, up to the port or path.
HOSTS = (
    re.compile(r'\b[A-Za-z][A-Za-z0-9+.-]*://(?:[^\s/?#@"\'`<>]*@)?(\[[^\]\s/]*\]|[^\s/?#:@"\'`<>()\[\],;\\]*)'),
    re.compile(r'[\w.+-]@([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+)'),
    re.compile(r'(?<![\w./-])([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+|\[[0-9A-Fa-f:.]+\]):\d+\b'),
    re.compile(r'(?<![\w.])(\d{1,3}(?:\.\d{1,3}){3})(?![\w.])'),
)
# A shell or Caddy environment placeholder in host position names no host.
PLACEHOLDER = re.compile(r'\$\w+|\$\{\w+\}|\{\$\w+\}')
ALLOWED_DOMAINS = ('example.invalid', 'example.com', 'localhost')
PROJECT_URL = 'https://github.com/ginsys/bronzeward/'
# Known synthetic development values, each allowed by exact value: the walkthrough's registry
# patches, the fixture's Talos nodes (fixtures/versions.env:45-46) and its issuer on the Compose
# network (fixtures/compose.yaml:133).
SYNTHETIC_HOSTS = {'mirror.s5.invalid', 'registry.s5.invalid', '10.55.0.2', '10.55.0.3', 'issuer'}
IMAGES = {'.png', '.jpg', '.jpeg', '.gif', '.webp'}


def disallowed_hosts(text):
    """Count the host names in text that are not example hosts, each compared whole."""
    count = 0
    for pattern in HOSTS:
        for match in pattern.finditer(text):
            host = match.group(1)
            if not host or PLACEHOLDER.fullmatch(host):
                continue
            if pattern is HOSTS[0] and text.startswith(PROJECT_URL, match.start()):
                continue
            host = host.lower().rstrip('.')
            if host == '127.0.0.1' or host in SYNTHETIC_HOSTS:
                continue
            if any(host == domain or host.endswith('.' + domain) for domain in ALLOWED_DOMAINS):
                continue
            count += 1
    return count


def operator_code(path, root=ROOT):
    """Yield (number, text) for the code an operator copies from a page: fenced lines, indented
    code (four or more spaces past a list item's content, or past the margin outside a list), and
    inline code spans, which may run over the lines of one paragraph. A tab counts as four
    spaces."""
    paragraph, content = [], None  # content: the open list item's content indentation

    def spans():
        text = '\n'.join(line for _, line in paragraph)
        for match in re.finditer(r'(?<!`)(`+)(?!`)(.+?)(?<!`)\1(?!`)', text, re.DOTALL):
            yield paragraph[0][0] + text.count('\n', 0, match.start()), match.group(2)
        paragraph.clear()

    for number, line, fenced in markdown_lines(path, root):
        body = BLOCKQUOTE.sub('', line).expandtabs(4)
        if fenced:
            yield from spans()
            yield number, line
            continue
        indent = len(body) - len(body.lstrip(' '))
        item = re.match(r' *(?:[-*+]|\d+[.)])(?: {1,4}|$)', body)
        if body.strip() and indent >= (content or 0) + 4:
            yield number, line
        elif item:
            content = item.end()
        elif body.strip() and not indent:
            content = None
        if body.strip():
            paragraph.append((number, body))
        else:
            yield from spans()
    yield from spans()


def rendered_caddyfile(root=ROOT):
    spec = importlib.util.spec_from_file_location('render_caddy', root / 'scripts/release/render-caddy.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    try:
        return module.render(json.loads(read_text(root / 'web/headers.json', root)))
    except ValueError:
        # Its message quotes the header value; scripts/release/render-caddy.py shows it locally.
        raise ValueError('web/headers.json: the Caddyfile does not render') from None


def scan(root=ROOT):
    """Fail on a credential or home path in docs/, the rendered Caddyfile or packaging's example
    files, and on a host name outside the allowed ones in code an operator copies: docs/operator/
    code blocks and spans, the Caddyfile and the example files. Prose is not host-checked: its
    links are references."""
    docs = sorted(path for path in (root / 'docs').rglob('*') if path.is_file() and path.suffix not in IMAGES)
    examples = sorted((root / 'packaging').glob('*.example.*'))
    texts = [(str(path.relative_to(root)), read_text(path, root)) for path in docs + examples]
    texts.append(('the Caddyfile rendered from web/headers.json', rendered_caddyfile(root)))
    # Whole texts, so that a value on the line after its key is found too.
    findings = [f'{label}:{text.count(chr(10), 0, match.start()) + 1}: {name}' for label, text in texts
                for name, pattern in SECRETS.items() for match in pattern.finditer(text)]
    code = [(f'{label}:{number}', line) for label, text in texts[len(docs):]
            for number, line in enumerate(text.splitlines(), 1)]
    for path in sorted((root / 'docs/operator').rglob('*.md')):
        code += [(f'{path.relative_to(root)}:{number}', text) for number, text in operator_code(path, root)]
    findings += [f'{place}: a host name that is not an example host' for place, text in code
                 for _ in range(disallowed_hosts(text))]
    require(not findings, 'P12 scan:\n' + '\n'.join(findings))
    return len(texts)


def main(root=ROOT):
    # The scan runs first: the link checks below quote what they refuse.
    scanned = scan(root)
    documents = [root / name for name in ('README.md', 'CONTRIBUTING.md', 'AGENTS.md', 'CLAUDE.md', 'docs/spec/README.md')]
    documents = sorted(set(documents) | set((root / 'docs/spec').rglob('*.md')) | set((root / 'docs/operator').rglob('*.md')))
    for document in documents:
        check_markdown(document, root)
    check_form(root)
    require(read_text(root / 'CLAUDE.md', root) == '@AGENTS.md\n', 'CLAUDE.md must delegate to AGENTS.md')
    required = sum(field[2] for field in FIELDS.values())
    print(f'PASS: {len(documents)} guidance/specification/operator documents; local links/anchors, fences, whitespace; '
          f'issue-form YAML and {len(FIELDS)} fields ({required} required, {len(FIELDS) - required} optional); '
          f'blank issues disabled; CLAUDE delegation; P12 scan of {scanned} files (credentials, home paths, '
          'example hosts). External URLs, live tracker state and design semantics require separate review.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, yaml.YAMLError) as error:
        sys.exit(str(error))
