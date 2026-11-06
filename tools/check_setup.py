#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Exercise first-owner setup against an isolated real application binary."""
import argparse
from contextlib import closing
from html.parser import HTMLParser
from http.cookiejar import CookieJar
import os
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import build_opener, HTTPCookieProcessor, Request


class Form(HTMLParser):
    def __init__(self):
        super().__init__()
        self.fields = {}
        self.active = False

    def handle_starttag(self, tag, attrs):
        attributes = dict(attrs)
        if tag == 'form':
            self.active = attributes.get('method', '').lower() == 'post'
        elif tag == 'input' and self.active and attributes.get('name'):
            self.fields[attributes['name']] = attributes.get('value', '')

    def handle_endtag(self, tag):
        if tag == 'form':
            self.active = False


def check(binary, app):
    with tempfile.TemporaryDirectory(prefix='comfyware-setup-') as temporary:
        directory = Path(temporary)
        data = directory / 'data'
        data.mkdir(mode=0o700)
        with closing(socket.socket()) as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        origin = f'http://127.0.0.1:{port}'
        prefix = app.upper()
        environment = dict(os.environ)
        environment.update({prefix + '_DATA_DIR': str(data), prefix + '_DB': str(data / (app + '.db')),
                            prefix + '_ADDR': f'127.0.0.1:{port}', prefix + '_LISTEN': f'127.0.0.1:{port}',
                            prefix + '_BASE_URL': '', prefix + '_BACKUP_INTERVAL': '0',
                            prefix + '_SECURE_COOKIES': 'false'})
        enabled = subprocess.run([str(binary), 'setup'], env=environment, capture_output=True, text=True, timeout=30)
        assert enabled.returncode == 0, enabled.stderr
        found = re.search(r'Setup key: ([a-f0-9]{64})', enabled.stdout)
        assert found, 'missing setup key'
        key = found.group(1)
        browser = build_opener(HTTPCookieProcessor(CookieJar()))
        with (directory / 'server.log').open('wb') as log:
            server = subprocess.Popen([str(binary), 'serve'], env=environment, stdout=log, stderr=log)
            try:
                ready = False
                for _ in range(150):
                    try:
                        with browser.open(origin + '/healthz', timeout=1) as response:
                            ready = response.status == 200
                        if ready:
                            break
                    except (URLError, HTTPError, TimeoutError):
                        pass
                    if server.poll() is not None:
                        break
                    time.sleep(0.1)
                assert ready, 'server did not start: ' + (directory / 'server.log').read_text()
                with browser.open(origin + '/') as response:
                    page = response.read().decode()
                    assert urlsplit(response.url).path == '/setup'
                assert key not in page, 'setup key leaked in browser page'
                assert 'Create owner account' in page
                form = Form()
                form.feed(page)
                form.fields.update(username='alex', password='a long first owner password', confirm='a long first owner password', key='wrong')
                try:
                    browser.open(Request(origin + '/setup', data=urlencode(form.fields).encode()))
                except HTTPError as failure:
                    assert failure.code == 403
                else:
                    raise AssertionError('incorrect setup key accepted')
                form.fields['key'] = key
                with browser.open(Request(origin + '/setup', data=urlencode(form.fields).encode())) as response:
                    assert urlsplit(response.url).path == '/login'
                    login = Form()
                    login.feed(response.read().decode())
                login.fields.update(username='alex', password='a long first owner password')
                with browser.open(Request(origin + '/login', data=urlencode(login.fields).encode())) as response:
                    assert urlsplit(response.url).path not in ('/login', '/setup'), 'created owner cannot sign in'
                replay = subprocess.run([str(binary), 'setup'], env=environment, capture_output=True, text=True, timeout=30)
                assert replay.returncode != 0 and key not in replay.stdout, 'setup reopened after owner creation'
                assert not (data / '.setup.key').exists(), 'completed setup retained its key'
            finally:
                server.terminate()
                try:
                    server.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=5)
        assert key not in (directory / 'server.log').read_text(), 'server logged setup key'
    print(app + ': first-owner setup, host authorization, sign-in, and replay refusal passed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--app', required=True)
    args = parser.parse_args()
    check(args.binary.resolve(), args.app)


if __name__ == '__main__':
    main()
