// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ui supplies shared, palette-independent Comfyware presentation assets.
// Applications own their templates, routes and theme variables.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed accounts.css components.css palettes.css suite.js timezone.js
var assets embed.FS

// Assets returns read-only shared styles and browser behavior.
// Serve the styles after the application's stylesheet to preserve layout
// overrides. Browser test helpers are deliberately outside this filesystem.
func Assets() fs.FS { return assets }
