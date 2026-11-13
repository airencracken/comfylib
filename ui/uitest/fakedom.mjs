// SPDX-License-Identifier: AGPL-3.0-or-later
// A small fake DOM for running the shared browser scripts under node --test.
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const camel = name => name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());

export class Element {
  constructor(tag, attrs = {}) {
    this.tagName = tag; this.dataset = {}; this.attributes = {}; this.children = []; this.parentNode = null;
    this.hidden = false; this.listeners = {}; this.textContent = ''; this.value = '';
    Object.assign(this, attrs);
  }
  setAttribute(name, value) { if (name.startsWith('data-')) this.dataset[camel(name)] = String(value); else this.attributes[name] = String(value); }
  removeAttribute(name) { if (name.startsWith('data-')) delete this.dataset[camel(name)]; else delete this.attributes[name]; }
  addEventListener(type, fn) { this.listeners[type] = fn; }
  append(...nodes) { for (const node of nodes) { node.parentNode?.remove(node); node.parentNode = this; this.children.push(node); } }
  insertBefore(node, before) { node.parentNode?.remove(node); node.parentNode = this; this.children.splice(this.children.indexOf(before), 0, node); }
  remove(node) { this.children.splice(this.children.indexOf(node), 1); }
  matches(selector) {
    if (selector === '[data-palette-select]') return 'paletteSelect' in this.dataset;
    if (selector === '[data-theme-select]') return 'themeSelect' in this.dataset;
    return false;
  }
  closest(selector) {
    const cls = selector.startsWith('.') ? selector.slice(1) : null;
    for (let node = this; node; node = node.parentNode) if (cls && (node.className || '').split(' ').includes(cls)) return node;
    return null;
  }
  focus() { this.focused = true; }
  select() { this.selected = true; }
}

// load runs a script from ui/ with the given storage and root data.
export async function load(script, { stored = {}, blocked = false, rootData = {}, clipboard, build = () => {} } = {}) {
  const source = await readFile(new URL('../' + script, import.meta.url), 'utf8');
  const events = {}, windowEvents = {}, saved = { ...stored };
  const root = new Element('html'); Object.assign(root.dataset, rootData);
  const body = new Element('body');
  build(body);
  const all = () => { const out = []; const walk = node => { out.push(node); node.children.forEach(walk); }; walk(body); return out; };
  const document = {
    documentElement: root,
    createElement: tag => new Element(tag),
    addEventListener: (type, fn) => { events[type] = fn; },
    querySelectorAll(selector) {
      const nodes = all();
      if (selector === '[data-palette-select]') return nodes.filter(node => 'paletteSelect' in node.dataset);
      if (selector === '[data-theme-select]') return nodes.filter(node => 'themeSelect' in node.dataset);
      if (selector === 'input[data-comfy-copy]:not([data-comfy-copy-ready])') return nodes.filter(node => node.tagName === 'input' && 'comfyCopy' in node.dataset && !node.dataset.comfyCopyReady);
      if (selector === '.comfy-copy') return nodes.filter(node => node.className === 'comfy-copy');
      return [];
    },
  };
  const localStorage = {
    getItem(key) { if (blocked) throw new Error('blocked'); return Object.hasOwn(saved, key) ? saved[key] : null; },
    setItem(key, value) { if (blocked) throw new Error('blocked'); saved[key] = String(value); },
    removeItem(key) { if (blocked) throw new Error('blocked'); delete saved[key]; },
  };
  vm.runInNewContext(source, { document, localStorage, navigator: { clipboard }, window: { addEventListener: (type, fn) => { windowEvents[type] = fn; } } });
  return { root, body, events, windowEvents, saved, document, all };
}
