#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Check that an app's README and release notes agree with VERSION.

Rules:
- README has a "Current release" line naming VERSION.
- README has no release-note sections.
- README ends with a License or Support section.
- The newest versioned heading in the release notes (docs/release-notes.md,
  CHANGELOG.md, RELEASE-NOTES.md or docs/releases.md), or the newest file in
  docs/release-notes/, is VERSION.
- Every ghcr.io/airencracken/APP:TAG in current docs, compose files and deploy
  examples uses VERSION or vVERSION. Placeholders such as vX.Y.Z are allowed;
  "latest" is not. Release notes keep their historical tags.

Usage: python3 tools/check_docs.py [APP_ROOT]  (default: $COMFYWARE_APP_ROOT or .)
"""
import os
from pathlib import Path
import re
import sys

VERSION_RE = re.compile(r'^\d+\.\d+\.\d+$')
HEADING_RE = re.compile(r'^(#{1,6})\s+(.*?)\s*#*\s*$')
RELEASE_HEADING_RE = re.compile(r'\bv?\d+\.\d+\.\d+\b|release notes|what.s new|changelog|\bunreleased\b', re.I)
IMAGE_RE = re.compile(r'ghcr\.io/airencracken/([a-z0-9-]+):([A-Za-z0-9._-]+)')


def names(version, text):
    """Reports whether text names version as a whole token."""
    return re.search(r'(?<![\d.])v?' + re.escape(version) + r'(?![\d])', text) is not None


def headings(text):
    in_code = False
    for line in text.splitlines():
        if line.startswith('```'):
            in_code = not in_code
            continue
        match = None if in_code else HEADING_RE.match(line)
        if match:
            yield len(match[1]), match[2]


def check_readme(readme, version):
    problems = []
    text = readme.read_text(encoding='utf-8')
    current = [line for line in text.splitlines() if 'current release' in line.lower()]
    if not current:
        problems.append('README.md has no "Current release" line')
    elif not any(names(version, line) for line in current):
        problems.append(f'README.md "Current release" does not name {version}')
    sections = [title for level, title in headings(text) if level == 2]
    for level, title in headings(text):
        if level >= 2 and RELEASE_HEADING_RE.search(title):
            problems.append(f'README.md has a release-note section: {title!r}; move it to the release notes')
    if not sections or sections[-1].lower() not in ('license', 'support'):
        problems.append('README.md must end with a License or Support section')
    return problems


ANY_VERSION_RE = re.compile(r'(?<![\d.])v?(\d+)\.(\d+)\.(\d+)(?![\d])')
NOTES_FILES = ('docs/release-notes.md', 'CHANGELOG.md', 'RELEASE-NOTES.md', 'docs/releases.md')


def newest_heading(text):
    """Returns the first level-2 heading that names a version, or None."""
    for level, title in headings(text):
        if level == 2 and ANY_VERSION_RE.search(title):
            return title
    return None


def check_notes(root, version):
    for name in NOTES_FILES:
        notes = root / name
        if not notes.is_file():
            continue
        title = newest_heading(notes.read_text(encoding='utf-8'))
        if title is None:
            continue
        if names(version, title):
            return []
        return [f'{name}: newest release heading {title!r} is not {version}']
    directory = root / 'docs' / 'release-notes'
    if directory.is_dir():
        versions = sorted(tuple(map(int, m.groups())) for m in (ANY_VERSION_RE.fullmatch(p.stem) for p in directory.glob('*.md')) if m)
        if versions:
            newest = '.'.join(map(str, versions[-1]))
            return [] if newest == version else [f'docs/release-notes/{newest}.md is newer than VERSION {version}' if tuple(map(int, version.split('.'))) < versions[-1] else f'docs/release-notes has no {version}.md']
    return [f'no release notes name {version}; add a "## {version}" heading to docs/release-notes.md']


def documented_files(root):
    """Current docs and examples; release notes keep their historical tags."""
    patterns = ('README.md', 'docs/**/*.md', 'docker-compose*.yml', 'deploy/**/*')
    history = {root / name for name in NOTES_FILES if name != 'docs/releases.md'}
    seen = set()
    for pattern in patterns:
        for path in sorted(root.glob(pattern)):
            if 'release-notes' in path.relative_to(root).parts or path in history:
                continue
            if path.is_file() and path not in seen and path.stat().st_size <= 4 << 20:
                seen.add(path)
                yield path


def check_images(root, app, version):
    problems = []
    for path in documented_files(root):
        try:
            text = path.read_text(encoding='utf-8')
        except UnicodeDecodeError:
            continue
        for image, tag in IMAGE_RE.findall(text):
            placeholder = not tag[0].isdigit() and not tag.startswith('v') or tag.startswith('v') and not tag[1:2].isdigit()
            if image == app and not (placeholder and tag != 'latest') and tag not in (version, 'v' + version):
                problems.append(f'{path.relative_to(root)}: ghcr.io/airencracken/{image}:{tag} should be tagged v{version}')
    return problems


def check(root):
    root = Path(root).resolve()
    try:
        version = (root / 'VERSION').read_text(encoding='utf-8').strip()
    except (OSError, UnicodeDecodeError) as error:
        return [f'cannot read VERSION: {error}']
    if not VERSION_RE.match(version):
        return [f'VERSION {version[:40]!r} is not MAJOR.MINOR.PATCH']
    readme = root / 'README.md'
    if not readme.is_file():
        return ['README.md is missing']
    module = re.search(r'^module\s+(\S+)\s*$', (root / 'go.mod').read_text(encoding='utf-8'), re.M) if (root / 'go.mod').is_file() else None
    app = module[1].rsplit('/', 1)[-1] if module else root.name
    return check_readme(readme, version) + check_notes(root, version) + check_images(root, app, version)


def main(argv):
    root = argv[1] if len(argv) > 1 else os.environ.get('COMFYWARE_APP_ROOT', '.')
    problems = check(root)
    for problem in problems:
        print(problem, file=sys.stderr)
    if problems:
        return 1
    print('Documentation matches VERSION.')
    return 0


if __name__ == '__main__':
    raise SystemExit(main(sys.argv))
