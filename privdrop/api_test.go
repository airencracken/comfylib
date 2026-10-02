// SPDX-License-Identifier: AGPL-3.0-or-later

package privdrop

import (
	"testing"

	"github.com/airencracken/comfylib/internal/apisurface"
)

// The exported API is pinned in testdata/api.txt, so an accidental change to
// it fails here instead of in an application.
func TestExportedAPI(t *testing.T) { apisurface.Check(t) }
