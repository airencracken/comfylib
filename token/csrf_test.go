// SPDX-License-Identifier: AGPL-3.0-or-later

package token_test

import (
	"encoding/hex"
	"strings"
	"testing"
	"testing/quick"

	"github.com/airencracken/comfylib/token"
)

func TestSessionCSRFCompatibility(t *testing.T) {
	// Independent HMAC-SHA256 vectors for the existing applications' domains.
	for purpose, want := range map[string]string{
		"witmoot-csrf-v1": "d50afcd33e36c9ca4d2005e0be2c1e5209f24fe3de4cdb403754600ea42439ea",
		"imvault-csrf-v1": "5e50f58e8ce9dbbdbb60d09f404f92dce2ac0cc6ef1b5162bf9fe76c76423f4c",
	} {
		if got := token.SessionCSRF(strings.Repeat("a", 64), purpose); got != want {
			t.Fatalf("%s: got %s, want %s", purpose, got, want)
		}
	}
}

func TestSessionCSRFDomains(t *testing.T) {
	if token.SessionCSRF("", "app") != "" || token.SessionCSRF("secret", "") != "" {
		t.Fatal("empty inputs must fail closed")
	}
	check := func(session, purpose string) bool {
		if session == "" || purpose == "" {
			return true
		}
		got := token.SessionCSRF(session, purpose)
		_, err := hex.DecodeString(got)
		return err == nil && len(got) == 64 && got == token.SessionCSRF(session, purpose) &&
			got != token.SessionCSRF(session+"x", purpose) && got != token.SessionCSRF(session, purpose+"x") && got != token.Hash(session)
	}
	if err := quick.Check(check, nil); err != nil {
		t.Fatal(err)
	}
}

func FuzzSessionCSRF(f *testing.F) {
	f.Add("secret", "songstead-csrf-v1")
	f.Add("\x00\xff", "\x00")
	f.Fuzz(func(t *testing.T, session, purpose string) {
		got := token.SessionCSRF(session, purpose)
		if session == "" || purpose == "" {
			if got != "" {
				t.Fatal("empty input accepted")
			}
		} else if len(got) != 64 || got == token.Hash(session) {
			t.Fatal("invalid derived token")
		}
	})
}
