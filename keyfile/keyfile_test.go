// SPDX-License-Identifier: AGPL-3.0-or-later

package keyfile

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/quick"
	"time"
)

// Child processes are this test binary run again with KEYFILE_TEST_CHILD set.
// They act as separate servers starting at once, or as one that crashes.
const childEnv = "KEYFILE_TEST_CHILD"

func TestMain(m *testing.M) {
	switch os.Getenv(childEnv) {
	case "":
		os.Exit(m.Run())
	case "load":
		os.Exit(childLoad())
	case "crash":
		os.Exit(childCrash())
	default:
		fmt.Fprintln(os.Stderr, "unknown child mode")
		os.Exit(2)
	}
}

// childLoad waits for a shared start time so that every child races, then
// prints the key it ended up with.
func childLoad() int {
	start, err := strconv.ParseInt(os.Getenv("KEYFILE_TEST_START"), 10, 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for time.Now().UnixNano() < start {
		// Spin rather than sleep, so all children leave the barrier together.
	}
	key, err := LoadOrCreate(os.Getenv("KEYFILE_TEST_PATH"), 32, Hex)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(hex.EncodeToString(key))
	return 0
}

// childCrash dies, without running any deferred cleanup, after the key has
// been written and synced but before it is linked into place.
func childCrash() int {
	fs := systemFiles()
	fs.link = func(string, string) error {
		os.Exit(3)
		return nil
	}
	_, err := loadOrCreate(os.Getenv("KEYFILE_TEST_PATH"), 32, Hex, fs)
	fmt.Fprintln(os.Stderr, "the crash point was never reached:", err)
	return 1
}

func TestKeyFileIsCreatedOnceAndReused(t *testing.T) {
	for _, enc := range []Encoding{Raw, Hex} {
		t.Run(encodingName(enc), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nested", "deeper", "secret.key")
			first, err := LoadOrCreate(path, 32, enc)
			if err != nil {
				t.Fatal(err)
			}
			if len(first) != 32 || bytes.Equal(first, make([]byte, 32)) {
				t.Fatalf("key = %x", first)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			// Only the account running the server may read the key.
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("key file mode = %o, want 600", perm)
			}
			dir, err := os.Stat(filepath.Dir(path))
			if err != nil || dir.Mode().Perm() != 0o700 {
				t.Errorf("created directory mode = %v, %v; want 700", dir.Mode().Perm(), err)
			}
			second, err := LoadOrCreate(path, 32, enc)
			if err != nil || !bytes.Equal(first, second) {
				t.Fatalf("the key changed between loads: %x, %x, %v", first, second, err)
			}
		})
	}
}

// Both apps already have key files on disk. They must load exactly as before.
func TestExistingOnDiskFormatsLoadUnchanged(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 7)
	}
	for _, tc := range []struct {
		name     string
		enc      Encoding
		contents []byte
	}{
		{"witmoot raw image key", Raw, key},
		{"raw key that looks like whitespace", Raw, bytes.Repeat([]byte{'\n'}, 32)},
		{"imvault hex secret key", Hex, []byte(hex.EncodeToString(key) + "\n")},
		{"hex without newline", Hex, []byte(hex.EncodeToString(key))},
		{"hex with CRLF and spaces", Hex, []byte("  " + hex.EncodeToString(key) + "\r\n")},
		{"uppercase hex", Hex, []byte(strings.ToUpper(hex.EncodeToString(key)) + "\n")},
		{"base64 pasted by hand", Hex, []byte(base64.StdEncoding.EncodeToString(key) + "\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "existing.key")
			if err := os.WriteFile(path, tc.contents, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadOrCreate(path, 32, tc.enc)
			if err != nil {
				t.Fatal(err)
			}
			want := key
			if tc.name == "raw key that looks like whitespace" {
				want = tc.contents
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("loaded %x, want %x", got, want)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, tc.contents) {
				t.Fatal("loading rewrote the file")
			}
		})
	}
}

func TestNewFilesUseTheRequestedEncoding(t *testing.T) {
	dir := t.TempDir()
	raw, err := LoadOrCreate(filepath.Join(dir, "raw.key"), 32, Raw)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "raw.key")); err != nil || !bytes.Equal(data, raw) {
		t.Fatalf("raw file holds %q, want exactly the key bytes", data)
	}
	text, err := LoadOrCreate(filepath.Join(dir, "hex.key"), 32, Hex)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "hex.key")); err != nil || string(data) != hex.EncodeToString(text)+"\n" {
		t.Fatalf("hex file holds %q, want lowercase hex and a newline", data)
	}
}

