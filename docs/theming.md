# Theming

The shared pages (accounts, invitations, admin metrics, setup, menus) read only
the `--comfy-*` tokens below. Set them once on `:root`, or pick a palette.
Unset tokens use neutral system colors that follow light and dark mode.

## Tokens

| Token | Use |
| --- | --- |
| `--comfy-surface` | Page background |
| `--comfy-raised` | Panels and cards |
| `--comfy-border` | Dividers and panel edges |
| `--comfy-control-border` | Input and button edges; needs 3:1 contrast |
| `--comfy-text` | Body text |
| `--comfy-muted` | Secondary text |
| `--comfy-accent` | Links, focus rings, selected states |
| `--comfy-accent-text` | Text on an accent background |
| `--comfy-danger` | Errors |
| `--comfy-font` | Body font |
| `--comfy-heading-font` | Heading font |
| `--comfy-heading-weight` | Heading weight |
| `--comfy-text-sm` | Labels and help text (default 0.875rem) |
| `--comfy-text-base` | Body text (default 1rem) |
| `--comfy-text-lg` | Section headings (default 1.25rem) |
| `--comfy-text-xl` | Page titles and metric values (default 1.75rem) |
| `--comfy-radius` | Panel corners (default 0.625rem) |
| `--comfy-radius-small` | Control and card corners (default 0.375rem) |

Map your theme once, for example:

```css
:root {
  --comfy-surface: var(--bg);
  --comfy-raised: var(--bg-elev);
  --comfy-text: var(--text);
}
```

Don't also define the old names in terms of the tokens (for example
`--bg: var(--comfy-surface)`) while the deprecated fallback below exists; that
makes a loop and both values become invalid.

### Deprecated fallback

Until 0.8.0, unset tokens also read the older variables apps already define:
`--background`, `--bg`, `--bg-elev`, `--panel`, `--paper`, `--border`, `--line`,
`--text`, `--ink`, `--muted`, `--accent`, `--green`, `--button-text`,
`--button-ink`, `--danger`, `--rust` and `--radius`, plus the earlier
`--comfy-ink`, `--comfy-panel` and `--comfy-line`. Shared CSS no longer defines
`--comfy-ink`, `--comfy-panel`, `--comfy-line` or the `--invitation-*`
variables.

## Palettes

`palettes.css` has two palettes, each with light and dark values. `data-theme`
(or the system setting) picks light or dark, so every palette works with
System, Light and Dark. Each meets WCAG AA: 4.5:1 for text and 3:1 for control
borders.

| Palette | Based on |
| --- | --- |
| `sage` | Witmoot and comfyware.org: paper, ink and green, serif headings |
| `slate` | Imvault: cool slate with a blue accent |

To add a palette, add one block to `ui/palettes.css` and its name to the list
in `ui/palettes.go`. The tests check contrast and that both lists match.

### Adopting palettes

1. Load `palettes.css` after your stylesheet, and `suite.js` in `<head>`
   without `defer`.
2. Declare your app's default and read the owner's setting:
   `site := ui.SiteDefault(settings.Palette, ui.Slate)`. Validate the setting
   with `ui.ParsePalette` before saving it.
3. Render `<html data-comfy-app="imvault" {{.PaletteAttrs}}>` with
   `ui.PaletteAttrs(site)`. Pages use the site palette without JavaScript.
4. Put `{{.PalettePicker}}` (from `ui.PalettePicker()`) next to your theme
   picker. Members choose Site default, Sage or Slate. The choice is kept in
   the browser under `comfyware:<app>:palette`; `suite.js` applies it before
   the page is drawn and ignores names the server did not list.
5. Point your own variables at the tokens so your pages follow the palette too.

## Copy buttons

Add `data-comfy-copy` to the read-only input that shows an invitation or reset
link. `suite.js` adds a Copy link button next to it, and selects the link when
the clipboard is not available. Without JavaScript the input stays as it is, so
show the link once.
