// SPDX-License-Identifier: AGPL-3.0-or-later

package comfylib_test

import (
	"bytes"
	"flag"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateAPI = flag.Bool("update-api", false, "rewrite testdata/api from the current exported API")

const modulePath = "github.com/airencracken/comfylib"

// Both apps pin an exact comfylib version, but an accidental change to an
// exported name or signature should still be a decision, not a surprise. The
// exported API of every package is recorded under testdata/api; after an
// intended change, run: go test -run TestExportedAPI -update-api .
// Comments are left out, so documentation can change freely.
func TestExportedAPI(t *testing.T) {
	packages := goPackages(t)
	want := map[string]bool{}
	for _, dir := range packages {
		name := strings.ReplaceAll(dir, "/", "_") + ".txt"
		if dir == "." {
			name = "comfylib.txt"
		}
		want[name] = true
		got := exportedAPI(t, dir)
		golden := filepath.Join("testdata", "api", name)
		if *updateAPI {
			if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		recorded, err := os.ReadFile(golden)
		if err != nil {
			t.Errorf("%s has no recorded API (%v); run go test -run TestExportedAPI -update-api .", dir, err)
			continue
		}
		if string(recorded) != got {
			t.Errorf("the exported API of %s changed; if that is intended, run go test -run TestExportedAPI -update-api .\n--- recorded\n%s\n--- now\n%s", dir, recorded, got)
		}
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "api"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !want[entry.Name()] && !*updateAPI {
			t.Errorf("testdata/api/%s records a package that no longer exists", entry.Name())
		}
	}
}

// goPackages lists every directory holding non-test Go files, outside
// testdata and hidden directories.
func goPackages(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// internal packages are not importable outside the module, so they are
		// not part of the API the apps depend on.
		if entry.IsDir() && path != "." && (entry.Name() == "testdata" || entry.Name() == "internal" || strings.HasPrefix(entry.Name(), ".")) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			seen[filepath.ToSlash(filepath.Dir(path))] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

// exportedAPI renders a package's exported declarations without comments or
// function bodies, in go/doc's stable order.
func exportedAPI(t *testing.T, dir string) string {
	t.Helper()
	fset := token.NewFileSet()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	importPath := modulePath
	if dir != "." {
		importPath += "/" + dir
	}
	pkg, err := doc.NewFromFiles(fset, files, importPath)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	out.WriteString("package " + pkg.Name + " // import \"" + importPath + "\"\n")
	write := func(node ast.Node) {
		out.WriteString("\n")
		if err := (&printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}).Fprint(&out, fset, node); err != nil {
			t.Fatal(err)
		}
		out.WriteString("\n")
	}
	values := func(list []*doc.Value) {
		for _, value := range list {
			write(stripComments(value.Decl))
		}
	}
	funcs := func(list []*doc.Func) {
		for _, fn := range list {
			fn.Decl.Body, fn.Decl.Doc = nil, nil
			write(fn.Decl)
		}
	}
	values(pkg.Consts)
	values(pkg.Vars)
	funcs(pkg.Funcs)
	for _, typ := range pkg.Types {
		write(stripComments(typ.Decl))
		values(typ.Consts)
		values(typ.Vars)
		funcs(typ.Funcs)
		funcs(typ.Methods)
	}
	return out.String()
}

// stripComments removes doc and line comments from a declaration so that only
// its shape is compared.
func stripComments(decl *ast.GenDecl) *ast.GenDecl {
	decl.Doc = nil
	ast.Inspect(decl, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Field:
			node.Doc, node.Comment = nil, nil
		case *ast.ValueSpec:
			node.Doc, node.Comment = nil, nil
		case *ast.TypeSpec:
			node.Doc, node.Comment = nil, nil
		}
		return true
	})
	return decl
}
