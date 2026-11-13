// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestWindowPeekMatchesAllowWithoutSpending(t *testing.T) {
	now := time.Unix(1700000000, 0)
	w := NewWindow(WindowConfig{Attempts: 3, Interval: time.Minute, Clock: func() time.Time { return now }})
	if limited, wait := w.Peek("a"); limited || wait != 0 || w.Keys() != 0 {
		t.Fatal("unknown key limited or created", limited, wait)
	}
	for i := 0; i < 3; i++ {
		if limited, _ := w.Peek("a"); limited {
			t.Fatal("limited before the budget was spent", i)
		}
		if ok, _ := w.Allow("a"); !ok {
			t.Fatal("Peek spent an attempt", i)
		}
	}
	now = now.Add(20 * time.Second)
	limited, wait := w.Peek("a")
	if _, allowWait := w.Allow("a"); !limited || wait != 40*time.Second || wait != allowWait {
		t.Fatal("peek disagrees with allow", limited, wait, allowWait)
	}
	if limited, _ := w.Peek("b"); limited {
		t.Fatal("one key's state leaked to another")
	}
	now = now.Add(40 * time.Second)
	if limited, _ := w.Peek("a"); limited {
		t.Fatal("expired window still limited")
	}
	var zero *Window
	if limited, _ := zero.Peek("a"); limited {
		t.Fatal("nil window limited")
	}
	if limited, _ := NewWindow(WindowConfig{}).Peek("a"); limited {
		t.Fatal("disabled window limited")
	}
}

func TestLimiterPeekPredictsAllow(t *testing.T) {
	random := rand.New(rand.NewSource(3))
	now := time.Unix(1700000000, 0)
	l := NewBucket(60, 2, 100, func() time.Time { return now })
	for i := 0; i < 2000; i++ {
		key := fmt.Sprint(random.Intn(5))
		now = now.Add(time.Duration(random.Intn(3000)) * time.Millisecond)
		limited, wait := l.Peek(key)
		before := l.Buckets()
		if _, again := l.Peek(key); l.Buckets() != before || again != wait {
			t.Fatal("Peek changed state")
		}
		ok, allowWait := l.Allow(key)
		if ok == limited || wait != allowWait {
			t.Fatal("peek disagrees with allow", i, limited, wait, ok, allowWait)
		}
	}
	var disabled Limiter
	if limited, _ := disabled.Peek("a"); limited {
		t.Fatal("disabled limiter limited")
	}
}
