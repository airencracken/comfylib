#!/usr/bin/env python3
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

HERE=Path(__file__).resolve().parent

def module(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    result=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result

class SuiteToolsTest(unittest.TestCase):
    def test_manifest_integrity_and_adversarial_paths(self):
        verify=module('verify_suite').verify
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'scripts').mkdir();(root/'internal').mkdir()
            data=b'asset';(root/'internal/asset').write_bytes(data)
            manifest={'source':'github.com/airencracken/comfylib','files':{'internal/asset':hashlib.sha256(data).hexdigest()}}
            path=root/'scripts/suite-assets.json';path.write_text(json.dumps(manifest));verify(root)
            (root/'internal/asset').write_bytes(b'changed')
            with self.assertRaises(ValueError):verify(root)
            for name in ('/etc/passwd','internal/../../etc/passwd','scripts/asset'):
                manifest['files']={name:'0'*64};path.write_text(json.dumps(manifest))
                with self.assertRaises(ValueError):verify(root)

    def test_archive_formats_paths_and_empty(self):
        scanner=module('scan_artifacts')
        with tempfile.TemporaryDirectory() as directory:
            archive=Path(directory)/'release.tar.gz'
            for name,link,expected in [('app',False,True),('../outside',False,False),('app',True,False)]:
                with tarfile.open(archive,'w:gz') as output:
                    info=tarfile.TarInfo(name);info.mode=0o755
                    if link:info.type=tarfile.SYMTYPE;info.linkname='/etc/passwd';output.addfile(info)
                    else:data=b'\x7fELFfixture';info.size=len(data);output.addfile(info,io.BytesIO(data))
                if expected:self.assertEqual(list(scanner.binaries(archive)),[('app',b'\x7fELFfixture')])
                else:
                    with self.assertRaises(ValueError):list(scanner.binaries(archive))

    def test_sync_is_repeatable_and_checks_canonical_sources(self):
        sync=module('sync_suite').sync
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)/'feature-branch';root.mkdir();(root/'go.mod').write_text('module github.com/airencracken/songstead\n');sync(root);sync(root,True);sync(root);sync(root,True)
            path=root/'internal/suite/apps.go';path.write_text(path.read_text()+'\n// changed\n')
            with self.assertRaises(ValueError):sync(root,True)

    def test_rollout_requires_release_version(self):
        rollout=module('rollout_comfylib')
        for bad in ('latest','master','v1.2','v1.2.3; touch /tmp/no','v1.2.3\n'):
            with self.assertRaises(Exception):rollout.version(bad)
        self.assertEqual(rollout.version('v0.1.8'),'v0.1.8')

    def test_audit_report_records_failure_and_completeness(self):
        import contextlib
        import sys
        from unittest.mock import patch
        audit=module('audit_suite')
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            for app in audit.APPS:
                checkout=root/app;checkout.mkdir()
                (checkout/('README.md' if app in ('comfyware','comfyware_org') else 'go.mod')).write_text('fixture')
            output=root/'report'
            def commands(app,*_):return [[sys.executable,'-c','print("fixture");raise SystemExit('+('1' if app=='songstead' else '0')+')']]
            with patch.object(audit,'checks',commands),patch.object(sys,'argv',['audit_suite.py',str(root),'--output',str(output)]),contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(audit.main(),1)
            result=json.loads((output/'results.json').read_text())
            self.assertTrue(result['complete']);self.assertFalse(result['passed']);self.assertEqual(len(result['checks']),8)
            self.assertIn('fixture',(output/'songstead-0.log').read_text())

    def test_runner_has_required_checks(self):
        audit=module('audit_suite')
        commands=audit.checks('songstead',True,True)
        self.assertIn(['go','test','-race','./...'],commands)
        self.assertIn(['make','test-mutations'],commands)
        self.assertIn(['make','test-browser'],commands)
        self.assertTrue(any('govulncheck' in ' '.join(command) for command in commands))

if __name__=='__main__':unittest.main()
