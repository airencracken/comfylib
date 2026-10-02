// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// awsChain is the kind of list an application passes as ForwardEnv: a cloud
// SDK's credential chain and the time zone.
var awsChain = []string{"TZ", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_REGION"}

// mountFor returns how a policy exposed one path.
func mountFor(args []string, path string) []string {
	for i := 0; i+2 < len(args); i++ {
		if args[i+2] == path && (args[i] == "--ro-bind" || args[i] == "--symlink" || args[i] == "--bind") {
			return args[i : i+3]
		}
	}
	return nil
}

// writableBinds lists the host paths a policy binds writable.
func writableBinds(args []string) []string {
	var paths []string
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--bind" {
			paths = append(paths, args[i+1])
		}
	}
	return paths
}

func TestServicePolicyKeepsSecretsOutOfArguments(t *testing.T) {
	data := t.TempDir()
	args, env, err := (Service{Prefix: "TEST_", DataDir: data, Executable: "/usr/bin/true", ForwardEnv: awsChain, Env: []string{
		"TEST_SMTP_PASSWORD=secret-value", "TEST_DATA_DIR=wrong", "AWS_SECRET_ACCESS_KEY=chain-secret", "TZ=Europe/London",
		"GITHUB_TOKEN=unrelated", "AWS_SECRET_ACCESS_KEYX=lookalike", "LD_PRELOAD=host-loader", "TMPDIR=/host/tmp", "TEST_NO_EQUALS",
	}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, required := range []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--cap-drop", "--new-session", "--tmpfs", "--ro-bind", "--disable-userns", data} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing confinement: %s", required)
		}
	}
	// The data directory is the one writable host path, and nothing else is.
	if !strings.Contains(joined, "\n--bind\n"+data+"\n"+data+"\n") {
		t.Errorf("data directory is not a writable bind: %s", joined)
	}
	if got := writableBinds(args); !slices.Equal(got, []string{data}) {
		t.Errorf("unexpected writable mounts: %q", got)
	}
	for _, forbidden := range []string{"secret-value", "chain-secret", "--unshare-net", "--die-with-parent", "--ro-bind\n/\n/", "--ro-bind\n" + data + "\n"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("unsafe service policy contains %s", forbidden)
		}
	}
	joined = strings.Join(env, "\n")
	// The credential chain and the time zone are forwarded by name; the
	// application's own settings by prefix.
	for _, required := range []string{"TEST_SMTP_PASSWORD=secret-value", "TEST_DATA_DIR=" + data, "TMPDIR=/tmp", "AWS_SECRET_ACCESS_KEY=chain-secret", "TZ=Europe/London"} {
		if !strings.Contains(joined, required) {
			t.Errorf("environment missing %s", required)
		}
	}
	for _, forbidden := range []string{"LD_PRELOAD", "GITHUB_TOKEN", "lookalike", "=wrong", "/host/tmp", "TEST_NO_EQUALS", "PASSWORD=secret-value\nPASSWORD"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("environment leaks %s", forbidden)
		}
	}
	if strings.Contains(strings.Join(RuntimeEnv(), "\n"), "PASSWORD") {
		t.Fatal("runtime environment contains credentials")
	}
}

// Without a ForwardEnv list nothing outside the prefix is forwarded, which is
// how an application without a credential chain runs.
func TestServicePolicyForwardsNothingUnlisted(t *testing.T) {
	_, env, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", Env: []string{
		"AWS_SECRET_ACCESS_KEY=unrelated", "TZ=Europe/London", "TEST_NAME=board",
	}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "AWS_SECRET") || strings.Contains(joined, "TZ=") || !strings.Contains(joined, "TEST_NAME=board") {
		t.Fatalf("environment = %q", env)
	}
}

func TestNestedSandboxKeepsUserNamespaces(t *testing.T) {
	args, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", NestedSandbox: true}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--disable-userns") {
		t.Fatal("a nested sandbox could not be built inside this policy")
	}
}

