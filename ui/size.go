// SPDX-License-Identifier: AGPL-3.0-or-later
package ui

import "strconv"

// Size formats a byte count for people in binary units: "512 bytes",
// "5 MiB", "2.5 MiB". Every Comfyware app uses powers of 2 for sizes, so use
// it for upload limits, storage figures and other user-facing copy.
// Negative counts format as "0 bytes".
func Size(bytes int64) string {
	if bytes < 1024 {
		if bytes == 1 {
			return "1 byte"
		}
		return strconv.FormatInt(max(bytes, 0), 10) + " bytes"
	}
	value := float64(bytes)
	unit := ""
	for _, next := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		if value < 1023.95 && unit != "" {
			break
		}
		value /= 1024
		unit = next
	}
	text := strconv.FormatFloat(value, 'f', 1, 64)
	if len(text) > 2 && text[len(text)-2:] == ".0" {
		text = text[:len(text)-2]
	}
	return text + " " + unit
}
