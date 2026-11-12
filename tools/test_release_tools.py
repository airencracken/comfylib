"""Release schema, source-binding, atomic output and adversarial archive tests."""
import hashlib
import io
import json
from pathlib import Path
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

HERE=Path(__file__).resolve().parent
sys.path.insert(0,str(HERE/'releases'))
import debian
import artifacts
import plan
import prepare
from test_app_tools import fixture


def release_fixture(root):
    fixture(root)
    files={'go.sum':'', 'VERSION':'1.2.3\n','README.md':'## Folkweave 1.2.3\n',
           'docs/release-notes.md':'# Folkweave 1.2.3\n', 'docker-compose.yml':'image: ghcr.io/airencracken/folkweave:v1.2.3\n',
           'cmd/folkweave/main.go':'package main\nvar version = "1.2.3"\n',
           '.goreleaser.yaml':'version: 2\n',
           '.github/workflows/release.yml':'\n'.join(['RELEASE_TAG_INPUT: ${{ inputs.release_tag }}',
             'git merge-base --is-ancestor HEAD origin/master','refusing to replace it','--verify-tag',
             'needs: [build, packages, containers, browser]','uses: ./.github/workflows/browser.yml']),
           '.github/workflows/containers.yml':'shared workflow',
           'LICENSE':'app license','deploy/licenses/go.LICENSE':'runtime license',
           'deploy/systemd/folkweave.service':'ExecStart=/usr/local/bin/folkweave serve\n',
           'deploy/systemd/folkweave.env':'FOLKWEAVE_ADDR="127.0.0.1:8084"\n',
           'internal/folkweave/static/alpine-csp.min.js.LICENSE':'alpine license',
           'internal/folkweave/static/htmx.min.js.LICENSE':'htmx license'}
    for relative,text in files.items():
        path=root/relative;path.parent.mkdir(parents=True,exist_ok=True);path.write_text(text)
    for args in [('init','-q','-b','master'),('add','.'),('-c','user.name=Fixture','-c','user.email=fixture@example.org','commit','-qm','fixture')]:
        subprocess.run(['git',*args],cwd=root,check=True,capture_output=True)
    return root


