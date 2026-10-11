// SPDX-License-Identifier: AGPL-3.0-or-later

// Package autobackup schedules private compressed snapshots. Applications own
// snapshot consistency and verification; this package owns scheduling,
// compression, bounded copies, publication, and retention.
package autobackup

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config applies to automatic snapshots only, never to manual backups.
type Config struct {
	Name         string
	Directory    string
	Interval     time.Duration
	Keep         int
	MaxBytes     int64
	CopyMaxBytes int64
}

// Load reads NAME_BACKUP_INTERVAL (24h; 0 disables), NAME_BACKUP_KEEP (7),
// NAME_BACKUP_MAX_BYTES (10 GiB of retained archives) and
// NAME_BACKUP_COPY_MAX_BYTES (5 GiB of uncompressed snapshot data).
// The destination is always the private backups subdirectory of dataDir.
func Load(name, dataDir string, lookup func(string) string) (Config, error) {
	c := Config{Name: name, Directory: filepath.Join(dataDir, "backups"), Interval: 24 * time.Hour, Keep: 7, MaxBytes: 10 << 30, CopyMaxBytes: 5 << 30}
	prefix := strings.ToUpper(name) + "_BACKUP_"
	var err error
	if value := lookup(prefix + "INTERVAL"); value != "" && value != "0" {
		c.Interval, err = time.ParseDuration(value)
	} else if value == "0" {
		c.Interval = 0
	}
	if err != nil {
		return c, fmt.Errorf("%sINTERVAL: %w", prefix, err)
	}
	for _, entry := range []struct {
		key  string
		dest *int64
	}{{"MAX_BYTES", &c.MaxBytes}, {"COPY_MAX_BYTES", &c.CopyMaxBytes}} {
		if value := lookup(prefix + entry.key); value != "" {
			*entry.dest, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return c, fmt.Errorf("%s%s: %w", prefix, entry.key, err)
			}
		}
	}
	if value := lookup(prefix + "KEEP"); value != "" {
		c.Keep, err = strconv.Atoi(value)
		if err != nil {
			return c, fmt.Errorf("%sKEEP: %w", prefix, err)
		}
	}
	return c, c.Validate()
}

// Validate rejects unbounded retention, overflow and unsafe app names.
func (c Config) Validate() error {
	if c.Name == "" || strings.Trim(c.Name, "abcdefghijklmnopqrstuvwxyz") != "" {
		return errors.New("backup name must contain only lowercase ASCII letters")
	}
	if c.Directory == "" || c.Directory == "." {
		return errors.New("backup directory is required")
	}
	if c.Interval != 0 && (c.Interval < time.Hour || c.Interval > 365*24*time.Hour) {
		return errors.New("backup interval must be 0 or between 1h and 8760h")
	}
	if c.Keep < 1 || c.Keep > 30 || c.MaxBytes <= 0 || c.CopyMaxBytes <= 0 {
		return errors.New("backup keep must be 1..30 and byte caps must be positive")
	}
	return nil
}
