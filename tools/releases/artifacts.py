"""Validate checksums, native architectures, package contents and exact source."""
import hashlib
from pathlib import Path, PurePosixPath
import re
import struct
import subprocess
import sys
import tarfile
import tempfile

from common import project, TOOLS
sys.path.insert(0, str(TOOLS / 'containers'))
from release import stage


def checksums(dist, app, debian):
    manifests = list(dist.glob(app + '_*_checksums.txt'))
    if len(manifests) != 1:
        raise ValueError('expected one checksum manifest')
    names = set()
    for line in manifests[0].read_text().splitlines():
        digest, name = line.split(maxsplit=1)
        name = name.removeprefix('*')
        if (Path(name).name != name or '\\' in name or name in names
                or not re.fullmatch('[a-f0-9]{64}', digest)):
            raise ValueError('invalid or duplicate checksum entry')
        if hashlib.sha256((dist / name).read_bytes()).hexdigest() != digest:
            raise ValueError('artifact checksum mismatch: ' + name)
        names.add(name)
    expected = {p.name for pattern in [app + '_*_linux_*.tar.gz', app + '_*_source.tar.gz']
                for p in dist.glob(pattern)}
    if debian:
        expected.update(p.name for p in dist.glob(app + '_*.deb'))
    if names != expected or len(names) != (5 if debian else 3):
        raise ValueError('unexpected artifact set')


def static_elf(binary, arch):
    if len(binary) < 64 or binary[:6] != b'\x7fELF\x02\x01' or int.from_bytes(binary[18:20], 'little') != {'amd64':62,'arm64':183}[arch]:
        raise ValueError('wrong ELF architecture')
    offset = struct.unpack_from('<Q', binary, 32)[0]
    size, count = struct.unpack_from('<HH', binary, 54)
    if not count or size < 56 or offset + count * size > len(binary):
        raise ValueError('invalid ELF program table')
    if any(struct.unpack_from('<I', binary, offset + i * size)[0] == 3 for i in range(count)):
        raise ValueError('release executable requires a dynamic loader')


def archive_files(path):
    prefix = path.name.removesuffix('.tar.gz')
    result = {}
    with tarfile.open(path) as archive:
        for member in archive:
            name = PurePosixPath(member.name)
            if (name.is_absolute() or '..' in name.parts or '\\' in member.name
                    or not name.parts or name.parts[0] != prefix
                    or not (member.isdir() or member.isfile())):
                raise ValueError('unsafe archive entry')
            relative = str(name.relative_to(prefix))
            if relative in result:
                raise ValueError('duplicate archive entry')
            result[relative] = (archive.extractfile(member).read(),member.mode) if member.isfile() else (None,member.mode)
    return result


def verify_source(root, source):
    actual = archive_files(source)
    tracked = subprocess.check_output(['git','ls-files','-z'],cwd=root).decode().split('\0')
    expected = set(filter(None,tracked))
    files = {name for name,(content,_) in actual.items() if content is not None}
    if files != expected:
        raise ValueError('source archive file set differs from tracked source')
    for name in expected:
        if actual[name][0] != (root / name).read_bytes():
            raise ValueError('source archive differs: ' + name)


def package(root, dist, temporary, app, arch, binary, config):
    packages = list(dist.glob(f'{app}_*_{arch}.deb'))
    if len(packages) != 1:
        raise ValueError('expected one Debian package per architecture')
    path = packages[0]
    fields = [('Package',app),('Architecture',arch)]
    if app in ('imvault','witmoot'): fields.append(('Suggests','bubblewrap'))
    for field, expected in fields:
        if subprocess.check_output(['dpkg-deb','-f',str(path),field],text=True).strip() != expected:
            raise ValueError('Debian package ' + field + ' differs')
    control = temporary / ('control-' + arch)
    files = temporary / ('package-' + arch)
    subprocess.run(['dpkg-deb','-e',str(path),str(control)],check=True)
    subprocess.run(['dpkg-deb','-x',str(path),str(files)],check=True)
    if (files / 'usr/bin' / app).read_bytes() != binary:
        raise ValueError('Debian and archive executables differ')
    if '/etc/' + app + '/' + app + '.env' not in (control / 'conffiles').read_text().splitlines():
        raise ValueError('service settings are not a conffile')
    for name in ('postinst','prerm','postrm'):
        script = control / name
        if not script.is_file() or not script.stat().st_mode & 0o111:
            raise ValueError('missing executable package script: ' + name)
        subprocess.run(['sh','-n',str(script)],check=True)
    service = (files / 'usr/lib/systemd/system' / (app + '.service')).read_text()
    for setting in ('StandardOutput=journal','StandardError=journal','SyslogIdentifier=' + app):
        if setting not in service:
            raise ValueError('package logging setting missing')
    if config['backup_service']:
        timer = 'usr/lib/systemd/system/' + app + '-backup.timer'
        if (files / timer).read_bytes() != (root / config['layout'] / 'systemd' / (app + '-backup.timer')).read_bytes():
            raise ValueError('backup timer differs')
        if (files / ('etc/cron.d/' + app + '-backup')).exists():
            raise ValueError('package enables automatic backups without consent')


def main():
    root, app, config = project()
    dist = root / 'dist'
    checksums(dist,app,config['debian'])
    with tempfile.TemporaryDirectory() as temporary:
        temporary = Path(temporary)
        for arch in ('amd64','arm64'):
            native = temporary / arch
            stage(dist,native,app,arch)
            binary = (native / 'bin' / app).read_bytes()
            static_elf(binary,arch)
            archive = next(dist.glob(f'{app}_*_linux_{arch}.tar.gz'))
            contents = archive_files(archive)
            for relative in ('docs/releases.md','docs/deployment.md','docs/containers.md',
                             config['layout'] + '/systemd/' + app + '.service',
                             config['layout'] + '/openrc/' + app):
                if relative not in contents or contents[relative][0] != (root / relative).read_bytes():
                    raise ValueError('archive operational document differs: ' + relative)
            if not contents[app][1] & 0o111:
                raise ValueError('native executable has no executable permission')
            tracked = subprocess.check_output(['git','ls-files','-z','--','docs',config['layout']],cwd=root).decode().split('\0')
            for relative in filter(None,tracked):
                if relative not in contents or contents[relative][0] != (root / relative).read_bytes():
                    raise ValueError('archive deployment example differs: ' + relative)
            if config['debian']:
                package(root,dist,temporary,app,arch,binary,config)
    sources = list(dist.glob(app + '_*_source.tar.gz'))
    if len(sources) != 1:
        raise ValueError('expected one source archive')
    verify_source(root,sources[0])
    print(app + ': source, notices, static architectures, checksums and package contracts passed')


if __name__ == '__main__':
    main()
