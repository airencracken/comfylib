// SPDX-License-Identifier: AGPL-3.0-or-later

package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SessionCSRF derives a browser CSRF token from a secret session token and an
// application-specific purpose, such as "witmoot-csrf-v1". Keep the session in
// an HttpOnly cookie; expose only the derived token in forms. Preserve the
// purpose across upgrades to keep existing sessions compatible. Empty inputs
// return an empty string and must never be accepted as a valid CSRF token.
func SessionCSRF(session, purpose string) string {
	if session == "" || purpose == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(session))
	mac.Write([]byte(purpose))
	return hex.EncodeToString(mac.Sum(nil))
}
