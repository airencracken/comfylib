// SPDX-License-Identifier: AGPL-3.0-or-later
package displaytime

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

type initState struct {
	failure               string
	execs                 int
	version               int64
	committed, rolledBack bool
}
type connector struct{ state *initState }

func (c connector) Connect(context.Context) (driver.Conn, error) { return initConn{c.state}, nil }
func (c connector) Driver() driver.Driver                        { return initDriver{} }

type initDriver struct{}

func (initDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type initConn struct{ s *initState }

func (c initConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepares") }
func (c initConn) Close() error                        { return nil }
func (c initConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c initConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.s.failure == "begin" {
		return nil, errors.New("begin failed")
	}
	return initTx(c), nil
}
func (c initConn) ExecContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	c.s.execs++
	if c.s.failure == "exec" && c.s.execs == 2 {
		return nil, errors.New("schema write failed")
	}
	return driver.RowsAffected(1), nil
}
func (c initConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.s.failure == "query" {
		return nil, errors.New("meta read failed")
	}
	return &initRows{s: c.s}, nil
}

type initTx struct{ s *initState }

func (tx initTx) Commit() error {
	if tx.s.failure == "commit" {
		return errors.New("commit failed")
	}
	tx.s.committed = true
	return nil
}
func (tx initTx) Rollback() error { tx.s.rolledBack = true; return nil }

type initRows struct {
	s    *initState
	done bool
}

func (r *initRows) Columns() []string { return []string{"version"} }
func (r *initRows) Close() error      { return nil }
func (r *initRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	if r.s.failure == "scan" {
		values[0] = "invalid integer"
	} else {
		values[0] = r.s.version
	}
	return nil
}
func TestInitializeRejectsPartialAndFutureNamespaces(t *testing.T) {
	for _, stage := range []string{"", "begin", "exec", "query", "scan", "future", "commit"} {
		t.Run(stage, func(t *testing.T) {
			s := &initState{failure: stage, version: 1}
			if stage == "future" {
				s.version = 99
			}
			db := sql.OpenDB(connector{s})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			err := Initialize(context.Background(), db)
			if stage == "" {
				if err != nil || !s.committed || s.execs != len(schema) {
					t.Fatal("namespace did not commit", s, err)
				}
				return
			}
			if err == nil || s.committed {
				t.Fatal("partial/future namespace accepted", s, err)
			}
			if stage != "begin" && stage != "commit" && !s.rolledBack {
				t.Fatal("failed initialization did not roll back", stage)
			}
			if stage == "future" && !strings.Contains(err.Error(), "99") {
				t.Fatal("lost unknown version", err)
			}
		})
	}
}
