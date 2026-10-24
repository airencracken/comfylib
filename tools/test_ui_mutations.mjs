// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const source = await readFile(new URL('../ui/uitest/layout.mjs', import.meta.url), 'utf8');
const tests = fileURLToPath(new URL('../ui/uitest/layout.test.mjs', import.meta.url));
const directory = await mkdtemp(join(tmpdir(), 'comfylib-ui-mutations-'));
try {
  const baseline = spawnSync(process.execPath, ['--test', '--test-reporter=tap', tests], { encoding: 'utf8', timeout: 30000 });
  if (baseline.error || baseline.signal) throw baseline.error || new Error(`baseline: ${baseline.signal}`);
  assert.equal(baseline.status, 0, `Browser helper baseline failed:\n${baseline.stdout}\n${baseline.stderr}`);
  console.log('Browser helper baseline passed.');
  for (const [name, before, after] of [
    ['account width', 'form.width <= 480', 'form.width <= 600'],
    ['viewport overflow', 'document.documentElement.scrollWidth <= innerWidth && form.width', 'true && form.width'],
    ['control height', 'bounds.height >= 44', 'bounds.height >= 40'],
    ['status contrast', ') >= 4.5;', ') >= 1;'],
    ['responsive boundary', 'width > 900 ?', 'width > 901 ?'],
    ['structure', "!document.querySelector('.eyebrow, .invitation-page table')", 'true'],
  ]) {
    assert.equal(source.split(before).length, 2, `${name}: unique mutation anchor`);
    const path = join(directory, 'layout.mjs');
    await writeFile(path, source.replace(before, after));
    const result = spawnSync(process.execPath, ['--test', '--test-reporter=tap', tests], { env: { ...process.env, COMFYWARE_LAYOUT_MODULE: path }, encoding: 'utf8', timeout: 30000 });
    if (result.error || result.signal) throw result.error || new Error(`${name}: ${result.signal}`);
    assert.notEqual(result.status, 0, `${name}: mutation survived`);
    console.log(`Rejected UI mutation: ${name}`);
  }
} finally { await rm(directory, { recursive: true, force: true }); }