func TestServicePolicyRejectsUnsafeSettings(t *testing.T) {
	data := t.TempDir()
	valid := Service{Prefix: "TEST_", DataDir: data, Executable: "/usr/bin/true"}
	cases := map[string]func(*Service){
		"empty prefix":            func(s *Service) { s.Prefix = "" },
		"prefix without _":        func(s *Service) { s.Prefix = "TEST" },
		"loader prefix":           func(s *Service) { s.Prefix = "LD_" },
		"prefix with =":           func(s *Service) { s.Prefix = "A=B_" },
		"forward PATH":            func(s *Service) { s.ForwardEnv = []string{"PATH"} },
		"forward HOME":            func(s *Service) { s.ForwardEnv = []string{"HOME"} },
		"forward loader":          func(s *Service) { s.ForwardEnv = []string{"LD_PRELOAD"} },
		"forward SSL_CERT_FILE":   func(s *Service) { s.ForwardEnv = []string{"SSL_CERT_FILE"} },
		"forward empty":           func(s *Service) { s.ForwardEnv = []string{""} },
		"forward assignment":      func(s *Service) { s.ForwardEnv = []string{"TZ=UTC"} },
		"relative executable":     func(s *Service) { s.Executable = "true" },
		"missing executable":      func(s *Service) { s.Executable = "/does/not/exist" },
		"directory executable":    func(s *Service) { s.Executable = data },
		"executable with newline": func(s *Service) { s.Executable = "/usr/bin/true\n" },
	}
	for name, mutate := range cases {
		s := valid
		mutate(&s)
		if _, _, err := s.Policy(); err == nil {
			t.Errorf("%s: policy accepted %+v", name, s)
		}
	}
	if _, _, err := valid.Policy(); err != nil {
		t.Fatalf("valid policy refused: %v", err)
	}
}

func TestWritableDirectoriesRejectBroadAndAdversarialPaths(t *testing.T) {
	for _, path := range []string{"/", "/var", "/var/lib", "/home", "/root", "/srv", "/opt", "/tmp", "/usr", "/usr/local", "/etc", "/proc", "/dev", "/run", "/app", "relative", "./data", "", "/var/lib/../lib", "/var/lib/evil\nname", "/var/lib/evil\rname", "/var/lib/evil\x00name"} {
		if _, err := writableDir(path); err == nil {
			t.Errorf("accepted unsafe directory %q", path)
		}
		if _, _, err := (Service{Prefix: "TEST_", DataDir: path, Executable: "/usr/bin/true"}).Policy(); err == nil {
			t.Errorf("policy accepted unsafe data directory %q", path)
		}
		if _, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", WriteDirs: []string{path}}).Policy(); err == nil {
			t.Errorf("policy accepted unsafe extra directory %q", path)
		}
	}
	root := t.TempDir()
	for name, target := range map[string]string{"looks-safe": "/", "etc-alias": "/etc", "usr-alias": "/usr/lib"} {
		link := filepath.Join(root, name)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := writableDir(link); err == nil {
			t.Errorf("symlink to %s was accepted as writable", target)
		}
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(root, "missing")} {
		if _, err := writableDir(path); err == nil {
			t.Fatalf("accepted non-directory %s", path)
		}
	}
	for _, name := range []string{"space and & punctuation", "--option-looking", "日本語", `quote " and '`, "percent %h", "$(touch pwned)"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if got, err := writableDir(path); err != nil || got != path {
			t.Fatalf("safe directory %q: %s %v", path, got, err)
		}
	}
}

