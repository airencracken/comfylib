"""Create and verify a source-bound release plan before creating immutable tags."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

from common import project, TOOLS

VERSION = re.compile(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z')
APPS = ('folkweave','imvault','songstead','tidings','witmoot')


def catalog(path):
    value = json.loads(Path(path).read_text())
    if set(value) != {'schema','releases'} or value['schema'] != 1 or not isinstance(value['releases'],dict):
        raise ValueError('invalid release catalog schema')
    if not set(APPS + ('comfylib','comfylib/oidc','arise')).issubset(value['releases']):
        raise ValueError('release catalog lacks suite versions')
    for name, version in value['releases'].items():
        if name not in APPS + ('comfylib','comfylib/oidc','arise') or not isinstance(version,str) or not VERSION.fullmatch(version):
            raise ValueError('invalid release catalog name or version')
    return value['releases']


def git(root,*args):
    return subprocess.check_output(['git',*args],cwd=root,text=True).strip()


def inspect(root, expected=None, require_clean=False):
    root, app, _ = project(root)
    compose = (root / 'docker-compose.yml').read_text()
    versions = re.findall(re.escape('ghcr.io/airencracken/' + app + ':v') + r'([0-9]+\.[0-9]+\.[0-9]+)',compose)
    if len(versions) != 1 or not VERSION.fullmatch(versions[0]):
        raise ValueError('Compose must have exactly one pinned stable image: ' + app)
    version = expected or versions[0]
    if not VERSION.fullmatch(version) or versions[0] != version:
        raise ValueError('Compose version differs from release plan: ' + app)
    if (root / 'VERSION').exists() and (root / 'VERSION').read_text().strip() != version:
        raise ValueError('VERSION differs from release plan: ' + app)
    main = (root / 'cmd' / app / 'main.go').read_text()
    fallback = re.search(r'var version\s*=\s*"([^"]*)"',main)
    if not fallback or fallback[1] not in ('','development',version):
        raise ValueError('binary source version differs from release plan: ' + app)
    readme = (root / 'README.md').read_text()
    first = re.search(r'^## ' + re.escape(app) + r' ([0-9]+\.[0-9]+\.[0-9]+)\s*$',readme,re.I|re.M)
    if first and first[1] != version:
        raise ValueError('current README release heading differs: ' + app)
    candidates = [root/'docs/release-notes'/(version+'.md'),root/'docs/release-notes.md',root/'CHANGELOG.md']
    if not any(path.is_file() and version in path.read_text() for path in candidates):
        raise ValueError('release notes are missing for planned version: '+app)
    workflow = (root / '.github/workflows/release.yml').read_text()
    for contract in ('RELEASE_TAG_INPUT: ${{ inputs.release_tag }}','git merge-base --is-ancestor HEAD origin/master',
                     'refusing to replace it','--verify-tag'):
        if contract not in workflow:
            raise ValueError('missing release publication contract: ' + contract)
    if not re.search(r'needs:\s*\[\s*build\s*,\s*(?:packages\s*,\s*)?containers\s*,\s*browser\s*\]',workflow):
        raise ValueError('browser checks must gate public release publication')
    if 'uses: ./.github/workflows/browser.yml' not in workflow:
        raise ValueError('release must invoke the shared browser workflow')
    dirty = bool(git(root,'status','--porcelain'))
    if require_clean and dirty:
        raise ValueError('release source must be committed and clean: ' + app)
    paths = ['go.mod','go.sum','docker-compose.yml','cmd/' + app + '/main.go',
             '.goreleaser.yaml','.github/workflows/release.yml','.github/workflows/containers.yml','README.md']
    if (root / 'VERSION').exists(): paths.append('VERSION')
    return {'app':app,'version':version,'tag':'v'+version,'commit':git(root,'rev-parse','HEAD'),
            'clean':not dirty,'inputs':{name:hashlib.sha256((root/name).read_bytes()).hexdigest() for name in paths}}


def suite(root, versions):
    plans = [inspect(root/app,versions[app]) for app in APPS]
    for app in APPS:
        module = (root/app/'go.mod').read_text()
        for name in ('comfylib','comfylib/oidc'):
            if not re.search(r'^\s*github.com/airencracken/'+re.escape(name)+r' v'+re.escape(versions[name])+r'(?:\s|$)',module,re.M):
                raise ValueError('module version differs from catalog: '+app+': '+name)
        if not (root/'comfyware/www-apps'/app/f'{app}-{versions[app]}.ebuild').is_file():
            raise ValueError('overlay recipe differs from catalog: '+app)
    website = json.loads((root/'comfyware_org/scripts/site/releases.json').read_text())
    for name, version in website.items():
        if versions.get(name) != version:
            raise ValueError('website version differs from catalog: '+name)
    return plans


def cold_cache(root):
    with tempfile.TemporaryDirectory(prefix='comfy-release-cache-') as directory:
        env = os.environ | {'GOWORK':'off','GOMODCACHE':directory}
        version=subprocess.check_output(['go','list','-m','-f','{{.Version}}','github.com/airencracken/comfylib'],cwd=root,env=env,text=True).strip()
        if not version.startswith('v') or not VERSION.fullmatch(version[1:]):
            raise ValueError('published module must use a stable version')
        result=subprocess.run(['go','mod','download','-json','github.com/airencracken/comfylib@'+version],cwd=root,env=env,check=True,capture_output=True,text=True)
        revision=json.loads(result.stdout).get('Origin',{}).get('Hash')
        if not revision or not re.fullmatch('[a-f0-9]{40}',revision):
            raise ValueError('published module must identify its immutable source revision')
        from check_consumer import verify_workflows
        verify_workflows(root,revision)
        subprocess.run(['go','mod','verify'],cwd=root,env=env,check=True)
        subprocess.run(['python3','scripts/comfy.py','check'],cwd=root,
                       env=env|{'GOPROXY':'off','GOSUMDB':'off'},check=True)


def write(path, value):
    path = Path(path);path.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.NamedTemporaryFile('w',dir=path.parent,delete=False) as stream:
        temporary=Path(stream.name)
        try:
            stream.write(json.dumps(value,indent=2)+'\n');stream.flush();os.fsync(stream.fileno())
            os.replace(temporary,path)
        finally:
            temporary.unlink(missing_ok=True)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--catalog',type=Path)
    parser.add_argument('--suite-root',type=Path)
    parser.add_argument('--version')
    parser.add_argument('--require-clean',action='store_true')
    parser.add_argument('--cold-cache',action='store_true')
    parser.add_argument('--output',type=Path)
    parser.add_argument('--verify-plan',type=Path)
    args=parser.parse_args()
    root,_,_=project()
    if args.suite_root:
        if not args.catalog:parser.error('--suite-root requires --catalog')
        plans=suite(args.suite_root,catalog(args.catalog))
    else:
        plans=[inspect(root,args.version,args.require_clean)]
    value={'schema':1,'plans':plans}
    if args.verify_plan and json.loads(args.verify_plan.read_text())!=value:
        raise ValueError('release source or metadata changed after plan approval')
    if args.cold_cache:cold_cache(root)
    if args.output:write(args.output,value)
    print(json.dumps(value,indent=2))


if __name__=='__main__':main()
