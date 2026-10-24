// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { accountLayout, invitationLayout, assertInvitationLayout, accountWidths, invitationWidths } from './layout.mjs';

export async function checkInvitations(browser, base, storageState, { path = '/invites', revokePath = path, screenshots, app }) {
  // Copy only the authenticated cookies: each context starts with system colors.
  for (const javaScriptEnabled of [true, false]) {
    const context = await browser.newContext({ javaScriptEnabled, storageState: { cookies: storageState.cookies, origins: [] } });
    const page = await context.newPage();
    try {
      await page.goto(base + path);
      assert.equal(await page.locator('h1').innerText(), 'Invitations');
      assert.equal(await page.locator('.eyebrow, .invitation-page table').count(), 0);
      const label = `Invitation for ${javaScriptEnabled ? 'script' : 'plain'} `.padEnd(64, 'w');
      await page.getByLabel('Label (optional)', { exact: true }).fill(label);
      await page.getByLabel('Uses', { exact: true }).fill('2');
      await page.getByLabel('Expires after (days)', { exact: true }).fill('7');
      await page.getByRole('button', { name: 'Create invitation', exact: true }).click();
      const link = await page.getByLabel('Invitation link', { exact: true }).inputValue();
      assert.equal(new URL(link).origin, base);
      assert.equal(await page.locator('.invitation-result').count(), 1);
      const guestContext = await browser.newContext({ javaScriptEnabled });
      try {
        const guest = await guestContext.newPage();
        await guest.goto(link);
        assert.equal(await guest.locator('h1').innerText(), 'Create an account');
        for (const width of accountWidths) {
          await guest.setViewportSize({ width, height: 960 });
          assert.ok(await guest.evaluate(accountLayout), `${app}: invitation acceptance is compact at ${width}px`);
          if (screenshots && javaScriptEnabled) {
            await mkdir(screenshots, { recursive: true });
            await guest.screenshot({ path: join(screenshots, `join-${width}.png`), fullPage: true });
          }
        }
      } finally { await guestContext.close(); }

      await page.goto(base + path);
      assert.equal(await page.locator('.invitation-result').count(), 0, 'Invitation secret disappears on a fresh visit');
      for (const theme of ['light', 'dark']) {
        await page.emulateMedia({ colorScheme: theme });
        for (const width of invitationWidths) {
          await page.setViewportSize({ width, height: 960 });
          const layout = await page.evaluate(invitationLayout);
          assertInvitationLayout(layout, width, `${app} (${theme}, JavaScript ${javaScriptEnabled})`);
          if (screenshots && javaScriptEnabled && [390, 1280].includes(width)) {
            await mkdir(screenshots, { recursive: true });
            await page.screenshot({ path: join(screenshots, `invitations-${theme}-${width}.png`), fullPage: true });
          }
        }
      }
      await page.getByLabel('Label (optional)', { exact: true }).focus();
      await page.keyboard.press('Tab');
      assert.equal(await page.locator('#invite-uses').evaluate(element => element === document.activeElement), true, 'Uses follows Label in keyboard order');
      const record = page.locator('.invitation-record').filter({ has: page.getByRole('heading', { name: label, exact: true }) });
      await record.getByRole('button', { name: `Revoke invitation ${label}`, exact: true }).click();
      await page.waitForURL(base + revokePath);
      await record.locator('.invitation-status').filter({ hasText: /revoked/i }).waitFor();
      assert.match(await record.innerText(), /revoked/i);
    } finally { await context.close(); }
  }
  console.log(`PASS: ${app} invitation layouts at four widths in both themes, with and without JavaScript; create, one-time link, keyboard order, and revoke`);
}
