#!/usr/bin/env python3
"""Plan, or apply and test, a published Comfylib version across app checkouts."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess

MODULE = 'github.com/airencracken/comfylib'


def version(raw):
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', raw):
        raise argparse.ArgumentTypeError('use an explicit release version, such as v0.1.8')
    return raw


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version', type=version)
    parser.add_argument('apps', type=Path, nargs='+')
    parser.add_argument('--oidc-version', type=version, help='also update the optional OIDC companion to this published version')
    parser.add_argument('--apply', action='store_true', help='download the published version, update and test; never commit or publish')
    args = parser.parse_args()
    env = os.environ | {'GOWORK': 'off'}
    plans = []
    modules = [MODULE+'@'+args.version]
    if args.oidc_version:
        modules.append(MODULE+'/oidc@'+args.oidc_version)
    for root in args.apps:
        root = root.resolve()
        text = (root / 'go.mod').read_text()
        if MODULE not in text or re.search(r'^replace\s', text, re.M):
            parser.error('expected an independent app module without replace directives: ' + str(root))
        commands = [['go', 'get', *modules], ['go', 'mod', 'tidy'], ['go', 'test', '-race', './...'], ['python3', 'scripts/comfy.py', 'check']]
        plans.append({'checkout': str(root), 'commands': commands})
    print(json.dumps(plans, indent=2), flush=True)
    if not args.apply:
        return 0
    # Resolve a published immutable version before changing any module files.
    for plan in plans:
        root = plan['checkout']
        dirty = subprocess.run(['git', 'status', '--porcelain', '--', 'go.mod', 'go.sum'], cwd=root, capture_output=True, text=True, check=True)
        if dirty.stdout:
            parser.error('commit or stash module changes before rollout: ' + root)
        for dependency in modules:
            if subprocess.run(['go', 'mod', 'download', dependency], cwd=root, env=env).returncode:
                return 1
    for plan in plans:
        for command in plan['commands']:
            if subprocess.run(command, cwd=plan['checkout'], env=env).returncode:
                print('Rollout stopped; review the local changes in '+plan['checkout'])
                return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
