// SPDX-License-Identifier: AGPL-3.0-or-later
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Element, load } from './fakedom.mjs';

const pickers = body => {
  const theme = new Element('label', { className: 'theme-picker', hidden: true });
  const themeSelect = new Element('select'); themeSelect.dataset.themeSelect = '';
  theme.append(themeSelect);
  const palette = new Element('label', { className: 'theme-picker', hidden: true });
  const paletteSelect = new Element('select'); paletteSelect.dataset.paletteSelect = '';
  palette.append(paletteSelect);
  body.append(theme, palette);
};
const site = { comfyApp: 'witmoot', palettes: 'sage slate', paletteDefault: 'slate', palette: 'slate' };
const page = options => load('theme.js', { rootData: site, build: pickers, ...options });
const selects = p => ({ theme: p.all().find(n => 'themeSelect' in n.dataset), palette: p.all().find(n => 'paletteSelect' in n.dataset) });
const change = (p, select, value) => { select.value = value; p.events.change({ target: select }); };

test('theme: saved choices apply before drawing; System removes the attribute', async () => {
  for (const [stored, want] of [['dark', 'dark'], ['light', 'light'], [null, undefined], ['blue', undefined], ['DARK', undefined], ['__proto__', undefined]]) {
    const p = await page({ stored: stored === null ? {} : { 'comfyware:witmoot:theme': stored } });
    assert.equal(p.root.dataset.theme, want, String(stored));
  }
  const p = await page();
  p.events.DOMContentLoaded();
  const { theme } = selects(p);
  assert.equal(theme.parentNode.hidden, false);
  assert.equal(theme.value, 'system');
  change(p, theme, 'dark');
  assert.equal(p.root.dataset.theme, 'dark');
  assert.equal(p.saved['comfyware:witmoot:theme'], 'dark');
  change(p, theme, 'system');
  assert.equal(p.root.dataset.theme, undefined);
  assert.equal('comfyware:witmoot:theme' in p.saved, false);
});

test('theme: an app keeps its older storage key', async () => {
  const p = await page({ rootData: { ...site, themeKey: 'witmoot-theme' }, stored: { 'witmoot-theme': 'dark' } });
  assert.equal(p.root.dataset.theme, 'dark');
});

test('palette: the site default applies without a saved choice', async () => {
  const p = await page();
  assert.equal(p.root.dataset.palette, 'slate');
  p.events.DOMContentLoaded();
  assert.equal(selects(p).palette.parentNode.hidden, false);
  assert.equal(selects(p).palette.value, '');
});

test('palette: a member choice persists and can return to the site default', async () => {
  const p = await page({ stored: { 'comfyware:witmoot:palette': 'sage' } });
  assert.equal(p.root.dataset.palette, 'sage');
  const { palette } = selects(p);
  change(p, palette, 'slate');
  assert.equal(p.saved['comfyware:witmoot:palette'], 'slate');
  change(p, palette, '');
  assert.equal(p.root.dataset.palette, 'slate');
  assert.equal('comfyware:witmoot:palette' in p.saved, false);
});

test('palette: unknown or hostile names in storage, the select and attributes are ignored', async () => {
  for (const bad of ['neon', 'SAGE', 'sage slate', '"><script>', '__proto__', 'constructor', '']) {
    const p = await page({ stored: { 'comfyware:witmoot:palette': bad } });
    assert.equal(p.root.dataset.palette, 'slate', bad);
    change(p, selects(p).palette, bad);
    assert.equal(p.root.dataset.palette, 'slate', bad);
    assert.equal(p.saved['comfyware:witmoot:palette'], undefined, bad);
  }
  const forged = await page({ rootData: { ...site, paletteDefault: 'neon', palette: undefined } });
  assert.equal(forged.root.dataset.palette, undefined);
  const none = await page({ rootData: { comfyApp: 'witmoot' }, stored: { 'comfyware:witmoot:palette': 'sage' } });
  none.events.DOMContentLoaded();
  assert.equal(none.root.dataset.palette, undefined, 'apps without palettes are untouched');
  assert.equal(selects(none).palette.parentNode.hidden, true);
});

test('blocked storage still applies choices for this page', async () => {
  const p = await page({ blocked: true });
  assert.equal(p.root.dataset.palette, 'slate');
  assert.equal(p.root.dataset.theme, undefined);
  change(p, selects(p).palette, 'sage');
  change(p, selects(p).theme, 'dark');
  assert.equal(p.root.dataset.palette, 'sage');
  assert.equal(p.root.dataset.theme, 'dark');
});

test('other tabs update theme and palette through storage events, keyed by app', async () => {
  const p = await page();
  p.windowEvents.storage({ key: 'comfyware:imvault:palette', newValue: 'sage' });
  assert.equal(p.root.dataset.palette, 'slate');
  p.windowEvents.storage({ key: 'comfyware:witmoot:palette', newValue: 'sage' });
  p.windowEvents.storage({ key: 'comfyware:witmoot:theme', newValue: 'light' });
  assert.equal(p.root.dataset.palette, 'sage');
  assert.equal(p.root.dataset.theme, 'light');
  p.windowEvents.storage({ key: null, newValue: null });
  assert.equal(p.root.dataset.palette, 'slate');
  assert.equal(p.root.dataset.theme, undefined);
});
