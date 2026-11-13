// SPDX-License-Identifier: AGPL-3.0-or-later
// Runs the shared suite.js against a small fake DOM.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../suite.js', import.meta.url), 'utf8');

class Element {
  constructor(tag, attrs = {}) {
    this.tagName = tag; this.dataset = {}; this.attributes = {}; this.children = []; this.parentNode = null;
    this.hidden = false; this.listeners = {}; this.textContent = ''; this.value = '';
    Object.assign(this, attrs);
  }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  addEventListener(type, fn) { this.listeners[type] = fn; }
  append(...nodes) { for (const node of nodes) { node.parentNode?.remove(node); node.parentNode = this; this.children.push(node); } }
  insertBefore(node, before) { node.parentNode?.remove(node); node.parentNode = this; this.children.splice(this.children.indexOf(before), 0, node); }
  remove(node) { this.children.splice(this.children.indexOf(node), 1); }
  matches(selector) { return selector === '[data-palette-select]' && 'paletteSelect' in this.dataset; }
  closest(selector) {
    for (let node = this; node; node = node.parentNode) if (selector === '.comfy-palette-picker' && node.className === 'comfy-palette-picker') return node;
    return null;
  }
  focus() { this.focused = true; }
  select() { this.selected = true; }
}

function load({ stored = {}, blocked = false, rootData = { comfyApp: 'witmoot', palettes: 'sage slate', paletteDefault: 'slate', palette: 'slate' }, clipboard } = {}) {
  const events = {}, windowEvents = {}, saved = { ...stored };
  const root = new Element('html'); Object.assign(root.dataset, rootData);
  const body = new Element('body');
  const picker = new Element('label', { className: 'comfy-palette-picker', hidden: true });
  const select = new Element('select'); select.dataset.paletteSelect = '';
  picker.append(select);
  const link = new Element('input', { value: 'https://family.example/join/secret' }); link.dataset.comfyCopy = '';
  body.append(picker, link);
  const all = () => { const out = []; const walk = node => { out.push(node); node.children.forEach(walk); }; walk(body); return out; };
  const document = {
    documentElement: root,
    createElement: tag => new Element(tag),
    addEventListener: (type, fn) => { events[type] = fn; },
    querySelectorAll(selector) {
      if (selector === '[data-palette-select]') return all().filter(node => 'paletteSelect' in node.dataset);
      if (selector === 'input[data-comfy-copy]:not([data-comfy-copy-ready])') return all().filter(node => node.tagName === 'input' && 'comfyCopy' in node.dataset && !node.dataset.comfyCopyReady);
      if (selector === '.comfy-copy') return all().filter(node => node.className === 'comfy-copy');
      return [];
    },
  };
  const localStorage = {
    getItem(key) { if (blocked) throw new Error('blocked'); return key in saved ? saved[key] : null; },
    setItem(key, value) { if (blocked) throw new Error('blocked'); saved[key] = String(value); },
    removeItem(key) { if (blocked) throw new Error('blocked'); delete saved[key]; },
  };
  vm.runInNewContext(source, { document, localStorage, navigator: { clipboard }, window: { addEventListener: (type, fn) => { windowEvents[type] = fn; } } });
  return { root, picker, select, link, body, events, windowEvents, saved, document };
}

const choose = (page, value) => { page.select.value = value; page.events.change({ target: page.select }); };

test('the site default applies without a saved choice, and the picker appears', () => {
  const page = load();
  assert.equal(page.root.dataset.palette, 'slate');
  page.events.DOMContentLoaded();
  assert.equal(page.picker.hidden, false);
  assert.equal(page.select.value, '');
});

test('a member choice applies before drawing, persists, and can return to the site default', () => {
  const page = load({ stored: { 'comfyware:witmoot:palette': 'sage' } });
  assert.equal(page.root.dataset.palette, 'sage');
  choose(page, 'slate');
  assert.equal(page.saved['comfyware:witmoot:palette'], 'slate');
  choose(page, '');
  assert.equal(page.root.dataset.palette, 'slate');
  assert.equal('comfyware:witmoot:palette' in page.saved, false);
});

test('unknown or hostile palette names in storage, the select and attributes are ignored', () => {
  for (const bad of ['neon', 'SAGE', 'sage slate', '"><script>', '__proto__', 'constructor', '']) {
    const page = load({ stored: { 'comfyware:witmoot:palette': bad } });
    assert.equal(page.root.dataset.palette, 'slate', bad);
    choose(page, bad);
    assert.equal(page.root.dataset.palette, 'slate', bad);
    assert.equal(page.saved['comfyware:witmoot:palette'], undefined, bad);
  }
  const forged = load({ rootData: { comfyApp: 'witmoot', palettes: 'sage slate', paletteDefault: 'neon' } });
  assert.equal(forged.root.dataset.palette, undefined);
  const none = load({ rootData: { comfyApp: 'witmoot' }, stored: { 'comfyware:witmoot:palette': 'sage' } });
  none.events.DOMContentLoaded();
  assert.equal(none.root.dataset.palette, undefined, 'apps without palettes are untouched');
  assert.equal(none.picker.hidden, true);
});

test('blocked storage still applies the choice for this page', () => {
  const page = load({ blocked: true });
  assert.equal(page.root.dataset.palette, 'slate');
  choose(page, 'sage');
  assert.equal(page.root.dataset.palette, 'sage');
});

test('other tabs update the palette through storage events, keyed by app', () => {
  const page = load();
  page.windowEvents.storage({ key: 'comfyware:imvault:palette', newValue: 'sage' });
  assert.equal(page.root.dataset.palette, 'slate');
  page.windowEvents.storage({ key: 'comfyware:witmoot:palette', newValue: 'sage' });
  assert.equal(page.root.dataset.palette, 'sage');
  page.windowEvents.storage({ key: null, newValue: null });
  assert.equal(page.root.dataset.palette, 'slate');
});