// A key file that is present but wrong must be reported and left alone:
// replacing it would make everything sealed with the old key unreadable.
func TestInvalidKeyFilesAreReportedAndNeverReplaced(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, 32)
	for _, tc := range []struct {
		name     string
		enc      Encoding
		contents []byte
	}{
		{"empty raw", Raw, nil},
		{"short raw from a crash", Raw, key[:5]},
		{"raw with trailing newline", Raw, append(bytes.Clone(key), '\n')},
		{"long raw", Raw, append(bytes.Clone(key), key...)},
		{"empty hex", Hex, nil},
		{"whitespace only", Hex, []byte(" \n\t")},
		{"not a key", Hex, []byte("not a key\n")},
		{"short hex", Hex, []byte(hex.EncodeToString(key[:16]) + "\n")},
		{"long hex", Hex, []byte(hex.EncodeToString(append(bytes.Clone(key), 1)) + "\n")},
		{"odd hex", Hex, []byte(hex.EncodeToString(key)[1:] + "\n")},
		{"hex with interior space", Hex, []byte(hex.EncodeToString(key)[:10] + " " + hex.EncodeToString(key)[10:])},
		{"raw bytes in a hex file", Hex, key},
		{"short base64", Hex, []byte(base64.StdEncoding.EncodeToString(key[:31]))},
		{"url base64", Hex, []byte(base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 32)))},
		{"NUL padding", Hex, append([]byte(hex.EncodeToString(key)), 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "corrupt.key")
			if err := os.WriteFile(path, tc.contents, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadOrCreate(path, 32, tc.enc)
			if !errors.Is(err, ErrInvalid) || got != nil {
				t.Fatalf("LoadOrCreate = %x, %v; want ErrInvalid", got, err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the error does not name the file: %v", err)
			}
			if strings.Contains(err.Error(), hex.EncodeToString(key[:4])) {
				t.Errorf("the error repeats key material: %v", err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(after, tc.contents) {
				t.Fatal("an invalid key file was rewritten")
			}
		})
	}
}

// An oversized key file is rejected after reading only a little more than a
// valid one could hold. A FIFO stands in for a huge file: the writer counts
// what it managed to hand over before the reader stopped listening.
func TestOversizedFilesAreNotReadWhole(t *testing.T) {
	for _, enc := range []Encoding{Raw, Hex} {
		t.Run(encodingName(enc), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "huge.key")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skip("FIFOs unsupported:", err)
			}
			written := make(chan int, 1)
			go func() {
				total := 0
				defer func() { written <- total }()
				pipe, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					return
				}
				defer func() { _ = pipe.Close() }() // the count is the result
				chunk := bytes.Repeat([]byte("a"), 4096)
				for total < 64<<20 {
					n, err := pipe.Write(chunk)
					total += n
					if err != nil {
						return
					}
				}
			}()
			if _, err := LoadOrCreate(path, 32, enc); !errors.Is(err, ErrInvalid) {
				t.Fatalf("an oversized file was not rejected as invalid: %v", err)
			}
			if total := <-written; total >= 1<<20 {
				t.Fatalf("read %d bytes of an oversized file", total)
			}
		})
	}
}

func TestBadArgumentsAreRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never.key")
	for _, tc := range []struct {
		path string
		size int
		enc  Encoding
	}{
		{path, 0, Raw},
		{path, -1, Hex},
		{path, MaxSize + 1, Hex},
		{path, 32, 0},
		{path, 32, Hex + 1},
		{"", 32, Hex},
	} {
		if key, err := LoadOrCreate(tc.path, tc.size, tc.enc); err == nil || key != nil {
			t.Errorf("LoadOrCreate(%q, %d, %d) = %x, %v", tc.path, tc.size, tc.enc, key, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused call created a file")
	}
	if _, err := Decode([]byte("00"), 0, Hex); err == nil {
		t.Fatal("Decode accepted a zero size")
	}
}

func TestUnreadablePathsAreErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreate(dir, 32, Hex); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("a directory as the key path: %v", err)
	}
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(filepath.Join(blocker, "key"), 32, Hex); err == nil {
		t.Fatal("a key under a regular file was created")
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreate(filepath.Join(locked, "key"), 32, Hex); err == nil {
			t.Fatal("a key was created in a read-only directory")
		}
	}
}

