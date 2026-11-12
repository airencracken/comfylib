#!/usr/bin/env python3
"""Audit independent suite checkouts and keep a machine-readable local report."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time

APPS = ('comfyware', 'comfyware_org', 'comfylib', 'folkweave', 'imvault', 'songstead', 'tidings', 'witmoot')


def checks(app, mutations=False, browsers=False, checkout=None):
    if app == 'comfyware':
        root=Path(checkout) if checkout is not None else Path(__file__).resolve().parents[2]/app
        commands=[['python3', str(path.relative_to(root))] for path in sorted((root/'scripts').glob('test*.py'))]
        if not commands:
            raise ValueError('overlay has no test scripts')
        return commands+[['bash','scripts/test-make-deps.sh']]
    if app == 'comfyware_org':
        return [['make', 'check' if browsers else 'test-content'], ['make', 'test-http', 'test-deploy', 'check-config']]
    commands = [['go', 'vet', './...'], ['go', 'test', '-race', './...'], ['go', 'run', 'golang.org/x/vuln/cmd/govulncheck@v1.9.0', './...']]
    if app != 'comfylib':
        commands.append(['python3', 'scripts/comfy.py', 'check'])
    else:
        commands.append(['node', '--test', 'tools/test_suite.cjs'])
    if mutations:
        commands.append(['make', 'test-mutations'])
    if browsers and app != 'comfylib':
        commands.append(['make', 'test-browser'])
    return commands


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path, help='directory containing the eight project checkouts')
    parser.add_argument('--output', type=Path, default=Path('suite-audit'))
    parser.add_argument('--mutations', action='store_true')
    parser.add_argument('--browsers', action='store_true')
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    result = {'checks': [], 'passed': True, 'complete': False}
    environment = os.environ | {'GOWORK': 'off'}
    for app in APPS:
        checkout = args.root.resolve() / app
        if not (checkout / ('go.mod' if app not in ('comfyware', 'comfyware_org') else 'README.md')).is_file():
            parser.error('missing module: ' + str(checkout))
        for i, command in enumerate(checks(app, args.mutations, args.browsers, checkout)):
            log = args.output / f'{app}-{i}.log'
            start = time.monotonic()
            with log.open('w') as out:
                try:
                    status = subprocess.run(command, cwd=checkout, env=environment, stdout=out, stderr=subprocess.STDOUT, timeout=1800).returncode
                except (OSError, subprocess.TimeoutExpired) as error:
                    out.write(str(error) + '\n')
                    status = 1
            result['checks'].append({'app': app, 'command': command, 'exit': status, 'seconds': round(time.monotonic()-start, 2), 'log': log.name})
            result['passed'] &= status == 0
            (args.output / 'results.json').write_text(json.dumps(result, indent=2)+'\n')
            print(app, ' '.join(command), 'PASS' if status == 0 else 'FAIL', flush=True)
    result['complete'] = True
    (args.output / 'results.json').write_text(json.dumps(result, indent=2)+'\n')
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
