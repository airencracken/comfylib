// SPDX-License-Identifier: AGPL-3.0-or-later

// Package token mints opaque secrets and hashes them for storage.
//
// Sessions, reset links, invitations and API keys all follow the same shape: a
// high-entropy string handed to a person or a client, of which only a digest is
// kept. Keeping that in one place means there is one implementation to get
// right, and both apps keep reading the digests they already stored: a token is
// 64 lowercase hex digits and its digest is the hex SHA-256 of that text.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// Size is the number of random bytes in a token from New.
const Size = 32

// New returns a fresh token and the digest to store alongside it.
func New() (token, hash string) {
	random := make([]byte, Size)
	// crypto/rand.Read terminates the process if the system RNG fails, so
	// there is no error to handle.
	_, _ = rand.Read(random)
	token = hex.EncodeToString(random)
	return token, Hash(token)
}

// Hash returns the storage digest for a token: the hex SHA-256 of its text.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Equal reports whether a and b are the same string without revealing, through
// timing, how much of them matched or how long either is. Both are hashed
// first so that even a length mismatch costs the same as a full comparison.
func Equal(a, b string) bool {
	left := sha256.Sum256([]byte(a))
	right := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

// Prefixed is a token shaped "<scheme>_<prefix>_<secret>".
//
// The prefix is stored in the clear and indexed, so verification touches a
// single row instead of every hash; only the digest of the whole string is
// stored, so a database leak yields nothing usable.
type Prefixed struct {
	// Full is shown once and never stored.
	Full string
	// Prefix is the indexed lookup component.
	Prefix string
	// Hash is what gets stored.
	Hash string
}

// Lengths of the two random components. They are fixed, so a presented token
// can be rejected on shape alone before any database work.
const (
	PrefixLen = 12
	SecretLen = 32
)

// alphabet is lowercase base36, which keeps tokens tidy in URLs and safe to
// split on underscores.
const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// NewPrefixed mints a token for a scheme. The scheme must not contain an
// underscore, or SplitPrefixed could never accept the result.
func NewPrefixed(scheme string) Prefixed {
	prefix := randomBase36(PrefixLen)
	full := scheme + "_" + prefix + "_" + randomBase36(SecretLen)
	return Prefixed{Full: full, Prefix: prefix, Hash: Hash(full)}
}

// SplitPrefixed extracts the lookup prefix from a presented token, reporting
// false for anything not shaped like one this scheme could have issued.
func SplitPrefixed(scheme, full string) (string, bool) {
	parts := strings.Split(full, "_")
	if len(parts) != 3 || parts[0] != scheme {
		return "", false
	}
	if !base36(parts[1], PrefixLen) || !base36(parts[2], SecretLen) {
		return "", false
	}
	return parts[1], true
}

// VerifyPrefixed reports whether presented matches the stored digest, in
// constant time.
func VerifyPrefixed(presented, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(presented)), []byte(storedHash)) == 1
}

// randomBase36 returns n characters drawn uniformly from the alphabet.
func randomBase36(n int) string {
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		_, _ = rand.Read(buf) // see New
		for _, b := range buf {
			// 252 is the largest multiple of 36 that fits in a byte;
			// rejecting the rest keeps every character equally likely.
			if b < 252 && len(out) < n {
				out = append(out, alphabet[b%36])
			}
		}
	}
	return string(out)
}

func base36(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for i := 0; i < len(value); i++ {
		if strings.IndexByte(alphabet, value[i]) < 0 {
			return false
		}
	}
	return true
}
