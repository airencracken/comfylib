// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ui supplies shared, palette-independent Comfyware presentation assets.
// Applications own their templates, routes and theme variables.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed accounts.css
var assets embed.FS

// Assets returns the read-only styles for invitation and account forms.
// Serve accounts.css after the application's stylesheet to preserve its layout
// overrides. Browser test helpers are deliberately outside this filesystem.
func Assets() fs.FS { return assets }