class ReleaseTools(unittest.TestCase):
    def test_version_source_clean_tree_and_publication_contract_mutations(self):
        with tempfile.TemporaryDirectory() as directory:
            root=release_fixture(Path(directory))
            original=plan.inspect(root,require_clean=True)
            for relative, replacement in [('VERSION','9.9.9'),('cmd/folkweave/main.go','var version = "9.9.9"'),
                    ('README.md','## Folkweave 9.9.9'),('docs/release-notes.md','No current release notes'),('docker-compose.yml','image: ghcr.io/airencracken/folkweave:latest'),
                    ('.github/workflows/release.yml','--verify-tag')]:
                path=root/relative;before=path.read_text();path.write_text(replacement)
                with self.subTest(relative=relative),self.assertRaises(ValueError):plan.inspect(root)
                path.write_text(before)
            (root/'untracked').write_text('changed')
            with self.assertRaises(ValueError):plan.inspect(root,require_clean=True)
            self.assertFalse(plan.inspect(root)['clean'])
            (root/'untracked').unlink()
            self.assertEqual(original,plan.inspect(root,require_clean=True))
            (root/'go.sum').write_text('changed')
            self.assertNotEqual(original['inputs'],plan.inspect(root)['inputs'])

    def test_catalog_schema_property_and_adversarial_values(self):
        valid={'schema':1,'releases':dict.fromkeys(plan.APPS+('comfylib','comfylib/oidc','arise'),'1.2.3')}
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/'catalog.json';path.write_text(json.dumps(valid))
            self.assertEqual(plan.catalog(path),valid['releases'])
            for value in ['latest','v1.2.3','01.2.3','1.2.3\n','$(false)','1.2.3/../../x',None,17]:
                bad=json.loads(json.dumps(valid));bad['releases']['tidings']=value;path.write_text(json.dumps(bad))
                with self.subTest(value=value),self.assertRaises(ValueError):plan.catalog(path)
            for version in (f'{major}.{minor}.{patch}' for major in range(3) for minor in range(3) for patch in range(3)):
                self.assertIsNotNone(plan.VERSION.fullmatch(version))
            bad=valid|{'extra':True};path.write_text(json.dumps(bad))
            with self.assertRaises(ValueError):plan.catalog(path)

    def test_preparation_missing_license_preserves_existing_output_and_literal_text(self):
        with tempfile.TemporaryDirectory() as directory:
            root=release_fixture(Path(directory)/'app');out=Path(directory)/'output';out.mkdir();(out/'marker').write_text('keep')
            config={'layout':'deploy','licenses':[],'backup_service':False}
            module=Path(directory)/'module';module.mkdir()
            with self.assertRaises(ValueError):prepare.prepared(root,'folkweave',config,{'example.org/module':module})
            self.assertEqual(list(out.iterdir()),[out/'marker'])
            (module/'LICENSE').write_text('literal $(false) `false` license')
            files=prepare.prepared(root,'folkweave',config,{'example.org/module':module})
            prepare.write_files(out,files)
            self.assertIn('$(false) `false`',(out/'THIRD_PARTY_NOTICES.txt').read_text())
            self.assertEqual((out/'folkweave.service').read_text(),'ExecStart=/usr/bin/folkweave serve\n')
            with patch.object(prepare.os,'replace',side_effect=OSError('interrupted')):
                with self.assertRaises(OSError):prepare.write_files(out,{'marker':'replace'})
            self.assertEqual((out/'marker').read_text(),'keep')
            self.assertEqual(set(p.name for p in out.iterdir()),set(files)|{'marker'})

    def test_source_archive_exactness_and_unsafe_members(self):
        with tempfile.TemporaryDirectory() as directory:
            root=release_fixture(Path(directory)/'app');path=Path(directory)/'folkweave_1.2.3_source.tar.gz'
            data=subprocess.check_output(['git','archive','--format=tar.gz','--prefix=folkweave_1.2.3_source/','HEAD'],cwd=root);path.write_bytes(data)
            artifacts.verify_source(root,path)
            (root/'VERSION').write_text('different')
            with self.assertRaises(ValueError):artifacts.verify_source(root,path)
            for name in ['/absolute','folkweave_1.2.3_source/../escape','folkweave_1.2.3_source/a\\b']:
                with tarfile.open(path,'w:gz') as archive:
                    entry=tarfile.TarInfo(name);entry.size=1;archive.addfile(entry,io.BytesIO(b'x'))
                with self.subTest(name=name),self.assertRaises(ValueError):artifacts.archive_files(path)

    def test_debian_driver_binds_the_exact_package_and_shared_contract(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'dist').mkdir();package=root/'dist/folkweave_1.2.3_amd64.deb';package.write_bytes(b'fixture')
            command=debian.command(root,'folkweave','amd64')
            self.assertIn('type=bind,src='+str(package)+',dst=/release.deb,readonly',command)
            self.assertIn(debian.IMAGE,command)
            self.assertTrue(any('debian-contract.sh' in part and part.endswith('readonly') for part in command))
            self.assertNotIn('go',command)
            with self.assertRaises(ValueError):debian.command(root,'folkweave','arm64')
            with self.assertRaises(ValueError):debian.command(root,'../../escape','amd64')
            with self.assertRaises(ValueError):debian.command(root,'folkweave','amd64;false')

    def test_native_layout_globs_keep_deployment_docs_and_source_only_adapters(self):
        with tempfile.TemporaryDirectory() as directory:
            root=release_fixture(Path(directory))
            (root/'deploy/adapter.go').write_text('package deploy')
            subprocess.run(['git','add','deploy/adapter.go'],cwd=root,check=True)
            contents={}
            for path in root.rglob('*'):
                if path.is_file() and '.git' not in path.parts:
                    relative=str(path.relative_to(root))
                    contents[relative]=(path.read_bytes(),0o644)
            del contents['deploy/adapter.go']
            artifacts.verify_native_documents(root,contents,'deploy')
            for relative in ['deploy/systemd/folkweave.service','README.md','docs/release-notes.md']:
                broken=dict(contents);broken[relative]=(b'changed',0o644)
                with self.subTest(relative=relative),self.assertRaises(ValueError):artifacts.verify_native_documents(root,broken,'deploy')

    def test_static_architecture_and_loader_mutations(self):
        value=bytearray(120);value[:6]=b'\x7fELF\x02\x01';struct.pack_into('<H',value,18,62);struct.pack_into('<Q',value,32,64);struct.pack_into('<HH',value,54,56,1)
        artifacts.static_elf(value,'amd64')
        with self.assertRaises(ValueError):artifacts.static_elf(value,'arm64')
        struct.pack_into('<I',value,64,3)
        with self.assertRaises(ValueError):artifacts.static_elf(value,'amd64')
        with self.assertRaises(ValueError):artifacts.static_elf(b'bad','amd64')


if __name__=='__main__':unittest.main(verbosity=2)
