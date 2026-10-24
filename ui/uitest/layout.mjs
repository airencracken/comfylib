// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';

export const invitationWidths = Object.freeze([320, 390, 768, 1280]);
export const accountWidths = Object.freeze([320, 1280]);

// These functions are self-contained so both Playwright and CDP can evaluate
// the same assertions inside a real browser, without injecting a runtime library.
export function accountLayout() {
  const element = document.querySelector('.account-page');
  if (!element) return false;
  const form = element.getBoundingClientRect();
  return document.documentElement.scrollWidth <= innerWidth && form.width > 0 && form.width <= 480 && form.left >= 0 && form.right <= innerWidth;
}

export function invitationLayout() {
  const create = document.querySelector('.invitation-create');
  const records = document.querySelector('.invitation-list');
  if (!create || !records) return { fits: false, beside: false, stacked: false, readableStatuses: false, fields: false };
  const form = create.getBoundingClientRect(), list = records.getBoundingClientRect();
  return {
    fits: document.documentElement.scrollWidth <= innerWidth && !document.querySelector('.eyebrow, .invitation-page table'),
    beside: form.right < list.left && Math.abs(form.top - list.top) < 1,
    stacked: form.bottom < list.top,
    readableStatuses: [...document.querySelectorAll('.invitation-status')].every(status => {
      const luminance = color => {
        const values = color.match(/[\d.]+/g);
        if (!values || values.length < 3) return NaN;
        const channels = values.slice(0, 3).map(Number).map(value => {
          value /= 255;
          return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4;
        });
        return .2126 * channels[0] + .7152 * channels[1] + .0722 * channels[2];
      };
      const style = getComputedStyle(status);
      const first = luminance(style.color), second = luminance(style.backgroundColor);
      return (Math.max(first, second) + .05) / (Math.min(first, second) + .05) >= 4.5;
    }),
    fields: [...document.querySelectorAll('.invitation-create input:not([type="hidden"])')].every(input => {
      const bounds = input.getBoundingClientRect();
      return bounds.width >= 90 && bounds.height >= 44 && bounds.left >= 0 && bounds.right <= innerWidth;
    }),
  };
}

export function assertInvitationLayout(layout, width, label) {
  assert.ok(layout.readableStatuses, `${label}: readable invitation statuses`);
  assert.ok(layout.fits && layout.fields, `${label}: controls fit at ${width}px`);
  assert.ok(width > 900 ? layout.beside : layout.stacked, `${label}: responsive form/list layout at ${width}px`);
}