func TestDecode(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 16)
	for _, text := range []string{hex.EncodeToString(key), base64.StdEncoding.EncodeToString(key), " " + hex.EncodeToString(key) + "\n"} {
		got, err := Decode([]byte(text), 16, Hex)
		if err != nil || !bytes.Equal(got, key) {
			t.Errorf("Decode(%q) = %x, %v", text, got, err)
		}
	}
	input := bytes.Clone(key)
	got, err := Decode(input, 16, Raw)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatal(err)
	}
	// The caller may wipe its buffer; the key must not change with it.
	input[0] = 0
	if got[0] != 7 {
		t.Fatal("Decode returned the caller's buffer")
	}
}

// Processes starting together must agree on one key. If each wrote its own and
// the last writer won, the others would seal data nobody could open again.
func TestConcurrentCreatorsAgreeOnOneKey(t *testing.T) {
	for round := 0; round < 20; round++ {
		path := filepath.Join(t.TempDir(), "nested", "secret.key")
		const starters = 8
		keys := make([][]byte, starters)
		errs := make([]error, starters)
		var start, done sync.WaitGroup
		start.Add(1)
		for i := range starters {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				keys[i], errs[i] = LoadOrCreate(path, 32, Hex)
			}()
		}
		start.Done()
		done.Wait()
		for i := range starters {
			if errs[i] != nil {
				t.Fatalf("round %d, starter %d: %v", round, i, errs[i])
			}
			if !bytes.Equal(keys[i], keys[0]) {
				t.Fatalf("round %d: starter %d holds a different key", round, i)
			}
		}
		assertOnlyKeyFile(t, path)
	}
}

func TestConcurrentProcessesAgreeOnOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	start := strconv.FormatInt(time.Now().Add(300*time.Millisecond).UnixNano(), 10)
	const starters = 8
	commands := make([]*exec.Cmd, starters)
	outputs := make([]*bytes.Buffer, starters)
	for i := range starters {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), childEnv+"=load", "KEYFILE_TEST_PATH="+path, "KEYFILE_TEST_START="+start)
		outputs[i] = new(bytes.Buffer)
		cmd.Stdout, cmd.Stderr = outputs[i], outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands[i] = cmd
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("process %d: %v\n%s", i, err, outputs[i])
		}
	}
	stored, err := LoadOrCreate(path, 32, Hex)
	if err != nil {
		t.Fatal(err)
	}
	for i, output := range outputs {
		if strings.TrimSpace(output.String()) != hex.EncodeToString(stored) {
			t.Fatalf("process %d printed %q, want the stored key", i, output)
		}
	}
	assertOnlyKeyFile(t, path)
}

// A crash after writing but before linking must leave no key file at all, so
// the next start creates a complete one instead of tripping over a short file.
func TestACrashBeforeLinkingLeavesNoKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"=crash", "KEYFILE_TEST_PATH="+path)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("the child did not crash at the link: %v\n%s", err, output)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a crash left a key file behind: %v", err)
	}
	key, err := LoadOrCreate(path, 32, Hex)
	if err != nil {
		t.Fatalf("the next start could not create a key: %v", err)
	}
	again, err := LoadOrCreate(path, 32, Hex)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("the key created after a crash did not persist")
	}
}

// The order matters: contents synced, then linked, then the directory synced.
// Linking first would publish a file whose contents may not be on disk yet.
func TestCreationSyncsBeforeLinkingAndSyncsTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	var steps []string
	real := systemFiles()
	fs := files{
		sync: func(file *os.File) error {
			data, err := os.ReadFile(file.Name())
			if err != nil || len(data) != 65 {
				t.Errorf("synced a temporary file holding %q (%v)", data, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Error("the key file was visible before its contents were synced")
			}
			steps = append(steps, "sync")
			return real.sync(file)
		},
		link: func(oldname, newname string) error {
			if filepath.Dir(oldname) != filepath.Dir(newname) {
				t.Errorf("temporary file %s is not beside %s", oldname, newname)
			}
			steps = append(steps, "link")
			return real.link(oldname, newname)
		},
		syncDir: func(dir string) error {
			if dir != filepath.Dir(path) {
				t.Errorf("synced %s, want the key's directory", dir)
			}
			steps = append(steps, "syncdir")
			return real.syncDir(dir)
		},
	}
	key, err := loadOrCreate(path, 32, Hex, fs)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(steps, ","); got != "sync,link,syncdir" {
		t.Fatalf("creation steps = %s, want sync,link,syncdir", got)
	}
	if loaded, err := LoadOrCreate(path, 32, Hex); err != nil || !bytes.Equal(loaded, key) {
		t.Fatal("the created key does not load back")
	}
	assertOnlyKeyFile(t, path)
}

