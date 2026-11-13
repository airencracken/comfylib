// SPDX-License-Identifier: AGPL-3.0-or-later

package ui_test

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/ui"
)

func TestSizeUsesPlainDecimalUnits(t *testing.T) {
	for bytes, want := range map[int64]string{
		-5: "0 bytes", 0: "0 bytes", 1: "1 byte", 999: "999 bytes", 1000: "1 kB", 1500: "1.5 kB",
		999_949: "999.9 kB", 999_950: "1 MB", 5_000_000: "5 MB", 2 << 20: "2.1 MB", 2_500_000_000: "2.5 GB",
		math.MaxInt64: "9.2 EB",
	} {
		if got := ui.Size(bytes); got != want {
			t.Errorf("Size(%d) = %q, want %q", bytes, got, want)
		}
	}
}

func TestSizeProperty(t *testing.T) {
	random := rand.New(rand.NewSource(9))
	units := map[string]float64{"kB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15, "EB": 1e18}
	for i := 0; i < 10000; i++ {
		bytes := random.Int63n(int64(1) << uint(random.Intn(62)+1))
		number, unit, _ := strings.Cut(ui.Size(bytes), " ")
		value, err := strconv.ParseFloat(number, 64)
		if err != nil {
			t.Fatal(ui.Size(bytes))
		}
		scale, ok := units[unit]
		if !ok {
			if bytes >= 1000 || (unit != "bytes" && unit != "byte") {
				t.Fatal("unit", bytes, ui.Size(bytes))
			}
			continue
		}
		if value < 1 || value >= 1000 || math.Abs(value*scale-float64(bytes)) > 0.05*scale+1 {
			t.Fatal("value", bytes, ui.Size(bytes))
		}
	}
}
