// SPDX-License-Identifier: AGPL-3.0-or-later
// Package displaytimetest verifies timezone preferences using an application's
// actual SQLite driver. Run requires an isolated test database.
package displaytimetest

import (
	"context"
	"database/sql"
	"errors"
	"github.com/airencracken/comfylib/displaytime"
	"testing"
)

const update = `INSERT INTO comfy_display_timezones(member,timezone) SELECT id,? FROM comfy_display_test_users WHERE id=? AND active=1 ON CONFLICT(member) DO UPDATE SET timezone=excluded.timezone`

type fixture struct {
	t   *testing.T
	db  *sql.DB
	ctx context.Context
}

func (f fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f fixture) read(id int64) string {
	f.t.Helper()
	name, err := displaytime.Read(f.ctx, f.db, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return name
}
func (f fixture) save(name string, id int64) {
	f.t.Helper()
	if err := displaytime.Save(f.ctx, f.db, update, id, name); err != nil {
		f.t.Fatal(err)
	}
}

// Run exercises schema constraints, account isolation, validation, rollback,
// cancellation, repeated startup, future schemas and transactional deletion.
func Run(t *testing.T, db *sql.DB) {
	t.Helper()
	f := fixture{t, db, context.Background()}
	if err := displaytime.Initialize(f.ctx, db); err != nil {
		t.Fatal(err)
	}
	f.exec(`CREATE TABLE comfy_display_test_users(id INTEGER PRIMARY KEY,active INTEGER NOT NULL)`)
	f.exec(`INSERT INTO comfy_display_test_users VALUES(301,1),(302,1),(303,0)`)
	for _, test := range []struct {
		name string
		run  func(fixture)
	}{{"isolation", isolation}, {"validation", validation}, {"schema", schema}, {"rollback", rollback}, {"startup", startup}, {"deletion", deletion}} {
		t.Run(test.name, func(t *testing.T) { test.run(fixture{t, db, f.ctx}) })
	}
}
func isolation(f fixture) {
	if f.read(301) != "UTC" || f.read(302) != "UTC" {
		f.t.Fatal("accounts must default to UTC")
	}
	for _, zone := range displaytime.Suggestions() {
		f.save(zone, 301)
		if f.read(301) != zone || f.read(302) != "UTC" {
			f.t.Fatal("account isolation/persistence", zone)
		}
	}
	for _, id := range []int64{303, 999} {
		if err := displaytime.Save(f.ctx, f.db, update, id, "Asia/Tokyo"); !errors.Is(err, sql.ErrNoRows) {
			f.t.Fatal("inactive/unknown account accepted", id, err)
		}
	}
}
func validation(f fixture) {
	before := f.read(301)
	for _, zone := range []string{"", "Local", "../UTC", "Asia/Tokyo\n"} {
		if displaytime.Save(f.ctx, f.db, update, 301, zone) == nil || f.read(301) != before {
			f.t.Fatal("invalid update changed preference", zone)
		}
	}
}
func schema(f fixture) {
	for _, query := range []string{`INSERT INTO comfy_display_timezones VALUES(0,'UTC')`, `INSERT INTO comfy_display_timezones VALUES(-1,'UTC')`, `INSERT INTO comfy_display_timezones VALUES(304,'')`, `INSERT INTO comfy_display_timezones VALUES(304,'../UTC')`, `INSERT INTO comfy_display_timezones VALUES(304,'UTC'||char(0))`} {
		if _, err := f.db.ExecContext(f.ctx, query); err == nil {
			f.t.Fatal("schema accepted invalid row", query)
		}
	}
}
func rollback(f fixture) {
	before := f.read(301)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = displaytime.Save(f.ctx, tx, update, 301, "Asia/Tokyo"); err != nil {
		f.t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		f.t.Fatal(err)
	}
	if f.read(301) != before {
		f.t.Fatal("transaction rollback lost")
	}
	f.exec(`CREATE TRIGGER comfy_timezone_failure BEFORE UPDATE ON comfy_display_timezones BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
	if displaytime.Save(f.ctx, f.db, update, 301, "Asia/Tokyo") == nil || f.read(301) != before {
		f.t.Fatal("failed write changed preference")
	}
	f.exec(`DROP TRIGGER comfy_timezone_failure`)
	cancelled, cancel := context.WithCancel(f.ctx)
	cancel()
	if displaytime.Save(cancelled, f.db, update, 301, "Asia/Tokyo") == nil || f.read(301) != before {
		f.t.Fatal("cancelled update changed preference")
	}
}
func startup(f fixture) {
	before := f.read(301)
	if err := displaytime.Initialize(f.ctx, f.db); err != nil || f.read(301) != before {
		f.t.Fatal("repeated startup reset preference", err)
	}
	f.exec(`UPDATE comfy_display_time_meta SET version=99`)
	if displaytime.Initialize(f.ctx, f.db) == nil {
		f.t.Fatal("accepted future namespace")
	}
	f.exec(`UPDATE comfy_display_time_meta SET version=1`)
}
func deletion(f fixture) {
	before := f.read(301)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = displaytime.ForgetMember(f.ctx, tx, 301); err != nil {
		f.t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil || f.read(301) != before {
		f.t.Fatal("deletion rollback lost", err)
	}
	if err = displaytime.ForgetMember(f.ctx, f.db, 301); err != nil || f.read(301) != "UTC" {
		f.t.Fatal("deleted preference retained", err)
	}
}
