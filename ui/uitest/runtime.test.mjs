import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { rm } from 'node:fs/promises';
import { findBrowser, temporaryDirectory, reservePort, waitForHTTP, stopProcess, spawnServer, checkOwnerMetrics } from './runtime.mjs';

test('required browser discovery fails, optional discovery is explicit', async () => {
  await assert.rejects(findBrowser({ required: true, candidates: ['/nonexistent-browser'] }), /required/);
  assert.equal(await findBrowser({ required: false, candidates: ['/nonexistent-browser'] }), null);
  assert.equal(await findBrowser({ required: true, candidates: [process.execPath] }), process.execPath);
});

test('ports and private temporary instances are isolated and names bounded', async () => {
  const a = await temporaryDirectory('test-app'), b = await temporaryDirectory('test-app');
  try { assert.notEqual(a, b); assert.ok(await reservePort()); await assert.rejects(temporaryDirectory('../unsafe'), /Invalid/); }
  finally { await rm(a, { recursive: true }); await rm(b, { recursive: true }); }
});

test('real readiness, timeout and child cleanup', async () => {
  const server = createServer((req, res) => { res.end('ok'); });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try { assert.equal(await waitForHTTP(`http://127.0.0.1:${server.address().port}`), true); }
  finally { await new Promise(resolve => server.close(resolve)); }
  await assert.rejects(waitForHTTP('http://127.0.0.1:1', { timeout: 10, interval: 1 }), /ready/);
  const child = spawn(process.execPath, ['-e', 'setInterval(()=>{},1000)']);
  await stopProcess(child); assert.ok(child.exitCode !== null || child.signalCode !== null);
  await assert.rejects(spawnServer('/nonexistent-comfy-app', [], { health: 'http://127.0.0.1:1', timeout: 100 }));
  await assert.rejects(spawnServer(process.execPath, ['-e', 'process.exit(4)'], { health: 'http://127.0.0.1:1', timeout: 1000 }), /exited/);
});

test('role matrix rejects forged role access and owner routing regressions', async () => {
  const context = (status, text = '') => ({ request: { get: async () => ({ status: () => status, text: async () => text }) } });
  const args = { owner: context(200, 'Instance overview'), member: context(403), guest: context(303), origin: 'https://test.invalid' };
  await checkOwnerMetrics(args);
  await assert.rejects(checkOwnerMetrics({ ...args, member: context(200, 'Instance overview') }), /leaked/);
  await assert.rejects(checkOwnerMetrics({ ...args, owner: context(404) }), /unavailable/);
});
