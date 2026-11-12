#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Run shared maintenance tools against an explicitly selected application."""
import argparse
import os
from pathlib import Path
import re
import subprocess
import sys

HERE = Path(__file__).resolve().parent
APPS = ('folkweave', 'imvault', 'songstead', 'tidings', 'witmoot')
TOOLS = {
    'check': 'check_consumer.py',
    'service-config': 'check_service_config.py',
    'suite-deployment': 'test_suite_deployment.py',
    'release-dispatch': 'test_release_dispatch.py',
    'scan-artifacts': 'scan_artifacts.py',
    'prepare-release': 'releases/prepare.py',
    'check-artifacts': 'releases/artifacts.py',
    'test-deb': 'releases/debian.py',
    'release-plan': 'releases/plan.py',
    'password-cli': 'check_password_cli.py',
    'metrics-browser': 'check_metrics.py',
    'mutate': 'mutate.py',
    'container-release': 'containers/release.py',
    'container-registry': 'containers/check_registry.py',
}


def application(root):
    root = Path(root).resolve()
    match = re.search(r'^module\s+(\S+)\s*$', (root / 'go.mod').read_text(), re.M)
    if match is None or match[1] not in (*('github.com/airencracken/' + app for app in APPS), 'imvault', 'witmoot'):
        raise ValueError('expected an independent Comfyware application module')
    return root


def command(tool, root):
    if tool == 'test-containers':
        return [sys.executable, '-B', '-m', 'unittest', 'discover', '-s', str(HERE / 'containers'), '-p', 'test_*.py', '-v']
    if tool == 'container-lifecycle':
        sys.path.insert(0, str(HERE / 'containers'))
        from release import config
        name = 'command_lifecycle.py' if config(root)['lifecycle'] == 'commands' else 'lifecycle.py'
        return [sys.executable, str(HERE / 'containers' / name)]
    return [sys.executable, str(HERE / TOOLS[tool])]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('tool', choices=(*TOOLS, 'test-containers', 'container-lifecycle'))
    parser.add_argument('arguments', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    try:
        root = application(os.environ.get('COMFYWARE_APP_ROOT', Path.cwd()))
    except (OSError, ValueError) as error:
        parser.error(str(error))
    env = os.environ | {'GOWORK': 'off', 'COMFYWARE_APP_ROOT': str(root)}
    return subprocess.call(command(args.tool, root) + args.arguments, cwd=root, env=env)


if __name__ == '__main__':
    raise SystemExit(main())
