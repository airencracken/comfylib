// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tools validates how applications consume Comfylib.
package tools

import (
	"bytes"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

//go:embed app_bootstrap.py
var appBootstrap []byte

//go:embed browser_bootstrap.mjs
var browserBootstrap []byte

var (
	appModule        = regexp.MustCompile(`(?m)^module[ \t]+(github\.com/airencracken/(folkweave|imvault|songstead|tidings|witmoot)|imvault|witmoot)[ \t]*$`)
	replaceDirective = regexp.MustCompile(`(?m)^[ \t]*replace\b`)
	corePin          = regexp.MustCompile(`(?m)^[ \t]*(?:require[ \t]+)?github\.com/airencracken/comfylib[ \t]+v([0-9]+)\.([0-9]+)\.[0-9]+(?:-[\w.-]+)?(?:\s|$)`)
	oidcPin          = regexp.MustCompile(`(?m)^[ \t]*(?:require[ \t]+)?github\.com/airencracken/comfylib/oidc[ \t]+v[0-9]+\.[0-9]+\.[0-9]+(?:-[\w.-]+)?(?:\s|$)`)
	copiedWorkflow   = regexp.MustCompile(`(?m)^\s*(run:|steps:)`)
)

func VerifyConsumer(root string) error {
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	if !appModule.Match(module) {
		return fmt.Errorf("expected an independent Comfyware application module")
	}
	if replaceDirective.Match(module) {
		return fmt.Errorf("application dependencies must resolve without replace directives")
	}
	core := corePin.FindSubmatch(module)
	if core == nil {
		return fmt.Errorf("missing explicit dependency pin: github.com/airencracken/comfylib")
	}
	if !oidcPin.Match(module) {
		return fmt.Errorf("missing explicit dependency pin: github.com/airencracken/comfylib/oidc")
	}
	if err := verifyFiles(root); err != nil {
		return err
	}
	if err := verifyAssets(filepath.Join(root, "internal")); err != nil {
		return err
	}
	if string(core[1]) == "0" && (len(core[2]) == 1 && core[2][0] < '4') {
		return nil
	}
	return VerifyWorkflows(root, "")
}

func verifyFiles(root string) error {
	for _, relative := range []string{
		"internal/suite", "internal/sharedoidc", "scripts/suite-assets.json",
		"scripts/mutate.py", "tools/mutate.py", "scripts/scan_artifacts.py",
		"scripts/test_service_config.py", "scripts/test_suite_deployment.py", "scripts/test_release_dispatch.py",
	} {
		if _, err := os.Stat(filepath.Join(root, relative)); err == nil {
			return fmt.Errorf("shared implementation must come from Comfylib: %s", relative)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	containers, err := filepath.Glob(filepath.Join(root, "scripts", "containers", "*.py"))
	if err != nil {
		return err
	}
	if len(containers) != 0 {
		return fmt.Errorf("container implementations must come from Comfylib")
	}
	launcher, err := os.ReadFile(filepath.Join(root, "scripts", "comfy.py"))
	if err != nil || !bytes.Equal(launcher, appBootstrap) {
		return fmt.Errorf("application tool launcher differs from the shared bootstrap contract")
	}
	browser, err := os.ReadFile(filepath.Join(root, "scripts", "browser-runtime.mjs"))
	if !os.IsNotExist(err) && (err != nil || !bytes.Equal(browser, browserBootstrap)) {
		return fmt.Errorf("browser runtime must use the exact pinned bootstrap")
	}
	return nil
}

func verifyAssets(internal string) error {
	assets := map[string]bool{"components.css": false, "suite.js": false}
	err := filepath.WalkDir(internal, func(path string, file fs.DirEntry, err error) error {
		if err != nil || file.IsDir() {
			return err
		}
		switch file.Name() {
		case "comfy-components.css", "comfy-suite.js":
			return fmt.Errorf("shared browser asset must come from ui.Assets(): %s", file.Name())
		}
		switch filepath.Ext(path) {
		case ".go", ".html":
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if filepath.Ext(path) == ".go" {
				if bytes.Contains(data, []byte(`/internal/suite"`)) || bytes.Contains(data, []byte(`/internal/sharedoidc"`)) {
					return fmt.Errorf("application imports a copied shared implementation")
				}
				return nil
			}
			for name := range assets {
				assets[name] = assets[name] || bytes.Contains(data, []byte("/static/comfyware/"+name))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, name := range []string{"components.css", "suite.js"} {
		if !assets[name] {
			return fmt.Errorf("application templates must load shared asset: %s", name)
		}
	}
	return nil
}

// VerifyWorkflows checks the reusable workflow reference and inputs for each app.
// An empty revision accepts any full Git commit ID.
func VerifyWorkflows(root, revision string) error {
	for name, arguments := range map[string][]string{
		"containers": {"source_ref", "release_tag", "publish"},
		"browser":    {"source_ref"},
	} {
		source, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name+".yml"))
		if err != nil {
			return err
		}
		reference := regexp.MustCompile(`(?m)^\s*uses: airencracken/comfylib/\.github/workflows/` + name + `\.yml@([a-f0-9]{40})(?:\s|$)`)
		references := reference.FindAllSubmatch(source, -1)
		if len(references) != 1 || (revision != "" && string(references[0][1]) != revision) {
			return fmt.Errorf("shared workflow must pin the library revision: %s", name)
		}
		if copiedWorkflow.Match(source) {
			return fmt.Errorf("shared workflow implementation was copied into the app: %s", name)
		}
		for _, argument := range arguments {
			if !bytes.Contains(source, []byte(argument+": ${{ inputs."+argument+" }}")) {
				return fmt.Errorf("shared workflow input is not forwarded: %s", argument)
			}
		}
	}
	return nil
}
