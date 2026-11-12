"""Build and exercise the native metrics contract through the shared browser fixture."""
import os
from pathlib import Path
import subprocess
import tempfile

from app import application, application_name


def main():
    root = application(os.environ.get('COMFYWARE_APP_ROOT', Path.cwd()))
    app = application_name(root)
    playwright = Path(os.environ.get('COMFYWARE_PLAYWRIGHT_PATH', root / 'scripts/browser/node_modules/playwright/index.mjs'))
    if not playwright.is_file():
        raise ValueError('Install the browser fixtures with npm ci --prefix scripts/browser')
    with tempfile.TemporaryDirectory(prefix='comfy-metrics-binary-') as directory:
        env = os.environ | {'GOWORK': 'off', 'COMFYWARE_PLAYWRIGHT_PATH': str(playwright)}
        subprocess.run(['go','build','-buildvcs=false','-trimpath','-o',str(Path(directory)/app),'./cmd/'+app],cwd=root,env=env,check=True)
        subprocess.run(['node',str(Path(__file__).with_suffix('.mjs')),directory,app],cwd=root,env=env,check=True)


if __name__=='__main__':main()
