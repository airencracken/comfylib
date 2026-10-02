// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apisurface lists a package's exported API, one declaration per
// line, so that a test can compare it with a golden file and fail on any
// accidental change. Doc comments and function bodies are left out; only what
// a caller can depend on remains. It parses source rather than running go doc,
// whose output may change between Go releases.
package apisurface

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Surface returns the exported declarations of the package in dir, sorted.
func Surface(dir string) (string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return "", err
	}
	fset := token.NewFileSet()
	var lines []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			return "", err
		}
		for _, decl := range file.Decls {
			declared, err := declarations(fset, decl)
			if err != nil {
				return "", err
			}
			lines = append(lines, declared...)
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n") + "\n", nil
}

func declarations(fset *token.FileSet, decl ast.Decl) ([]string, error) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if !d.Name.IsExported() || d.Recv != nil && !exportedReceiver(d.Recv) {
			return nil, nil
		}
		line, err := render(fset, &ast.FuncDecl{Recv: d.Recv, Name: d.Name, Type: d.Type})
		return []string{line}, err
	case *ast.GenDecl:
		return genDeclarations(fset, d)
	}
	return nil, nil
}

func genDeclarations(fset *token.FileSet, d *ast.GenDecl) ([]string, error) {
	var lines []string
	for _, spec := range d.Specs {
		var node ast.Spec
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			line, err := renderType(fset, d.Tok, s)
			if err != nil {
				return nil, err
			}
			lines = append(lines, line)
			continue
		case *ast.ValueSpec:
			var names []*ast.Ident
			for _, name := range s.Names {
				if name.IsExported() {
					names = append(names, name)
				}
			}
			if len(names) == 0 {
				continue
			}
			// Values are shown only when they all belong to exported
			// names; otherwise they could not be matched up.
			values := s.Values
			if len(names) != len(s.Names) {
				values = nil
			}
			node = &ast.ValueSpec{Names: names, Type: s.Type, Values: values}
		default:
			continue
		}
		line, err := render(fset, &ast.GenDecl{Tok: d.Tok, Specs: []ast.Spec{node}})
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// renderType prints a type declaration. Struct fields and interface methods
// a caller cannot use are dropped, and the rest are separated by "; " so that
// each declaration stays on one line.
func renderType(fset *token.FileSet, tok token.Token, s *ast.TypeSpec) (string, error) {
	var keyword string
	var fields *ast.FieldList
	switch t := s.Type.(type) {
	case *ast.StructType:
		keyword, fields = "struct", t.Fields
	case *ast.InterfaceType:
		keyword, fields = "interface", t.Methods
	default:
		return render(fset, &ast.GenDecl{Tok: tok, Specs: []ast.Spec{&ast.TypeSpec{Name: s.Name, TypeParams: s.TypeParams, Assign: s.Assign, Type: s.Type}}})
	}
	head, err := render(fset, &ast.GenDecl{Tok: tok, Specs: []ast.Spec{&ast.TypeSpec{Name: s.Name, TypeParams: s.TypeParams, Assign: s.Assign, Type: &ast.Ident{Name: keyword}}}})
	if err != nil {
		return "", err
	}
	var members []string
	for _, field := range fields.List {
		var names []string
		for _, name := range field.Names {
			if name.IsExported() {
				names = append(names, name.Name)
			}
		}
		if len(names) == 0 && len(field.Names) > 0 {
			continue
		}
		typ, err := render(fset, field.Type)
		if err != nil {
			return "", err
		}
		member := typ
		switch {
		case len(names) > 0 && keyword == "interface":
			member = names[0] + strings.TrimPrefix(typ, "func")
		case len(names) > 0:
			member = strings.Join(names, ", ") + " " + typ
		}
		if field.Tag != nil {
			member += " " + field.Tag.Value
		}
		members = append(members, member)
	}
	if len(members) == 0 {
		return head + " {}", nil
	}
	return head + " { " + strings.Join(members, "; ") + " }", nil
}

func exportedReceiver(recv *ast.FieldList) bool {
	if len(recv.List) == 0 {
		return false
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if index, ok := t.(*ast.IndexExpr); ok {
		t = index.X
	}
	ident, ok := t.(*ast.Ident)
	return ok && ident.IsExported()
}

// render prints a declaration on one line, collapsing the printer's layout so
// that the golden file changes only when the API does.
func render(fset *token.FileSet, node ast.Node) (string, error) {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, node); err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(b.String()), " "), nil
}

// Check compares the package in the working directory with testdata/api.txt.
// Set COMFYLIB_UPDATE_API=1 to rewrite the golden file after an intended
// change.
func Check(t *testing.T) {
	t.Helper()
	got, err := Surface(".")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "api.txt")
	if os.Getenv("COMFYLIB_UPDATE_API") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run with COMFYLIB_UPDATE_API=1 to create it", err)
	}
	if got != string(want) {
		t.Fatalf("the exported API changed; if that was intended, run with COMFYLIB_UPDATE_API=1 and review the diff.\n%s", diff(string(want), got))
	}
}

// diff lists the lines only in want and only in got.
func diff(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for _, line := range wantLines {
		if !slices.Contains(gotLines, line) {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	for _, line := range gotLines {
		if !slices.Contains(wantLines, line) {
			fmt.Fprintf(&b, "+ %s\n", line)
		}
	}
	return b.String()
}
