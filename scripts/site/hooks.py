"""MkDocs hooks for the documentation site (mkdocs.yml `hooks:`).

The markdown is written for GitHub, and scripts/verify-docs.py checks it that way. The site
changes nothing on disk:

- on_files publishes the files docs-map.yaml names, from outside docs/, as extra pages.
- A Markdown tree processor rewrites each page's link and image destinations for where the page
  is served. A link to a file the site publishes (anything under docs/, or a mounted source)
  becomes a relative site link, anchor kept, so `mkdocs build --strict` validates it. Any other
  repository file (code, evidence, a directory) becomes its GitHub URL on main. External URLs
  are left alone, and so is link syntax in code: it never becomes a link element.
"""

import glob
import posixpath
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit

import markdown.extensions.toc
import yaml
from markdown.extensions import Extension
from markdown.treeprocessors import Treeprocessor
from mkdocs.exceptions import PluginError
from mkdocs.structure.files import File

ROOT = Path(__file__).resolve().parent.parent.parent
REPOSITORY = 'https://github.com/ginsys/bronzeward'
MAIN_DOCUMENT = f'{REPOSITORY}/blob/main/'


def _github_unique(id, ids):
    """GitHub's anchor for a repeated heading: `x`, then `x-1`, `x-2`, ..."""
    candidate = id
    suffix = 0
    while candidate in ids or not candidate:
        suffix += 1
        candidate = f'{id}-{suffix}'
    ids.add(candidate)
    return candidate


# mkdocs.yml's slugify gives each heading GitHub's anchor, but the toc extension then makes repeats
# unique its own way (`x_1`), which it offers no option to change. Its tree processor looks
# `unique` up in its module on every heading, so replacing it there gives repeats GitHub's
# anchors too, and the strict build validates links against those.
markdown.extensions.toc.unique = _github_unique

# Repository path -> site path (relative to docs/) for the mounted files, and the reverse.
_mounted = {}
_sources = {}
# The page being rendered: its repository path and its site path.
_page = {}


def _mounts():
    entries = yaml.safe_load((ROOT / 'docs-map.yaml').read_text(encoding='utf-8'))['mounts']
    result = {}
    for entry in entries:
        if 'glob' in entry:
            matches = sorted(glob.glob(entry['glob'], root_dir=ROOT))
            if not matches:
                raise PluginError(f"docs-map.yaml: glob {entry['glob']!r} matches nothing")
            pairs = [(match, entry['target'].format(dir=Path(match).parent.name)) for match in matches]
        else:
            if not (ROOT / entry['source']).is_file():
                raise PluginError(f"docs-map.yaml: missing source {entry['source']}")
            pairs = [(entry['source'], entry['target'])]
        for source, target in pairs:
            if source.startswith('docs/') or target in result.values():
                raise PluginError(f'docs-map.yaml: {source} is under docs/ or reuses target {target}')
            result[source] = target
    return result


def _site_path(path):
    """The site path serving repository path `path`, or None when the site does not publish it."""
    if (ROOT / path).is_dir():
        for index in ('README.md', 'index.md'):
            found = _site_path(posixpath.join(path, index))
            if found:
                return found
        return None
    if path in _mounted:
        return _mounted[path]
    if path.startswith('docs/') and (ROOT / path).is_file():
        return path[len('docs/'):]
    return None


def _rewrite(destination, source, page, image=False):
    """The destination for a link written in repository file `source`, served as site page `page`."""
    if destination.startswith(MAIN_DOCUMENT):
        parts = urlsplit(destination[len(MAIN_DOCUMENT):])
        absolute = True
    else:
        parts = urlsplit(destination)
        if parts.scheme or parts.netloc or not parts.path:
            return destination
        absolute = False
    if absolute:
        path = posixpath.normpath(unquote(parts.path))
    else:
        path = posixpath.normpath(posixpath.join(posixpath.dirname(source), unquote(parts.path)))
    if path in ('.', '..') or path.startswith('../'):
        return destination  # the repository root or outside it: nothing the site publishes
    fragment = f'#{parts.fragment}' if parts.fragment else ''
    # A query (`?plain=1`) asks for GitHub's view of the file, so it never becomes a site link.
    target = None if parts.query else _site_path(path)
    if target is not None:
        return quote(posixpath.relpath(target, posixpath.dirname(page) or '.')) + fragment
    if absolute or not (ROOT / path).exists():
        return destination  # a missing file: left for the strict build to report
    # A blob page is HTML; an image outside the site needs the raw file.
    kind = 'tree' if (ROOT / path).is_dir() else 'raw' if image else 'blob'
    query = f'?{parts.query}' if parts.query else ''
    return f'{REPOSITORY}/{kind}/main/{quote(path)}{query}{fragment}'


class _Links(Treeprocessor):
    def run(self, root):
        if not _page:
            return
        for element in root.iter('a'):
            if element.get('href'):
                element.set('href', _rewrite(element.get('href'), _page['source'], _page['served']))
        for element in root.iter('img'):
            if element.get('src'):
                element.set('src', _rewrite(element.get('src'), _page['source'], _page['served'], image=True))


class LinkExtension(Extension):
    def extendMarkdown(self, md):
        # After the inline patterns (20) have made the link elements, before MkDocs resolves and
        # validates relative links (its `relpath` processor, 0).
        md.treeprocessors.register(_Links(md), 'bronzeward_links', 2)


def on_config(config):
    config.markdown_extensions.append(LinkExtension())
    return config


def on_files(files, config):
    _mounted.clear()
    _mounted.update(_mounts())
    _sources.clear()
    _sources.update({target: source for source, target in _mounted.items()})
    for source, target in _mounted.items():
        if files.get_file_from_path(target) is not None:
            raise PluginError(f'docs-map.yaml: target {target} already exists under docs/')
        files.append(File.generated(config, target, content=(ROOT / source).read_text(encoding='utf-8')))
    return files


def on_page_markdown(text, page, config, files):
    # MkDocs renders the page right after this event, so the tree processor reads it from _page.
    served = page.file.src_uri
    source = _sources.get(served, f'docs/{served}')
    _page.update(source=source, served=served)
    page.edit_url = f'{REPOSITORY}/edit/main/{source}'
    return text
