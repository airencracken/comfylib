// SPDX-License-Identifier: AGPL-3.0-or-later

package apisurface

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `package sample

// Doc comments are not part of the API.
func Exported(a int, b ...string) (bool, error) { return false, nil }
func unexported() {}

type Public struct {
	// Field doc.
	Name, hidden string
	inner        int
	Embedded
}

type private struct{ Name string }

func (Public) Method() {}
func (p *Public) PointerMethod(x int) {}
func (private) Method() {}

type Iface interface {
	Do() error
	internal()
}

var Typed, Other int = 1, 2

const (
	Answer = 42
	secret = 1
)

var Global, local = 1, 2
`

func TestSurface(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte("package sample\nfunc TestOnly() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Surface(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := `const Answer = 42
func (Public) Method()
func (p *Public) PointerMethod(x int)
func Exported(a int, b ...string) (bool, error)
type Iface interface { Do() error }
type Public struct { Name string; Embedded }
var Global
var Typed, Other int = 1, 2
`
	if got != want {
		t.Fatalf("surface:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiff(t *testing.T) {
	if got := diff("a\nb\n", "a\nc\n"); got != "- b\n+ c\n" {
		t.Fatalf("diff = %q", got)
	}
}
