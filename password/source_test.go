// SPDX-License-Identifier: AGPL-3.0-or-later

package password

import (
	"bytes"
	"errors"
	"flag"
	"math/rand"
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

func TestValidateMeasuresBytesAndExplainsPlainly(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  error
	}{
		{strings.Repeat("a", 11), ErrTooShort},
		{strings.Repeat("a", 12), nil},
		{strings.Repeat("a", 72), nil},
		{strings.Repeat("a", 73), ErrTooLong},
		{strings.Repeat("é", 36), nil},         // 72 bytes, 36 characters
		{strings.Repeat("é", 37), ErrTooLong},  // 74 bytes, well under 72 characters
		{strings.Repeat("日", 11), ErrTooShort}, // 33 bytes but 11 characters
		{strings.Repeat("\U0001F600", 18), nil},     // 72 bytes of emoji
		{strings.Repeat("\U0001F600", 19), ErrTooLong},
		{"valid-start\xff\xfe-more", ErrInvalid},
		{"", ErrTooShort},
	} {
		if err := Validate(tc.value); !errors.Is(err, tc.want) {
			t.Errorf("Validate(%q) = %v, want %v", tc.value, err, tc.want)
		}
	}
	for _, err := range []RuleError{ErrTooShort, ErrTooLong, ErrInvalid} {
		if strings.Contains(err.Error(), "72") || strings.Contains(err.Error(), "UTF-8") || !strings.HasSuffix(err.Error(), ".") {
			t.Errorf("message is not plain: %q", err)
		}
	}
	if !Fits(strings.Repeat("a", MaxBytes)) || Fits(strings.Repeat("é", 37)) {
		t.Fatal("Fits")
	}
}

func TestValidateProperty(t *testing.T) {
	random := rand.New(rand.NewSource(5))
	alphabet := []rune("aZ9 éß日\U0001F600!")
	for i := 0; i < 5000; i++ {
		runes := make([]rune, random.Intn(60))
		for j := range runes {
			runes[j] = alphabet[random.Intn(len(alphabet))]
		}
		value := string(runes)
		ok := len(runes) >= MinCharacters && len(value) <= MaxBytes
		if (Validate(value) == nil) != ok {
			t.Fatalf("Validate(%q) disagrees with the rule", value)
		}
	}
}
