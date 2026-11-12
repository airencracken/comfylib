#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Test shared identity mutations across the optional module and the core."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import mutate

ROOT = Path(__file__).resolve().parents[1]
CASES = (
    ('nonce', 'oidc/provider.go', 'identity.Nonce != nonce', 'false', '^TestOIDCSignatureAudienceNonceAndPKCE$'),
    ('session binding', 'suite/auth.go', 'binding == "" || subtle.ConstantTimeCompare(digest[:], flow.Binding[:]) != 1',
     'false && (binding == "" || subtle.ConstantTimeCompare(digest[:], flow.Binding[:]) != 1)', '^TestSharedSignInExplicitMembershipSessionBindingAndAtomicity$'),
    ('identity ownership', 'suite/auth.go', 'INSERT INTO comfy_identities(issuer,subject,user_id) VALUES(?,?,?)',
     'INSERT OR REPLACE INTO comfy_identities(issuer,subject,user_id) VALUES(?,?,?)', '^TestSharedSignInExplicitMembershipSessionBindingAndAtomicity$'),
    ('discovery cache', 'oidc/provider.go', 'p.issuer != nil', 'false', '^TestDiscoveryIsCachedAndFailedDiscoveryCanRetry$'),
)


class IdentityMutationTests(unittest.TestCase):
    def test_regressions_reject_mutations_without_changing_the_source_tree(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'library'
            mutate.copy_tree(ROOT, root)
            env = os.environ | {'GOWORK': 'off', 'GOPROXY': 'off', 'GOSUMDB': 'off'}
            env['GOFLAGS'] = '-p=4 -buildvcs=false'
            changed = subprocess.run(['go', 'mod', 'edit', '-replace', 'github.com/airencracken/comfylib=' + str(root)],
                                     cwd=root / 'oidc', env=env, capture_output=True, text=True)
            self.assertEqual(changed.returncode, 0, changed.stderr)
            for name, relative, before, after, run in CASES:
                with self.subTest(mutation=name):
                    source = root / relative
                    original = source.read_text()
                    self.assertEqual(original.count(before), 1)
                    command = ['go', 'test', '-count=1', '-run', run, '.']
                    baseline = subprocess.run(command, cwd=root / 'oidc', env=env, capture_output=True, text=True, timeout=120)
                    self.assertEqual(baseline.returncode, 0, baseline.stdout + baseline.stderr)
                    try:
                        source.write_text(original.replace(before, after, 1))
                        mutant = subprocess.run(command, cwd=root / 'oidc', env=env, capture_output=True, text=True, timeout=120)
                        self.assertNotEqual(mutant.returncode, 0, 'mutation survived: ' + name)
                        self.assertIn('--- FAIL:', mutant.stdout, mutant.stdout + mutant.stderr)
                    finally:
                        source.write_text(original)


if __name__ == '__main__':
    unittest.main(verbosity=2)
