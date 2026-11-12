// SPDX-License-Identifier: AGPL-3.0-or-later
// Native metrics contract against real application binaries and isolated roles.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdir, readdir, rm } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { findBrowser, temporaryDirectory, reservePort, spawnServer, stopProcess, checkOwnerMetrics } from '../ui/uitest/runtime.mjs';

const { chromium } = await import(pathToFileURL(process.env.COMFYWARE_PLAYWRIGHT_PATH).href);
const [directory, app] = process.argv.slice(2);
assert.ok(['folkweave','imvault','songstead','tidings','witmoot'].includes(app));
const temporary = await temporaryDirectory(app + '-metrics');
const data = path.join(temporary, 'data'); await mkdir(data, { mode: 0o700 });
const port = await reservePort(), origin = `http://127.0.0.1:${port}`, prefix = app.toUpperCase();
const env = Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.startsWith(prefix + '_')));
Object.assign(env, { [prefix + '_DATA_DIR']: data, [prefix + '_ADDR']: `127.0.0.1:${port}`,
  [prefix + '_LISTEN']: `127.0.0.1:${port}`, [prefix + '_BASE_URL']: origin,
  [prefix + '_SECURE_COOKIES']: 'false', [prefix + '_BACKUP_INTERVAL']: '24h' });
const binary = path.resolve(directory, app), password = 'a-private-metrics-test-password';
let server, browser;
try {
  const command = app === 'imvault' ? 'create-admin' : 'create-owner';
  for (const username of ['metricsowner', 'metricsmember']) {
    const result = spawnSync(binary, [command, '--username', username, '--password-stdin'], { env, input: password + '\n', encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
  }
  // Test fixture only: keep the original owner and downgrade the second local
  // account before starting the server. Hashes come from the actual account CLI.
  const databases = (await readdir(data)).filter(name => name.endsWith('.db'));
  assert.equal(databases.length, 1, 'one isolated application database');
  const roleSQL = app === 'tidings' ? "UPDATE users SET owner=0 WHERE username='metricsmember'"
    : app === 'imvault' ? "UPDATE users SET role='member' WHERE username='metricsmember'"
    : "UPDATE users SET role='member' WHERE username='metricsmember'";
  const updated = spawnSync('python3', ['-c', 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute(sys.argv[2]); c.commit()', path.join(data, databases[0]), roleSQL], { encoding: 'utf8' });
  assert.equal(updated.status, 0, updated.stderr);
  server = await spawnServer(binary, ['serve'], { env, health: origin + '/healthz' });
  browser = await chromium.launch({ executablePath: await findBrowser({ required: true }), headless: true });
  async function session(username, javaScriptEnabled = false) {
    const context = await browser.newContext({ javaScriptEnabled });
    if (username) {
      const page = await context.newPage(); await page.goto(origin + '/login');
      await page.getByLabel('Username', { exact: true }).fill(username);
      await page.getByLabel('Password', { exact: true }).fill(password);
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.waitForURL(url => url.pathname !== '/login');
      await page.close();
    }
    return context;
  }
  const owner = await session('metricsowner'), member = await session('metricsmember'), guest = await session();
  await checkOwnerMetrics({ owner, member, guest, origin });
  await member.close(); await guest.close(); await owner.close();
  for (const javascript of [false, true]) {
    const context = await session('metricsowner', javascript), page = await context.newPage();
    for (const theme of ['light', 'dark']) for (const width of [320,390,768,1280]) {
      await page.setViewportSize({ width, height: 1000 }); await page.emulateMedia({ colorScheme: theme });
      await page.goto(origin + '/admin/metrics');
      assert.equal(await page.getByRole('heading', { name: 'Instance metrics', exact: true }).count(), 1);
      assert.ok(await page.getByRole('heading', { name: 'Instance overview', exact: true }).isVisible());
      assert.ok(await page.locator('.comfy-metrics dd').count() >= 8);
      assert.ok(await page.getByRole('heading', { name: 'Activity', exact: true }).isVisible());
      assert.ok(await page.getByRole('table', { name: 'Recent sign-ins by account', exact: true }).isVisible());
      const styles = await page.evaluate(() => {
        const css = selector => getComputedStyle(document.querySelector(selector));
        return { root: css('.comfy-admin-dashboard').fontSize, title: css('.comfy-admin-heading h1').fontSize,
          label: css('.comfy-metrics dt').fontSize, value: css('.comfy-metrics dd').fontSize,
          gap: css('.comfy-metrics').gap, radius: css('.comfy-metrics > div').borderRadius,
          panel: css('.comfy-admin-section').backgroundColor, padding: css('.comfy-admin-section').padding };
      });
      const dashboardWidth = await page.locator('.comfy-admin-dashboard').evaluate(element => ({ actual: element.getBoundingClientRect().width, expected: Math.min(document.documentElement.clientWidth - 32, 1120) }));
      assert.ok(Math.abs(dashboardWidth.actual - dashboardWidth.expected) < 1, `${app}: shared dashboard width ${width}`);
      assert.equal(await page.locator('main .fetch-status').count(), 0, 'application-specific status panels do not alter the shared dashboard');
      assert.equal(await page.locator('main').evaluate(element => getComputedStyle(element).backgroundColor), theme === 'dark' ? 'rgb(21, 28, 39)' : 'rgb(243, 245, 248)', 'shared dashboard canvas');
      assert.deepEqual(styles, { root: '16px', title: '30px', label: '14px', value: '28px', gap: '16px', radius: '8px',
        panel: theme === 'dark' ? 'rgb(32, 41, 55)' : 'rgb(255, 255, 255)', padding: width <= 480 ? '16px' : '24px' },
        `${app}: common dashboard appearance ${theme}/${width}/${javascript}`);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${app}: metrics overflow ${theme}/${width}/${javascript}`);
      assert.equal((await context.request.get(origin + '/admin/metrics')).headers()['cache-control'], 'no-store');
      if (process.env.COMFYWARE_METRICS_SCREENSHOTS && javascript && [390,1280].includes(width)) {
        await mkdir(process.env.COMFYWARE_METRICS_SCREENSHOTS, { recursive: true });
        await page.screenshot({ path: path.join(process.env.COMFYWARE_METRICS_SCREENSHOTS, `${app}-${theme}-${width}.png`), fullPage: true });
      }
    }
    await context.close();
  }
  console.log(app + ': native metrics, isolated owner/member/guest roles, forged inputs, no-store, both themes, four widths, and JavaScript-free pages passed');
} finally {
  if (browser) await browser.close();
  await stopProcess(server);
  await rm(temporary, { recursive: true, force: true });
}
