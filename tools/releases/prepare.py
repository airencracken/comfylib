"""Prepare service files and linked license notices from one shared implementation."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

from common import private_file, project


def modules(root, app):
    found = {}
    for arch in ('amd64', 'arm64'):
        env = os.environ | {'GOWORK': 'off', 'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': arch}
        result = subprocess.run(['go', 'list', '-buildvcs=false', '-deps', '-f',
                                 '{{with .Module}}{{if not .Main}}{{.Path}}|{{.Dir}}{{end}}{{end}}',
                                 './cmd/' + app], cwd=root, env=env, check=True, capture_output=True, text=True)
        for line in result.stdout.splitlines():
            if not line:
                continue
            name, directory = line.split('|', 1)
            path = Path(directory)
            if not path.is_absolute() or not path.is_dir():
                raise ValueError('module cache directory unavailable: ' + name)
            if name in found and found[name] != path:
                raise ValueError('architectures resolve different module contents: ' + name)
            found[name] = path
    return found


def notices(root, app, config, linked=None):
    output = ['Go runtime and linked dependency license notices\n',
              private_file(root, config['layout'] + '/licenses/go.LICENSE').read_text()]
    for name, directory in sorted((linked if linked is not None else modules(root, app)).items()):
        paths = sorted({p for pattern in ('LICENSE*', 'COPYING*', 'NOTICE*')
                        for p in directory.glob(pattern) if p.is_file()})
        if not paths:
            raise ValueError('No license notice found for ' + name)
        for path in paths:
            output.extend(['\n--- ' + name + ': ' + path.name + ' ---\n', path.read_text()])
    for relative in config['licenses']:
        output.extend(['\n--- ' + relative + ' ---\n', private_file(root, relative).read_text()])
    return '\n'.join(output) + '\n'


def prepared(root, app, config, linked=None):
    text = notices(root, app, config, linked)
    files = {'THIRD_PARTY_NOTICES.txt': text,
             'copyright': 'Copyright (C) 2026 Marcus J. Hildum.\nLicensed under AGPL-3.0-or-later.\n'
                          + private_file(root, 'LICENSE').read_text() + '\n' + text}
    if app != 'songstead':
        base = config['layout'] + '/systemd/' + app
        files[app + '.service'] = private_file(root, base + '.service').read_text().replace('/usr/local/bin/' + app, '/usr/bin/' + app)
        files[app + '.env'] = private_file(root, base + '.env').read_text()
        if app == 'imvault':
            files[app + '.env'] += '\n# Debian package defaults.\nIMVAULT_ADDR="127.0.0.1:8080"\n'
        if config['backup_service']:
            files[app + '-backup.service'] = private_file(root, base + '-backup.service').read_text().replace('/usr/local/bin/' + app, '/usr/bin/' + app)
    return files


def write_files(directory, files):
    # Resolve all inputs before touching output. Each destination is then replaced
    # atomically, so interrupted preparation never leaves a truncated notice.
    directory.mkdir(parents=True, exist_ok=True)
    for name, content in files.items():
        with tempfile.NamedTemporaryFile('w', dir=directory, delete=False) as stream:
            temporary = Path(stream.name)
            try:
                stream.write(content)
                stream.flush()
                os.fsync(stream.fileno())
            except BaseException:
                temporary.unlink(missing_ok=True)
                raise
        try:
            temporary.chmod(0o600 if name.endswith('.env') else 0o644)
            os.replace(temporary, directory / name)
        finally:
            temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--notices', action='store_true')
    args = parser.parse_args()
    root, app, config = project()
    if args.notices:
        print(notices(root, app, config), end='')
    else:
        directory = Path(os.environ.get('RELEASE_DIR', root / '.release'))
        write_files(directory, prepared(root, app, config))


if __name__ == '__main__':
    main()
