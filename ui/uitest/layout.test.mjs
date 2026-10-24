// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import test from 'node:test';
import { pathToFileURL } from 'node:url';

const moduleURL = process.env.COMFYWARE_LAYOUT_MODULE ? pathToFileURL(process.env.COMFYWARE_LAYOUT_MODULE) : new URL('./layout.mjs', import.meta.url);
const { accountLayout, invitationLayout, assertInvitationLayout, accountWidths, invitationWidths } = await import(moduleURL);

function documentFor(t, width, overrides = {}) {
  const panel = bounds => ({ getBoundingClientRect: () => bounds });
  const desktop = width > 900;
  const nodes = {
    '.account-page': panel({ width: Math.min(480, width - 40), left: 20, right: Math.min(500, width - 20) }),
    '.invitation-create': panel({ left: 20, right: desktop ? 380 : width - 20, top: 100, bottom: 400 }),
    '.invitation-list': panel({ left: desktop ? 404 : 20, top: desktop ? 100 : 424 }),
    '.eyebrow, .invitation-page table': null,
    ...overrides,
  };
  const previous = Object.fromEntries(['document', 'innerWidth', 'getComputedStyle'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  globalThis.innerWidth = width;
  globalThis.getComputedStyle = status => status;
  globalThis.document = {
    documentElement: { scrollWidth: width },
    querySelector: selector => nodes[selector],
    querySelectorAll: selector => selector === '.invitation-status'
      ? [{ color: 'rgb(0, 0, 0)', backgroundColor: 'rgb(255, 255, 255)' }, { color: 'rgb(255, 255, 255)', backgroundColor: 'rgb(0, 0, 0)' }]
      : [panel({ width: 100, height: 44, left: 20, right: 120 })],
  };
  t.after(() => {
    for (const [key, descriptor] of Object.entries(previous)) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete globalThis[key];
    }
  });
  return { nodes, document: globalThis.document };
}

test('standard widths, readable palettes, and serialized browser evaluation', async t => {
  assert.deepEqual(accountWidths, [320, 1280]);
  assert.deepEqual(invitationWidths, [320, 390, 768, 1280]);
  for (const width of [...invitationWidths, 900, 901]) {
    await t.test(`${width}px`, t => {
      documentFor(t, width);
      assert.equal(accountLayout(), true);
      assertInvitationLayout(invitationLayout(), width, 'fixture');
      assert.equal(Function(`return (${accountLayout.toString()})();`)(), true);
      assert.deepEqual(Function(`return (${invitationLayout.toString()})();`)(), invitationLayout());
    });
  }
});

test('account assertions reject missing, overflowing, oversized and clipped forms', async t => {
  for (const bounds of [null, { width: 481, left: 0, right: 481 }, { width: 0, left: 0, right: 0 }, { width: 300, left: -1, right: 299 }, { width: 300, left: 21, right: 321 }]) {
    await t.test(JSON.stringify(bounds), t => {
      documentFor(t, 320, { '.account-page': bounds && { getBoundingClientRect: () => bounds } });
      assert.equal(accountLayout(), false);
    });
  }
  await t.test('wide form on desktop', t => {
    documentFor(t, 1280, { '.account-page': { getBoundingClientRect: () => ({ width: 600, left: 20, right: 620 }) } });
    assert.equal(accountLayout(), false);
  });
  await t.test('document overflow', t => {
    const { document } = documentFor(t, 320);
    document.documentElement.scrollWidth = 321;
    assert.equal(accountLayout(), false);
  });
});

test('invitation assertions reject broken structure, flow, controls and contrast', async t => {
  const failures = {
    missing: ({ nodes }) => { nodes['.invitation-list'] = null; },
    eyebrow: ({ nodes }) => { nodes['.eyebrow, .invitation-page table'] = {}; },
    overflow: ({ document }) => { document.documentElement.scrollWidth = 321; },
    flow: ({ nodes }) => { nodes['.invitation-list'].getBoundingClientRect = () => ({ left: 20, top: 100 }); },
    contrast: ({ document }) => { const original = document.querySelectorAll; document.querySelectorAll = selector => selector === '.invitation-status' ? [{ color: 'rgb(120, 120, 120)', backgroundColor: 'rgb(125, 125, 125)' }] : original(selector); },
    invalidColor: ({ document }) => { const original = document.querySelectorAll; document.querySelectorAll = selector => selector === '.invitation-status' ? [{ color: 'invalid', backgroundColor: 'invalid' }] : original(selector); },
  };
  for (const [name, mutate] of Object.entries(failures)) {
    await t.test(name, t => {
      mutate(documentFor(t, 320));
      assert.throws(() => assertInvitationLayout(invitationLayout(), 320, 'fixture'), /fixture/);
    });
  }
  for (const [name, changed] of Object.entries({ short: { height: 43 }, narrow: { width: 89 }, left: { left: -1 }, right: { right: 321 } })) {
    await t.test(name, t => {
      const { document } = documentFor(t, 320);
      const original = document.querySelectorAll;
      document.querySelectorAll = selector => selector === '.invitation-status' ? original(selector) : [{ getBoundingClientRect: () => ({ width: 100, height: 44, left: 20, right: 120, ...changed }) }];
      assert.throws(() => assertInvitationLayout(invitationLayout(), 320, 'fixture'), /controls fit/);
    });
  }
});
