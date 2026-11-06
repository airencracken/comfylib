// SPDX-License-Identifier: AGPL-3.0-or-later

package password

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommonPasswordSources(t *testing.T) {
	secret := "a long private password"
	for _, ending := range []string{"", "\n", "\r\n"} {
		got, err := (Source{Stdin: true}).Read(strings.NewReader(secret+ending), &bytes.Buffer{})
		if err != nil || got != secret {
			t.Fatal(got, err)
		}
	}
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := (Source{File: path}).Read(nil, &bytes.Buffer{})
	if err != nil || got != secret {
		t.Fatal(got, err)
	}
	// #nosec G302 -- Deliberately public fixture must be rejected.
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = (Source{File: path}).Read(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("public password file accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = (Source{File: link}).Read(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("symlink password file accepted")
	}
	for _, source := range []Source{{Prompt: true, Stdin: true}, {Prompt: true, File: path}, {Stdin: true, File: path}, {}} {
		var out bytes.Buffer
		if _, err = source.Read(strings.NewReader(secret), &out); err == nil {
			t.Fatal("conflicting sources or nonterminal prompt accepted", source)
		}
		if out.Len() != 0 {
			t.Fatal("prompt displayed before hiding input")
		}
	}
}
func TestPasswordSourceFlagsAndAdversarialInput(t *testing.T) {
	fs := flag.NewFlagSet("owner", flag.ContinueOnError)
	source := Flags(fs)
	if err := fs.Parse([]string{"--password-stdin"}); err != nil {
		t.Fatal(err)
	}
	if !source.Stdin || source.Prompt || source.File != "" {
		t.Fatal(source)
	}
	for _, value := range []string{"short", strings.Repeat("x", 73), strings.Repeat("x", 1025), "a long password\nsecond", string([]byte{0xff})} {
		if _, err := (Source{Stdin: true}).Read(strings.NewReader(value), &bytes.Buffer{}); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	for count := 12; count <= 24; count++ {
		if err := Validate(strings.Repeat("日", count)); err != nil {
			t.Fatal("valid UTF-8 password rejected", count, err)
		}
	}
	if err := Validate(strings.Repeat("日", 25)); err == nil {
		t.Fatal("bcrypt byte limit exceeded")
	}
}
