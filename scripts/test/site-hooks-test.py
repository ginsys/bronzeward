#!/usr/bin/env python3
"""scripts/site/hooks.py, run by `mise run site` and ci.yml's `site` job.

The strict build validates the links that become site links; these cases cover the rest: the
GitHub fallback, queries, encoding, images, link syntax in code and repeated-heading anchors.
They use real repository files, so a case fails if a file it names moves.
"""

import re
import sys
import unittest
from pathlib import Path

import markdown
from pymdownx.slugs import slugify

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'site'))
import hooks

GITHUB = 'https://github.com/ginsys/bronzeward/'
GO_MOD = GITHUB + 'blob/main/go.mod'


class RewriteTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        hooks._mounted.update(hooks._mounts())

    def check(self, destination, source, page, expected):
        self.assertEqual(hooks._rewrite(destination, source, page), expected)

    def test_site_links(self):
        self.check('docs/spec/README.md#x', 'README.md', 'index.md', 'spec/README.md#x')
        self.check(GITHUB + 'blob/main/docs/design/bronzeward-name-rationale.md', 'docs/spec/compilation.md',
                   'spec/compilation.md', '../design/bronzeward-name-rationale.md')
        self.check(GITHUB + 'blob/main/CONTRIBUTING.md#a', 'docs/spec/README.md', 'spec/README.md',
                   '../contributing.md#a')
        self.check('../../fixtures/README.md', 'experiments/e5-retention-classification/README.md',
                   'experiments/e5-retention-classification.md', 'fixtures.md')
        self.check('../e4-dispatch-safety/', 'experiments/f12-reproduction/README.md',
                   'experiments/f12-reproduction.md', 'e4-dispatch-safety.md')

    def test_github_fallback(self):
        self.check('oidc/', 'fixtures/README.md', 'experiments/fixtures.md', GITHUB + 'tree/main/fixtures/oidc')
        self.check('versions.env?plain=1#L3', 'fixtures/README.md', 'experiments/fixtures.md',
                   GITHUB + 'blob/main/fixtures/versions.env?plain=1#L3')
        self.check('a%20b/../versions.env', 'fixtures/README.md', 'experiments/fixtures.md',
                   GITHUB + 'blob/main/fixtures/versions.env')
        self.check(GO_MOD, 'README.md', 'index.md', GO_MOD)

    def test_left_alone(self):
        for destination in ('https://example.com/x', '#local', 'missing.md'):
            self.check(destination, 'README.md', 'index.md', destination)
        self.check('../../../outside.md', 'docs/spec/README.md', 'spec/README.md', '../../../outside.md')


class RenderTest(unittest.TestCase):
    """Rewriting runs on link elements, so link syntax in code is never touched."""

    @classmethod
    def setUpClass(cls):
        hooks._mounted.update(hooks._mounts())

    def render(self, text, source='README.md', served='index.md'):
        hooks._page.update(source=source, served=served)
        page = markdown.Markdown(extensions=[hooks.LinkExtension(), 'pymdownx.superfences'])
        return page.convert(text)

    def destinations(self, text, **page):
        return re.findall(r'(?:href|src)="([^"]*)"', self.render(text, **page))

    def test_links(self):
        self.assertEqual(self.destinations('[`spec`](docs/spec/README.md) and `x` [c](CONTRIBUTING.md)'),
                         ['spec/README.md', 'contributing.md'])
        self.assertEqual(self.destinations('[g][r]\n\n[r]: go.mod'), [GO_MOD])
        self.assertEqual(self.destinations('![f](oidc/issuer/issuer.go)', source='fixtures/README.md',
                                           served='experiments/fixtures.md'),
                         [GITHUB + 'raw/main/fixtures/oidc/issuer/issuer.go'])

    def test_code_is_literal(self):
        cases = [
            'see `[spec](docs/spec/README.md)` here',
            'run `kubectl\npatch [g](go.mod)` here',
            '```sh\n[g](go.mod)\n```',
            'para\n\n    [g](go.mod)\n',
            '1. step\n\n    ```sh\n    [g](go.mod)\n    ```',
        ]
        for text in cases:
            html = self.render(text)
            # Highlighting splits the code into spans, so check the destination rather than the whole link.
            self.assertEqual(re.findall(r'href="([^"]*)"', html), [], text)
            self.assertNotIn('github.com', html, text)
            self.assertIn('go.mod' if 'go.mod' in text else 'docs/spec/README.md', html, text)


class AnchorTest(unittest.TestCase):
    def test_repeated_headings_get_github_suffixes(self):
        page = markdown.Markdown(extensions=['toc'], extension_configs={'toc': {'slugify': slugify(case='lower')}})
        page.convert('## Confirmed decision\n\n## Confirmed decision\n\n## Confirmed decision\n\n## 18.2 Phase 1 - A')
        self.assertEqual([entry['id'] for entry in page.toc_tokens],
                         ['confirmed-decision', 'confirmed-decision-1', 'confirmed-decision-2', '182-phase-1---a'])


if __name__ == '__main__':
    unittest.main()
