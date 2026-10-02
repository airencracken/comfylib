// SPDX-License-Identifier: AGPL-3.0-or-later

package token_test

import (
	"fmt"

	"github.com/airencracken/comfylib/token"
)

func Example() {
	// Hand secret to the person; store only digest.
	secret, digest := token.New()

	// Later, the person presents the secret again.
	presented := secret
	fmt.Println(token.Equal(token.Hash(presented), digest))
	// Output: true
}

func ExampleNewPrefixed() {
	key := token.NewPrefixed("imv")

	// The prefix finds the stored row; the digest proves the rest.
	prefix, ok := token.SplitPrefixed("imv", key.Full)
	fmt.Println(ok, prefix == key.Prefix, token.VerifyPrefixed(key.Full, key.Hash))
	// Output: true true true
}
