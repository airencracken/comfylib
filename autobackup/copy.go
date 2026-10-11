// SPDX-License-Identifier: AGPL-3.0-or-later

package autobackup

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
)

// CheckDatabase rejects an oversized live SQLite database before VACUUM INTO.
// Applications must also check the resulting snapshot and bound media copies.
func CheckDatabase(ctx context.Context, db *sql.DB, limit int64) error {
	var pages, size int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return err
	}
	if size <= 0 || pages < 0 || pages > limit/size {
		return errors.New("database exceeds backup copy cap")
	}
	return nil
}

// CopyPrivate copies a bounded regular file to a new private file. It refuses
// symlinks, special files and an existing destination.
func CopyPrivate(ctx context.Context, source, destination string, limit int64) (err error) {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("backup file is not regular or exceeds copy cap")
	}
	r, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	w, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, w.Close()) }()
	_, err = io.Copy(&cappedWriter{w, limit}, cancelReader{ctx, r})
	if err != nil {
		return err
	}
	return w.Sync()
}
