// SPDX-License-Identifier: AGPL-3.0-or-later

package fixture

import (
	"os"
	"testing"
)

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("Add(2, 3) != 5")
	}
}

func TestMode(t *testing.T) {
	if os.Getenv("FIXTURE_MODE") != "on" {
		t.Skip("set FIXTURE_MODE=on")
	}
	if Mode() != "on" {
		t.Fatal("Mode() changed")
	}
}