func TestExtraWriteDirsAreTheOnlyOtherWritableBinds(t *testing.T) {
	data, extra := t.TempDir(), t.TempDir()
	args, _, err := (Service{Prefix: "TEST_", DataDir: data, Executable: "/usr/bin/true", WriteDirs: []string{extra}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	if got := writableBinds(args); !slices.Equal(got, []string{data, extra}) {
		t.Fatalf("writable binds = %q", got)
	}
}

func TestMediaBaseHasNoNetworkOrHostEnvironment(t *testing.T) {
	args, err := Base(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--unshare-net", "--die-with-parent", "--unshare-pid", "--unshare-user"} {
		if !slices.Contains(args, want) {
			t.Fatal("missing " + want)
		}
	}
	if strings.Contains(strings.Join(RuntimeEnv(), "\n"), "PASSWORD") {
		t.Fatal("media environment contains credentials")
	}
}

func TestServerBaseKeepsTheNetwork(t *testing.T) {
	args, err := Base(Options{Network: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--unshare-net") || slices.Contains(args, "--die-with-parent") {
		t.Fatalf("server base drops the network or ties itself to the supervisor: %q", args)
	}
}

func TestDynamicLoaderPathsAreReadOnly(t *testing.T) {
	for _, options := range []Options{{}, {Network: true}} {
		args, err := Base(options)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ld.so.cache", "/etc/alternatives"} {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			}
			mount := mountFor(args, path)
			if mount == nil {
				t.Fatalf("runtime path is missing: %s", path)
			}
			switch mount[0] {
			case "--symlink":
				if target, err := os.Readlink(path); err != nil || target != mount[1] {
					t.Fatalf("%s recreated as a different symlink: %v (%v)", path, mount, err)
				}
			case "--ro-bind":
				if mount[1] != path {
					t.Fatalf("runtime path is not bound read-only in place: %v", mount)
				}
			default:
				t.Fatalf("runtime path is writable: %v", mount)
			}
		}
		joined := strings.Join(args, "\n")
		if strings.Contains(joined, "--ro-bind\n/etc\n/etc") || strings.Contains(joined, "\n--bind\n") {
			t.Fatal("runtime libraries exposed the host configuration or a writable path")
		}
	}
}

func TestRuntimeMountsRecreateMergedUsrSymlinks(t *testing.T) {
	root := t.TempDir()
	usr, outside := filepath.Join(root, "usr"), filepath.Join(root, "outside")
	for _, dir := range []string{filepath.Join(usr, "bin"), filepath.Join(usr, "lib"), filepath.Join(outside, "sub")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{"bin": "usr/bin", "lib64": "usr/lib", "escape": "outside", "dangling": "usr/missing", "absolute": filepath.Join(usr, "lib"), "dotdot": "usr/../outside", "usrlike": "usr-not", "via-escape": "escape/sub"}
	if err := os.Mkdir(filepath.Join(root, "usr-not"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	path := func(name string) string { return filepath.Join(root, name) }
	args, err := runtimeMounts([]string{usr, path("bin"), path("lib64"), path("escape"), path("dangling"), path("absolute"), path("dotdot"), path("usrlike"), path("via-escape"), path("missing")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--ro-bind", usr, usr,
		"--symlink", "usr/bin", path("bin"),
		"--symlink", "usr/lib", path("lib64"),
		"--ro-bind", path("escape"), path("escape"),
		"--symlink", filepath.Join(usr, "lib"), path("absolute"),
		"--ro-bind", path("dotdot"), path("dotdot"),
		"--ro-bind", path("usrlike"), path("usrlike"),
		"--symlink", "escape/sub", path("via-escape"),
	}
	if !slices.Equal(args, want) {
		t.Fatalf("runtime mounts:\n got %q\nwant %q", args, want)
	}
	// "dotdot" resolves on the host into a bound directory, but that
	// directory is mounted as "escape", so inside the sandbox the recreated
	// link would dangle; "via-escape" names the mount itself and is kept.
	// A symlink listed before the directory it points into is bound, not
	// recreated, because the sandbox would not contain its target yet.
	args, err = runtimeMounts([]string{path("bin"), usr})
	if err != nil {
		t.Fatal(err)
	}
	if mount := mountFor(args, path("bin")); mount == nil || mount[0] != "--ro-bind" {
		t.Fatalf("symlink into an unbound path was recreated: %q", args)
	}
}

func TestSandboxSetupFailureIsAnError(t *testing.T) {
	if err := Check(context.Background(), "/does-not-exist", nil, nil, "/app/server"); err == nil {
		t.Fatal("missing Bubblewrap accepted")
	}
	if err := Check(context.Background(), "/usr/bin/false", nil, nil, "/app/server"); err == nil {
		t.Fatal("failed confinement accepted")
	}
	if err := Check(context.Background(), "/usr/bin/true", nil, nil); err == nil {
		t.Fatal("check without a command accepted")
	}
}

// Check must run the command it is given inside the sandbox rather than a
// host utility that split-/usr systems keep elsewhere.
func TestCheckRunsTheGivenCommand(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "arguments")
	fake := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RECORD\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), fake, []string{"--unshare-user"}, []string{"RECORD=" + record}, "/app/server", "--help"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "--unshare-user\n--\n/app/server\n--help\n" {
		t.Fatalf("check ran %q", got)
	}
}

// The probe's own output explains a failure, so it belongs in the error.
func TestCheckReportsTheLauncherOutput(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'bwrap: No permissions to create new namespace' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := Check(context.Background(), fake, nil, nil, "/app/server")
	if err == nil || !strings.Contains(err.Error(), "No permissions to create new namespace") {
		t.Fatalf("check error = %v", err)
	}
}

func TestServicePolicyHonoursCustomCertificateBundle(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "private ca.pem")
	if err := os.WriteFile(bundle, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args, env, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", ForwardEnv: []string{"TZ"}, Env: []string{"SSL_CERT_FILE=/etc/hosts", "SSL_CERT_FILE=" + bundle}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, "\n"), "--ro-bind\n"+bundle+"\n/app/ca-bundle.crt") {
		t.Fatalf("custom bundle not bound: %q", args)
	}
	if !slices.Contains(env, "SSL_CERT_FILE=/app/ca-bundle.crt") || strings.Contains(strings.Join(env, "\n"), bundle) {
		t.Fatalf("custom bundle not selected inside the sandbox: %q", env)
	}
	if n := strings.Count(strings.Join(env, "\n"), "SSL_CERT_FILE="); n != 1 {
		t.Fatalf("SSL_CERT_FILE set %d times: %q", n, env)
	}
}

func TestCustomCertificateBundleMustBeAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "dir-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, link, "/missing-file", "relative.pem", "./ca.pem", "/tmp/file\nname", "/tmp/file\rname", "/tmp/file\x00name", "/dev/null"} {
		_, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", Env: []string{"SSL_CERT_FILE=" + path}}).Policy()
		if err == nil {
			t.Errorf("accepted certificate bundle %q", path)
		}
	}
}

// Paths that name real files are still refused when they are relative or
// contain a line break, so the refusal cannot hinge on the file being absent.
func TestExistingFilesWithUnsafeNamesAreRefused(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, name := range []string{"relative.pem", "line\nbreak.pem", "carriage\rreturn.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"relative.pem", "./relative.pem", filepath.Join(dir, "line\nbreak.pem"), filepath.Join(dir, "carriage\rreturn.pem")} {
		service := Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", Env: []string{"SSL_CERT_FILE=" + path}}
		if _, _, err := service.Policy(); err == nil {
			t.Errorf("accepted certificate bundle %q", path)
		}
		service = Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", ReadFiles: []string{path}}
		if _, _, err := service.Policy(); err == nil {
			t.Errorf("accepted read mount %q", path)
		}
	}
	if _, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "relative.pem"}).Policy(); err == nil {
		t.Error("accepted a relative executable that exists")
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (Service{Prefix: "TEST_", DataDir: "data", Executable: "/usr/bin/true"}).Policy(); err == nil {
		t.Error("accepted a relative data directory that exists")
	}
}

func TestReadMountsMustBeExistingFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "credentials")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "dir-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, link, "/missing-file", "relative", "", dir + "/../" + filepath.Base(dir) + "/credentials", "/tmp/file\nname", "/tmp/file\x00name", "/dev/null"} {
		_, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", ReadFiles: []string{path}}).Policy()
		if err == nil {
			t.Errorf("accepted read mount %q", path)
		}
	}
	args, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", ReadFiles: []string{file}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	if mount := mountFor(args, file); mount == nil || mount[0] != "--ro-bind" || mount[1] != file {
		t.Fatalf("read file not bound read-only: %q", mount)
	}
}

func TestSetuidBubblewrapIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Binary(path); err != nil || got != path {
		t.Fatalf("plain launcher refused: %q %v", got, err)
	}
	if err := os.Chmod(path, 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if _, err := Binary(path); err == nil {
		t.Fatal("setuid launcher accepted")
	}
	if _, err := Binary(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing launcher accepted")
	}
}
