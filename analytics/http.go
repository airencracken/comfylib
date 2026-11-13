// SPDX-License-Identifier: AGPL-3.0-or-later
package analytics

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// FlushInterval is how often a Monitor writes its counts, at most.
	FlushInterval = time.Minute
	// maxRoutes bounds the distinct route labels a Monitor holds between
	// flushes; further patterns are counted as "other".
	maxRoutes = 256
	// maxRouteLength matches the column's CHECK constraint.
	maxRouteLength = 128
	// Unmatched labels requests that no ServeMux pattern handled.
	Unmatched = "unmatched"
	// Other labels patterns that are too long, not printable ASCII, or over
	// the route limit.
	Other = "other"
)

type routeKey struct {
	day   int64
	route string
}

// Monitor counts requests per UTC day and ServeMux route pattern in memory and
// writes them to the database at most once per FlushInterval. It is safe for
// concurrent use. Counts not yet flushed are lost if the process exits without
// calling Flush.
type Monitor struct {
	db      *sql.DB
	mu      sync.Mutex
	pending map[routeKey]*Traffic
	last    time.Time
	now     func() time.Time
}

// NewMonitor returns a Monitor that writes to db. Call Initialize first.
func NewMonitor(db *sql.DB) *Monitor {
	return &Monitor{db: db, pending: map[routeKey]*Traffic{}, now: time.Now, last: time.Now()}
}

type response struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *response) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *response) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *response) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

// Middleware counts each request under the pattern the ServeMux matched, read
// from Request.Pattern after next returns. Wrap the *http.ServeMux itself, so
// the mux sets the pattern on the same request. A handler that panics is
// counted as a 500 and the panic continues.
func (m *Monitor) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := m.now()
		out := &response{ResponseWriter: w}
		completed := false
		defer func() {
			status := out.status
			if !completed {
				status = http.StatusInternalServerError
			}
			m.observe(start, routeLabel(r.Pattern), status, out.bytes, m.now().Sub(start))
			if completed {
				m.maybeFlush(r.Context())
			}
		}()
		next.ServeHTTP(out, r)
		completed = true
	})
}

// routeLabel keeps a registered pattern only if it is short printable ASCII.
func routeLabel(pattern string) string {
	if pattern == "" {
		return Unmatched
	}
	if len(pattern) > maxRouteLength {
		return Other
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] < 0x20 || pattern[i] > 0x7e {
			return Other
		}
	}
	return pattern
}

func (m *Monitor) observe(at time.Time, route string, status int, bytes int64, elapsed time.Duration) {
	if status == 0 {
		status = http.StatusOK
	}
	if route == "" {
		route = Other
	}
	key := routeKey{day(at), route}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.pending[key]
	if t == nil {
		if len(m.pending) >= maxRoutes {
			key.route = Other
			t = m.pending[key]
		}
		if t == nil {
			t = &Traffic{}
			m.pending[key] = t
		}
	}
	t.observe(status, bytes, elapsed)
}

func (m *Monitor) maybeFlush(parent context.Context) {
	m.mu.Lock()
	due := m.now().Sub(m.last) >= FlushInterval
	if due {
		m.last = m.now()
	}
	m.mu.Unlock()
	if !due {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Second)
	defer cancel()
	if err := m.Flush(ctx); err != nil {
		slog.WarnContext(ctx, "write request metrics", "error", err)
	}
}

// Flush writes pending counts in one transaction. On failure the counts stay
// pending for the next attempt. Call it during shutdown.
func (m *Monitor) Flush(ctx context.Context) error {
	m.mu.Lock()
	batch := m.pending
	m.pending = map[routeKey]*Traffic{}
	m.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := writeRoutes(ctx, m.db, batch)
	if err != nil {
		m.mu.Lock()
		for key, t := range batch {
			if m.pending[key] == nil {
				m.pending[key] = &Traffic{}
			}
			m.pending[key].add(*t)
		}
		m.mu.Unlock()
	}
	return err
}

var upsertRoute = func() string {
	columns := "day,route,requests,status_2xx,status_3xx,status_4xx,status_5xx,bytes," + latencyColumns
	names := strings.Split(columns, ",")
	updates := make([]string, 0, len(names)-2)
	for _, name := range names[2:] {
		updates = append(updates, name+"="+name+"+excluded."+name)
	}
	return "INSERT INTO comfy_analytics_routes(" + columns + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(names)), ",") +
		") ON CONFLICT(day,route) DO UPDATE SET " + strings.Join(updates, ",")
}()

func writeRoutes(ctx context.Context, db *sql.DB, batch map[routeKey]*Traffic) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for key, t := range batch {
		args := []any{key.day, key.route, t.Requests, t.Status2xx, t.Status3xx, t.Status4xx, t.Status5xx, t.Bytes}
		for _, c := range t.Latency {
			args = append(args, c)
		}
		if _, err = tx.ExecContext(ctx, upsertRoute, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Maintenance prunes expired rows on the first request of each UTC day. It
// starts no goroutine. Initialize also prunes on startup.
func Maintenance(db *sql.DB, next http.Handler) http.Handler {
	var mu sync.Mutex
	var previous int64 = -1
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		today := day(now)
		mu.Lock()
		if today != previous {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			if err := Prune(ctx, db, now); err != nil {
				slog.WarnContext(ctx, "expire activity metrics", "error", err)
			} else {
				previous = today
			}
			cancel()
		}
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
}
