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

type collectDriver struct{}
type collectConn struct {
	fail, query int
	readonly    bool
}
type collectTx struct{}
type collectRows struct {
	values []driver.Value
	sent   bool
}

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
	c.readonly = true
	c.query = 0
	return collectTx{}, nil
}
func (c *collectConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.query++
	if c.query == c.fail {
		return nil, errors.New("query failure")
	}
	var values []driver.Value
	switch c.query {
	case 1:
		values = []driver.Value{"1", "admin"}
	case 2:
		values = []driver.Value{int64(1700000000)}
	case 3:
		values = []driver.Value{int64(1700000000 / 86400), int64(2), int64(3)}
	case 4:
		values = []driver.Value{int64(1)}
	case 5:
		values = []driver.Value{"1", int64(2), int64(1700000000)}
	case 6:
		values = []driver.Value{"post", "1", int64(3)}
	default:
		return nil, fmt.Errorf("unexpected query %d", c.query)
	}
	return &collectRows{values: values}, nil
}
func (collectTx) Commit() error   { return nil }
func (collectTx) Rollback() error { return nil }
func (r *collectRows) Columns() []string {
	columns := make([]string, len(r.values))
	for i := range columns {
		columns[i] = fmt.Sprint(i)
	}
	return columns
}
func (*collectRows) Close() error { return nil }
func (r *collectRows) Next(values []driver.Value) error {
	if r.sent {
		return io.EOF
	}
	r.sent = true
	copy(values, r.values)
	return nil
}
func init() { sql.Register("comfy-analytics-collect-test", collectDriver{}) }
func TestCollectReadonlySnapshotFailureAndCancellation(t *testing.T) {
	for failure := 0; failure <= 6; failure++ {
		db, err := sql.Open("comfy-analytics-collect-test", fmt.Sprint(failure))
		if err != nil {
			t.Fatal(err)
		}
		result, err := Collect(t.Context(), db, "accounts", time.Unix(1700000000, 0))
		if failure == 0 {
			if err != nil || result.Logins != 2 || result.Views != 3 || result.ActiveMembers != 1 || len(result.Days) != 7 {
				t.Fatal("snapshot", result, err)
			}
		} else if err == nil || !reflect.DeepEqual(result, Summary{}) {
			t.Fatal("partial results published", failure, result, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("comfy-analytics-collect-test", "0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := Collect(ctx, db, "accounts", time.Now())
	if err == nil || !reflect.DeepEqual(result, Summary{}) {
		t.Fatal("cancellation", result, err)
	}
}
