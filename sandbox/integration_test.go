// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// realBubblewrap skips unless the real namespace tests were requested, and
// then insists on a working launcher.
func realBubblewrap(t *testing.T) string {
	t.Helper()
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	binary, err := Binary("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestRealBubblewrapMediaBoundary(t *testing.T) {
	binary := realBubblewrap(t)
	args, err := Base(Options{})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), binary, args, RuntimeEnv(), sh, "-c", "exit 0"); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "host-secret")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, append(args, "--", "/bin/sh", "-c", `test ! -e "$1" && test ! -e /etc/shadow && test -z "$TEST_PASSWORD" && test ! -w /usr && test "$(ls /sys/class/net 2>/dev/null)" = ""`, "check", secret)...)
	cmd.Env = RuntimeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("media confinement failed: %v: %s", err, out)
	}
	// Inspect the namespace instead of merely relying on an absent sysfs mount.
	cmd = exec.Command(binary, append(args, "--", "/bin/sh", "-c", `test "$(cat /proc/net/dev | wc -l)" -eq 3`)...)
	cmd.Env = RuntimeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("media network namespace is not isolated: %v: %s", err, out)
	}
}

func TestRealBubblewrapRuntimeBoundary(t *testing.T) {
	binary := realBubblewrap(t)
	data := t.TempDir()
	args, env, err := (Service{Prefix: "TEST_", DataDir: data, Executable: "/bin/sh", Env: []string{"UNRELATED_PASSWORD=hidden"}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "host-secret")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Merged-/usr symlinks stay symlinks, so a path like /lib64/ld-linux keeps
	// resolving the same way it does on the host.
	var links []string
	for _, path := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if mount := mountFor(args, path); mount == nil || mount[0] != "--symlink" {
				t.Fatalf("merged-/usr link %s was not recreated: %q", path, mount)
			}
			links = append(links, path)
		}
	}
	if len(links) == 0 {
		t.Log("this host does not merge /usr, so only the bound paths are checked")
	}
	script := `test ! -e "$1" && test ! -e /etc/shadow && test -z "$UNRELATED_PASSWORD" && test ! -w /usr && touch "$2/written" && shift 2 && for link; do test -L "$link" && test -d "$link/" && test "$(readlink "$link")" = "$(cd / && readlink "$link")"; done`
	cmd := exec.Command(binary, append(args, append([]string{"--", "/app/server", "-c", script, "check", secret, data}, links...)...)...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("service confinement failed: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(data, "written")); err != nil {
		t.Fatalf("data directory was not writable: %v", err)
	}
}

// The check must not depend on host utilities being in /usr/bin; hiding that
// directory simulates a split-/usr host where /usr/bin/true does not exist.
func TestRealCheckDoesNotNeedHostUtilities(t *testing.T) {
	binary := realBubblewrap(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, env, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: executable}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "--tmpfs", "/usr/bin")
	if err := Check(t.Context(), binary, args, env, "/app/server", "-test.run=^$"); err != nil {
		t.Fatal(err)
	}
}

// A server without NestedSandbox cannot create user namespaces of its own;
// with it, it can, which is what a server building child sandboxes needs.
func TestRealServiceUserNamespacesFollowNestedSandbox(t *testing.T) {
	binary := realBubblewrap(t)
	nested := []string{"/app/server", "--unshare-user", "--ro-bind", "/", "/", "--", "/bin/sh", "-c", "exit 0"}
	for _, allowed := range []bool{false, true} {
		args, env, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: binary, NestedSandbox: allowed}).Policy()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, append(append(args, "--"), nested...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if allowed && err != nil {
			t.Fatalf("nested sandbox refused although allowed: %v: %s", err, out)
		}
		if !allowed && err == nil {
			t.Fatal("the server created a user namespace although they are disabled")
		}
	}
}

func TestServiceHelper(t *testing.T) {
	if os.Getenv("TEST_SANDBOX_HELPER") != "1" {
		return
	}
	os.Exit(serviceHelper())
}

// serviceHelper is the confined server: it probes the boundary, then writes
// "ready" and waits for SIGTERM. Each failure has its own exit status.
func serviceHelper() int {
	data := os.Getenv("TEST_DATA_DIR")
	// The bundle the policy binds must hold certificates the server can use.
	bundle, err := os.ReadFile(os.Getenv("SSL_CERT_FILE"))
	if err != nil || !x509.NewCertPool().AppendCertsFromPEM(bundle) {
		return 18
	}
	for _, forbidden := range []string{os.Getenv("TEST_FORBIDDEN"), "/etc/shadow"} {
		if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "sandbox exposed", forbidden, err)
			return 10
		}
	}
	if os.Getenv("UNRELATED_PASSWORD") != "" || os.Getenv("LD_PRELOAD") != "" {
		return 11
	}
	if os.Getenv("TZ") != "Europe/London" {
		return 19
	}
	if status := get(os.Getenv("TEST_HTTP"), 12); status != 0 {
		return status
	}
	// The private CA reaches the server only through SSL_CERT_FILE.
	if status := get(os.Getenv("TEST_HTTPS"), 16); status != 0 {
		return status
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	if err := os.WriteFile(filepath.Join(data, "ready"), []byte("ready"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 14
	}
	<-signals
	if err := os.WriteFile(filepath.Join(data, "stopped"), []byte("graceful"), 0o600); err != nil {
		return 15
	}
	return 0
}

func get(url string, status int) int {
	response, err := http.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return status
	}
	if err := response.Body.Close(); err != nil || response.StatusCode != 200 {
		return status + 1
	}
	return 0
}

