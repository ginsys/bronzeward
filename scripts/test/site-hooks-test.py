#!/usr/bin/env python3
"""Link rewriting in scripts/site/hooks.py, run by `mise run site` and ci.yml's `site` job.

The strict build validates the links that become site links; these cases cover the rest: the
GitHub fallback, queries, encoding, images and code spans. They use real repository files, so a
case fails if a file it names moves.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'site'))
import hooks

GITHUB = 'https://github.com/ginsys/bronzeward/'


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
        self.check(GITHUB + 'blob/main/go.mod', 'README.md', 'index.md', GITHUB + 'blob/main/go.mod')

    def test_left_alone(self):
        for destination in ('https://example.com/x', '#local', 'missing.md'):
            self.check(destination, 'README.md', 'index.md', destination)
        self.check('../../../outside.md', 'docs/spec/README.md', 'spec/README.md', '../../../outside.md')

    def test_markdown(self):
        go_mod = GITHUB + 'blob/main/go.mod'
        cases = [
            ('see `[spec](docs/spec/README.md)` here', 'see `[spec](docs/spec/README.md)` here'),
            ('[`spec`](docs/spec/README.md) and `x` [c](CONTRIBUTING.md)',
             '[`spec`](spec/README.md) and `x` [c](contributing.md)'),
            ('``a ` b`` [g](go.mod)', f'``a ` b`` [g]({go_mod})'),
            # A code span wrapping onto the next line, then a link whose text is a span.
            ('run `kubectl\npatch` (see [`go.mod`](go.mod))', f'run `kubectl\npatch` (see [`go.mod`]({go_mod}))'),
            # A code span never crosses a blank line, so the stray backtick pairs with nothing.
            ('stray `\n\n[g](go.mod) `x`', f'stray `\n\n[g]({go_mod}) `x`'),
            ('```sh\n[g](go.mod)\n```\n[g](go.mod)', f'```sh\n[g](go.mod)\n```\n[g]({go_mod})'),
        ]
        for markdown, expected in cases:
            self.assertEqual(hooks._rewrite_markdown(markdown, 'README.md', 'index.md'), expected)
        self.assertEqual(hooks._rewrite_markdown('![f](oidc/issuer/issuer.go)', 'fixtures/README.md',
                                                 'experiments/fixtures.md'),
                         '![f](' + GITHUB + 'raw/main/fixtures/oidc/issuer/issuer.go)')


if __name__ == '__main__':
    unittest.main()
