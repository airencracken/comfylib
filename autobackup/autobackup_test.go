// SPDX-License-Identifier: AGPL-3.0-or-later

package autobackup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func config(t *testing.T) Config {
	t.Helper()
	c, err := Load("songstead", t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func snapshot(data []byte) Snapshot {
	return func(ctx context.Context, dest string, limit int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if int64(len(data)) > limit {
			return errors.New("copy limit")
		}
		if err := os.Mkdir(dest, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, "sample.db"), data, 0o600)
	}
}
func manifest(t *testing.T, output string) archiveManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m archiveManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func archives(t *testing.T, c Config) []string {
	t.Helper()
	entries, err := os.ReadDir(c.Directory)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), c.Name+"-") && entry.IsDir() {
			out = append(out, entry.Name())
		}
	}
	return out
}

func TestConfigAdversarial(t *testing.T) {
	c := config(t)
	if c.Interval != 24*time.Hour || c.Keep != 7 || c.MaxBytes != 10<<30 || c.CopyMaxBytes != 5<<30 {
		t.Fatal(c)
	}
	for _, pair := range [][2]string{{"INTERVAL", "garbage"}, {"INTERVAL", "-1h"}, {"INTERVAL", "1m"}, {"INTERVAL", "8761h"}, {"KEEP", "0"}, {"KEEP", "31"}, {"KEEP", "-1"}, {"KEEP", "999999999999999999999"}, {"MAX_BYTES", "0"}, {"MAX_BYTES", "-1"}, {"MAX_BYTES", "9223372036854775808"}, {"COPY_MAX_BYTES", "0"}, {"COPY_MAX_BYTES", "1e6"}} {
		if _, err := Load("witmoot", t.TempDir(), func(key string) string {
			if key == "WITMOOT_BACKUP_"+pair[0] {
				return pair[1]
			}
			return ""
		}); err == nil {
			t.Fatal(pair)
		}
	}
	for _, name := range []string{"", "../songstead", "Songstead", "songstead/", "songstead\x00"} {
		if _, err := Load(name, t.TempDir(), func(string) string { return "" }); err == nil {
			t.Fatal(name)
		}
	}
	c, err := Load("imvault", t.TempDir(), func(key string) string {
		if key == "IMVAULT_BACKUP_INTERVAL" {
			return "0"
		}
		return ""
	})
	if err != nil || c.Interval != 0 {
		t.Fatal(c, err)
	}
}

func TestGzipArchiveRoundTripPrivacyAndChecksum(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := config(t)
	payload := []byte("private song data\x00and UTF-8 music")
	output, err := Create(t.Context(), c, snapshot(payload))
	if err != nil {
		t.Fatal(err)
	}
	m := manifest(t, output)
	if m.Archive != "snapshot.tar.gz" || m.App != c.Name || m.Version != 1 {
		t.Fatal(m)
	}
	encoded, err := os.ReadFile(filepath.Join(output, m.Archive))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if m.Bytes != int64(len(encoded)) || m.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal(m)
	}
	r, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()
	tr := tar.NewReader(r)
	header, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(tr)
	if err != nil || !bytes.Equal(got, payload) || header.Name != "sample.db" || header.Mode != 0o600 || header.Uname != "" {
		t.Fatal(header, err)
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatal("gzip integrity", err)
	}
	for _, path := range []string{c.Directory, output, filepath.Join(output, "manifest.json"), filepath.Join(output, m.Archive)} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatal(path, err)
		}
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 2 {
		t.Fatal("uncompressed staging data retained", entries, err)
	}
}

func TestZstdPreferredAndBrokenCompressorFailsClosed(t *testing.T) {
	binary, err := exec.LookPath("zstd")
	if err != nil {
		t.Skip("install zstd for real compression integration")
	}
	c := config(t)
	output, err := Create(t.Context(), c, snapshot([]byte("zstd payload")))
	if err != nil {
		t.Fatal(err)
	}
	m := manifest(t, output)
	if m.Archive != "snapshot.tar.zst" {
		t.Fatal(m)
	}
	data, err := exec.Command(binary, "-q", "-d", "-c", filepath.Join(output, m.Archive)).Output()
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(data))
	if _, err := tr.Next(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(tr)
	if err != nil || string(got) != "zstd payload" {
		t.Fatal(string(got), err)
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "zstd"), []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bad)
	if _, err := Create(t.Context(), c, snapshot([]byte("failed payload"))); err == nil {
		t.Fatal("broken compressor was ignored")
	}
	if len(archives(t, c)) != 1 {
		t.Fatal("failed backup changed old archives")
	}
}

