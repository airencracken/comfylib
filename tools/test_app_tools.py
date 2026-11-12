#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Exercise module consumers, tool dispatch and offline bootstrap boundaries."""
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import app
import check_consumer
import rollout_comfylib

HERE = Path(__file__).resolve().parent


def fixture(root, name='folkweave'):
    root.mkdir(parents=True, exist_ok=True)
    (root / 'scripts/containers').mkdir(parents=True)
    (root / 'internal/web').mkdir(parents=True)
    (root / 'go.mod').write_text('module github.com/airencracken/' + name + '\n\ngo 1.26.11\nrequire (\n github.com/airencracken/comfylib v0.3.0\n github.com/airencracken/comfylib/oidc v0.1.0\n)\n')
    (root / 'scripts/comfy.py').write_bytes((HERE / 'app_bootstrap.py').read_bytes())
    (root / 'internal/web/layout.html').write_text('<link href="/static/comfyware/components.css"><script src="/static/comfyware/suite.js"></script>')
    cfg = dict(app=name, previous='1.2.3', port=8080, tmp_mb=128, owner_command='create-owner', lifecycle='commands' if name in ('folkweave', 'tidings') else 'snapshot')
    (root / 'scripts/containers/config.json').write_text(json.dumps(cfg))
    return root


class ConsumerTests(unittest.TestCase):
    def test_all_consumers_and_removed_copy_mutations(self):
        for name in app.APPS:
            with self.subTest(app=name), tempfile.TemporaryDirectory() as directory:
                root = fixture(Path(directory), name)
                check_consumer.verify(root)
                for relative in ('internal/suite/auth.go', 'internal/sharedoidc/provider.go', 'scripts/containers/release.py',
                                 'internal/web/comfy-suite.js', 'scripts/mutate.py'):
                    path = root / relative
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text('copied')
                    with self.assertRaises(ValueError):
                        check_consumer.verify(root)
                    path.unlink()
                    if relative.startswith(('internal/suite/', 'internal/sharedoidc/')):
                        path.parent.rmdir()

    def test_bad_module_pins_bootstrap_and_missing_asset_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = fixture(Path(directory))
            manifest = root / 'go.mod'
            original = manifest.read_text()
            for mutation in (original.replace(' v0.3.0', ' latest'), original.replace('/oidc v0.1.0', '/oidc latest'),
                             original + '\nreplace example.org/a => ../a\n', original.replace('folkweave', '../../escape')):
                manifest.write_text(mutation)
                with self.assertRaises(ValueError):
                    check_consumer.verify(root)
            manifest.write_text(original)
            (root / 'scripts/comfy.py').write_text('print("different")')
            with self.assertRaises(ValueError):
                check_consumer.verify(root)
            (root / 'scripts/comfy.py').write_bytes((HERE / 'app_bootstrap.py').read_bytes())
            (root / 'internal/web/layout.html').write_text('missing assets')
            with self.assertRaises(ValueError):
                check_consumer.verify(root)

    def test_shared_workflow_pins_inputs_and_copied_implementations(self):
        with tempfile.TemporaryDirectory() as directory:
            root=fixture(Path(directory));folder=root/'.github/workflows';folder.mkdir(parents=True)
            for name in ('containers','browser'):
                text='jobs:\n  shared:\n    uses: airencracken/comfylib/.github/workflows/'+name+'.yml@'+'a'*40+'\n'
                if name=='containers':text+='    with:\n'+''.join('      '+arg+': ${{ inputs.'+arg+' }}\n' for arg in ('source_ref','release_tag','publish'))
                (folder/(name+'.yml')).write_text(text)
            check_consumer.verify_workflows(root,'a'*40)
            path=folder/'containers.yml';original=path.read_text()
            for broken in (original.replace('a'*40,'master'),original.replace('inputs.source_ref','inputs.other'),original+'    steps:\n      - run: copied\n'):
                path.write_text(broken)
                with self.assertRaises(ValueError):check_consumer.verify_workflows(root)
            path.write_text(original)
            with self.assertRaises(ValueError):check_consumer.verify_workflows(root,'b'*40)

    def test_runner_rejects_unknown_command_before_invoking_any_process(self):
        with patch.object(sys, 'argv', ['app.py', '../../escape']), patch.object(subprocess, 'call') as called, contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                app.main()
            called.assert_not_called()

    def test_dispatch_keeps_arguments_literal_and_forces_the_application_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = fixture(Path(directory) / 'app with spaces')
            for profile, filename in (('commands', 'command_lifecycle.py'), ('snapshot', 'lifecycle.py')):
                cfg_path = root / 'scripts/containers/config.json'
                cfg = json.loads(cfg_path.read_text())
                cfg['lifecycle'] = profile
                cfg_path.write_text(json.dumps(cfg))
                self.assertEqual(Path(app.command('container-lifecycle', root)[1]).name, filename)
            args = ['app.py', 'scan-artifacts', 'archive with spaces.tar.gz', '$(false)']
            with patch.object(sys, 'argv', args), patch.dict(os.environ, {'COMFYWARE_APP_ROOT': str(root), 'GOWORK': 'auto'}), patch.object(subprocess, 'call', return_value=9) as call:
                self.assertEqual(app.main(), 9)
            self.assertEqual(call.call_args.args[0][-2:], args[-2:])
            self.assertEqual(call.call_args.kwargs['cwd'], root)
            self.assertEqual(call.call_args.kwargs['env']['GOWORK'], 'off')

    def test_rollout_plans_explicit_core_and_optional_module_pins_without_mutation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = fixture(Path(directory))
            original = (root / 'go.mod').read_bytes()
            output = io.StringIO()
            argv = ['rollout_comfylib.py', 'v0.3.0', str(root), '--oidc-version', 'v0.1.0']
            with patch.object(sys, 'argv', argv), patch.object(subprocess, 'run') as call, contextlib.redirect_stdout(output):
                self.assertEqual(rollout_comfylib.main(), 0)
            call.assert_not_called()
            command = json.loads(output.getvalue())[0]['commands'][0]
            self.assertEqual(command, ['go', 'get', 'github.com/airencracken/comfylib@v0.3.0', 'github.com/airencracken/comfylib/oidc@v0.1.0'])
            self.assertEqual((root / 'go.mod').read_bytes(), original)

    def test_launcher_resolves_cached_module_with_proxy_disabled_and_propagates_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = fixture(Path(directory) / 'app with spaces')
            binary = Path(directory) / 'bin'
            binary.mkdir()
            shared = Path(directory) / 'cached module'
            (shared / 'tools').mkdir(parents=True)
            (shared / 'tools/app.py').write_text('import os,sys\nassert os.environ["GOWORK"] == "off"\nassert os.environ["COMFYWARE_APP_ROOT"] == os.getcwd()\nassert sys.argv[1:] == ["mutate", "$(false)"]\nraise SystemExit(7)\n')
            (binary / 'go').write_text('#!/bin/sh\n[ "$GOWORK" = off ] || exit 11\n[ "$GOPROXY" = off ] || exit 12\n[ "$*" = "list -m -f {{.Dir}} github.com/airencracken/comfylib" ] || exit 13\nprintf "%s\\n" "$FIXTURE_MODULE"\n')
            (binary / 'go').chmod(0o755)
            env = os.environ | {'PATH': str(binary), 'GOWORK': 'auto', 'GOPROXY': 'off', 'FIXTURE_MODULE': str(shared)}
            command = [sys.executable, str(root / 'scripts/comfy.py'), 'mutate', '$(false)']
            self.assertEqual(subprocess.run(command, cwd=directory, env=env, capture_output=True).returncode, 7)
            (shared / 'tools/app.py').unlink()
            failed = subprocess.run(command, cwd=directory, env=env, capture_output=True, text=True)
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn('Pinned Comfylib has no app tooling', failed.stderr)


class SharedContainerTests(unittest.TestCase):
    def test_archive_schema_publication_atomicity_properties_and_mutations(self):
        names = ['test_release.' + name for name in ('ArchiveTests', 'SavedImageTests', 'PublishTests', 'MutationTests', 'BackupTests')]
        names += ['test_release.ConfigurationTests.test_schema_rejects_unknown_fields_types_and_adversarial_values',
                  'test_release.ConfigurationTests.test_version_properties_and_adversarial_inputs', 'test_command_lifecycle.UpgradeTests']
        for name in app.APPS:
            with self.subTest(app=name), tempfile.TemporaryDirectory() as directory:
                root = fixture(Path(directory), name)
                result = subprocess.run([sys.executable, '-B', '-m', 'unittest', *names], cwd=HERE / 'containers',
                                        env=os.environ | {'COMFYWARE_APP_ROOT': str(root)}, capture_output=True, text=True, timeout=120)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == '__main__':
    unittest.main(verbosity=2)
