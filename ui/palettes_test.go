// SPDX-License-Identifier: AGPL-3.0-or-later

package ui_test

import (
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/ui"
)

var (
	paletteBlock = regexp.MustCompile(`(?s):root\[data-palette="([^"]*)"\] \{(.*?)\n\}`)
	paletteColor = regexp.MustCompile(`(--comfy-[a-z-]+): light-dark\((#[0-9a-f]{6}), (#[0-9a-f]{6})\);`)
)

// palettesCSS maps palette name to mode ("light", "dark") to token colors.
func palettesCSS(t *testing.T) map[string]map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]map[string]string{}
	for _, block := range paletteBlock.FindAllStringSubmatch(asset(t, "palettes.css"), -1) {
		modes := map[string]map[string]string{"light": {}, "dark": {}}
		for _, color := range paletteColor.FindAllStringSubmatch(block[2], -1) {
			modes["light"][color[1]], modes["dark"][color[1]] = color[2], color[3]
		}
		out[block[1]] = modes
	}
	return out
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	var channels [3]float64
	for i := range channels {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		c := float64(v) / 255
		if c <= .04045 {
			channels[i] = c / 12.92
		} else {
			channels[i] = math.Pow((c+.055)/1.055, 2.4)
		}
	}
	return .2126*channels[0] + .7152*channels[1] + .0722*channels[2]
}

func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	x, y := luminance(t, a), luminance(t, b)
	return (math.Max(x, y) + .05) / (math.Min(x, y) + .05)
}

func TestContrastRatioMatchesWCAGExamples(t *testing.T) {
	if r := contrast(t, "#000000", "#ffffff"); math.Abs(r-21) > 1e-9 {
		t.Fatal(r)
	}
	if r := contrast(t, "#777777", "#ffffff"); r >= 4.5 || r < 4.4 {
		t.Fatal("#777 on white is just under AA", r)
	}
}

// Every palette meets WCAG AA in light and dark: 4.5:1 for text, 3:1 for
// control borders.
func TestPalettesMeetWCAGAAInLightAndDark(t *testing.T) {
	css := palettesCSS(t)
	for name, modes := range css {
		for mode, colors := range modes {
			for _, token := range colorTokens {
				if colors[token] == "" {
					t.Errorf("%s %s does not set %s", name, mode, token)
				}
			}
			if len(colors) != len(colorTokens) {
				t.Errorf("%s %s sets unexpected tokens: %v", name, mode, colors)
			}
			check := func(fg, bg string, minimum float64) {
				if r := contrast(t, colors[fg], colors[bg]); r < minimum {
					t.Errorf("%s %s: %s on %s is %.2f:1, want %.1f:1", name, mode, fg, bg, r, minimum)
				}
			}
			for _, bg := range []string{"--comfy-surface", "--comfy-raised"} {
				for _, fg := range []string{"--comfy-text", "--comfy-muted", "--comfy-accent", "--comfy-danger"} {
					check(fg, bg, 4.5)
				}
				check("--comfy-control-border", bg, 3)
			}
			check("--comfy-accent-text", "--comfy-accent", 4.5)
		}
	}
}

func TestGoAndCSSListTheSamePalettes(t *testing.T) {
	var names []string
	for name := range palettesCSS(t) {
		names = append(names, name)
	}
	slices.Sort(names)
	var listed []string
	for _, p := range ui.Palettes() {
		listed = append(listed, string(p))
	}
	sorted := slices.Sorted(slices.Values(listed))
	if !slices.Equal(names, sorted) || len(names) < 2 {
		t.Fatalf("palettes.css has %v, Palettes() has %v", names, listed)
	}
	ui.Palettes()[0] = "changed"
	if ui.Palettes()[0] != ui.Sage {
		t.Fatal("caller changed the palette list")
	}
}

func TestParsePaletteRejectsUnknownNames(t *testing.T) {
	for _, p := range ui.Palettes() {
		if got, err := ui.ParsePalette(string(p)); err != nil || got != p || p.Label() == "" {
			t.Fatal(p, got, err)
		}
	}
	for _, bad := range []string{"", "Sage", "SLATE", " sage", "sage ", "sage slate", "neon", `sage" onload="x`, "<script>", "sage\x00", strings.Repeat("s", 1<<16)} {
		if _, err := ui.ParsePalette(bad); !errors.Is(err, ui.ErrUnknownPalette) {
			t.Errorf("accepted %q", bad)
		}
		if ui.Palette(bad).Label() != "" {
			t.Errorf("label for %q", bad)
		}
	}
	if ui.Sage.Label() != "Sage" || ui.Slate.Label() != "Slate" {
		t.Fatal("labels")
	}
}

func TestSiteDefaultFallsBackToTheAppDefault(t *testing.T) {
	for setting, want := range map[string]ui.Palette{"": ui.Slate, "sage": ui.Sage, "slate": ui.Slate, "neon": ui.Slate, `"><x>`: ui.Slate} {
		if got := ui.SiteDefault(setting, ui.Slate); got != want {
			t.Errorf("SiteDefault(%q) = %q, want %q", setting, got, want)
		}
	}
}

func TestPaletteAttrsAndPicker(t *testing.T) {
	if got := string(ui.PaletteAttrs(ui.Sage)); got != `data-palettes="sage slate" data-palette="sage" data-palette-default="sage"` {
		t.Fatal(got)
	}
	for _, bad := range []ui.Palette{"", "neon", `sage" onload="alert(1)`} {
		if got := string(ui.PaletteAttrs(bad)); got != `data-palettes="sage slate"` {
			t.Fatalf("PaletteAttrs(%q) = %s", bad, got)
		}
	}
	picker := string(ui.ThemePicker(true))
	for _, want := range []string{`<label class="theme-picker" hidden>Theme`, `data-theme-select`, `<option value="system">System</option>`, `<option value="dark">Dark</option>`, `data-palette-select`, `<option value="">Site default</option>`, `<option value="sage">Sage</option>`, `<option value="slate">Slate</option>`} {
		if !strings.Contains(picker, want) {
			t.Fatal("picker missing", want, picker)
		}
	}
	themeOnly := string(ui.ThemePicker(false))
	if !strings.Contains(themeOnly, "data-theme-select") || strings.Contains(themeOnly, "palette") {
		t.Fatal("theme-only picker", themeOnly)
	}
}

func TestPalettesFollowTheThemeAttribute(t *testing.T) {
	css := asset(t, "palettes.css")
	for _, rule := range []string{`:root[data-palette] { color-scheme: light dark; }`, `:root[data-palette][data-theme="light"] { color-scheme: light; }`, `:root[data-palette][data-theme="dark"] { color-scheme: dark; }`} {
		if !strings.Contains(css, rule) {
			t.Error("missing", rule)
		}
	}
}

func TestPalettesChangeOnlyColors(t *testing.T) {
	for _, block := range paletteBlock.FindAllStringSubmatch(asset(t, "palettes.css"), -1) {
		for _, declaration := range strings.Split(block[2], ";") {
			name, _, ok := strings.Cut(strings.TrimSpace(declaration), ":")
			if ok && !slices.Contains(colorTokens, name) {
				t.Errorf("palette %s changes non-color token %s", block[1], name)
			}
		}
	}
}
