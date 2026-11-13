// SPDX-License-Identifier: AGPL-3.0-or-later

package ui_test

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/ui"
)

func asset(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(ui.Assets(), name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// tokenBlock returns the @layer comfy-tokens block and the rest of a file.
func tokenBlock(t *testing.T, css string) (block, rest string) {
	t.Helper()
	start := strings.Index(css, "/* Comfyware tokens.")
	end := strings.Index(css, "\n  }\n}\n")
	if start < 0 || end < start {
		t.Fatal("no token block")
	}
	end += len("\n  }\n}\n")
	return css[start:end], css[:start] + css[end:]
}

var colorTokens = []string{"--comfy-surface", "--comfy-raised", "--comfy-border", "--comfy-control-border", "--comfy-text", "--comfy-muted", "--comfy-accent", "--comfy-accent-text", "--comfy-danger"}

var allTokens = append(slices.Clone(colorTokens), "--comfy-font", "--comfy-heading-font", "--comfy-heading-weight",
	"--comfy-text-sm", "--comfy-text-base", "--comfy-text-lg", "--comfy-text-xl", "--comfy-radius", "--comfy-radius-small")

func TestSharedStylesReadOnlyTokens(t *testing.T) {
	accountsBlock, accounts := tokenBlock(t, asset(t, "accounts.css"))
	componentsBlock, components := tokenBlock(t, asset(t, "components.css"))
	if accountsBlock != componentsBlock {
		t.Fatal("accounts.css and components.css define different token defaults")
	}
	if !strings.Contains(accountsBlock, "@layer comfy-tokens") {
		t.Fatal("token defaults must be layered so app and palette values win")
	}
	for _, token := range allTokens {
		if !strings.Contains(accountsBlock, "    "+token+":") {
			t.Errorf("token %s has no default", token)
		}
	}
	use := regexp.MustCompile(`var\((--[a-z0-9-]+)`)
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	pixelsFont := regexp.MustCompile(`font(-size)?:[^;]*\b\d+px`)
	for name, rules := range map[string]string{"accounts.css": accounts, "components.css": components} {
		for _, match := range use.FindAllStringSubmatch(rules, -1) {
			if !strings.HasPrefix(match[1], "--comfy-") {
				t.Errorf("%s reads app variable %s outside the token defaults", name, match[1])
			}
		}
		for _, color := range hex.FindAllString(rules, -1) {
			if color != "#0002" { // the menu shadow
				t.Errorf("%s hard-codes color %s", name, color)
			}
		}
		if found := pixelsFont.FindAllString(rules, -1); len(found) > 0 {
			t.Errorf("%s hard-codes font sizes: %v", name, found)
		}
		for _, old := range []string{"light-dark(", "border-radius: 10px", "border-radius: 8px", "ui-sans-serif"} {
			if strings.Contains(rules, old) {
				t.Errorf("%s still contains %q", name, old)
			}
		}
	}
}

func TestDeprecatedVariablesStillFeedTheTokens(t *testing.T) {
	block, _ := tokenBlock(t, asset(t, "components.css"))
	for _, legacy := range []string{"--bg", "--background", "--bg-elev", "--panel", "--paper", "--border", "--line", "--text", "--ink", "--muted", "--accent", "--green", "--danger", "--comfy-ink", "--comfy-panel", "--comfy-line"} {
		if !strings.Contains(block, "var("+legacy+",") && !strings.Contains(block, "var("+legacy+")") {
			t.Errorf("deprecated variable %s is no longer read", legacy)
		}
	}
	if !strings.Contains(block, "deprecated") {
		t.Error("the fallback is not marked deprecated")
	}
}