// When another process links its key first, the loser must use the winner's
// key and never overwrite it.
func TestTheFirstLinkedKeyWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	winner := bytes.Repeat([]byte{0x5a}, 32)
	real := systemFiles()
	fs := real
	fs.link = func(oldname, newname string) error {
		if err := os.WriteFile(newname, []byte(hex.EncodeToString(winner)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return real.link(oldname, newname)
	}
	key, err := loadOrCreate(path, 32, Hex, fs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, winner) {
		t.Fatalf("loser kept its own key %x", key)
	}
	if stored, err := os.ReadFile(path); err != nil || string(stored) != hex.EncodeToString(winner)+"\n" {
		t.Fatalf("the winner's file was replaced: %q", stored)
	}
	assertOnlyKeyFile(t, path)
}

func TestFailuresDuringCreationLeaveNothingBehind(t *testing.T) {
	failure := errors.New("disk on fire")
	for _, step := range []string{"sync", "link", "syncdir"} {
		t.Run(step, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret.key")
			fs := systemFiles()
			switch step {
			case "sync":
				fs.sync = func(*os.File) error { return failure }
			case "link":
				fs.link = func(string, string) error { return failure }
			case "syncdir":
				fs.syncDir = func(string) error { return failure }
			}
			if _, err := loadOrCreate(path, 32, Hex, fs); !errors.Is(err, failure) {
				t.Fatalf("error = %v, want the %s failure", err, step)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.Contains(entry.Name(), ".tmp-") {
					t.Errorf("temporary file %s was left behind", entry.Name())
				}
			}
			if step != "syncdir" {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("a failed creation published a key file")
				}
			}
		})
	}
}

func TestLoadAfterCreateReturnsTheSameKey(t *testing.T) {
	dir := t.TempDir()
	round := 0
	property := func(sizeSeed uint16, hexEncoded bool) bool {
		round++
		size := int(sizeSeed)%MaxSize + 1
		enc := Raw
		if hexEncoded {
			enc = Hex
		}
		path := filepath.Join(dir, strconv.Itoa(round)+".key")
		created, err := LoadOrCreate(path, size, enc)
		if err != nil || len(created) != size {
			return false
		}
		loaded, err := LoadOrCreate(path, size, enc)
		return err == nil && bytes.Equal(created, loaded)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatal(err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte("00112233445566778899aabbccddeeff"), uint8(16), true)
	f.Add([]byte("ABEiM0RVZneImaq7zN3u/w=="), uint8(16), true)
	f.Add([]byte("0123456789abcdef"), uint8(16), false)
	f.Fuzz(func(t *testing.T, data []byte, size uint8, hexEncoded bool) {
		enc := Raw
		if hexEncoded {
			enc = Hex
		}
		key, err := Decode(data, int(size), enc)
		if err != nil {
			if key != nil {
				t.Fatal("a failed decode returned a key")
			}
			return
		}
		if len(key) != int(size) {
			t.Fatalf("decoded %d bytes, want %d", len(key), size)
		}
		if enc == Raw && !bytes.Equal(key, data) {
			t.Fatal("raw decode changed the bytes")
		}
		if enc == Hex {
			again, err := Decode(encode(key, Hex), int(size), Hex)
			if err != nil || !bytes.Equal(again, key) {
				t.Fatal("a decoded key does not survive encoding")
			}
		}
	})
}

func assertOnlyKeyFile(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("directory holds %v (%v), want only %s", names, err, filepath.Base(path))
	}
}

func encodingName(enc Encoding) string {
	if enc == Raw {
		return "raw"
	}
	return "hex"
}

// randomKey is used by the example-style tests that need a key that is not
// all one byte.
func randomKey(t *testing.T, size int) []byte {
	t.Helper()
	key := make([]byte, size)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestRandomKeysRoundTripThroughBothEncodings(t *testing.T) {
	key := randomKey(t, 32)
	for _, enc := range []Encoding{Raw, Hex} {
		got, err := Decode(encode(key, enc), 32, enc)
		if err != nil || !bytes.Equal(got, key) {
			t.Fatalf("%s: %x, %v", encodingName(enc), got, err)
		}
	}
}
