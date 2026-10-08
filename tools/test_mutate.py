#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Self-test for tools/mutate.py against the fixture module in testdata.

Run with: python3 tools/test_mutate.py
"""
import contextlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

TOOLS = Path(__file__).resolve().parent
FIXTURE = TOOLS / "testdata" / "fixture"
sys.path.insert(0, str(TOOLS))

import mutate  # noqa: E402  (imported after the path is set)


def run(*args):
    """Run the engine in-process and return (status, stdout, stderr)."""
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        status = mutate.main(["--root", str(FIXTURE), *map(str, args)])
    return status, out.getvalue(), err.getvalue()


def entry(**changes):
    base = {
        "name": "addition",
        "file": "fixture.go",
        "before": "return a + b",
        "after": "return a - b",
        "package": "./",
        "run": "^TestAdd$",
    }
    base.update(changes)
    return {key: value for key, value in base.items() if value is not None}


class MutateTest(unittest.TestCase):
    def test_adversarial_non_utf8_test_output_is_a_failure(self):
        original = mutate.subprocess.run
        def noisy_test(command, **kwargs):
            return original([sys.executable, "-c",
                             "import sys; sys.stdout.buffer.write(b'--- FAIL: bad input \\xff\\n'); sys.exit(1)"],
                            **kwargs)
        with patch.object(mutate.subprocess, "run", side_effect=noisy_test):
            result = mutate.go_test(FIXTURE, entry())
        self.assertIn("\ufffd", result.stdout)
        self.assertEqual(mutate.verdict(result), "failed")

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="mutate-test-")
        self.addCleanup(self.temporary.cleanup)
        self.source = (FIXTURE / "fixture.go").read_text(encoding="utf-8")
        self.addCleanup(self.assert_fixture_untouched)

    def assert_fixture_untouched(self):
        self.assertEqual((FIXTURE / "fixture.go").read_text(encoding="utf-8"), self.source,
                         "the engine modified the real tree")

    def table(self, *entries, raw=None):
        path = Path(self.temporary.name) / "table{}.json".format(len(list(Path(self.temporary.name).iterdir())))
        path.write_text(raw if raw is not None else json.dumps(list(entries)), encoding="utf-8")
        return path

    def test_a_caught_mutation_passes(self):
        status, out, _ = run(FIXTURE / "caught.json")
        self.assertEqual(status, 0)
        self.assertIn("PASS: regression rejects broken addition", out)

    def test_a_surviving_mutation_fails_the_run(self):
        status, out, err = run(FIXTURE / "survivor.json")
        self.assertEqual(status, 1)
        self.assertIn("SURVIVED: greeting", out)
        self.assertIn("1 of 1 mutations were not caught", err)

    def test_one_survivor_among_caught_mutations_still_fails(self):
        status, out, _ = run(FIXTURE / "caught.json", FIXTURE / "survivor.json")
        self.assertEqual(status, 1)
        self.assertIn("PASS: regression rejects broken addition", out)
        self.assertIn("SURVIVED: greeting", out)

    def test_a_mutation_that_breaks_the_build_is_not_counted_as_caught(self):
        status, out, _ = run(self.table(entry(name="syntax", after="return a +")))
        self.assertEqual(status, 1)
        self.assertIn("BROKEN: syntax", out)

    def test_a_run_pattern_that_selects_nothing_is_reported(self):
        status, out, _ = run(self.table(entry(name="nothing", run="^TestMissing$")))
        self.assertEqual(status, 1)
        self.assertIn("NO TESTS: nothing", out)

    def test_table_env_reaches_the_test(self):
        mode = entry(name="mode", before='return "on"', after='return "off"', run="^TestMode$")
        status, out, _ = run(self.table(dict(mode, env={"FIXTURE_MODE": "on"})))
        self.assertEqual(status, 0, out)
        # Without the variable the test skips, so the mutation survives.
        status, out, _ = run(self.table(mode))
        self.assertEqual(status, 1)
        self.assertIn("SURVIVED: mode", out)

    def test_missing_and_repeated_anchors_stop_before_running(self):
        for before, count in (("return a * b", "0 times"), ("return", "3 times")):
            status, out, err = run(self.table(entry(before=before)))
            self.assertEqual(status, 2)
            self.assertIn(count, err)
            self.assertEqual(out, "")

    def test_malformed_tables_are_rejected(self):
        cases = {
            "not json": "[",
            "not a list": json.dumps({"name": "x"}),
            "empty list": "[]",
            "entry not an object": json.dumps(["x"]),
            "missing field": json.dumps([entry(run=None)]),
            "unknown field": json.dumps([dict(entry(), extra="x")]),
            "empty name": json.dumps([entry(name="")]),
            "empty before": json.dumps([entry(before="")]),
            "number field": json.dumps([entry(run=1)]),
            "no change": json.dumps([entry(after="return a + b")]),
            "absolute file": json.dumps([entry(file=str(FIXTURE / "fixture.go"))]),
            "escaping file": json.dumps([entry(file="../fixture/fixture.go")]),
            "bad package": json.dumps([entry(package="example.com/fixture")]),
            "package with space": json.dumps([entry(package="./ x")]),
            "env not an object": json.dumps([dict(entry(), env=["A=1"])]),
            "env value not a string": json.dumps([dict(entry(), env={"A": 1})]),
            "missing file": json.dumps([entry(file="missing.go")]),
            "duplicate names": json.dumps([entry(), entry(before='return "hello"', after='return ""')]),
        }
        for name, raw in cases.items():
            with self.subTest(name):
                status, out, err = run(self.table(raw=raw))
                self.assertEqual(status, 2, err)
                self.assertEqual(out, "")
                self.assertTrue(err)

    def test_list_prints_without_running(self):
        status, out, _ = run("--list", FIXTURE / "caught.json", FIXTURE / "survivor.json")
        self.assertEqual(status, 0)
        self.assertEqual(out.splitlines(), [
            "addition: ./ -run ^TestAdd$ (fixture.go)",
            "greeting: ./ -run ^TestAdd$ (fixture.go)",
        ])

    def test_the_copy_leaves_out_workspace_vcs_and_data(self):
        root = Path(self.temporary.name) / "repo"
        for path in ("go.mod", "go.work", "go.work.sum", ".git/HEAD", "data/app.db", "internal/data/keep.go",
                     "cmd/app/main.go", "web/node_modules/x.js", "state.db", "bin/app"):
            (root / path).parent.mkdir(parents=True, exist_ok=True)
            (root / path).write_text("x", encoding="utf-8")
        destination = Path(self.temporary.name) / "copy"
        mutate.copy_tree(root, destination)
        copied = sorted(str(p.relative_to(destination)) for p in destination.rglob("*") if p.is_file())
        self.assertEqual(copied, ["cmd/app/main.go", "go.mod", "internal/data/keep.go"])

    def test_go_environment_isolates_the_copy(self):
        env = mutate.go_env(entry())
        self.assertEqual(env["GOWORK"], "off")
        self.assertIn("-mod=mod", env["GOFLAGS"].split())
        self.assertEqual(env["GOPROXY"], "off")


if __name__ == "__main__":
    unittest.main()
