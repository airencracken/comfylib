// SPDX-License-Identifier: AGPL-3.0-or-later
package analytics

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Event is supplied by the application's authorized detail/open route. A topic
// page can supply an event for each post actually rendered on the current page.
type Event struct {
	Kind         Kind
	Member, Item string
}

type response struct {
	http.ResponseWriter
	status int
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
	return w.ResponseWriter.Write(data)
}

// Track records only successful GET responses, after authorization and rendering.
// It must be inside the application's authentication middleware. The resolver
// supplies existing, visible IDs only; never copy arbitrary request IDs into it.
// Collection errors do not change a successful content response.
func Track(db *sql.DB, next http.HandlerFunc, resolve func(*http.Request) []Event) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := &response{ResponseWriter: w}
		next(out, r)
		if r.Method != http.MethodGet || out.status != http.StatusOK {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 250*time.Millisecond)
		defer cancel()
		if err := RecordViews(ctx, db, resolve(r), time.Now()); err != nil {
			slog.WarnContext(ctx, "record authorized content views", "error", err)
		}
	}
}

// RecordViews commits all visible items on a page together, avoiding a separate
// SQLite write transaction for each post. No partial page counts survive errors.
func RecordViews(ctx context.Context, db *sql.DB, events []Event, at time.Time) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, event := range events {
		if err := RecordView(ctx, tx, event.Kind, event.Member, event.Item, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Maintenance prunes expired activity on the first request of each UTC day,
// independently of visiting the admin page. No background goroutine survives
// the application's database lifecycle. Initialize also prunes on startup.
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