func TestRetentionCountsByteCapsAndFailureAtomicity(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for keep := 1; keep <= 7; keep++ {
		c := config(t)
		c.Keep = keep
		for i := 0; i < 9; i++ {
			if _, err := Create(t.Context(), c, snapshot([]byte(strings.Repeat("data", i+1)))); err != nil {
				t.Fatal(err)
			}
			if n := len(archives(t, c)); n != min(i+1, keep) {
				t.Fatal(keep, i, n)
			}
		}
		before := strings.Join(archives(t, c), ",")
		failure := func(context.Context, string, int64) error { return errors.New("simulated full disk") }
		if _, err := Create(t.Context(), c, failure); err == nil {
			t.Fatal("failure ignored")
		}
		if strings.Join(archives(t, c), ",") != before {
			t.Fatal("failure rotated backups")
		}
	}
	c := config(t)
	c.MaxBytes = 2800
	rng := rand.New(rand.NewSource(101))
	data := make([]byte, 1000)
	if _, err := rng.Read(data); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := Create(t.Context(), c, snapshot(data)); err != nil {
			t.Fatal(err)
		}
		var used int64
		for _, name := range archives(t, c) {
			used += manifest(t, filepath.Join(c.Directory, name)).Bytes
		}
		if used > c.MaxBytes || len(archives(t, c)) < 1 {
			t.Fatal(used)
		}
	}
	before := strings.Join(archives(t, c), ",")
	c.MaxBytes = 1
	if _, err := Create(t.Context(), c, snapshot(data)); err == nil {
		t.Fatal("archive cap ignored")
	}
	if strings.Join(archives(t, c), ",") != before {
		t.Fatal("oversized backup rotated old ones")
	}
	c.MaxBytes = 2800
	c.CopyMaxBytes = 10
	if _, err := Create(t.Context(), c, snapshot(data)); err == nil {
		t.Fatal("copy cap ignored")
	}
}

func TestRotationLeavesManualForeignSymlinkAndIncomplete(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := config(t)
	c.Keep = 1
	first, err := Create(t.Context(), c, snapshot([]byte("first")))
	if err != nil {
		t.Fatal(err)
	}
	foreign := c
	foreign.Name = "witmoot"
	foreignOutput, err := Create(t.Context(), foreign, snapshot([]byte("foreign")))
	if err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(c.Directory, "manual")
	if err := os.Mkdir(manual, 0o700); err != nil {
		t.Fatal(err)
	}
	incomplete := filepath.Join(c.Directory, c.Name+"-20260101T000000.000000000Z-incomplete")
	if err := os.Mkdir(incomplete, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(c.Directory, c.Name+"-20260101T000000.000000000Z-symlink")
	if err := os.Symlink(first, symlink); err != nil {
		t.Fatal(err)
	}
	latest, err := Create(t.Context(), c, snapshot([]byte("second")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("old backup retained", err)
	}
	for _, path := range []string{latest, manual, foreignOutput, incomplete, symlink} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal(path, err)
		}
	}
}

