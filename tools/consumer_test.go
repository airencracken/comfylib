// SPDX-License-Identifier: AGPL-3.0-or-later

package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyAssetsScopesSymlinkReads(t *testing.T) {
	internal := t.TempDir()
	const layout = `<link href="/static/comfyware/components.css"><script src="/static/comfyware/suite.js"></script>`
	name := filepath.Join(internal, "layout.tmpl")
	if err := os.WriteFile(name, []byte(layout), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(internal, "layout.html")
	if err := os.Symlink(filepath.Base(name), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := verifyAssets(internal); err != nil {
		t.Fatalf("a template inside the checkout should be readable: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "layout.html")
	if err := os.WriteFile(outside, []byte(layout), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := verifyAssets(internal); err == nil {
		t.Fatal("an out-of-checkout file supplied the shared assets")
	}
}
