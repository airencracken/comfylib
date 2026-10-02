// SPDX-License-Identifier: AGPL-3.0-or-later

// Package keyfile keeps a random key in a file of its own, creating it on
// first use.
//
// A key file is created once and then trusted for the life of an instance:
// losing it makes everything sealed with it unreadable. So creation must never
// leave a half-written file behind, two processes starting together must agree
// on one key, and a file that is present but wrong is an error to report, not
// something to quietly replace.
package keyfile

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Encoding is how a key is stored on disk.
type Encoding int

const (
	// Raw stores exactly the key bytes and nothing else. Witmoot's image key
	// uses it.
	Raw Encoding = iota + 1
	// Hex stores the key as lowercase hex followed by a newline. Imvault's
	// secret key uses it. Surrounding whitespace is ignored when reading,
	// and standard base64 is accepted too, because imvault has always taken
	// a key pasted in either form.
	Hex
)

// MaxSize bounds the key length, and with it how much of a file is read.
const MaxSize = 1024

// ErrInvalid reports a key file that exists but does not hold a usable key.
// The file is left exactly as it was found.
var ErrInvalid = errors.New("key file does not hold a valid key")

// LoadOrCreate returns the key stored at path, first creating the file with a
// fresh random key of size bytes if nothing is there yet.
//
// A new file is readable only by its owner. It is written and synced under a
// temporary name and then hard-linked into place; a link never replaces an
// existing file, so when several processes race to create the key, exactly one
// wins and the rest read what it wrote. The directory is synced afterwards so
// the new name survives a crash. A missing parent directory is created with
// mode 0700.
func LoadOrCreate(path string, size int, enc Encoding) ([]byte, error) {
	return loadOrCreate(path, size, enc, systemFiles())
}

// Decode checks and decodes the contents of a key file, or a key supplied some
// other way such as an environment variable.
func Decode(data []byte, size int, enc Encoding) ([]byte, error) {
	if err := checkArguments(size, enc); err != nil {
		return nil, err
	}
	switch enc {
	case Raw:
		if len(data) != size {
			return nil, fmt.Errorf("%w: holds %d bytes, want exactly %d", ErrInvalid, len(data), size)
		}
		return bytes.Clone(data), nil
	default:
		text := bytes.TrimSpace(data)
		if key, err := hex.DecodeString(string(text)); err == nil && len(key) == size {
			return key, nil
		}
		if key, err := base64.StdEncoding.DecodeString(string(text)); err == nil && len(key) == size {
			return key, nil
		}
		return nil, fmt.Errorf("%w: want %d bytes encoded as hex or base64", ErrInvalid, size)
	}
}

// files is the handful of file operations creation depends on. Tests replace
// single steps to observe their order and to stop half way, as a crash would.
type files struct {
	// sync makes the temporary file's contents durable before it is linked.
	sync func(*os.File) error
	// link publishes the temporary file under its final name, failing with
	// os.ErrExist rather than replacing anything.
	link func(oldname, newname string) error
	// syncDir makes the new directory entry durable.
	syncDir func(dir string) error
}

func systemFiles() files {
	return files{sync: (*os.File).Sync, link: os.Link, syncDir: syncDirectory}
}

func loadOrCreate(path string, size int, enc Encoding, fs files) ([]byte, error) {
	if err := checkArguments(size, enc); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("keyfile: no path given")
	}
	key, err := load(path, size, enc)
	if !errors.Is(err, os.ErrNotExist) {
		return key, err
	}

	key = make([]byte, size)
	// crypto/rand.Read terminates the process if the system RNG fails.
	_, _ = rand.Read(key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("keyfile: create directory for %s: %w", path, err)
	}
	err = create(path, encode(key, enc), fs)
	if errors.Is(err, os.ErrExist) {
		// Another process got there first and its file is complete,
		// because it was synced before it was linked. Use that key.
		return load(path, size, enc)
	}
	if err != nil {
		return nil, fmt.Errorf("keyfile: create %s: %w", path, err)
	}
	return key, nil
}

// create publishes a complete key file only if none exists. Nobody can ever
// read half a key: until the link, the file has a temporary name.
func create(path string, data []byte, fs files) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		// The temporary name is removed whatever happened; the key now
		// lives under its final name, or nowhere.
		if removeErr := os.Remove(tmp.Name()); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
	}()
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = fs.sync(tmp)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := fs.link(tmp.Name(), path); err != nil {
		return err
	}
	return fs.syncDir(dir)
}

func load(path string, size int, enc Encoding) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		// Still recognisable as os.ErrNotExist, which is what tells
		// loadOrCreate to create the file.
		return nil, fmt.Errorf("keyfile: open %s: %w", path, err)
	}
	// Read one byte more than any valid file can hold, so an oversized file
	// is reported without reading all of it.
	data, err := io.ReadAll(io.LimitReader(file, int64(maxEncodedLen(size, enc))+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("keyfile: read %s: %w", path, err)
	}
	key, err := Decode(data, size, enc)
	if err != nil {
		return nil, fmt.Errorf("keyfile: %s: %w", path, err)
	}
	return key, nil
}

func encode(key []byte, enc Encoding) []byte {
	if enc == Raw {
		return key
	}
	return []byte(hex.EncodeToString(key) + "\n")
}

// maxEncodedLen is the longest file Decode could accept, with generous room
// for whitespace around a text key.
func maxEncodedLen(size int, enc Encoding) int {
	if enc == Raw {
		return size
	}
	return 2*size + 64
}

func checkArguments(size int, enc Encoding) error {
	if size <= 0 || size > MaxSize {
		return fmt.Errorf("keyfile: key size %d is outside 1 to %d bytes", size, MaxSize)
	}
	if enc != Raw && enc != Hex {
		return fmt.Errorf("keyfile: unknown encoding %d", enc)
	}
	return nil
}

func syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = handle.Sync()
	return errors.Join(err, handle.Close())
}
