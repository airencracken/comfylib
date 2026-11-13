// SPDX-License-Identifier: AGPL-3.0-or-later
package ui

import (
	"errors"
	"html/template"
	"strings"
)

// Palette names a color palette in palettes.css.
type Palette string

// The shared palettes.
const (
	Sage  Palette = "sage"  // paper, ink and green
	Slate Palette = "slate" // cool slate with a blue accent
)

// palettes lists every palette in palettes.css, in menu order.
var palettes = []Palette{Sage, Slate}

// Palettes returns the palette names, in menu order.
func Palettes() []Palette { return append([]Palette(nil), palettes...) }

// ErrUnknownPalette is returned for a name that is not a shared palette.
var ErrUnknownPalette = errors.New("unknown palette")

// ParsePalette returns the palette with exactly this name. Use it to validate
// an owner's site palette setting before saving it.
func ParsePalette(name string) (Palette, error) {
	for _, p := range palettes {
		if string(p) == name {
			return p, nil
		}
	}
	return "", ErrUnknownPalette
}

// Label returns the palette's menu label, such as "Sage".
func (p Palette) Label() string {
	if _, err := ParsePalette(string(p)); err != nil {
		return ""
	}
	return strings.ToUpper(string(p[:1])) + string(p[1:])
}

// SiteDefault returns the owner's saved palette setting, or appDefault when
// the setting is empty or no longer a known palette.
func SiteDefault(setting string, appDefault Palette) Palette {
	if p, err := ParsePalette(setting); err == nil {
		return p
	}
	return appDefault
}

// PaletteAttrs returns the attributes for the <html> element: the site
// palette, so pages render in it without JavaScript, and the list theme.js
// accepts as a member's choice. An unknown siteDefault renders no palette.
func PaletteAttrs(siteDefault Palette) template.HTMLAttr {
	names := make([]string, len(palettes))
	for i, p := range palettes {
		names[i] = string(p)
	}
	attrs := `data-palettes="` + strings.Join(names, " ") + `"`
	if _, err := ParsePalette(string(siteDefault)); err == nil {
		attrs += ` data-palette="` + string(siteDefault) + `" data-palette-default="` + string(siteDefault) + `"`
	}
	return template.HTMLAttr(attrs) // #nosec G203 -- only fixed palette names are written.
}

var pickerTemplate = template.Must(template.New("picker").Parse(`<label class="theme-picker" hidden>Theme <select data-theme-select aria-label="Color theme"><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></label>` +
	`{{if .}}<label class="theme-picker" hidden>Colors <select data-palette-select aria-label="Color palette"><option value="">Site default</option>{{range .}}<option value="{{.}}">{{.Label}}</option>{{end}}</select></label>{{end}}`))

// ThemePicker returns the shared theme menu: System, Light or Dark, and with
// palettes also Site default and each palette. It stays hidden until theme.js
// runs; choices are kept in the browser.
func ThemePicker(withPalettes bool) template.HTML {
	var list []Palette
	if withPalettes {
		list = palettes
	}
	var out strings.Builder
	if err := pickerTemplate.Execute(&out, list); err != nil {
		return ""
	}
	return template.HTML(out.String()) // #nosec G203 -- escaped by html/template above.
}
