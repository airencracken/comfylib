// SPDX-License-Identifier: AGPL-3.0-or-later
package problem

import (
	"bytes"
	"net/http"
)

// bufferedWriter collects a layout's output so a failed render can fall back
// to the minimal page without a half-written response.
type bufferedWriter struct {
	header http.Header
	body   bytes.Buffer
}

func (b *bufferedWriter) Header() http.Header         { return b.header }
func (b *bufferedWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedWriter) WriteHeader(int)             {}
