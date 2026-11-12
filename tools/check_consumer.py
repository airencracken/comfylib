#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Reject copied shared implementations and verify published dependency wiring."""
import argparse
import os
from pathlib import Path
import re

from app import application


def verify(root):
    root = application(root)
    module = (root / 'go.mod').read_text()
    if re.search(r'^replace\b', module, re.M):
        raise ValueError('application dependencies must resolve without replace directives')
    for name in ('github.com/airencracken/comfylib', 'github.com/airencracken/comfylib/oidc'):
        if not re.search(r'^\s*' + re.escape(name) + r' v\d+\.\d+\.\d+(?:-[\w.-]+)?(?:\s|$)', module, re.M):
            raise ValueError('missing explicit dependency pin: ' + name)
    for relative in ('internal/suite', 'internal/sharedoidc', 'scripts/suite-assets.json',
                     'scripts/mutate.py', 'tools/mutate.py', 'scripts/scan_artifacts.py',
                     'scripts/test_service_config.py', 'scripts/test_suite_deployment.py', 'scripts/test_release_dispatch.py'):
        if (root / relative).exists():
            raise ValueError('shared implementation must come from Comfylib: ' + relative)
    for pattern in ('comfy-components.css', 'comfy-suite.js'):
        if any((root / 'internal').rglob(pattern)):
            raise ValueError('shared browser asset must come from ui.Assets(): ' + pattern)
    if list((root / 'scripts/containers').glob('*.py')):
        raise ValueError('container implementations must come from Comfylib')
    bootstrap = Path(__file__).with_name('app_bootstrap.py').read_bytes()
    if (root / 'scripts/comfy.py').read_bytes() != bootstrap:
        raise ValueError('application tool launcher differs from the shared bootstrap contract')
    templates = '\n'.join(p.read_text() for p in (root / 'internal').rglob('*.html'))
    for asset in ('components.css', 'suite.js'):
        if '/static/comfyware/' + asset not in templates:
            raise ValueError('application templates must load shared asset: ' + asset)
    sources = '\n'.join(p.read_text() for p in (root / 'internal').rglob('*.go'))
    if '/internal/suite"' in sources or '/internal/sharedoidc"' in sources:
        raise ValueError('application imports a copied shared implementation')
    if (root / 'scripts/browser-runtime.mjs').exists():
        if (root / 'scripts/browser-runtime.mjs').read_bytes() != Path(__file__).with_name('browser_bootstrap.mjs').read_bytes():
            raise ValueError('browser runtime must use the exact pinned bootstrap')
    core = re.search(r'^\s*github.com/airencracken/comfylib v(\d+)\.(\d+)\.',module,re.M)
    if core and tuple(map(int,core.groups())) >= (0,4):
        verify_workflows(root)


def verify_workflows(root, revision=None):
    for name in ('containers','browser'):
        source = (root / '.github/workflows' / (name + '.yml')).read_text()
        references = re.findall(r'uses: airencracken/comfylib/\.github/workflows/' + name + r'\.yml@([a-f0-9]{40})(?:\s|$)',source)
        if len(references) != 1 or (revision and references[0] != revision):
            raise ValueError('shared workflow must pin the library revision: '+name)
        if re.search(r'^\s*(run:|steps:)',source,re.M):
            raise ValueError('shared workflow implementation was copied into the app: '+name)
        if name == 'containers':
            for argument in ('source_ref','release_tag','publish'):
                if argument + ': ${{ inputs.' + argument + ' }}' not in source:
                    raise ValueError('shared workflow input is not forwarded: '+argument)


def workflow_source(root, name):
    path = root / '.github/workflows' / (name + '.yml')
    text = path.read_text()
    if 'uses: airencracken/comfylib/.github/workflows/' + name + '.yml@' in text:
        verify_workflows(root)
        return (Path(__file__).resolve().parents[1] / '.github/workflows' / (name + '.yml')).read_text()
    return text


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('apps', nargs='*', type=Path)
    args = parser.parse_args()
    for root in args.apps or [Path(os.environ.get('COMFYWARE_APP_ROOT', Path.cwd()))]:
        verify(root)
        print(root.name + ': shared module integration passed')


if __name__ == '__main__':
    main()
