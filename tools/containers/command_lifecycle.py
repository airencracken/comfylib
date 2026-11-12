#!/usr/bin/env python3
"""Fresh installation, account creation, restart, live backup and restore."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import time
import tempfile
import urllib.request
import uuid
from release import config, run, stage, version


def download_previous(cfg, arch, directory):
    previous = version(cfg['previous'])
    app = cfg['app']
    base = f'https://github.com/airencracken/{app}/releases/download/v{previous}'
    for name in (f'{app}_{previous}_linux_{arch}.tar.gz', f'{app}_{previous}_checksums.txt'):
        with urllib.request.urlopen(f'{base}/{name}', timeout=120) as response:
            (directory / name).write_bytes(response.read())
    target = directory / 'previous'
    stage(directory, target, app, arch)
    return target / 'bin' / app


class Lifecycle:
    def __init__(self, cfg, image):
        self.cfg, self.image = cfg, image
        self.app = cfg['app']
        self.prefix = self.app.upper()
        self.name = self.app + '-release-test-' + uuid.uuid4().hex
        self.containers = []

    def options(self, directory='/data'):
        return ['--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges:true',
                '--tmpfs', '/tmp:rw,noexec,nosuid,size=128m,mode=1777',
                '-v', self.name + ':/data', '-e', self.prefix + '_DATA_DIR=' + directory,
                '-e', self.prefix + '_BASE_URL=https://' + self.app + '.example.org',
                '-e', self.prefix + '_BACKUP_INTERVAL=0']

    def once(self, *args, stdin=None, shell=False, binary=None):
        return run('docker', 'run', '--rm', '-i', *self.options(),
                   *(['-v', str(binary) + ':/previous/app:ro', '--entrypoint', '/previous/app'] if binary else ['--entrypoint', 'sh'] if shell else []), self.image, *args, input=stdin)

    def start(self, directory='/data', binary=None):
        name = self.name + '-' + str(len(self.containers))
        run('docker', 'run', '-d', '--name', name, *self.options(directory),
            *(['-v', str(binary) + ':/previous/app:ro', '--entrypoint', '/previous/app'] if binary else []), self.image, *(['serve'] if binary else []))
        self.containers.append(name)
        for _ in range(100):
            state = json.loads(run('docker', 'inspect', name))[0]['State']
            if not state['Running']:
                raise AssertionError(run('docker', 'logs', name))
            probe = subprocess.run(['docker', 'exec', name, 'wget', '-qO-', 'http://127.0.0.1:8080/healthz'], capture_output=True)
            if probe.returncode == 0:
                return name
            time.sleep(.1)
        raise AssertionError('container did not become ready')

    def stop(self, name):
        run('docker', 'stop', '--time', '20', name)
        assert json.loads(run('docker', 'inspect', name))[0]['State']['ExitCode'] == 0

    def prepare_owner(self, previous=None):
        password = 'container-test-password-long-enough\n'
        if previous:
            self.once('create-owner', '--username', 'containerowner', '--password-stdin',
                      stdin=password, binary=previous)
            previous_server = self.start(binary=previous)
            self.stop(previous_server)
            # This command fails if the existing account was lost during migration.
            self.once('set-password', '--username', 'containerowner', '--password-stdin', stdin=password)
            return
        if self.app == 'folkweave':
            self.once('create-owner', '--username', 'containerowner', '--name', 'Owner', '--password-stdin', stdin='container-test-password-long-enough\n')
        else:
            self.once('-c', 'umask 077; cat > /tmp/password || exit 1; exec /usr/local/bin/tidings create-owner --username containerowner --password-file /tmp/password', shell=True, stdin='container-test-password-long-enough\n')

    def check(self, version, binary=None, previous=None):
        info = json.loads(run('docker', 'image', 'inspect', self.image))[0]
        assert info['Config']['User'] == '10001:10001'
        assert info['Config']['Healthcheck']['Test'][0] == 'CMD-SHELL'
        run('docker', 'volume', 'create', '--label', 'org.comfyware.fixture=true', self.name)
        assert self.once('--version').split()[-1] == version
        self.once('-c', 'test "$(id -u)" = 10001 && test "$(stat -c %a /data)" = 700 && test -s /etc/ssl/certs/ca-certificates.crt && test -f /usr/share/zoneinfo/UTC && command -v zstd', shell=True)
        if binary:
            raw = subprocess.run(['docker', 'run', '--rm', '--entrypoint', 'cat', self.image,
                                  '/usr/local/bin/' + self.app], capture_output=True, check=True).stdout
            assert hashlib.sha256(raw).digest() == hashlib.sha256(binary.read_bytes()).digest()
        self.prepare_owner(previous)
        first = self.start()
        run('docker', 'exec', first, 'sh', '-c', info['Config']['Healthcheck']['Test'][1])
        page = run('docker', 'exec', first, 'wget', '--header=Host: ' + self.app + '.example.org', '-qO-', 'http://127.0.0.1:8080/login')
        assert '<form' in page and 'Password' in page
        if self.app == 'folkweave':
            self.once('backup', '--output', '/data/manual')
            self.once('restore', '--from', '/data/manual/folkweave.db', '--data', '/data/restored')
        else:
            result = self.once('backup', '--destination', '/data/manual')
            archive = result.removeprefix('Verified backup: ').strip()
            assert archive.startswith('/data/manual/') and re.fullmatch(r'/[a-zA-Z0-9/_.-]+', archive)
            self.once('restore', '--archive', archive, '--destination', '/data/restored')
        self.stop(first)
        restarted = self.start(); self.stop(restarted)
        restored = self.start('/data/restored'); self.stop(restored)
        for value in ('not-an-ip', '127.0.0.1;touch /tmp/injected', '$(id)'):
            failed = subprocess.run(['docker', 'run', '--rm', *self.options(), '-e',
                                     self.prefix + '_TRUSTED_PROXIES=' + value, self.image],
                                    capture_output=True, timeout=10)
            assert failed.returncode != 0, 'invalid proxy setting accepted'
        print(self.app + ': version, private non-root volume, CA trust, owner creation, readiness, live backup, restart and restored instance passed')

    def close(self):
        for name in self.containers:
            subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
        subprocess.run(['docker', 'volume', 'rm', self.name], capture_output=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--image', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--upgrade', action='store_true')
    args = parser.parse_args()
    test = Lifecycle(config(), args.image)
    try:
        with tempfile.TemporaryDirectory() as temporary:
            arch = json.loads(run('docker', 'image', 'inspect', args.image))[0]['Architecture']
            previous = download_previous(test.cfg, arch, Path(temporary)) if args.upgrade else None
            test.check(args.version, args.binary, previous)
    except Exception:
        for name in test.containers:
            print(run('docker', 'logs', name))
        raise
    finally:
        test.close()


if __name__ == '__main__':
    main()
