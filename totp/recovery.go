// SPDX-License-Identifier: AGPL-3.0-or-later

package totp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// RecoveryCodeCount is the number of backup codes issued in one set.
const RecoveryCodeCount = 10

const (
	// Omit characters that are easily misread in printed and copied codes.
	recoveryAlphabet   = "ABCDEFGHJKMNPQRSTVWXYZ23456789"
	recoveryCodeLength = 10
)

// GenerateRecoveryCode returns one grouped code, for example "K7QP2-MX4RT".
//
// Each character is drawn without bias: a random byte is only used when it
// falls below the largest multiple of the alphabet's length, since mapping all
// 256 values with a modulo would make the first few letters slightly likelier.
func GenerateRecoveryCode() (string, error) {
	return recoveryCodeFrom(rand.Reader)
}

// recoveryCodeFrom builds a code from a source of random bytes.
func recoveryCodeFrom(source io.Reader) (string, error) {
	limit := byte(256 - 256%len(recoveryAlphabet))
	var b strings.Builder
	buf := make([]byte, 1)
	for written := 0; written < recoveryCodeLength; {
		if _, err := io.ReadFull(source, buf); err != nil {
			return "", fmt.Errorf("recovery code: %w", err)
		}
		if buf[0] >= limit {
			continue
		}
		if written == recoveryCodeLength/2 {
			b.WriteByte('-')
		}
		b.WriteByte(recoveryAlphabet[int(buf[0])%len(recoveryAlphabet)])
		written++
	}
	return b.String(), nil
}

// IsCode reports whether the input has the shape of a
// six-digit code. Anything else is treated as a recovery code, which is longer
// and carries a separator, so the two can never be confused.
func IsCode(code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// HashRecoveryCode returns the digest stored for a recovery code.
//
// This unkeyed digest preserves compatibility with Imvault recovery codes.
// Generate the codes randomly and keep the persisted digests private.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(NormaliseRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// NormaliseRecoveryCode strips the grouping and case a person may type.
func NormaliseRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}
