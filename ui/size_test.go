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

func TestSizeUsesBinaryUnits(t *testing.T) {
	for bytes, want := range map[int64]string{
		-5: "0 bytes", 0: "0 bytes", 1: "1 byte", 1023: "1023 bytes", 1024: "1 KiB", 1536: "1.5 KiB",
		1_048_524: "1023.9 KiB", 1_048_525: "1 MiB", 5 << 20: "5 MiB", 5_000_000: "4.8 MiB",
		2 << 20: "2 MiB", 5 << 29: "2.5 GiB", math.MaxInt64: "8 EiB",
	} {
		if got := ui.Size(bytes); got != want {
			t.Errorf("Size(%d) = %q, want %q", bytes, got, want)
		}
	}
}

func TestSizeProperty(t *testing.T) {
	random := rand.New(rand.NewSource(9))
	units := map[string]float64{"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "PiB": 1 << 50, "EiB": 1 << 60}
	for i := 0; i < 10000; i++ {
		bytes := random.Int63n(int64(1) << uint(random.Intn(62)+1))
		number, unit, _ := strings.Cut(ui.Size(bytes), " ")
		value, err := strconv.ParseFloat(number, 64)
		if err != nil {
			t.Fatal(ui.Size(bytes))
		}
		scale, ok := units[unit]
		if !ok {
			if bytes >= 1024 || (unit != "bytes" && unit != "byte") {
				t.Fatal("unit", bytes, ui.Size(bytes))
			}
			continue
		}
		if value < 1 || value >= 1024 || math.Abs(value*scale-float64(bytes)) > 0.05*scale+1 {
			t.Fatal("value", bytes, ui.Size(bytes))
		}
	}
}
