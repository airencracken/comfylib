// SPDX-License-Identifier: AGPL-3.0-or-later
// Shared admin metrics dashboard contract. dashboardReport runs in the
// browser (Playwright or CDP); assertDashboard checks its result in Node.
import assert from 'node:assert/strict';

export const forbiddenTables = Object.freeze(['Recent sign-ins by account', 'Most viewed content']);

// dashboardReport is self-contained so it can be serialized into a page. It
// compares the dashboard's computed styles with a probe styled from the
// --comfy-* tokens, so each app's own token values pass.
export function dashboardReport() {
  const dashboard = document.querySelector('.comfy-admin-dashboard');
  if (!dashboard) return null;
  const css = selector => { const element = dashboard.querySelector(selector) || (dashboard.matches(selector) ? dashboard : null); return element && getComputedStyle(element); };
  const probe = document.createElement('div');
  probe.style.cssText = 'position:absolute;visibility:hidden;background:var(--comfy-raised);color:var(--comfy-muted);border-radius:var(--comfy-radius-small);font-size:var(--comfy-text-xl)';
  const sizes = {};
  dashboard.append(probe);
  for (const name of ['sm', 'base', 'xl']) { probe.style.fontSize = `var(--comfy-text-${name})`; sizes[name] = getComputedStyle(probe).fontSize; }
  const probeStyle = getComputedStyle(probe);
  const tokens = { raised: probeStyle.backgroundColor, muted: probeStyle.color, radiusSmall: probeStyle.borderTopLeftRadius, ...sizes };
  probe.style.background = 'var(--comfy-surface)';
  tokens.surface = getComputedStyle(probe).backgroundColor;
  probe.remove();
  const headings = [...dashboard.querySelectorAll('h2')].map(h => h.textContent.trim());
  const captions = [...dashboard.querySelectorAll('caption')].map(c => c.textContent.trim());
  const main = dashboard.parentElement;
  return {
    headings, captions, tokens,
    styles: {
      root: css('.comfy-admin-dashboard').fontSize, title: css('.comfy-admin-heading h1').fontSize,
      label: css('.comfy-metrics dt')?.fontSize, labelColor: css('.comfy-metrics dt')?.color, value: css('.comfy-metrics dd')?.fontSize,
      radius: css('.comfy-metrics > div')?.borderTopLeftRadius, panel: css('.comfy-admin-section').backgroundColor,
      padding: css('.comfy-admin-section').padding, canvas: main ? getComputedStyle(main).backgroundColor : '',
    },
    width: dashboard.getBoundingClientRect().width,
    expectedWidth: Math.min(document.documentElement.clientWidth - 32, 1120),
    fits: document.documentElement.scrollWidth <= innerWidth,
  };
}

// assertDashboard accepts a minimal dashboard (overview and operations) and a
// full one (with Site health and Security). Per-account and per-item tables
// are never allowed.
export function assertDashboard(report, { width, label = 'dashboard' }) {
  assert.ok(report, `${label}: shared dashboard present`);
  for (const heading of ['Instance overview', 'Operations']) assert.ok(report.headings.includes(heading), `${label}: ${heading} section`);
  const health = report.headings.includes('Site health'), security = report.headings.includes('Security');
  assert.equal(health, security, `${label}: Site health and Security appear together`);
  if (health) for (const caption of ['Daily traffic', 'Slowest routes', 'Routes with errors', 'Daily security events']) assert.ok(report.captions.includes(caption), `${label}: ${caption} table`);
  for (const caption of forbiddenTables) assert.ok(!report.captions.includes(caption), `${label}: no ${caption} table`);
  const { styles, tokens } = report;
  assert.equal(styles.root, tokens.base, `${label}: body size from --comfy-text-base`);
  assert.equal(styles.title, tokens.xl, `${label}: title size from --comfy-text-xl`);
  assert.equal(styles.panel, tokens.raised, `${label}: panels from --comfy-raised`);
  assert.equal(styles.canvas, tokens.surface, `${label}: canvas from --comfy-surface`);
  if (styles.label) {
    assert.equal(styles.label, tokens.sm, `${label}: metric labels from --comfy-text-sm`);
    assert.equal(styles.labelColor, tokens.muted, `${label}: metric labels from --comfy-muted`);
    assert.equal(styles.value, tokens.xl, `${label}: metric values from --comfy-text-xl`);
    assert.equal(styles.radius, tokens.radiusSmall, `${label}: cards from --comfy-radius-small`);
  }
  assert.equal(styles.padding, width <= 480 ? '16px' : '24px', `${label}: section padding at ${width}px`);
  assert.ok(Math.abs(report.width - report.expectedWidth) < 1, `${label}: dashboard width at ${width}px`);
  assert.ok(report.fits, `${label}: no horizontal overflow at ${width}px`);
}
