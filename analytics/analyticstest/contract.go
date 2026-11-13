// SPDX-License-Identifier: AGPL-3.0-or-later

// Package analyticstest checks the analytics storage against an application's
// own SQLite driver. The core module does not import a driver.
package analyticstest

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/airencracken/comfylib/analytics"
)

var now = time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)

// Run checks migration, request metrics, security counts, concurrency,
// retention and failure handling. db must be a dedicated, empty SQLite
// database owned by the test.
func Run(t *testing.T, db *sql.DB) {
	t.Helper()
	db.SetMaxOpenConns(1)
	migration(t, db)
	if err := analytics.Initialize(t.Context(), db, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	traffic(t, db)
	security(t, db)
	retention(t, db)
	adversarial(t, db)
	failure(t, db)
}

func run(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func total(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// migration checks that Initialize drops the per-account table kept by
// versions before 0.7.0.
func migration(t *testing.T, db *sql.DB) {
	run(t, db, "CREATE TABLE comfy_analytics_activity(day INTEGER, kind TEXT, member TEXT, item TEXT, count INTEGER, last_at INTEGER)")
	run(t, db, "INSERT INTO comfy_analytics_activity VALUES(1,'login','7','',1,1)")
	if err := analytics.Initialize(t.Context(), db, now); err != nil {
		t.Fatal(err)
	}
	if total(t, db, "SELECT count(*) FROM sqlite_master WHERE name='comfy_analytics_activity'") != 0 {
		t.Fatal("per-account activity table kept")
	}
}

func traffic(t *testing.T, db *sql.DB) {
	monitor := analytics.NewMonitor(db)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "broken" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("item"))
	})
	handler := monitor.Middleware(mux)
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Go(func() {
			for i := 0; i < 25; i++ {
				target := "/items/1?private=query"
				if i%5 == 0 {
					target = "/items/broken"
				}
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
				if i%7 == 0 {
					if err := monitor.Flush(t.Context()); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	group.Wait()
	if err := monitor.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := analytics.Collect(t.Context(), db, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Requests != 200 || result.Status5xx != 40 || result.Bytes != 640 || len(result.Failing) != 1 || result.Failing[0].Pattern != "GET /items/{id}" {
		t.Fatalf("request metrics %+v", result.Traffic)
	}
	if total(t, db, "SELECT count(*) FROM comfy_analytics_routes WHERE route LIKE '%private%' OR route LIKE '%broken%'") != 0 {
		t.Fatal("request detail stored")
	}
}

func security(t *testing.T, db *sql.DB) {
	for _, record := range []func() error{
		func() error { return analytics.RecordFailedSignIn(t.Context(), db, now) },
		func() error { return analytics.RecordFailedSignIn(t.Context(), db, now) },
		func() error { return analytics.RecordRateLimited(t.Context(), db, now) },
		func() error { return analytics.RecordRejectedToken(t.Context(), db, now) },
	} {
		if err := record(); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = analytics.RecordFailedSignIn(t.Context(), tx, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, err := analytics.Collect(t.Context(), db, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Security != (analytics.Security{FailedSignIns: 2, RateLimited: 1, RejectedTokens: 1}) || len(result.Days) != 7 {
		t.Fatalf("security counts %+v", result.Security)
	}
}

func retention(t *testing.T, db *sql.DB) {
	for _, age := range []int{89, 90} {
		if err := analytics.RecordFailedSignIn(t.Context(), db, now.Add(-time.Duration(age)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := analytics.Prune(t.Context(), db, now); err != nil {
		t.Fatal(err)
	}
	oldest := now.Add(-89*24*time.Hour).Unix() / 86400
	if total(t, db, "SELECT count(*) FROM comfy_analytics_security WHERE day<?", oldest) != 0 || total(t, db, "SELECT count(*) FROM comfy_analytics_security WHERE day=?", oldest) != 1 {
		t.Fatal("retention boundary")
	}
	var a, b, c int
	var detail string
	if err := db.QueryRowContext(t.Context(), "EXPLAIN QUERY PLAN DELETE FROM comfy_analytics_routes WHERE day<?", 0).Scan(&a, &b, &c, &detail); err != nil || !strings.Contains(detail, "INDEX") {
		t.Fatal("expiry requires an indexed lookup", detail, err)
	}
}

func adversarial(t *testing.T, db *sql.DB) {
	zeros := strings.TrimSuffix(strings.Repeat("0,", 12), ",")
	for _, q := range []string{
		"INSERT INTO comfy_analytics_routes VALUES(1,'',1,0,0,0,0,0," + zeros + ")",
		"INSERT INTO comfy_analytics_routes VALUES(1,'" + strings.Repeat("x", 129) + "',1,0,0,0,0,0," + zeros + ")",
		"INSERT INTO comfy_analytics_routes VALUES(1,'x',0,0,0,0,0,0," + zeros + ")",
		"INSERT INTO comfy_analytics_routes VALUES(1,'x',1,0,0,0,0,-1," + zeros + ")",
		"INSERT INTO comfy_analytics_security(day,failed_sign_ins) VALUES(1,-1)",
	} {
		if _, err := db.ExecContext(t.Context(), q); err == nil {
			t.Fatal("invalid row accepted", q)
		}
	}
	if analytics.RecordRejectedToken(t.Context(), db, time.Unix(-1, 0)) == nil {
		t.Fatal("negative time accepted")
	}
}

func failure(t *testing.T, db *sql.DB) {
	run(t, db, "ALTER TABLE comfy_analytics_routes RENAME TO broken_routes")
	result, err := analytics.Collect(t.Context(), db, "", now)
	if err == nil || !reflect.DeepEqual(result, analytics.Summary{}) {
		t.Fatal("partial metrics escaped", result, err)
	}
	run(t, db, "ALTER TABLE broken_routes RENAME TO comfy_analytics_routes")
}
