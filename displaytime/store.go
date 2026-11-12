// SPDX-License-Identifier: AGPL-3.0-or-later
package displaytime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var schema = []string{
	`CREATE TABLE IF NOT EXISTS comfy_display_time_meta(id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL)`,
	`INSERT INTO comfy_display_time_meta(id,version) VALUES(1,1) ON CONFLICT(id) DO NOTHING`,
	`CREATE TABLE IF NOT EXISTS comfy_display_timezones(member INTEGER PRIMARY KEY CHECK(member>0), timezone TEXT NOT NULL CHECK(length(timezone) BETWEEN 1 AND 64 AND timezone NOT GLOB '*[^A-Za-z0-9_+/-]*' AND instr(timezone,char(0))=0 AND (timezone='UTC' OR instr(timezone,'/')>1)))`,
}

// Initialize creates an independently versioned namespace for applications
// without a native timezone column. Call only from normal writable startup.
func Initialize(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range schema {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var version int
	if err = tx.QueryRowContext(ctx, "SELECT version FROM comfy_display_time_meta WHERE id=1").Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("unsupported display timezone schema %d", version)
	}
	return tx.Commit()
}

// Read returns UTC for guests and accounts that have not chosen a timezone.
func Read(ctx context.Context, db *sql.DB, id int64) (string, error) {
	if id <= 0 {
		return "UTC", nil
	}
	var name string
	err := db.QueryRowContext(ctx, "SELECT timezone FROM comfy_display_timezones WHERE member=?", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "UTC", nil
	}
	if err != nil {
		return "", err
	}
	return New(name).Name(), nil
}

// ForgetMember removes the preference in the native account-deletion transaction.
func ForgetMember(ctx context.Context, exec Executor, id int64) error {
	_, err := exec.ExecContext(ctx, "DELETE FROM comfy_display_timezones WHERE member=?", id)
	return err
}
