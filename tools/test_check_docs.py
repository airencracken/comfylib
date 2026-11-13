#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Tests for check_docs.py."""
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_docs  # noqa: E402

README = """# Folkweave

Current release: **[0.6.0](https://example.org/v0.6.0)**. [What's new](docs/release-notes.md)

## Install

Run `docker run ghcr.io/airencracken/folkweave:v0.6.0`.

```sh
## not a heading 0.1.0
```

## Support

Open an issue.

## License

AGPL-3.0-or-later.
"""
NOTES = """# Folkweave release notes

## Unreleased

- Next things.

## 0.6.0

- Things.

## 0.5.0
"""


class CheckDocsTests(unittest.TestCase):
    def app(self, files):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        base = {'VERSION': '0.6.0\n', 'go.mod': 'module github.com/airencracken/folkweave\n', 'README.md': README,
                'docs/release-notes.md': NOTES, 'docker-compose.yml': 'image: ghcr.io/airencracken/folkweave:v0.6.0\n'}
        base.update(files)
        for name, text in base.items():
            if text is None:
                continue
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding='utf-8')
        return root

    def problems(self, **files):
        return check_docs.check(self.app({name.replace('__', '/').replace('_dot_', '.'): text for name, text in files.items()}))

    def test_consistent_docs_pass(self):
        self.assertEqual(check_docs.check(self.app({})), [])
        self.assertEqual(check_docs.check(self.app({'README.md': README.replace('## License\n\nAGPL-3.0-or-later.\n', '')})), [])

    def test_current_release_must_match(self):
        self.assertTrue(check_docs.check(self.app({'README.md': README.replace('0.6.0](', '0.5.0](').replace('v0.6.0)', 'v0.5.0)')})))
        self.assertTrue(check_docs.check(self.app({'README.md': README.replace('Current release', 'Latest')})))
        # A longer version that contains VERSION is not a match.
        self.assertTrue(check_docs.check(self.app({'README.md': README.replace('[0.6.0](https://example.org/v0.6.0)', '[10.6.0](x)')})))
        self.assertTrue(check_docs.check(self.app({'README.md': README.replace('[0.6.0](https://example.org/v0.6.0)', '[0.6.01](x)')})))

    def test_readme_has_no_release_sections(self):
        for heading in ('## Folkweave 0.6.0', '## What\'s new', '### Release notes', '## Changelog', '## v0.5.0', '## Unreleased'):
            with self.subTest(heading=heading):
                self.assertTrue(check_docs.check(self.app({'README.md': README.replace('## Install', heading)})))

    def test_readme_ends_with_license_or_support(self):
        self.assertTrue(check_docs.check(self.app({'README.md': README + '\n## Development\n\nMore.\n'})))
        self.assertTrue(check_docs.check(self.app({'README.md': '# App\n\nCurrent release: 0.6.0\n'})))

    def test_newest_release_heading_must_match(self):
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': NOTES.replace('## 0.6.0', '## 0.5.1')})))
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': '# Notes\n\n## Unreleased\n'})))
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': None})))
        self.assertEqual(check_docs.check(self.app({'docs/release-notes.md': None, 'docs/releases.md': NOTES})), [])

    def test_other_release_note_layouts(self):
        self.assertEqual(check_docs.check(self.app({'docs/release-notes.md': None, 'CHANGELOG.md': '# Changes\n\n## [0.6.0] - 2026-11-01\n'})), [])
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': None, 'CHANGELOG.md': '# Changes\n\n## [0.5.9]\n'})))
        install = '# Install\n\n## Linux binary\n\n## Upgrading\n'
        self.assertEqual(check_docs.check(self.app({'docs/release-notes.md': None, 'docs/releases.md': install, 'docs/release-notes/0.6.0.md': '# 0.6.0\n', 'docs/release-notes/0.5.10.md': 'x'})), [])
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': None, 'docs/releases.md': install, 'docs/release-notes/0.6.1.md': 'x'})))
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': None, 'docs/releases.md': install, 'docs/release-notes/0.5.0.md': 'x'})))
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': None, 'docs/releases.md': install})))
        # Introductory sections before the first versioned heading are fine.
        self.assertEqual(check_docs.check(self.app({'docs/release-notes.md': '# Notes\n\n## Before upgrading\n\n## 0.6.0\n'})), [])

    def test_notes_for_the_next_version_must_be_marked_unreleased(self):
        install = '# Install\n\n## Linux binary\n'
        layout = {'docs/release-notes.md': None, 'docs/releases.md': install, 'docs/release-notes/0.6.0.md': '# Folkweave 0.6.0\n\nThings.\n'}
        # A file for the next version marked in its first line, as Imvault does.
        self.assertEqual(check_docs.check(self.app({**layout, 'docs/release-notes/0.7.0.md': '# Folkweave 0.7.0\n\nUnreleased.\n'})), [])
        self.assertEqual(check_docs.check(self.app({**layout, 'docs/release-notes/0.7.0.md': '# Folkweave 0.7.0 (unreleased)\n'})), [])
        unmarked = check_docs.check(self.app({**layout, 'docs/release-notes/0.7.0.md': '# Folkweave 0.7.0\n\nThings.\n'}))
        self.assertTrue(unmarked and 'not marked unreleased' in unmarked[0])
        # An unreleased file does not stand in for the missing current one.
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': None, 'docs/releases.md': install, 'docs/release-notes/0.7.0.md': '# 0.7.0\n\nUnreleased.\n', 'docs/release-notes/0.5.0.md': '# 0.5.0\n'})))
        # The same rules for headings in one notes file.
        for heading in ('## 0.7.0 (unreleased)', '## Unreleased 0.7.0', '## 0.7.0\n\nUnreleased: still in progress.'):
            with self.subTest(heading=heading):
                self.assertEqual(check_docs.check(self.app({'docs/release-notes.md': f'# Notes\n\n{heading}\n\n## 0.6.0\n'})), [])
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': '# Notes\n\n## 0.7.0\n\nThings.\n\n## 0.6.0\n'})))
        # A marked older section is skipped, so the next released one must match.
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': '# Notes\n\n## 0.6.0 (unreleased)\n\n## 0.5.0\n'})))
        # The word must open the line, not appear later in it.
        self.assertTrue(check_docs.check(self.app({'docs/release-notes.md': '# Notes\n\n## 0.7.0\n\nFixes an unreleased bug.\n\n## 0.6.0\n'})))

    def test_container_tags_must_match(self):
        for tag in ('latest', 'v0.5.0', '0.6', 'v0.6.0-rc1'):
            with self.subTest(tag=tag):
                self.assertTrue(check_docs.check(self.app({'docs/containers.md': f'ghcr.io/airencracken/folkweave:{tag}\n'})))
        self.assertEqual(check_docs.check(self.app({'docs/containers.md': 'ghcr.io/airencracken/folkweave:0.6.0 and ghcr.io/airencracken/witmoot:v9.9.9\n'})), [])
        self.assertTrue(check_docs.check(self.app({'deploy/compose/app.yml': 'image: ghcr.io/airencracken/folkweave:latest\n'})))
        self.assertEqual(check_docs.check(self.app({'docs/containers.md': 'ghcr.io/airencracken/folkweave:vX.Y.Z\n'})), [])
        self.assertEqual(check_docs.check(self.app({'docs/release-notes/0.5.0.md': 'ghcr.io/airencracken/folkweave:v0.5.0\n', 'docs/release-notes.md': NOTES + '\nghcr.io/airencracken/folkweave:v0.5.0\n'})), [])

    def test_bad_version_files_are_rejected(self):
        for version in ('', 'v0.6.0', '0.6', '0.6.0.1', '../../etc/passwd', '0.6.0; rm -rf /', '٠.6.0'):
            with self.subTest(version=version):
                self.assertTrue(check_docs.check(self.app({'VERSION': version})))
        self.assertTrue(check_docs.check(self.app({'VERSION': None})))
        self.assertTrue(check_docs.check(self.app({'README.md': None})))

    def test_binary_and_unicode_docs_do_not_crash(self):
        root = self.app({'docs/notes.md': 'café — ghcr.io/airencracken/folkweave:v0.6.0\n'})
        (root / 'docs' / 'blob.md').write_bytes(b'\xff\xfe\x00binary')
        self.assertEqual(check_docs.check(root), [])

    def test_command_line_exit_status(self):
        good, bad = self.app({}), self.app({'VERSION': '0.7.0\n'})
        script = str(Path(check_docs.__file__))
        self.assertEqual(subprocess.run([sys.executable, script, str(good)], capture_output=True).returncode, 0)
        result = subprocess.run([sys.executable, script, str(bad)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn('0.7.0', result.stderr)


if __name__ == '__main__':
    unittest.main()
