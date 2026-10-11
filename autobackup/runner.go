// SPDX-License-Identifier: AGPL-3.0-or-later

package autobackup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status is an owner-only operational view. It contains private paths and
// errors and must never be put on a public health endpoint.
type Status struct {
	Enabled      bool
	Directory    string
	Interval     time.Duration
	Keep         int
	MaxBytes     int64
	CopyMaxBytes int64
	Running      bool
	LastAttempt  time.Time
	LastSuccess  time.Time
	LastError    string
	NextAttempt  time.Time
}

// ArchiveCap formats the retained archive limit for an owner page.
func (s Status) ArchiveCap() string { return formatBytes(s.MaxBytes) }

// CopyCap formats the temporary uncompressed copy limit for an owner page.
func (s Status) CopyCap() string { return formatBytes(s.CopyMaxBytes) }
func formatBytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	}
	if n >= 1<<20 {
		return fmt.Sprintf("%.2f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d bytes", n)
}

type savedState struct {
	Version     int       `json:"version"`
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error"`
}

// Runner executes one backup at a time and remembers success across restarts.
// New does not start a goroutine; run Run in an app worker and join it before
// closing the database. Cancellation interrupts copying and compression.
type Runner struct {
	config     Config
	snapshot   Snapshot
	logger     *slog.Logger
	mu         sync.RWMutex
	state      Status
	startDelay time.Duration
	retryDelay time.Duration
}

// New loads private scheduling state without creating the destination. The
// first backup starts one minute after startup; subsequent backups are due
// relative to the last success. Failures retry after fifteen minutes.
func New(c Config, snapshot Snapshot, logger *slog.Logger) (*Runner, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, errors.New("backup snapshot callback is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	r := &Runner{config: c, snapshot: snapshot, logger: logger, startDelay: time.Minute, retryDelay: 15 * time.Minute}
	r.state = Status{Enabled: c.Interval != 0, Directory: c.Directory, Interval: c.Interval, Keep: c.Keep, MaxBytes: c.MaxBytes, CopyMaxBytes: c.CopyMaxBytes}
	if c.Interval != 0 {
		state, err := loadState(c.Directory)
		if err == nil {
			r.state.LastAttempt, r.state.LastSuccess, r.state.LastError = state.LastAttempt, state.LastSuccess, state.LastError
		} else if !errors.Is(err, os.ErrNotExist) {
			r.state.LastError = "Cannot read backup scheduling state: " + err.Error()
			logger.Error("automatic backup state", "error", err)
		}
	}
	return r, nil
}

// Status returns an independent copy safe for concurrent owner requests.
// A nil Runner indicates that this server has no automatic worker attached.
func (r *Runner) Status() Status {
	if r == nil {
		return Status{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *Runner) setNext(now time.Time) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.state.LastSuccess.Add(r.config.Interval)
	// A clock change must not postpone a backup indefinitely.
	if next.Before(now.Add(r.startDelay)) {
		next = now.Add(r.startDelay)
	}
	if next.After(now.Add(r.config.Interval)) {
		next = now.Add(r.config.Interval)
	}
	r.state.NextAttempt = next
	return next
}

// Run blocks until ctx is canceled. Backup failures remain visible and logged
// but do not take down the service. Do not call Run concurrently or twice.
func (r *Runner) Run(ctx context.Context) {
	if r.config.Interval == 0 {
		return
	}
	// The app's single-server lock must already be held. Reserved stages left
	// by an interrupted former worker are removed before another copy begins.
	if err := cleanupStages(r.config); err != nil {
		r.mu.Lock()
		r.state.LastError = err.Error()
		r.mu.Unlock()
		r.logger.Error("automatic backup staging cleanup", "error", err)
	}
	next := r.setNext(time.Now())
	r.logger.Info("automatic backups enabled", "directory", r.config.Directory, "interval", r.config.Interval, "keep", r.config.Keep, "max_bytes", r.config.MaxBytes, "copy_max_bytes", r.config.CopyMaxBytes)
	for {
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		next = r.attempt(ctx)
		if ctx.Err() != nil {
			return
		}
	}
}

func cleanupStages(c Config) (err error) {
	if err := privateDirectory(c.Directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	entries, err := os.ReadDir(c.Directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), "."+c.Name+"-autobackup-stage-")
		if !ok || !entry.IsDir() || suffix == "" || strings.Trim(suffix, "0123456789") != "" {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) attempt(ctx context.Context) time.Time {
	r.mu.Lock()
	r.state.Running, r.state.LastAttempt = true, time.Now().UTC()
	r.mu.Unlock()
	path, err := Create(ctx, r.config, r.snapshot)
	now := time.Now().UTC()
	r.mu.Lock()
	r.state.Running = false
	if err == nil {
		r.state.LastSuccess, r.state.LastError = now, ""
		r.state.NextAttempt = now.Add(r.config.Interval)
	} else {
		r.state.LastError = err.Error()
		r.state.NextAttempt = now.Add(min(r.retryDelay, r.config.Interval))
	}
	saved := savedState{1, r.state.LastAttempt, r.state.LastSuccess, r.state.LastError}
	next := r.state.NextAttempt
	r.mu.Unlock()
	if ctx.Err() != nil {
		return next
	}
	if stateErr := saveState(r.config.Directory, saved); stateErr != nil {
		err = errors.Join(err, stateErr)
		r.mu.Lock()
		r.state.LastError = err.Error()
		r.mu.Unlock()
	}
	if err != nil {
		r.logger.Error("automatic backup failed", "error", err)
	} else {
		r.logger.Info("automatic backup verified", "path", path)
	}
	return next
}

const stateName = ".automatic-backup.json"

func loadState(directory string) (s savedState, err error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return s, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return s, errors.New("backup directory is not private or is a symlink")
	}
	path := filepath.Join(directory, stateName)
	info, err = os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 || info.Mode().Perm()&0o077 != 0 {
		return s, errors.New("invalid backup state file")
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	d := json.NewDecoder(io.LimitReader(f, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return s, err
	}
	if d.Decode(new(any)) != io.EOF || s.Version != 1 || len(s.LastError) > 4096 {
		return s, errors.New("invalid backup state")
	}
	return s, nil
}

func saveState(directory string, state savedState) (err error) {
	if err := privateDirectory(directory); err != nil {
		return err
	}
	if len(state.LastError) > 4096 {
		state.LastError = state.LastError[:4096]
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(directory, ".backup-state-")
	if err != nil {
		return err
	}
	path := f.Name()
	defer func() {
		if removeErr := os.Remove(path); !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(directory, stateName)); err != nil {
		return err
	}
	// Rename removed the temporary name; ENOENT is an expected cleanup result.
	return syncDirectory(directory)
}
