// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Service builds a server policy. Extra mounts are explicit operator choices;
// the default grants write access only to the existing data directory.
type Service struct {
	// Prefix starts the names of the application's own settings, for
	// example "IMVAULT_". Those settings are forwarded to the server, and
	// Prefix+"DATA_DIR" is set to the mounted data directory.
	Prefix string
	// DataDir is the existing directory the server writes. It must be a
	// clean absolute path.
	DataDir string
	// Executable is the server binary on the host, bound at /app/server.
	Executable string
	// WriteDirs are further existing directories the server may write.
	WriteDirs []string
	// ReadFiles are further regular files bound read-only at their own
	// paths, for example a credentials file a forwarded variable names.
	ReadFiles []string
	// Env is the launcher's environment, normally os.Environ(). Only the
	// application's settings and the names in ForwardEnv reach the server.
	// SSL_CERT_FILE, when set, names a certificate bundle to trust instead
	// of the system one.
	Env []string
	// ForwardEnv names the variables outside Prefix the server still needs,
	// such as TZ or a cloud SDK's credential chain.
	ForwardEnv []string
	// NestedSandbox keeps user namespaces available inside the server, which
	// it needs to build sandboxes of its own. Without it they are disabled,
	// so a compromised server has that much less kernel to reach.
	NestedSandbox bool
}

// Policy returns the Bubblewrap arguments, ending before the "--" that
// separates them from the command, and the server's environment. Secrets stay
// in the environment and never appear among the arguments.
func (s Service) Policy() (args, env []string, err error) {
	if err := s.validate(); err != nil {
		return nil, nil, err
	}
	if args, err = Base(Options{Network: true}); err != nil {
		return nil, nil, err
	}
	data, err := writableDir(s.DataDir)
	if err != nil {
		return nil, nil, err
	}
	for _, dir := range append([]string{data}, s.WriteDirs...) {
		path, err := writableDir(dir)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--bind", path, path)
	}
	readOnly, err := readOnlyFiles(append(systemFiles(), s.ReadFiles...))
	if err != nil {
		return nil, nil, err
	}
	args = append(args, readOnly...)
	// The CA directory is needed by mail, identity, storage and federation
	// clients.
	if _, err := os.Stat("/etc/ssl/certs"); err == nil {
		args = append(args, "--ro-bind", "/etc/ssl/certs", "/etc/ssl/certs")
	}
	args = append(args, "--ro-bind", s.Executable, "/app/server", "--chdir", data)
	env = RuntimeEnv()
	bundle, err := certificateBundle(s.Env)
	if err != nil {
		return nil, nil, err
	}
	if bundle != "" {
		args = append(args, "--ro-bind", bundle, "/app/ca-bundle.crt")
		env = append(env, "SSL_CERT_FILE=/app/ca-bundle.crt")
	}
	env = append(env, s.forwarded()...)
	if !s.NestedSandbox {
		args = append(args, "--disable-userns")
	}
	env = append(env, s.Prefix+"DATA_DIR="+data)
	return args, env, nil
}

// validate refuses settings that would forward more than intended. An empty
// prefix would match every variable, and a forwarded name the sandbox sets
// itself would override it, since the last duplicate wins.
func (s Service) validate() error {
	if !validName(s.Prefix) || !strings.HasSuffix(s.Prefix, "_") || strings.HasPrefix(s.Prefix, "LD_") {
		return fmt.Errorf("sandbox setting prefix must be a variable name ending in an underscore: %q", s.Prefix)
	}
	for _, name := range s.ForwardEnv {
		if !validName(name) || reservedName(name) {
			return fmt.Errorf("sandbox cannot forward the variable %q", name)
		}
	}
	if !filepath.IsAbs(s.Executable) || strings.ContainsAny(s.Executable, "\x00\r\n") {
		return fmt.Errorf("sandbox server executable must be an absolute path: %q", s.Executable)
	}
	if info, err := os.Stat(s.Executable); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("sandbox server executable must be an existing file: %q", s.Executable)
	}
	return nil
}

func validName(name string) bool {
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	for _, r := range name {
		if !nameChar(r) {
			return false
		}
	}
	return true
}

func nameChar(r rune) bool {
	return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// reservedName reports the variables the sandbox controls: those RuntimeEnv
// sets, the certificate bundle, and the dynamic loader's.
func reservedName(name string) bool {
	if name == "SSL_CERT_FILE" || strings.HasPrefix(name, "LD_") {
		return true
	}
	for _, entry := range RuntimeEnv() {
		if key, _, _ := strings.Cut(entry, "="); key == name {
			return true
		}
	}
	return false
}

// forwarded picks the variables the server receives from its launcher: its own
// settings, by prefix, and the few others named in ForwardEnv. The data
// directory and certificate bundle are set separately, to the paths actually
// mounted.
func (s Service) forwarded() []string {
	allowed := make(map[string]bool, len(s.ForwardEnv))
	for _, name := range s.ForwardEnv {
		allowed[name] = true
	}
	var env []string
	for _, entry := range s.Env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (strings.HasPrefix(key, s.Prefix) || allowed[key]) && key != s.Prefix+"DATA_DIR" {
			env = append(env, entry)
		}
	}
	return env
}

// readOnlyFiles binds each file read-only at its own path, refusing anything
// that is not an existing regular file named by a clean absolute path.
func readOnlyFiles(paths []string) ([]string, error) {
	var args []string
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
			return nil, fmt.Errorf("sandbox read file must be a clean absolute path: %q", path)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("sandbox read file must exist and be regular: %q", path)
		}
		args = append(args, "--ro-bind", path, path)
	}
	return args, nil
}

func systemFiles() []string {
	var files []string
	for _, path := range []string{"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/localtime", "/etc/ssl/cert.pem"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files
}

func writableDir(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("sandbox writable directory must be a clean absolute path: %q", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("sandbox directory must already exist: %w", err)
	}
	if err := validateWriteMount(path); err != nil {
		return "", err
	}
	if err := validateWriteMount(real); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("sandbox writable directory is not a directory: %q", path)
	}
	return path, nil
}

// certificateBundle picks the CA bundle the server trusts. An SSL_CERT_FILE in
// the service environment names a custom bundle, for example one including a
// private CA for the mail relay; otherwise the system bundle is used. Binding
// it to a stable name also handles distributions whose CA paths are symlinks
// into directories which are otherwise hidden by the sandbox.
func certificateBundle(environment []string) (string, error) {
	custom := ""
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "SSL_CERT_FILE="); ok {
			custom = value
		}
	}
	if custom != "" {
		if !filepath.IsAbs(custom) || strings.ContainsAny(custom, "\x00\r\n") {
			return "", fmt.Errorf("SSL_CERT_FILE must be an absolute path: %q", custom)
		}
		if info, err := os.Stat(custom); err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("SSL_CERT_FILE must name an existing certificate file: %q", custom)
		}
		return custom, nil
	}
	for _, path := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/cert.pem", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", nil
}

func validateWriteMount(name string) error {
	if name == "/" || name == "/var" || name == "/var/lib" || name == "/home" || name == "/root" || name == "/srv" || name == "/opt" || name == "/tmp" {
		return fmt.Errorf("sandbox writable directory is too broad: %q", name)
	}
	for _, reserved := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/proc", "/dev", "/run", "/app"} {
		if name == reserved || strings.HasPrefix(name, reserved+"/") {
			return fmt.Errorf("sandbox writable directory overlaps runtime files: %q", name)
		}
	}
	return nil
}
