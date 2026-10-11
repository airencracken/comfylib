// SPDX-License-Identifier: AGPL-3.0-or-later

package autobackup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/airencracken/comfylib/sandbox"
)

// Snapshot writes a verified app snapshot to a fresh destination. The callback
// must enforce limit while copying, not merely after completing the copy.
type Snapshot func(ctx context.Context, destination string, limit int64) error

type archiveManifest struct {
	Version int       `json:"version"`
	App     string    `json:"app"`
	Created time.Time `json:"created"`
	Archive string    `json:"archive"`
	Bytes   int64     `json:"bytes"`
	SHA256  string    `json:"sha256"`
}

const stampLayout = "20060102T150405.000000000Z"

// Create publishes a compressed snapshot only after verification and fsync,
// then rotates only this app's completed automatic archives. Manual backups,
// other apps, symlinks and incomplete stages are never removed by rotation.
// Callers must serialize Create calls for the same destination.
func Create(ctx context.Context, c Config, snapshot Snapshot) (output string, err error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if err := privateDirectory(c.Directory); err != nil {
		return "", err
	}
	stagePrefix := "." + c.Name + "-autobackup-stage-"
	stage, err := os.MkdirTemp(c.Directory, stagePrefix)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	data := filepath.Join(stage, "data")
	if err := snapshot(ctx, data, c.CopyMaxBytes); err != nil {
		return "", err
	}
	if err := checkTree(ctx, data, c.CopyMaxBytes); err != nil {
		return "", err
	}
	manifest, err := compress(ctx, data, stage, c.MaxBytes)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(data); err != nil {
		return "", err
	}
	manifest.App, manifest.Version, manifest.Created = c.Name, 1, time.Now().UTC()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err := writePrivate(filepath.Join(stage, "manifest.json"), encoded); err != nil {
		return "", err
	}
	if err := syncDirectory(stage); err != nil {
		return "", err
	}
	name := c.Name + "-" + manifest.Created.Format(stampLayout) + "-" + strings.TrimPrefix(filepath.Base(stage), stagePrefix)
	output = filepath.Join(c.Directory, name)
	if err := os.Rename(stage, output); err != nil {
		return "", err
	}
	if err := syncDirectory(c.Directory); err != nil {
		return output, err
	}
	return output, rotate(ctx, c, name)
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("backup destination must be a private directory, not a symlink")
	}
	return nil
}

func checkTree(ctx context.Context, root string, limit int64) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("snapshot contains a symlink or special file")
		}
		if info.Size() > limit {
			return errors.New("backup exceeds uncompressed copy cap")
		}
		limit -= info.Size()
		return nil
	})
}

type cappedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("backup exceeds archive byte cap")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func compress(ctx context.Context, data, stage string, limit int64) (m archiveManifest, err error) {
	zstd, lookupErr := exec.LookPath("zstd")
	m.Archive = "snapshot.tar.gz"
	if lookupErr == nil {
		m.Archive = "snapshot.tar.zst"
	}
	file, err := os.OpenFile(filepath.Join(stage, m.Archive), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	limited := &cappedWriter{writer: io.MultiWriter(file, hash), remaining: limit}
	if lookupErr == nil {
		err = compressZstd(ctx, zstd, data, limited)
	} else {
		err = compressGzip(ctx, data, limited)
	}
	if err != nil {
		return m, err
	}
	if err := file.Sync(); err != nil {
		return m, err
	}
	m.Bytes, m.SHA256 = limit-limited.remaining, hex.EncodeToString(hash.Sum(nil))
	return m, nil
}

func compressGzip(ctx context.Context, data string, out io.Writer) error {
	w := gzip.NewWriter(out)
	return errors.Join(writeTar(ctx, data, w), w.Close())
}

func compressZstd(ctx context.Context, binary, data string, out io.Writer) error {
	r, w := io.Pipe()
	cmd := exec.CommandContext(ctx, binary, "-q", "-3", "-T1", "-c")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r, out, io.Discard
	done := make(chan error, 1)
	go func() { err := writeTar(ctx, data, w); done <- errors.Join(err, w.CloseWithError(err)) }()
	// RunChild coordinates with the PID-1 reaper in a service sandbox.
	err := sandbox.RunChild(cmd)
	return errors.Join(err, r.CloseWithError(err), <-done)
}

type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cancelReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func writeTar(ctx context.Context, data string, out io.Writer) error {
	w := tar.NewWriter(out)
	err := filepath.WalkDir(data, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == data {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("snapshot contains a symlink or special file")
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		name, err := filepath.Rel(data, path)
		if err != nil {
			return err
		}
		header.Name, header.Uid, header.Gid = filepath.ToSlash(name), 0, 0
		header.Uname, header.Gname = "", ""
		if err := w.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		return copyTarFile(ctx, path, w)
	})
	return errors.Join(err, w.Close())
}

func copyTarFile(ctx context.Context, path string, w io.Writer) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	_, err = io.Copy(w, cancelReader{ctx, f})
	return err
}

