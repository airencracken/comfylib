// SPDX-License-Identifier: AGPL-3.0-or-later

package totp

import "testing"

func TestRecoveryHashCompatibility(t *testing.T) {
	const original = "dcb8b3996f9738fbbe2f9ca07ea8a34bf27c49611a55c35b4720c5d37faca6a9"
	for _, spelling := range []string{"K7QP2-MX4RT", " k7qp2 mx4rt ", "K7QP2MX4RT"} {
		if got := HashRecoveryCode(spelling); got != original {
			t.Fatalf("old recovery digest changed for %q: %s", spelling, got)
		}
	}
}
