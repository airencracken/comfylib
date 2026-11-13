// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ui serves the shared Comfyware styles, palettes and browser
// scripts, and validates palette names. Apps own their templates and routes.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed accounts.css components.css palettes.css suite.js theme.js timezone.js
var assets embed.FS

// Assets returns read-only shared styles and browser behavior.
// Serve the styles after the application's stylesheet to preserve layout
// overrides. Browser test helpers are deliberately outside this filesystem.
func Assets() fs.FS { return assets }