func writePrivate(path string, data []byte) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func syncDirectory(path string) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return f.Sync()
}

type retained struct {
	name string
	size int64
}

func rotate(ctx context.Context, c Config, current string) (err error) {
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	entries, err := os.ReadDir(c.Directory)
	if err != nil {
		return err
	}
	var old []retained
	var used int64
	for _, entry := range entries {
		m, err := readManifest(root, entry, c.Name)
		if err != nil {
			continue
		}
		if entry.Name() == current {
			used = m.Bytes
		} else {
			old = append(old, retained{entry.Name(), m.Bytes})
		}
	}
	if used == 0 {
		return errors.New("new backup is not complete; refusing rotation")
	}
	sort.Slice(old, func(i, j int) bool { return old[i].name > old[j].name })
	count := 1
	for _, entry := range old {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count < c.Keep && entry.size <= c.MaxBytes-used {
			used += entry.size
			count++
			continue
		}
		if err := root.RemoveAll(entry.name); err != nil {
			return fmt.Errorf("rotate backup: %w", err)
		}
	}
	return syncDirectory(c.Directory)
}

func readManifest(root *os.Root, entry fs.DirEntry, name string) (m archiveManifest, err error) {
	if !entry.IsDir() || !strings.HasPrefix(entry.Name(), name+"-") {
		return m, errors.New("not an automatic backup")
	}
	parts := strings.Split(strings.TrimPrefix(entry.Name(), name+"-"), "-")
	if len(parts) != 2 {
		return m, errors.New("invalid snapshot name")
	}
	stamp, err := time.Parse(stampLayout, parts[0])
	if err != nil || stamp.Format(stampLayout) != parts[0] {
		return m, errors.New("invalid snapshot timestamp")
	}
	path := filepath.Join(entry.Name(), "manifest.json")
	if _, err := regularFile(root, path, 2048); err != nil {
		return m, err
	}
	f, err := root.Open(path)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	d := json.NewDecoder(io.LimitReader(f, 2048))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	if d.Decode(new(any)) != io.EOF {
		return m, errors.New("invalid snapshot manifest")
	}
	if err := validateManifest(m, name, stamp); err != nil {
		return m, err
	}
	info, err := regularFile(root, filepath.Join(entry.Name(), m.Archive), m.Bytes)
	if err != nil || info.Size() != m.Bytes {
		return m, errors.New("incomplete snapshot archive")
	}
	return m, nil
}

func regularFile(root *os.Root, path string, limit int64) (fs.FileInfo, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid backup file")
	}
	return info, nil
}

func validateManifest(m archiveManifest, name string, stamp time.Time) error {
	if m.Version != 1 || m.App != name || !m.Created.Equal(stamp) || m.Bytes <= 0 {
		return errors.New("invalid snapshot manifest")
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("invalid snapshot checksum")
	}
	if m.Archive != "snapshot.tar.gz" && m.Archive != "snapshot.tar.zst" {
		return errors.New("invalid snapshot archive")
	}
	return nil
}
