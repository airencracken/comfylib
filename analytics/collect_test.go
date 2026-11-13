// SPDX-License-Identifier: AGPL-3.0-or-later
package analytics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// collectDriver answers Collect's three queries in order with fixed rows and
// fails the query whose number is the data source name.
type collectDriver struct{}
type collectConn struct{ fail, query int }
type collectTx struct{}
type collectRows struct {
	rows [][]driver.Value
	next int
}

var collectNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func (collectDriver) Open(name string) (driver.Conn, error) {
	n, err := strconv.Atoi(name)
	if err != nil {
		return nil, err
	}
	return &collectConn{fail: n}, nil
}
func (*collectConn) Close() error                        { return nil }
func (*collectConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (*collectConn) Begin() (driver.Tx, error) {
	return nil, errors.New("expected explicit transaction")
}
func (c *collectConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !options.ReadOnly {
		return nil, errors.New("snapshot must be read-only")
	}
	c.query = 0
	return collectTx{}, nil
}

func routeRow(d int64, route string, requests, s2, s4, s5, bytes int64, latency Histogram) []driver.Value {
	row := []driver.Value{d, route, requests, s2, int64(0), s4, s5, bytes}
	for _, c := range latency {
		row = append(row, c)
	}
	return row
}

func (c *collectConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.query++
	if c.query == c.fail {
		return nil, errors.New("query failure")
	}
	today := day(collectNow)
	switch c.query {
	case 1:
		return &collectRows{rows: [][]driver.Value{{int64(1700000000)}}}, nil
	case 2:
		return &collectRows{rows: [][]driver.Value{{today, int64(4), int64(2), int64(1)}, {today - 6, int64(1), int64(0), int64(0)}}}, nil
	case 3:
		fast, slow := Histogram{10}, Histogram{8, 0, 0, 0, 0, 0, 0, 0, 0, 2}
		return &collectRows{rows: [][]driver.Value{
			routeRow(today, "GET /", 10, 10, 0, 0, 1000, fast),
			routeRow(today, "GET /slow", 10, 7, 1, 2, 50, slow),
			routeRow(today-1, "GET /missing", 3, 0, 3, 0, 9, Histogram{3}),
			routeRow(today-1, "GET /", 5, 5, 0, 0, 500, Histogram{5}),
		}}, nil
	}
	return nil, fmt.Errorf("unexpected query %d", c.query)
}
func (collectTx) Commit() error   { return nil }
func (collectTx) Rollback() error { return nil }
func (r *collectRows) Columns() []string {
	columns := make([]string, len(r.rows[0]))
	for i := range columns {
		columns[i] = fmt.Sprint(i)
	}
	return columns
}
func (*collectRows) Close() error { return nil }
func (r *collectRows) Next(values []driver.Value) error {
	if r.next == len(r.rows) {
		return io.EOF
	}
	copy(values, r.rows[r.next])
	r.next++
	return nil
}
func init() { sql.Register("comfy-analytics-collect-test", collectDriver{}) }

func TestCollectTotalsRanksAndFailsWhole(t *testing.T) {
	for failure := 0; failure <= 3; failure++ {
		db, err := sql.Open("comfy-analytics-collect-test", fmt.Sprint(failure))
		if err != nil {
			t.Fatal(err)
		}
		result, err := Collect(t.Context(), db, "", collectNow)
		if failure > 0 {
			if err == nil || !reflect.DeepEqual(result, Summary{}) {
				t.Fatal("partial results published", failure, result, err)
			}
			_ = db.Close()
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Days) != 7 || !result.Days[0].Day.Equal(WindowStart(collectNow)) {
			t.Fatal("window", result.Days)
		}
		if result.Requests != 28 || result.Status5xx != 2 || result.Status4xx != 4 || result.Bytes != 1559 || result.Latency.Count() != 28 {
			t.Fatalf("traffic totals %+v", result.Traffic)
		}
		if result.Security != (Security{FailedSignIns: 5, RateLimited: 2, RejectedTokens: 1}) || result.Days[6].FailedSignIns != 4 || result.Days[0].FailedSignIns != 1 {
			t.Fatalf("security totals %+v", result.Security)
		}
		if result.Days[6].Requests != 20 || result.Days[5].Requests != 8 {
			t.Fatal("daily traffic", result.Days[5], result.Days[6])
		}
		if len(result.Slowest) != 3 || result.Slowest[0].Pattern != "GET /slow" || result.Slowest[1].Pattern != "GET /" || result.Slowest[1].Requests != 15 {
			t.Fatal("slowest routes", result.Slowest)
		}
		if len(result.Failing) != 2 || result.Failing[0].Pattern != "GET /slow" || result.Failing[1].Pattern != "GET /missing" {
			t.Fatal("failing routes", result.Failing)
		}
		if result.Logins != 0 || result.ActiveMembers != 0 || result.Views != 0 || result.LoginsByAccount != nil || result.Popular != nil {
			t.Fatal("deprecated fields filled")
		}
		_ = db.Close()
	}
	db, err := sql.Open("comfy-analytics-collect-test", "0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := Collect(ctx, db, "", collectNow); err == nil || !reflect.DeepEqual(result, Summary{}) {
		t.Fatal("cancellation", result, err)
	}
}

func TestRankRoutesBoundsLists(t *testing.T) {
	routes := map[string]*Traffic{}
	for i := 0; i < 20; i++ {
		traffic := &Traffic{Requests: int64(i + 1), Status5xx: int64(i % 3)}
		traffic.Latency[i%len(traffic.Latency)] = int64(i + 1)
		routes[fmt.Sprint("GET /r", i)] = traffic
	}
	slowest, failing := rankRoutes(routes)
	if len(slowest) != RouteLimit || len(failing) != RouteLimit {
		t.Fatal(len(slowest), len(failing))
	}
	for i := 1; i < len(slowest); i++ {
		a, _ := slowest[i-1].Latency.Percentile(.95)
		b, _ := slowest[i].Latency.Percentile(.95)
		if a < b {
			t.Fatal("slowest not ordered")
		}
	}
	for i := 1; i < len(failing); i++ {
		if failing[i-1].Status5xx < failing[i].Status5xx {
			t.Fatal("failing not ordered")
		}
	}
	slowest, failing = rankRoutes(map[string]*Traffic{"GET /ok": {Requests: 1, Status2xx: 1}})
	if len(slowest) != 1 || len(failing) != 0 {
		t.Fatal("healthy route listed as failing")
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
