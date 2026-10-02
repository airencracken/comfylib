// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig

import (
	"bytes"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// The quoting forms a conf.d or unit file may use for an arbitrary value.
func singleQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func doubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if strings.IndexByte("\\\"$`", s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// backslashed escapes every character, copying its original bytes so that a
// value that is not UTF-8 is unchanged.
func backslashed(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteByte('\\')
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// randomValue returns bytes from an alphabet heavy in quoting and shell
// metacharacters, with the occasional byte that is not UTF-8. Newlines and NUL
// cannot occur inside one line.
func randomValue(r *rand.Rand, max int) string {
	alphabet := []string{"a", "b", "/", " ", "\t", "'", "\"", "\\", "$", "`", ";", "#", "|", "&", "<", ">", "(", ")", "~", "*", "?", "=", "{", "}", "!", "%", "é", "\xff", "\r"}
	var b strings.Builder
	for range r.IntN(max + 1) {
		b.WriteString(alphabet[r.IntN(len(alphabet))])
	}
	return b.String()
}

func newRand(t *testing.T) *rand.Rand {
	seed := rand.Uint64()
	t.Logf("seed %d", seed)
	return rand.New(rand.NewPCG(seed, 0))
}

func TestParseShellValueRoundTrips(t *testing.T) {
	r := newRand(t)
	for range 5000 {
		value := randomValue(r, 12)
		for _, quoted := range []string{singleQuote(value), doubleQuote(value), singleQuote(value) + " # comment", doubleQuote(value) + "\t#"} {
			if got, err := ParseShellValue(quoted); err != nil || got != value {
				t.Fatalf("ParseShellValue(%q) = %q, %v; want %q", quoted, got, err, value)
			}
		}
		if got, err := ParseShellValue(backslashed(value)); err != nil || got != value {
			t.Fatalf("ParseShellValue(%q) = %q, %v; want %q", backslashed(value), got, err, value)
		}
	}
}

func TestSplitConfigWordsRoundTrips(t *testing.T) {
	r := newRand(t)
	quote := []func(string) string{singleQuote, backslashed, func(s string) string {
		// systemd double quotes take a backslash before any character.
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}}
	for range 5000 {
		words := make([]string, r.IntN(5))
		var line []string
		for i := range words {
			words[i] = randomValue(r, 8)
			form := quote[r.IntN(len(quote))]
			if words[i] == "" {
				// Only quotes can write an empty word.
				form = singleQuote
			}
			line = append(line, form(words[i]))
		}
		separator := []string{" ", "\t", "  \t "}[r.IntN(3)]
		raw := strings.Join(line, separator)
		got, err := splitConfigWords(raw)
		if err != nil || !slices.Equal(got, words) {
			t.Fatalf("splitConfigWords(%q) = %q, %v; want %q", raw, got, err, words)
		}
	}
}

// The parser must never report a value the shell would not assign. Every
// value it accepts is compared with what sh itself assigns; refusals are
// always safe, since the operator is told to set the variable explicitly.
func TestParseShellValueAgreesWithTheShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to compare with")
	}
	r := newRand(t)
	dir := t.TempDir()
	agreed := 0
	for range 3000 {
		raw := randomValue(r, 6)
		if strings.Contains(raw, "\r") {
			continue
		}
		want, err := ParseShellValue(raw)
		if err != nil {
			continue
		}
		// An empty PATH means no command a stray operator might start can
		// be found, and the working directory is a scratch one.
		cmd := exec.Command(sh, "-c", "X="+raw+"\nprintf '%s' \"$X\"")
		cmd.Dir, cmd.Env = dir, []string{"PATH=", "LC_ALL=C"}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil || stdout.String() != want {
			t.Fatalf("for X=%s the parser gives %q but sh gives %q (%v %s)", raw, want, stdout.String(), err, stderr.String())
		}
		agreed++
	}
	if agreed < 300 {
		t.Fatalf("only %d accepted values were compared", agreed)
	}
}

// DataDir always returns an absolute, clean path, whatever the environment
// or configuration holds, or an error.
func TestDataDirIsAlwaysAbsoluteAndClean(t *testing.T) {
	r := newRand(t)
	pieces := []string{"/", "//", ".", "..", "a", "b c", "-x", "%h", "$X", "'q'"}
	for range 500 {
		var b strings.Builder
		for range r.IntN(6) {
			b.WriteString(pieces[r.IntN(len(pieces))])
			if r.IntN(2) == 0 {
				b.WriteByte('/')
			}
		}
		value := b.String()
		t.Setenv("APP_DATA_DIR", value)
		checkAbsoluteClean(t, testPaths(), "environment "+value)
		t.Setenv("APP_DATA_DIR", "")
		p := testPaths()
		p.SystemdUnit = filepath.Join(t.TempDir(), "app.service")
		p.SystemdActive = true
		if err := os.WriteFile(p.SystemdUnit, []byte("[Service]\nEnvironment="+singleQuote("APP_DATA_DIR="+value)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		checkAbsoluteClean(t, p, "unit "+value)
	}
}

func checkAbsoluteClean(t *testing.T, p Paths, source string) {
	t.Helper()
	got, err := p.DataDir("APP_DATA_DIR")
	if err != nil {
		return
	}
	if !filepath.IsAbs(got) || filepath.Clean(got) != got {
		t.Fatalf("%s resolved to %q", source, got)
	}
}
