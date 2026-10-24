// SPDX-License-Identifier: AGPL-3.0-or-later

package ui_test

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/airencracken/comfylib/ui"
)

func TestAssetsContractAndIsolation(t *testing.T) {
	assets := ui.Assets()
	if err := fstest.TestFS(assets, "accounts.css"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(assets, ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "accounts.css" {
		t.Fatal("unexpected public assets", entries, err)
	}
	data, err := fs.ReadFile(assets, "accounts.css")
	if err != nil {
		t.Fatal(err)
	}
	original := string(data)
	data[0] = 'X'
	again, err := fs.ReadFile(ui.Assets(), "accounts.css")
	if err != nil || string(again) != original {
		t.Fatal("caller mutated shared asset bytes", err)
	}
	for _, path := range []string{"../accounts.css", "/accounts.css", "missing.css", "uitest/invitations.mjs", "assets.go"} {
		if _, err := fs.ReadFile(assets, path); err == nil {
			t.Fatalf("unexpected public asset %q", path)
		}
	}
	for _, rule := range []string{
		".invitation-layout { display: grid; grid-template-columns: 360px minmax(0, 1fr)",
		"@media (max-width: 900px)", "@media (max-width: 380px)",
		".account-page { width: 100%; max-width: 480px", "min-height: 44px",
		"var(--invitation-panel)", "var(--invitation-border)",
	} {
		if !strings.Contains(original, rule) {
			t.Errorf("missing layout contract %q", rule)
		}
	}
	if strings.Contains(original, "url(") || strings.Contains(original, "@import") {
		t.Fatal("shared styles fetch external resources")
	}
}