func TestRealServiceBoundaryAndGracefulStop(t *testing.T) {
	binary := realBubblewrap(t)
	root := t.TempDir()
	data := filepath.Join(root, "data with spaces")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "unrelated-secret")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer host.Close()
	private := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer private.Close()
	bundle := filepath.Join(root, "private ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: private.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, env, err := (Service{Prefix: "TEST_", DataDir: data, Executable: executable, ForwardEnv: []string{"TZ"}, Env: []string{
		"TEST_SANDBOX_HELPER=1", "TEST_FORBIDDEN=" + secret, "TEST_HTTP=" + host.URL, "TEST_HTTPS=" + private.URL,
		"UNRELATED_PASSWORD=hidden", "SSL_CERT_FILE=" + bundle, "TZ=Europe/London", "LD_PRELOAD=/nonexistent.so",
	}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, binary, append(args, "--", "/app/server", "-test.run=^TestServiceHelper$"), env)
	}()
	deadline := time.After(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(data, "ready")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("sandbox service exited before ready: %v", err)
		case <-deadline:
			t.Fatal("sandbox service did not start")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sandbox did not forward SIGTERM")
	}
	if got, err := os.ReadFile(filepath.Join(data, "stopped")); err != nil || string(got) != "graceful" {
		t.Fatalf("sandbox prevented graceful shutdown: %s %v", got, err)
	}
}

// A server that exits on its own is reported through Run's error.
func TestRealRunReportsTheServerExit(t *testing.T) {
	binary := realBubblewrap(t)
	args, env, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/bin/sh"}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	err = Run(t.Context(), binary, append(args, "--", "/app/server", "-c", "exit 7"), env)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("Run = %v, want exit status 7", err)
	}
	if err := Run(t.Context(), "/bin/false", nil, nil); err == nil {
		t.Fatal("a launcher that never reported its server was accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(cancelled, binary, nil, nil); err == nil {
		t.Fatal("Run started after its context ended")
	}
}

// TestRealOrphanedChildrenAreReaped reproduces the leak end to end: this test
// binary runs as PID 1 inside Bubblewrap, starts nested child sandboxes, and
// kills their launchers the way a timeout does. StartReaper must then collect
// the orphans on its own.
func TestRealOrphanedChildrenAreReaped(t *testing.T) {
	if os.Getenv("COMFYLIB_REAPER_CHILD") == "1" {
		orphanAndReap(t)
		return
	}
	bwrap := realBubblewrap(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bwrap, "--as-pid-1", "--die-with-parent", "--unshare-user", "--unshare-pid",
		"--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev",
		"--", self, "-test.run=^TestRealOrphanedChildrenAreReaped$", "-test.v")
	cmd.Env = append(os.Environ(), "COMFYLIB_REAPER_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "zombies after reaping: 0") {
		t.Fatalf("orphaned jobs were not reaped (%v):\n%s", err, out)
	}
}

func orphanAndReap(t *testing.T) {
	if os.Getpid() != 1 {
		t.Fatalf("the helper is PID %d, not 1", os.Getpid())
	}
	bwrap, err := Binary("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	args, err := Base(Options{})
	if err != nil {
		t.Fatal(err)
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		job := exec.Command(bwrap, append(append([]string{}, args...), "--", sleep, "30")...)
		job.Env = RuntimeEnv()
		if err := job.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		if err := job.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := job.Wait(); err == nil {
			t.Fatal("the killed launcher reported success")
		}
	}
	time.Sleep(300 * time.Millisecond)
	if before := countZombies(t); before == 0 {
		t.Fatal("no orphan was left behind, so this proves nothing")
	}
	// The reaper wakes on SIGCHLD or its ticker; a fresh child exiting
	// raises the signal without anyone calling ReapOrphans directly.
	StartReaper(t.Context())
	if err := RunChild(exec.Command(sleep, "0")); err != nil {
		t.Fatalf("a recorded child lost its status to the reaper: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for countZombies(t) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("zombies after reaping: %d", countZombies(t))
}

func countZombies(t *testing.T) int {
	t.Helper()
	entries, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(filepath.Base(entry))
		if err == nil && zombieChildOf(pid, os.Getpid()) {
			n++
		}
	}
	return n
}

// StartReaper outside PID 1 must leave zombies alone: the process is not an
// init, and its children belong to os/exec.
func TestStartReaperOnlyActsAsPIDOne(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("running as PID 1")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	StartReaper(ctx)
	child := exec.Command("true")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waitForZombie(t, child.Process.Pid)
	time.Sleep(100 * time.Millisecond)
	if !zombieChildOf(child.Process.Pid, os.Getpid()) {
		t.Fatal("a reaper started outside PID 1 took a child")
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}
