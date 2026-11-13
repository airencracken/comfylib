// SPDX-License-Identifier: AGPL-3.0-or-later
// Copy buttons added by suite.js to one-time links.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Element, load } from './fakedom.mjs';

const withLink = body => {
  const link = new Element('input', { value: 'https://family.example/join/secret' });
  link.dataset.comfyCopy = '';
  body.append(link);
};
const page = clipboard => load('suite.js', { rootData: { comfyApp: 'witmoot' }, clipboard, build: withLink });
const linkOf = p => p.all().find(node => node.tagName === 'input');

test('one copy button is added to each one-time link and copies it', async () => {
  const copied = [];
  const p = await page({ writeText: async text => { copied.push(text); } });
  p.events.DOMContentLoaded();
  p.events['htmx:afterSwap']();
  const buttons = p.document.querySelectorAll('.comfy-copy');
  assert.equal(buttons.length, 1);
  assert.equal(buttons[0].type, 'button');
  assert.equal(buttons[0].textContent, 'Copy link');
  await buttons[0].listeners.click();
  assert.deepEqual(copied, ['https://family.example/join/secret']);
  const status = buttons[0].parentNode.children.find(node => node.className === 'comfy-copy-status');
  assert.equal(status.attributes.role, 'status');
  assert.equal(status.textContent, 'Link copied.');
  assert.equal(linkOf(p).parentNode, buttons[0].parentNode, 'the link and its button share a row');
});

test('without clipboard access the link is selected for manual copying', async () => {
  for (const clipboard of [undefined, { writeText: async () => { throw new Error('denied'); } }]) {
    const p = await page(clipboard);
    p.events.DOMContentLoaded();
    const [button] = p.document.querySelectorAll('.comfy-copy');
    await button.listeners.click();
    assert.equal(linkOf(p).selected, true);
    assert.equal(linkOf(p).focused, true);
    assert.match(button.parentNode.children.at(-1).textContent, /selected link/);
  }
});

test('pages without one-time links get no buttons', async () => {
  const p = await load('suite.js', { rootData: { comfyApp: 'witmoot' } });
  p.events.DOMContentLoaded();
  assert.equal(p.document.querySelectorAll('.comfy-copy').length, 0);
});
