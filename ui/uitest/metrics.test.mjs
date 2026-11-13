// SPDX-License-Identifier: AGPL-3.0-or-later
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { assertDashboard, dashboardReport, forbiddenTables } from './metrics.mjs';

// fakePage builds just enough DOM for dashboardReport. Token values differ
// per "app" so the contract must read them rather than fixed numbers.
function fakePage(t, { width = 1280, full = true, captions, headings, override = {}, tokens = {} } = {}) {
  const values = { raised: 'rgb(255, 254, 249)', surface: 'rgb(245, 243, 234)', muted: 'rgb(98, 110, 101)', radiusSmall: '6px', sm: '14px', base: '16px', xl: '28px', ...tokens };
  const styles = {
    '.comfy-admin-dashboard': { fontSize: values.base }, '.comfy-admin-heading h1': { fontSize: values.xl },
    '.comfy-metrics dt': { fontSize: values.sm, color: values.muted }, '.comfy-metrics dd': { fontSize: values.xl },
    '.comfy-metrics > div': { borderTopLeftRadius: values.radiusSmall }, '.comfy-admin-section': { backgroundColor: values.raised, padding: width <= 480 ? '16px' : '24px' },
    main: { backgroundColor: values.surface }, ...override,
  };
  const element = selector => ({ selector, textContent: selector });
  headings ??= full ? ['Instance overview', 'Site health', 'Security', 'Operations'] : ['Instance overview', 'Operations'];
  captions ??= full ? ['Daily traffic', 'Slowest routes', 'Routes with errors', 'Daily security events'] : [];
  const probe = { style: { cssText: '', fontSize: '', background: '' }, remove() {} };
  const main = { selector: 'main' };
  const dashboard = {
    selector: '.comfy-admin-dashboard', parentElement: main,
    matches: selector => selector === '.comfy-admin-dashboard',
    querySelector: selector => (selector in styles ? element(selector) : null),
    querySelectorAll: selector => (selector === 'h2' ? headings : captions).map(text => ({ textContent: text })),
    append() {},
    getBoundingClientRect: () => ({ width: Math.min(width - 32, 1120) }),
  };
  const computed = node => {
    if (node === probe) {
      const size = probe.style.fontSize.match(/--comfy-text-(\w+)/)?.[1];
      return { fontSize: values[size] || values.xl, backgroundColor: probe.style.background.includes('surface') ? values.surface : values.raised, color: values.muted, borderTopLeftRadius: values.radiusSmall };
    }
    return styles[node.selector];
  };
  const saved = Object.fromEntries(['document', 'innerWidth', 'getComputedStyle'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  globalThis.innerWidth = width;
  globalThis.getComputedStyle = computed;
  globalThis.document = { querySelector: () => dashboard, createElement: () => probe, documentElement: { clientWidth: width, scrollWidth: width } };
  t.after(() => { for (const [key, d] of Object.entries(saved)) { if (d) Object.defineProperty(globalThis, key, d); else delete globalThis[key]; } });
}

test('full and minimal dashboards pass at every width with any app tokens', async t => {
  for (const full of [true, false]) for (const width of [320, 390, 768, 1280]) for (const tokens of [{}, { xl: '30px', raised: 'rgb(23, 26, 33)', surface: 'rgb(16, 18, 22)', radiusSmall: '8px' }]) {
    await t.test(`${full ? 'full' : 'minimal'} ${width} ${tokens.xl || 'default'}`, t => {
      fakePage(t, { width, full, tokens });
      assertDashboard(dashboardReport(), { width, label: 'fixture' });
      assert.equal(Function(`return (${dashboardReport.toString()})();`)().styles.root, '16px', 'serializable');
    });
  }
});

test('the contract rejects per-person tables, partial sections and untokenized styles', async t => {
  const failures = {
    'per-account table': { captions: ['Daily traffic', 'Slowest routes', 'Routes with errors', 'Daily security events', forbiddenTables[0]] },
    'per-item table': { full: false, captions: [forbiddenTables[1]] },
    'health without security': { headings: ['Instance overview', 'Site health', 'Operations'] },
    'missing traffic table': { captions: ['Slowest routes', 'Routes with errors', 'Daily security events'] },
    'missing operations': { headings: ['Instance overview'] },
    'fixed title size': { override: { '.comfy-admin-heading h1': { fontSize: '30px' } } },
    'fixed panel colour': { override: { '.comfy-admin-section': { backgroundColor: 'rgb(32, 41, 55)', padding: '24px' } } },
    'fixed canvas colour': { override: { main: { backgroundColor: 'rgb(243, 245, 248)' } } },
    'fixed label colour': { override: { '.comfy-metrics dt': { fontSize: '14px', color: 'rgb(88, 101, 121)' } } },
    'fixed corners': { override: { '.comfy-metrics > div': { borderTopLeftRadius: '10px' } } },
  };
  for (const [name, options] of Object.entries(failures)) {
    await t.test(name, t => {
      fakePage(t, options);
      assert.throws(() => assertDashboard(dashboardReport(), { width: 1280, label: 'fixture' }), /fixture/);
    });
  }
  await t.test('missing dashboard', t => {
    fakePage(t);
    globalThis.document.querySelector = () => null;
    assert.throws(() => assertDashboard(dashboardReport(), { width: 1280, label: 'fixture' }), /present/);
  });
  await t.test('wrong padding on phones', t => {
    fakePage(t, { width: 320, override: { '.comfy-admin-section': { backgroundColor: 'rgb(255, 254, 249)', padding: '24px' } } });
    assert.throws(() => assertDashboard(dashboardReport(), { width: 320, label: 'fixture' }), /padding/);
  });
});
