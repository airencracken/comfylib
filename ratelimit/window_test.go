// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindowCapacityPreservesExistingBudgetAndExactExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	w := NewWindow(WindowConfig{Attempts: 2, Interval: time.Minute, Capacity: 2, RejectNew: true, Clock: func() time.Time { return now }})
	for _, key := range []string{"a", "b", "a"} {
		if ok, _ := w.Allow(key); !ok {
			t.Fatal("capacity rejected existing budget", key)
		}
	}
	if ok, wait := w.Allow("a"); ok || wait != time.Minute {
		t.Fatal(ok, wait)
	}
	if ok, wait := w.Allow("c"); ok || wait != time.Minute {
		t.Fatal(ok, wait)
	}
	now = now.Add(time.Minute)
	if ok, _ := w.Allow("c"); !ok || w.Keys() != 1 {
		t.Fatal("exact expiry failed")
	}
}

func TestWindowRefundEvictionAndClockRegression(t *testing.T) {
	now := time.Unix(100, 0)
	w := NewWindow(WindowConfig{Attempts: 1, Interval: time.Minute, Capacity: 2, Clock: func() time.Time { return now }})
	w.Allow("a")
	w.Refund("a")
	w.Refund("a")
	if ok, _ := w.Allow("a"); !ok {
		t.Fatal("refund lost")
	}
	now = now.Add(-time.Hour)
	if ok, wait := w.Allow("a"); ok || wait != time.Minute {
		t.Fatal("clock regression reset budget", ok, wait)
	}
	w.Allow("b")
	w.Allow("c")
	if w.Keys() != 2 {
		t.Fatal("unbounded keys")
	}
	if _, found := w.entries["a"]; found {
		t.Fatal("oldest window retained")
	}
}

func TestWindowConcurrentAtomicityAndRandomCapacity(t *testing.T) {
	w := NewWindow(WindowConfig{Attempts: 20, Interval: time.Hour, Capacity: 32})
	var allowed atomic.Int64
	var group sync.WaitGroup
	for range 100 {
		group.Go(func() {
			if ok, _ := w.Allow("shared"); ok {
				allowed.Add(1)
			}
		})
	}
	group.Wait()
	if allowed.Load() != 20 {
		t.Fatal("non-atomic budget", allowed.Load())
	}
	random := rand.New(rand.NewSource(7))
	for range 10000 {
		w.Allow(fmt.Sprint(random.Intn(1000)))
		if w.Keys() > 32 {
			t.Fatal("capacity exceeded")
		}
	}
	for _, key := range []string{"", "\x00\r\n", "../../../a", string(make([]byte, 100000))} {
		w.Allow(key)
	}
	if w.Keys() > 32 {
		t.Fatal("adversarial keys exceeded capacity")
	}
}

func TestDisabledAndInvalidLimiters(t *testing.T) {
	var w Window
	if ok, _ := w.Allow("a"); !ok {
		t.Fatal("zero window enabled")
	}
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		b := NewBucket(rate, 1, 1, nil)
		if b.Enabled() {
			t.Fatal("invalid rate enabled", rate)
		}
	}
}

func BenchmarkWindowExistingKeyAtCapacity(b *testing.B) {
	w := NewWindow(WindowConfig{Attempts: 1 << 30, Interval: time.Hour, Capacity: 10000})
	for i := range 10000 {
		w.Allow(fmt.Sprint(i))
	}
	b.ResetTimer()
	for range b.N {
		w.Allow("1")
	}
}
