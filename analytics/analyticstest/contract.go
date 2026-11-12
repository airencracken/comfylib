// SPDX-License-Identifier: AGPL-3.0-or-later
// Package analyticstest verifies shared storage against an application's real
// SQLite driver. The core module does not import or require a database driver.
package analyticstest

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/airencracken/comfylib/analytics"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)

// Run exercises schema, deduplication, properties, atomicity, expiry and failure
// behavior. db must be a dedicated empty SQLite database owned by this test.
func Run(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := t.Context()
	db.SetMaxOpenConns(1)
	if err := analytics.Initialize(ctx, db, now); err != nil {
		t.Fatal(err)
	}
	if err := analytics.Initialize(ctx, db, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	concurrentViews(t, db)
	snapshot(t, db)
	atomicity(t, db)
	retention(t, db)
	adversarial(t, db)
	failure(t, db)
}
func exec(t *testing.T, db *sql.DB, q string, args ...any) {
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
func concurrentViews(t *testing.T, db *sql.DB) {
	var group sync.WaitGroup
	for i := 0; i < 50; i++ {
		group.Go(func() {
			if err := analytics.RecordView(t.Context(), db, analytics.Post, "1", "10", now); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if n := total(t, db, "SELECT sum(count) FROM comfy_analytics_activity"); n != 1 {
		t.Fatal("concurrent opens inflated views", n)
	}
	unique := map[string]bool{}
	for i := 0; i < 200; i++ {
		member, item := fmt.Sprint(i%7), fmt.Sprint((i*13)%11)
		unique[member+":"+item] = true
		if err := analytics.RecordView(t.Context(), db, analytics.Image, member, item, now); err != nil {
			t.Fatal(err)
		}
	}
	if n := total(t, db, "SELECT sum(count) FROM comfy_analytics_activity WHERE kind='image'"); n != int64(len(unique)) {
		t.Fatal("deduplication property", n, len(unique))
	}
	exec(t, db, "DELETE FROM comfy_analytics_activity WHERE kind='image'")
}
func snapshot(t *testing.T, db *sql.DB) {
	for i := 0; i < 2; i++ {
		if err := analytics.RecordLogin(t.Context(), db, "1", now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := analytics.RecordView(t.Context(), db, analytics.Post, "2", "10", now); err != nil {
		t.Fatal(err)
	}
	if err := analytics.RecordView(t.Context(), db, analytics.Post, "1", "11", now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err := analytics.Collect(t.Context(), db, "SELECT '1','<script>name</script>' UNION ALL SELECT '2','second'", now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]int64{result.Logins, result.Views, result.ActiveMembers, int64(len(result.Days)), int64(len(result.Popular)), int64(len(result.LoginsByAccount))}, []int64{2, 2, 2, 7, 1, 1}) {
		t.Fatal(result)
	}
	if result.Popular[0].Views != 2 || result.LoginsByAccount[0].Count != 2 || !result.Started.Equal(now) {
		t.Fatal("snapshot", result, err)
	}
	if !result.Days[0].Day.Equal(now.UTC().Truncate(24 * time.Hour).Add(-6 * 24 * time.Hour)) {
		t.Fatal("UTC reporting boundary")
	}
	result, err = analytics.Collect(t.Context(), db, "SELECT '2','second'", now)
	if err != nil || len(result.LoginsByAccount) != 0 {
		t.Fatal("deleted account name exposed", result, err)
	}
}
func atomicity(t *testing.T, db *sql.DB) {
	if err := analytics.RecordViews(t.Context(), db, []analytics.Event{{Kind: analytics.Post, Member: "batch", Item: "first"}, {Kind: "invalid", Member: "batch", Item: "second"}}, now); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if total(t, db, "SELECT count(*) FROM comfy_analytics_activity WHERE member='batch'") != 0 {
		t.Fatal("partial page view counts committed")
	}

	exec(t, db, "CREATE TABLE session_fixture(id INTEGER PRIMARY KEY)")
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(), "INSERT INTO session_fixture VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	if err = analytics.RecordLogin(t.Context(), tx, "aborted", now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if total(t, db, "SELECT count(*) FROM session_fixture") != 0 || total(t, db, "SELECT count(*) FROM comfy_analytics_activity WHERE member='aborted'") != 0 {
		t.Fatal("aborted login was recorded")
	}
}
func retention(t *testing.T, db *sql.DB) {
	for _, age := range []int{89, 90} {
		if err := analytics.RecordView(t.Context(), db, analytics.Post, "expiry", fmt.Sprint(age), now.Add(-time.Duration(age)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := analytics.Prune(t.Context(), db, now); err != nil {
		t.Fatal(err)
	}
	if total(t, db, "SELECT count(*) FROM comfy_analytics_activity WHERE member='expiry'") != 1 || total(t, db, "SELECT count(*) FROM comfy_analytics_activity WHERE member='expiry' AND item='89'") != 1 {
		t.Fatal("retention boundary")
	}
	var a, b, c int
	var detail string
	if err := db.QueryRowContext(t.Context(), "EXPLAIN QUERY PLAN DELETE FROM comfy_analytics_activity WHERE day<?", 0).Scan(&a, &b, &c, &detail); err != nil || !strings.Contains(detail, "INDEX") {
		t.Fatal("expiry requires indexed lookup", detail, err)
	}
}
func adversarial(t *testing.T, db *sql.DB) {
	if err := analytics.RecordLogin(t.Context(), db, "forgotten", now); err != nil {
		t.Fatal(err)
	}
	if err := analytics.ForgetMember(t.Context(), db, "forgotten"); err != nil {
		t.Fatal(err)
	}
	if total(t, db, "SELECT count(*) FROM comfy_analytics_activity WHERE member='forgotten'") != 0 {
		t.Fatal("deleted member activity retained")
	}

	for _, q := range []string{"INSERT INTO comfy_analytics_activity VALUES(1,'unknown','1','1',1,1)", "INSERT INTO comfy_analytics_activity VALUES(1,'post','1','1',0,1)"} {
		if _, err := db.ExecContext(t.Context(), q); err == nil {
			t.Fatal("invalid schema row accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if analytics.RecordView(ctx, db, analytics.Post, "1", "canceled", now) == nil {
		t.Fatal("cancellation ignored")
	}
}
func failure(t *testing.T, db *sql.DB) {
	exec(t, db, "ALTER TABLE comfy_analytics_activity RENAME TO broken_activity")
	result, err := analytics.Collect(t.Context(), db, "SELECT '1','name'", now)
	if err == nil || !reflect.DeepEqual(result, analytics.Summary{}) {
		t.Fatal("partial metrics escaped", result, err)
	}
	exec(t, db, "ALTER TABLE broken_activity RENAME TO comfy_analytics_activity")
}
