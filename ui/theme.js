/* SPDX-License-Identifier: AGPL-3.0-or-later */
/* Theme and palette choices. Load in <head> without defer so a saved choice
   applies before the page is drawn. Choices are kept in this browser; when
   storage is blocked they still apply to the current page. */
(() => {
  'use strict';
  const root = document.documentElement;
  const app = root.dataset.comfyApp || 'app';
  const get = key => { try { return localStorage.getItem(key); } catch { return null; } };
  const set = (key, value) => {
    try { if (value) localStorage.setItem(key, value); else localStorage.removeItem(key); } catch { /* The choice still applies to this page. */ }
  };

  // Theme: System (no attribute), Light or Dark. data-theme-key keeps an
  // app's older storage key, such as "witmoot-theme".
  const themeKey = root.dataset.themeKey || 'comfyware:' + app + ':theme';
  const validTheme = value => value === 'light' || value === 'dark';
  let theme = 'system';

  // Palette: the server renders the site default; only names listed in
  // data-palettes are accepted as a member's choice.
  const paletteKey = 'comfyware:' + app + ':palette';
  const palettes = (root.dataset.palettes || '').split(' ').filter(Boolean);
  const validPalette = value => typeof value === 'string' && palettes.includes(value);
  const siteDefault = validPalette(root.dataset.paletteDefault) ? root.dataset.paletteDefault : '';
  let palette = '';

  const applyTheme = value => {
    theme = validTheme(value) ? value : 'system';
    if (theme === 'system') root.removeAttribute('data-theme'); else root.setAttribute('data-theme', theme);
  };
  const applyPalette = value => {
    palette = validPalette(value) ? value : '';
    const used = palette || siteDefault;
    if (used) root.setAttribute('data-palette', used); else root.removeAttribute('data-palette');
  };
  const sync = () => {
    document.querySelectorAll('[data-theme-select]').forEach(select => {
      select.value = theme;
      const picker = select.closest('.theme-picker');
      if (picker) picker.hidden = false;
    });
    document.querySelectorAll('[data-palette-select]').forEach(select => {
      select.value = palette;
      const picker = select.closest('.theme-picker');
      if (picker) picker.hidden = !palettes.length;
    });
  };

  applyTheme(get(themeKey));
  applyPalette(get(paletteKey));
  document.addEventListener('DOMContentLoaded', sync);
  document.addEventListener('htmx:afterSwap', sync);
  document.addEventListener('change', event => {
    const target = event.target;
    if (!target.matches) return;
    if (target.matches('[data-theme-select]')) {
      applyTheme(target.value);
      set(themeKey, theme === 'system' ? '' : theme);
    } else if (target.matches('[data-palette-select]')) {
      applyPalette(target.value);
      set(paletteKey, palette);
    } else return;
    sync();
  });
  window.addEventListener('storage', event => {
    if (event.key === themeKey || event.key === null) applyTheme(event.key === null ? null : event.newValue);
    if (event.key === paletteKey || event.key === null) applyPalette(event.key === null ? null : event.newValue);
    sync();
  });
})();
