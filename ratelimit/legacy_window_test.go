// SPDX-License-Identifier: AGPL-3.0-or-later
package ratelimit

import (
	"fmt"
	"github.com/airencracken/comfylib/clientip"
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"
)

const rateClients = 10000
const rateAttempts = 20

func newLegacyWindow() *Window {
	return NewWindow(WindowConfig{Attempts: rateAttempts, Interval: 15 * time.Minute, Capacity: rateClients})
}
func spend(l *Window, key string) bool { ok, _ := l.Allow(key); return ok }
func TestFloodedLimiterStillAdmitsNewClients(t *testing.T) {
	l := newLegacyWindow()
	for i := range rateClients + 500 {
		spend(l, clientip.NetworkKey(netip.MustParseAddr(fmt.Sprintf("2001:db8:%x:%x::1", i>>16, i&0xffff))))
	}
	if len(l.entries) > rateClients || l.order.Len() != len(l.entries) {
		t.Fatalf("limiter grew to %d records (%d in order)", len(l.entries), l.order.Len())
	}
	if !spend(l, "198.51.100.7") {
		t.Fatal("a full limiter locked out a new client")
	}
}

func TestExpiredBudgetsAreForgotten(t *testing.T) {
	l := newLegacyWindow()
	for spend(l, "198.51.100.7") {
	}
	l.order.Front().Value.(*windowEntry).until = time.Now().Add(-time.Second)
	if !spend(l, "198.51.100.7") || len(l.entries) != 1 {
		t.Fatal("an expired budget still blocked its client")
	}
}

func TestLimiterPropertiesUnderRandomTraffic(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	for round := range 20 {
		l := newLegacyWindow()
		granted := map[string]int{}
		for range 30000 {
			var client string
			switch random.IntN(3) {
			case 0:
				client = fmt.Sprintf("10.%d.%d.%d", random.IntN(4), random.IntN(256), random.IntN(256))
			case 1:
				client = fmt.Sprintf("2001:db8:%x:%x::%x", random.IntN(8), random.IntN(4096), random.IntN(65536))
			default:
				client = fmt.Sprintf("2001:db8:ffff:1::%x", random.IntN(65536))
			}
			key := clientip.NetworkKey(netip.MustParseAddr(client))
			if random.IntN(10) == 0 {
				l.Refund(key)
				continue
			}
			if spend(l, key) {
				granted[key]++
			}
		}
		if len(l.entries) > rateClients || l.order.Len() != len(l.entries) {
			t.Fatalf("round %d: %d records, %d ordered", round, len(l.entries), l.order.Len())
		}
		for element := l.order.Front(); element != nil; element = element.Next() {
			entry := element.Value.(*windowEntry)
			if l.entries[entry.key] != element || entry.count < 0 || entry.count > rateAttempts {
				t.Fatalf("round %d: inconsistent record %+v", round, entry)
			}
		}
	}
}
