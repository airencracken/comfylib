// SPDX-License-Identifier: AGPL-3.0-or-later
package analytics

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type call struct {
	query string
	args  []any
}
type executor struct {
	calls []call
	err   error
}

func (e *executor) ExecContext(_ context.Context, q string, args ...any) (sql.Result, error) {
	e.calls = append(e.calls, call{q, args})
	return driver.RowsAffected(1), e.err
}
func TestValidationBindingsLabelsAndRetention(t *testing.T) {
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for _, kind := range []Kind{Post, Image, Video, Recommendation, Story, Topic} {
		e := &executor{}
		if err := RecordView(t.Context(), e, kind, "member", "x';--", at); err != nil {
			t.Fatal(err)
		}
		if len(e.calls) != 1 || strings.Contains(e.calls[0].query, "x';--") || e.calls[0].args[3] != "x';--" {
			t.Fatal("unbound input", e.calls)
		}
		if Label(kind) == "Content" {
			t.Fatal(kind)
		}
	}
	for _, id := range []string{"", "\x00", "a\nb", strings.Repeat("a", 129)} {
		e := &executor{}
		if RecordView(t.Context(), e, Post, id, "1", at) == nil || RecordLogin(t.Context(), e, id, at) == nil || len(e.calls) != 0 {
			t.Fatal("invalid identifier reached SQL", id)
		}
	}
	for _, kind := range []Kind{"", "login", "post;drop", "POST"} {
		e := &executor{}
		if RecordView(t.Context(), e, kind, "1", "1", at) == nil || len(e.calls) != 0 {
			t.Fatal(kind)
		}
	}
	if Label("") != "Content" {
		t.Fatal("empty kind")
	}
	e := &executor{}
	if err := Prune(t.Context(), e, at); err != nil {
		t.Fatal(err)
	}
	if e.calls[0].args[0] != day(at)-89 {
		t.Fatal("retention boundary", e.calls)
	}
	e = &executor{err: errors.New("storage failure")}
	if RecordLogin(t.Context(), e, "1", at) == nil {
		t.Fatal("error swallowed")
	}
	if RecordLogin(t.Context(), e, "1", time.Unix(-1, 0)) == nil {
		t.Fatal("negative timestamp")
	}
}

var httpCalls struct {
	sync.Mutex
	n int
}

type httpDriver struct{}
type httpConn struct{}

func (httpDriver) Open(string) (driver.Conn, error)  { return httpConn{}, nil }
func (httpConn) Close() error                        { return nil }
func (httpConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (httpConn) Begin() (driver.Tx, error)           { return httpTx{}, nil }
func (httpConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	httpCalls.Lock()
	defer httpCalls.Unlock()
	httpCalls.n++
	return driver.RowsAffected(1), nil
}
func init() { sql.Register("comfy-analytics-http-test", httpDriver{}) }
func TestTrackRequiresSuccessfulGetAndAuthorizedEvent(t *testing.T) {
	db, err := sql.Open("comfy-analytics-http-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, tc := range []struct {
		method string
		status int
		want   int
	}{{"GET", 200, 1}, {"GET", 403, 0}, {"GET", 404, 0}, {"GET", 500, 0}, {"GET", 304, 0}, {"POST", 200, 0}, {"HEAD", 200, 0}} {
		before := httpCalls.n
		resolves := 0
		h := Track(db, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }, func(*http.Request) []Event { resolves++; return []Event{{Kind: Post, Member: "1", Item: "2"}} })
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(tc.method, "/posts/2", nil))
		if w.Code != tc.status || httpCalls.n-before != tc.want || resolves != tc.want {
			t.Fatal(tc, w.Code, httpCalls.n-before, resolves)
		}
	}
	h := Track(db, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }, func(*http.Request) []Event { return nil })
	before := httpCalls.n
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/public", nil))
	if httpCalls.n != before {
		t.Fatal("guest recorded")
	}
}

type httpTx struct{}

func (httpTx) Commit() error   { return nil }
func (httpTx) Rollback() error { return nil }

func TestSQLiteCountQueriesDeduplicateAndExpire(t *testing.T) {
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	e := &executor{}
	for _, v := range []struct {
		member, item string
		age          int
	}{{"1", "one", 0}, {"1", "one", 0}, {"2", "one", 0}, {"1", "one", 1}, {"1", "two", 89}, {"1", "three", 90}} {
		if err := RecordView(t.Context(), e, Post, v.member, v.item, at.Add(-time.Duration(v.age)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordLogin(t.Context(), e, "1", at); err != nil {
		t.Fatal(err)
	}
	if err := RecordLogin(t.Context(), e, "1", at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := Prune(t.Context(), e, at); err != nil {
		t.Fatal(err)
	}
	queries := make([]map[string]any, 0, len(e.calls))
	for _, c := range e.calls {
		queries = append(queries, map[string]any{"sql": c.query, "args": c.args})
	}
	data, err := json.Marshal(map[string]any{"schema": schema, "queries": queries, "day": day(at), "at": at.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	script := `import json,sqlite3,sys
v=json.load(sys.stdin); c=sqlite3.connect(':memory:'); c.executescript(v['schema'])
for q in v['queries']: c.execute(q['sql'],q['args'])
assert c.execute("SELECT sum(count) FROM comfy_analytics_activity WHERE kind='post'").fetchone()[0]==4
assert c.execute("SELECT sum(count) FROM comfy_analytics_activity WHERE kind='post' AND day>=?",(v['day']-6,)).fetchone()[0]==3
assert c.execute("SELECT sum(count),max(last_at) FROM comfy_analytics_activity WHERE kind='login'").fetchone()==(2,v['at'])
assert c.execute("SELECT count(*) FROM comfy_analytics_activity WHERE item='three'").fetchone()[0]==0
assert c.execute("SELECT count(*) FROM comfy_analytics_activity WHERE item='two'").fetchone()[0]==1
`
	cmd := exec.CommandContext(t.Context(), "python3", "-c", script)
	cmd.Stdin = bytes.NewReader(data)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real SQLite activity contracts: %v\n%s", err, output)
	}
}
func TestNumericContentIDCanonicalization(t *testing.T) {
	for _, raw := range []string{"1", "001", "+1"} {
		if NumericID(raw) != "1" {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"0", "-1", "1.0", "1;drop", "999999999999999999999", ""} {
		if NumericID(raw) != "" {
			t.Fatal(raw)
		}
	}
}

func TestWindowStartUsesTheSameUtcCalendarBoundary(t *testing.T) {
	want := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	for hour := 0; hour < 24; hour++ {
		for _, offset := range []int{-7, 0, 13} {
			at := time.Date(2026, 11, 12, hour, 30, 0, 0, time.UTC).In(time.FixedZone("local", offset*3600))
			got := WindowStart(at)
			if !got.Equal(want) || got.Location() != time.UTC {
				t.Fatal(at, got, want)
			}
		}
	}
}
