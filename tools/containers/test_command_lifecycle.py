from pathlib import Path
import json,os,shutil,subprocess,sys,tempfile,unittest
from unittest.mock import patch
import release
import command_lifecycle as lifecycle

class UpgradeTests(unittest.TestCase):
    def test_previous_account_is_created_and_started_before_current_binary(self):
        test = lifecycle.Lifecycle(release.config(), 'example:current')
        previous = Path('/tmp/verified-previous-binary')
        events = []
        test.once = lambda *args, **kw: events.append(('once', args, kw))
        test.start = lambda **kw: events.append(('start', kw)) or 'old-server'
        test.stop = lambda server: events.append(('stop', server))
        test.prepare_owner(previous)
        self.assertEqual(events[0][1][0], 'create-owner')
        self.assertEqual(events[0][2]['binary'], previous)
        self.assertEqual(events[1], ('start', {'binary': previous}))
        self.assertEqual(events[2], ('stop', 'old-server'))
        self.assertEqual(events[3][1][:3], ('set-password', '--username', 'containerowner'))
        self.assertNotIn('binary', events[3][2])

    def test_previous_server_uses_read_only_binary_and_explicit_serve_command(self):
        test = lifecycle.Lifecycle(release.config(), 'example:current')
        previous = Path('/tmp/verified-previous-binary')
        commands = []
        def run(*args):
            commands.append(args)
            return json.dumps([{'State': {'Running': True}}]) if args[1] == 'inspect' else ''
        with patch.object(lifecycle, 'run', side_effect=run), patch.object(lifecycle.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)):
            test.start(binary=previous)
        start = commands[0]
        self.assertEqual(start[-2:], ('example:current', 'serve'))
        self.assertIn(str(previous) + ':/previous/app:ro', start)
        self.assertEqual(start[start.index('--entrypoint') + 1], '/previous/app')

    def test_upgrade_order_regression_rejects_starting_current_image_as_previous(self):
        original = Path(lifecycle.__file__).read_text()
        before = 'previous_server = self.start(binary=previous)'
        self.assertEqual(original.count(before), 1)
        with tempfile.TemporaryDirectory() as temporary:
            work = Path(temporary) / 'scripts/containers'
            work.mkdir(parents=True)
            shutil.copyfile(Path(release.__file__), work / 'release.py')
            shutil.copyfile(release.ROOT / 'scripts/containers/config.json', work / 'config.json')
            shutil.copyfile(Path(__file__), work / 'test_command_lifecycle.py')
            target = work / 'command_lifecycle.py'
            command = [sys.executable, '-B', '-m', 'unittest',
                       'test_command_lifecycle.UpgradeTests.test_previous_account_is_created_and_started_before_current_binary']
            target.write_text(original)
            baseline = subprocess.run(command, cwd=work, capture_output=True)
            self.assertEqual(baseline.returncode, 0, baseline.stdout + baseline.stderr)
            target.write_text(original.replace(before, 'previous_server = self.start()', 1))
            result = subprocess.run(command, cwd=work, capture_output=True)
            self.assertNotEqual(result.returncode, 0, 'upgrade-order mutation survived')
            self.assertIn(b'FAIL:', result.stderr)
