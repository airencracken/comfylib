/* SPDX-License-Identifier: AGPL-3.0-or-later */
(() => {
  'use strict';
  const root = document.documentElement;
  const app = root.dataset.comfyApp || 'app';
  const storage = {
    get(key) { try { return localStorage.getItem(key); } catch { return null; } },
    set(key, value) {
      try { if (value) localStorage.setItem(key, value); else localStorage.removeItem(key); } catch { /* The choice still applies to this page. */ }
    },
  };

  const densityKey = 'comfyware:' + app + ':density';
  const validDensity = value => value === 'compact' || value === 'comfortable';
  const applyDensity = value => {
    root.dataset.density = validDensity(value) ? value : 'comfortable';
    document.querySelectorAll('[data-density-controls]').forEach(control => { control.hidden = false; });
    document.querySelectorAll('[data-density-choice]').forEach(button => {
      button.setAttribute('aria-pressed', String(button.dataset.densityChoice === root.dataset.density));
    });
  };

  // Copy buttons for one-time links: <input readonly data-comfy-copy value="...">.
  const enhanceCopy = () => {
    document.querySelectorAll('input[data-comfy-copy]:not([data-comfy-copy-ready])').forEach(input => {
      input.dataset.comfyCopyReady = '1';
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'comfy-copy';
      button.textContent = 'Copy link';
      const status = document.createElement('p');
      status.className = 'comfy-copy-status';
      status.setAttribute('role', 'status');
      const row = document.createElement('div');
      row.className = 'comfy-copy-row';
      input.parentNode.insertBefore(row, input);
      row.append(input, button, status);
      button.addEventListener('click', async () => {
        try {
          await navigator.clipboard.writeText(input.value);
          status.textContent = 'Link copied.';
        } catch {
          input.focus();
          input.select();
          status.textContent = 'Copy the selected link.';
        }
      });
    });
  };

  const refresh = () => { applyDensity(root.dataset.density); enhanceCopy(); };
  applyDensity(storage.get(densityKey));

  document.addEventListener('click', event => {
    const button = event.target.closest('[data-density-choice]');
    if (button && validDensity(button.dataset.densityChoice)) {
      applyDensity(button.dataset.densityChoice);
      storage.set(densityKey, root.dataset.density);
    }
    document.querySelectorAll('.comfy-menu[open]').forEach(menu => {
      if (!menu.contains(event.target) || event.target.closest('a')) menu.open = false;
    });
  });
  document.addEventListener('change', event => {
    if (event.target.closest('.theme-picker')) document.querySelectorAll('.comfy-menu[open]').forEach(menu => { menu.open = false; });
  });
  document.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    document.querySelectorAll('.comfy-menu[open]').forEach(menu => {
      menu.open = false;
      menu.querySelector('summary').focus();
    });
  });
  document.addEventListener('DOMContentLoaded', refresh);
  document.addEventListener('htmx:afterSwap', refresh);
  window.addEventListener('storage', event => {
    if (event.key === densityKey) applyDensity(event.newValue);
  });
})();
