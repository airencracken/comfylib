// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

type metricDriver struct{}
type metricConn struct{}
type metricTx struct{}
type metricRows struct{ sent bool }

var committed atomic.Int64
var rolledBack atomic.Int64

func (metricDriver) Open(string) (driver.Conn, error)  { return metricConn{}, nil }
func (metricConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (metricConn) Close() error                        { return nil }
func (metricConn) Begin() (driver.Tx, error)           { return metricTx{}, nil }
func (metricConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !options.ReadOnly {
		return nil, errors.New("metrics transaction is not read-only")
	}
	return metricTx{}, nil
}
func (metricConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == "fail" {
		return nil, errors.New("broken query")
	}
	return &metricRows{}, nil
}
func (metricTx) Commit() error        { committed.Add(1); return nil }
func (metricTx) Rollback() error      { rolledBack.Add(1); return nil }
func (*metricRows) Columns() []string { return []string{"total"} }
func (*metricRows) Close() error      { return nil }
func (rows *metricRows) Next(values []driver.Value) error {
	if rows.sent {
		return io.EOF
	}
	rows.sent = true
	values[0] = int64(42)
	return nil
}

func init() { sql.Register("comfy-metrics-contract", metricDriver{}) }

func TestCollectSnapshotAtomicityFailureAndCancellation(t *testing.T) {
	db, err := sql.Open("comfy-metrics-contract", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	before := committed.Load()
	metrics, err := Collect(context.Background(), db, []Query{{Label: "Members", SQL: "count"}, {Label: "Posts", SQL: "count", Detail: "Last 7 days"}})
	if err != nil || len(metrics) != 2 || metrics[0].Value != 42 || committed.Load() != before+1 {
		t.Fatal(metrics, err)
	}
	before = rolledBack.Load()
	metrics, err = Collect(context.Background(), db, []Query{{Label: "Members", SQL: "count"}, {Label: "Posts", SQL: "fail"}})
	if err == nil || metrics != nil || !strings.Contains(err.Error(), "Posts") || rolledBack.Load() != before+1 {
		t.Fatal("partial metrics or missing rollback", metrics, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if metrics, err = Collect(ctx, db, []Query{{SQL: "count"}}); err == nil || metrics != nil {
		t.Fatal("cancelled query succeeded")
	}
}
