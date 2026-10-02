// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every accepted value survives being quoted again, so the parser cannot
// return something that is not a plain string.
func FuzzParseShellValue(f *testing.F) {
	for _, seed := range []string{"", "/var/lib/app", `"a b" # c`, `'it'\''s'`, `"a\b"`, "$HOME", "a\\", "#x", "\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		value, err := ParseShellValue(raw)
		if err != nil {
			return
		}
		for _, quoted := range []string{singleQuote(value), doubleQuote(value)} {
			if again, err := ParseShellValue(quoted); err != nil || again != value {
				t.Fatalf("%q parsed to %q, which requoted as %q parses to %q, %v", raw, value, quoted, again, err)
			}
		}
		// Nothing the shell would expand can have been accepted unquoted.
		if strings.HasPrefix(raw, "$") || strings.HasPrefix(raw, "`") || strings.HasPrefix(raw, "~") {
			t.Fatalf("%q was accepted", raw)
		}
	})
}

func FuzzSplitConfigWords(f *testing.F) {
	for _, seed := range []string{"", "A=1 B=2", `"A=with space" 'B=x'`, `A=\"`, "\t A=1\t"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		words, err := splitConfigWords(raw)
		if err != nil {
			return
		}
		var quoted []string
		for _, word := range words {
			quoted = append(quoted, singleQuote(word))
		}
		again, err := splitConfigWords(strings.Join(quoted, " "))
		if err != nil || !slices.Equal(again, words) && len(words) > 0 {
			t.Fatalf("%q split to %q, but requoted gives %q, %v", raw, words, again, err)
		}
	})
}

// An OpenRC conf.d file never panics the reader, and a value it reports is
// one ParseShellValue accepts on its own.
func FuzzOpenRCConfig(f *testing.F) {
	for _, seed := range []string{"APP_DATA_DIR=/srv/a\n", "export APP_DATA_DIR='x y'\n#c\n", "APP_DATA_DIR=$X\n", "APP_DATA_DIR=\"a\nb\"\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, contents string) {
		path := filepath.Join(t.TempDir(), "app.confd")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		value, found, err := readShellConfigValue(path, "APP_DATA_DIR")
		if err != nil && (found || value != "") {
			t.Fatalf("error %v came with a value %q", err, value)
		}
		if found && strings.ContainsAny(value, "\n\x00") {
			t.Fatalf("value %q spans lines", value)
		}
		p := Paths{Name: "App", Prefix: "APP_", OpenRCConfig: path, OpenRCInstalled: true, systemdDirs: []string{}}
		if user, group, _, err := p.Account("app"); err == nil && (user == "" || group == "") {
			t.Fatalf("account %q:%q", user, group)
		}
	})
}

// A unit never panics the reader; an account read from it always names a
// user, and a data directory is absolute and clean.
func FuzzSystemdUnit(f *testing.F) {
	for _, seed := range []string{
		"[Service]\nEnvironment=APP_DATA_DIR=/srv/a\nUser=app\n",
		"[Service]\nEnvironment=\"APP_DATA_DIR=/a b\" X=1\nEnvironment=\n",
		"[Unit]\nUser=x\n[Service]\nGroup='g'\n",
		"[Service]\nEnvironmentFile=-/nonexistent/%i\n",
	} {
		f.Add(seed)
	}
	f.Setenv("APP_DATA_DIR", "")
	f.Fuzz(func(t *testing.T, contents string) {
		// EnvironmentFile= could name any host file, including a FIFO or a
		// device that blocks; only the unit itself is under test here.
		if strings.Contains(contents, "EnvironmentFile") {
			return
		}
		p := Paths{Name: "App", Prefix: "APP_", DefaultDataDir: "/var/lib/app", SystemdActive: true, systemdDirs: []string{}}
		p.SystemdUnit = filepath.Join(t.TempDir(), "app.service")
		if err := os.WriteFile(p.SystemdUnit, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if user, _, managed, err := p.Account("app"); err == nil && (user == "" || !managed) {
			t.Fatalf("account %q managed=%t", user, managed)
		}
		if dir, err := p.DataDir("APP_DATA_DIR"); err == nil && (!filepath.IsAbs(dir) || filepath.Clean(dir) != dir) {
			t.Fatalf("data directory %q", dir)
		}
	})
}

// The EnvironmentFile= setting is accepted only as one literal absolute
// path, and the path read is exactly the setting without its "-".
func FuzzEnvironmentFileSetting(f *testing.F) {
	for _, seed := range []string{"/etc/app.env", "-/etc/app.env", "/etc/a b.env", "%h/x", "-", "--/x", "relative", "/x\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, setting string) {
		path, optional, ok := environmentFilePath(setting)
		if !ok {
			return
		}
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "%\x00") {
			t.Fatalf("%q accepted as %q", setting, path)
		}
		want := path
		if optional {
			want = "-" + path
		}
		if want != setting {
			t.Fatalf("%q read as %q (optional=%t)", setting, path, optional)
		}
	})
}

// The contents of an environment file never panic the reader.
func FuzzEnvironmentFile(f *testing.F) {
	for _, seed := range []string{"APP_DATA_DIR=/srv/a\n", "; c\n# c\nAPP_DATA_DIR = ' x '\n", "APP_DATA_DIR=\"unterminated\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, contents string) {
		path := filepath.Join(t.TempDir(), "app.env")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		value, found, err := readEnvironmentFile(path, "/unit", "APP_DATA_DIR")
		if err != nil && (found || value != "") {
			t.Fatalf("error %v came with a value %q", err, value)
		}
		if value != strings.TrimSpace(value) {
			t.Fatalf("value %q was not trimmed", value)
		}
	})
}
