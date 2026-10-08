// SPDX-License-Identifier: AGPL-3.0-or-later
package password_test

import (
	"bytes"
	"errors"
	"github.com/airencracken/comfylib/password"
	"io"
	"strings"
	"testing"
	"testing/quick"
)

func TestConfirmationContract(t *testing.T) {
	for _, same := range []bool{true, false} {
		var out bytes.Buffer
		reads := 0
		value, err := password.Confirm(&out, "First: ", "Again: ", func() ([]byte, error) {
			reads++
			if reads == 2 && !same {
				return []byte("different-secret"), nil
			}
			return []byte("a-private-secret"), nil
		})
		if same && (err != nil || value != "a-private-secret") {
			t.Fatal(value, err)
		}
		if !same && (err == nil || value != "") {
			t.Fatal("mismatch returned password")
		}
		if reads != 2 || out.String() != "First: \nAgain: \n" || strings.Contains(out.String(), "secret") {
			t.Fatal("prompt contract", out.String())
		}
	}
}

type failingWriter struct{ remaining int }

func (w *failingWriter) Write(p []byte) (int, error) {
	w.remaining--
	if w.remaining < 0 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}
func TestPromptFailuresReturnNoSecret(t *testing.T) {
	for fail := 0; fail < 4; fail++ {
		value, err := password.Confirm(&failingWriter{fail}, "First", "Again", func() ([]byte, error) { return []byte("secret"), nil })
		if value != "" || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(fail, value, err)
		}
	}
	for fail := 1; fail <= 2; fail++ {
		calls := 0
		var out bytes.Buffer
		value, err := password.Confirm(&out, "First", "Again", func() ([]byte, error) {
			calls++
			if calls == fail {
				return []byte("secret"), io.ErrUnexpectedEOF
			}
			return []byte("secret"), nil
		})
		if value != "" || !errors.Is(err, io.ErrUnexpectedEOF) || calls != fail {
			t.Fatal(fail, value, err)
		}
	}
}
func TestConfirmationProperty(t *testing.T) {
	property := func(first, second string) bool {
		reads := 0
		value, err := password.Confirm(io.Discard, "", "", func() ([]byte, error) {
			reads++
			if reads == 1 {
				return []byte(first), nil
			}
			return []byte(second), nil
		})
		return (first == second && err == nil && value == first) || (first != second && err != nil && value == "")
	}
	if err := quick.Check(property, nil); err != nil {
		t.Fatal(err)
	}
}
