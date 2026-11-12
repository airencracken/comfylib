/* SPDX-License-Identifier: AGPL-3.0-or-later */
import { access, mkdtemp } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export async function findBrowser({ required = Boolean(process.env.CI), candidates } = {}) {
  for (const candidate of candidates || [process.env.CHROME, process.env.CHROMIUM,
    '/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable']) {
    if (!candidate) continue;
    try { await access(candidate); return candidate; } catch { /* Try the next installed browser. */ }
  }
  if (required) throw new Error('A Chromium browser is required for these checks; install it or set CHROMIUM.');
  return null;
}

export async function temporaryDirectory(app) {
  if (!/^[a-z][a-z0-9-]*$/.test(app)) throw new Error('Invalid application name');
  return mkdtemp(join(tmpdir(), app + '-browser-'));
}

export async function reservePort() {
  const server = createServer();
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const port = server.address().port;
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  return port;
}

export async function waitForHTTP(url, { timeout = 15000, process: child, output = () => '', interval = 100 } = {}) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (child && (!child.pid || child.exitCode !== null || child.signalCode !== null)) throw new Error('Server exited before readiness: ' + output());
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(Math.max(1, Math.min(1000, deadline - Date.now()))) });
      await response.body?.cancel();
      if (response.ok) return true;
    } catch { /* Poll until the deadline, including connection refusal. */ }
    await new Promise(resolve => setTimeout(resolve, interval));
  }
  throw new Error('Server did not become ready: ' + url + '\n' + output());
}

export async function stopProcess(child, { timeout = 3000 } = {}) {
  if (!child || !child.pid || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise(resolve => {
    const timer = setTimeout(() => { child.kill('SIGKILL'); }, timeout);
    child.once('exit', () => { clearTimeout(timer); resolve(); });
    child.kill('SIGTERM');
  });
}

export async function spawnServer(binary, args, { env = process.env, cwd, health, timeout = 15000 } = {}) {
  const child = spawn(binary, args, { env, cwd, stdio: ['ignore', 'pipe', 'pipe'] });
  let output = '';
  const capture = chunk => { output = (output + chunk).slice(-32768); };
  child.stdout.on('data', capture); child.stderr.on('data', capture);
  let failure;
  child.on('error', error => { failure = error; });
  try {
    await waitForHTTP(health, { process: child, timeout, output: () => failure?.message || output });
    return child;
  } catch (error) {
    await stopProcess(child);
    throw failure || error;
  }
}

// Sessions are isolated browser contexts supplied by the application fixture.
// The owner must see metrics and operations; members and guests must not see
// their contents even when requesting the route directly or forging a role.
export async function checkOwnerMetrics({ owner, member, guest, origin, path = '/admin/metrics' }) {
  const allowed = await owner.request.get(origin + path);
  if (allowed.status() !== 200 || !(await allowed.text()).includes('Instance overview')) throw new Error('Owner metrics unavailable');
  for (const session of [member, guest]) {
    for (const suffix of ['', '?owner=true&role=admin']) {
      const denied = await session.request.get(origin + path + suffix, { maxRedirects: 0 });
      if (![302, 303, 401, 403].includes(denied.status()) || (await denied.text()).includes('Instance overview')) throw new Error('Metrics leaked to an unauthorized browser');
    }
  }
}
