"""Strict project metadata shared by release preparation and validation."""
import json
import os
from pathlib import Path
import re
import sys

TOOLS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(TOOLS))
from app import application


def project(root=None):
    root = application(root or os.environ.get('COMFYWARE_APP_ROOT', Path.cwd()))
    name = re.search(r'^module\s+(\S+)', (root / 'go.mod').read_text(), re.M)[1].split('/')[-1]
    catalog = json.loads(Path(__file__).with_name('projects.json').read_text())
    if catalog['schema'] != 1 or name not in catalog['projects']:
        raise ValueError('unknown project schema or application')
    return root, name, catalog['projects'][name]


def private_file(root, relative):
    path = root / relative
    if not path.resolve().is_relative_to(root.resolve()) or not path.is_file():
        raise ValueError('missing or unsafe project file: ' + relative)
    return path