func TestHostileTreeAndDestination(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := config(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, c.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(t.Context(), c, snapshot([]byte("data"))); err == nil {
		t.Fatal("symlink destination accepted")
	}
	if err := os.Remove(c.Directory); err != nil {
		t.Fatal(err)
	}
	hostile := func(_ context.Context, dest string, _ int64) error {
		if err := os.Mkdir(dest, 0o700); err != nil {
			return err
		}
		return os.Symlink(outside, filepath.Join(dest, "escape"))
	}
	if _, err := Create(t.Context(), c, hostile); err == nil {
		t.Fatal("symlink snapshot accepted")
	}
	c.CopyMaxBytes = 1
	ignoresCap := func(_ context.Context, dest string, _ int64) error {
		return snapshot([]byte("oversized"))(context.Background(), dest, 100)
	}
	if _, err := Create(t.Context(), c, ignoresCap); err == nil {
		t.Fatal("post-copy cap ignored")
	}
}

func TestRunnerPersistenceFailureStatusAndCancellation(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := config(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r, err := New(c, snapshot([]byte("state")), logger)
	if err != nil {
		t.Fatal(err)
	}
	r.attempt(t.Context())
	status := r.Status()
	if status.LastSuccess.IsZero() || status.LastError != "" || status.Running {
		t.Fatal(status)
	}
	loaded, err := New(c, snapshot([]byte("next")), logger)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Status().LastSuccess.Equal(status.LastSuccess) {
		t.Fatal("lost schedule on restart")
	}
	next := loaded.setNext(time.Now())
	if time.Until(next) < 23*time.Hour {
		t.Fatal("restart triggered extra backup", next)
	}
	loaded.snapshot = func(context.Context, string, int64) error { return errors.New("disk full") }
	loaded.attempt(t.Context())
	failed := loaded.Status()
	if failed.LastError != "disk full" || !failed.LastSuccess.Equal(status.LastSuccess) || time.Until(failed.NextAttempt) > 16*time.Minute {
		t.Fatal(failed)
	}
	var called atomic.Int32
	loaded.snapshot = func(ctx context.Context, _ string, _ int64) error { called.Add(1); <-ctx.Done(); return ctx.Err() }
	loaded.state.LastSuccess = time.Time{}
	loaded.startDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loaded.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for called.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker failed to stop")
	}
	if called.Load() != 1 || loaded.Status().Running {
		t.Fatal(called.Load(), loaded.Status())
	}
	c.Interval = 0
	disabled, err := New(c, snapshot(nil), logger)
	if err != nil {
		t.Fatal(err)
	}
	disabled.Run(t.Context())
	if disabled.Status().Enabled {
		t.Fatal("disabled worker ran")
	}
}

func TestStateSchemaAndCopyContracts(t *testing.T) {
	c := config(t)
	if err := privateDirectory(c.Directory); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"version":2}`, `{"version":1,"unknown":0}`, `{"version":1} {}`, strings.Repeat("x", 16385)} {
		if err := os.WriteFile(filepath.Join(c.Directory, stateName), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadState(c.Directory); err == nil {
			t.Fatal(data)
		}
	}
	src := filepath.Join(t.TempDir(), "source")
	dest := filepath.Join(t.TempDir(), "dest")
	if err := os.WriteFile(src, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyPrivate(t.Context(), src, dest, 7); err != nil {
		t.Fatal(err)
	}
	if err := CopyPrivate(t.Context(), src, dest, 7); err == nil {
		t.Fatal("overwrote existing copy")
	}
	if err := CopyPrivate(t.Context(), src, dest+"2", 6); err == nil {
		t.Fatal("ignored copy cap")
	}
	if err := os.Symlink(src, src+"link"); err != nil {
		t.Fatal(err)
	}
	if err := CopyPrivate(t.Context(), src+"link", dest+"3", 7); err == nil {
		t.Fatal("followed symlink")
	}
}

func TestInterruptedStagesCleanedWithoutDeletingManualData(t *testing.T) {
	c := config(t)
	if err := privateDirectory(c.Directory); err != nil {
		t.Fatal(err)
	}
	stage, err := os.MkdirTemp(c.Directory, "."+c.Name+"-autobackup-stage-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "partial"), []byte("partial backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(c.Directory, "."+c.Name+"-autobackup-stage-manual")
	if err := os.Mkdir(manual, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(c.Directory, ".witmoot-autobackup-stage-12345")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(c.Directory, "."+c.Name+"-autobackup-stage-99999")
	if err := os.Symlink(manual, link); err != nil {
		t.Fatal(err)
	}
	if err := cleanupStages(c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("interrupted copy retained", err)
	}
	for _, path := range []string{manual, foreign, link} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("unrelated data removed", path, err)
		}
	}
}
