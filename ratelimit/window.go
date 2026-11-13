// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"container/list"
	"sync"
	"time"
)

// WindowConfig defines a process-local fixed-window budget. App policy supplies
// the key and decides whether successful authentication refunds an attempt.
type WindowConfig struct {
	Attempts int
	Interval time.Duration
	Capacity int
	// RejectNew refuses new keys at capacity; existing keys retain their budget.
	// Otherwise the oldest window is evicted, matching the legacy Witmoot policy.
	RejectNew bool
	Clock     func() time.Time
}

// Window keeps expiry-ordered records and prunes only expired entries.
// Concurrent Allow and Refund calls are atomic. A zero Window allows requests.
type Window struct {
	mu      sync.Mutex
	config  WindowConfig
	entries map[string]*list.Element
	order   list.List
	last    time.Time
}

type windowEntry struct {
	key   string
	count int
	until time.Time
}

// NewWindow constructs a bounded fixed-window limiter. Nonpositive attempts or
// interval disable limiting. A nonpositive capacity defaults to 10,000.
func NewWindow(config WindowConfig) *Window {
	if config.Capacity <= 0 {
		config.Capacity = defaultMaxKeys
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Window{config: config, entries: make(map[string]*list.Element)}
}

// Allow consumes one attempt and returns the remaining wait when refused.
func (w *Window) Allow(key string) (bool, time.Duration) {
	if w == nil || w.config.Attempts <= 0 || w.config.Interval <= 0 {
		return true, 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.config.Clock()
	// Clamp clock regression to preserve expiry ordering and avoid extra budgets.
	if now.Before(w.last) {
		now = w.last
	}
	w.last = now
	w.prune(now)
	element := w.entries[key]
	if element == nil {
		if len(w.entries) >= w.config.Capacity {
			if w.config.RejectNew {
				return false, w.order.Front().Value.(*windowEntry).until.Sub(now)
			}
			w.remove(w.order.Front())
		}
		element = w.order.PushBack(&windowEntry{key: key, until: now.Add(w.config.Interval)})
		w.entries[key] = element
	}
	entry := element.Value.(*windowEntry)
	if entry.count >= w.config.Attempts {
		return false, entry.until.Sub(now)
	}
	entry.count++
	return true, 0
}

// Peek reports whether key is currently refused and for how long, without
// spending an attempt. Use it to show one account's state to an administrator.
func (w *Window) Peek(key string) (limited bool, retryAfter time.Duration) {
	if w == nil || w.config.Attempts <= 0 || w.config.Interval <= 0 {
		return false, 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.config.Clock()
	if now.Before(w.last) {
		now = w.last
	}
	element, ok := w.entries[key]
	if !ok {
		return false, 0
	}
	entry := element.Value.(*windowEntry)
	if !entry.until.After(now) || entry.count < w.config.Attempts {
		return false, 0
	}
	return true, entry.until.Sub(now)
}

// Refund returns one spent attempt without extending its original expiry.
func (w *Window) Refund(key string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if element := w.entries[key]; element != nil {
		entry := element.Value.(*windowEntry)
		if entry.count > 0 {
			entry.count--
		}
	}
}

// Keys reports the number of retained windows, bounded by configured capacity.
func (w *Window) Keys() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.entries)
}

func (w *Window) prune(now time.Time) {
	for element := w.order.Front(); element != nil && !element.Value.(*windowEntry).until.After(now); element = w.order.Front() {
		w.remove(element)
	}
}

func (w *Window) remove(element *list.Element) {
	delete(w.entries, element.Value.(*windowEntry).key)
	w.order.Remove(element)
}
