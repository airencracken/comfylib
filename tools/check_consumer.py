#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Compatibility entry point for the Go consumer validator."""
import argparse
import os
from pathlib import Path
import subprocess

LIBRARY = Path(__file__).resolve().parents[1]


def run(*arguments):
    env = os.environ | {"GOWORK": "off"}
    result = subprocess.run(
        ["go", "run", "./tools/cmd/check-consumer", *arguments],
        cwd=LIBRARY, env=env, capture_output=True, text=True,
    )
    if result.returncode:
        raise ValueError(result.stderr.strip())
    return result.stdout


def verify(root):
    run(str(root.resolve()))


def verify_workflows(root, revision=None):
    run("-workflows", "-revision", revision or "", str(root.resolve()))


def workflow_source(root, name):
    path = root / ".github/workflows" / (name + ".yml")
    text = path.read_text()
    if "uses: airencracken/comfylib/.github/workflows/" + name + ".yml@" in text:
        verify_workflows(root)
        return (LIBRARY / ".github/workflows" / (name + ".yml")).read_text()
    return text


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("apps", nargs="*", type=Path)
    args = parser.parse_args()
    roots = args.apps or [Path(os.environ.get("COMFYWARE_APP_ROOT", Path.cwd()))]
    try:
        print(run(*(str(root.resolve()) for root in roots)), end="")
    except ValueError as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()
