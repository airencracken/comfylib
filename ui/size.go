// SPDX-License-Identifier: AGPL-3.0-or-later
package ui

import "strconv"

// Size formats a byte count for people, in decimal units: "512 bytes",
// "5 MB", "2.1 MB". Use it for upload limits and other user-facing copy.
// Negative counts format as "0 bytes".
func Size(bytes int64) string {
	if bytes < 1000 {
		if bytes == 1 {
			return "1 byte"
		}
		return strconv.FormatInt(max(bytes, 0), 10) + " bytes"
	}
	value := float64(bytes)
	unit := ""
	for _, next := range []string{"kB", "MB", "GB", "TB", "PB", "EB"} {
		if value < 999.95 && unit != "" {
			break
		}
		value /= 1000
		unit = next
	}
	text := strconv.FormatFloat(value, 'f', 1, 64)
	if len(text) > 2 && text[len(text)-2:] == ".0" {
		text = text[:len(text)-2]
	}
	return text + " " + unit
}
