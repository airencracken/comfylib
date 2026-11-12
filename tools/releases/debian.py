"""Run the command-profile Debian package lifecycle without Go in the container."""
import argparse
from pathlib import Path
import subprocess

from common import project

IMAGE='public.ecr.aws/docker/library/debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f'


def command(root, app, arch):
    if app not in ('folkweave','tidings') or arch not in ('amd64','arm64'):
        raise ValueError('unsupported Debian lifecycle profile')
    packages=list((root/'dist').glob(f'{app}_*_{arch}.deb'))
    if len(packages)!=1 or not packages[0].is_file():
        raise ValueError('expected exactly one architecture-specific Debian package')
    return ['docker','run','--rm','--platform','linux/'+arch,'--label','org.comfyware.fixture=true',
            '--mount','type=bind,src='+str(packages[0])+',dst=/release.deb,readonly',
            '--mount','type=bind,src='+str(Path(__file__).with_name('debian-contract.sh'))+',dst=/tests/test-deb.sh,readonly',
            IMAGE,'sh','/tests/test-deb.sh',app]


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arch',choices=['amd64','arm64'],required=True)
    args=parser.parse_args()
    root,app,_=project()
    subprocess.run(command(root,app,args.arch),check=True)


if __name__=='__main__':main()
