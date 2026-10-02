// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sandbox constructs explicit Bubblewrap policies. It never falls back
// to an unrestricted process when confinement was requested.
//
// A server runs under Service.Policy and Run. Children that only process local
// files, such as media converters, run under Base with no network and are
// started through RunChild so that StartReaper, in a server that is PID 1 in
// its namespace, never takes a child os/exec is still waiting for.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Options selects what Base grants beyond the runtime files.
type Options struct {
	// Network keeps the host network. Servers need it for HTTP, mail and
	// object storage. Without it the sandbox gets its own empty network
	// namespace and dies with its parent, which suits children that only
	// process local files.
	Network bool
}

// Base exposes runtime libraries, private temporary space and minimal devices.
func Base(o Options) ([]string, error) {
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--cap-drop", "ALL", "--new-session"}
	if !o.Network {
		args = append(args, "--unshare-net", "--die-with-parent")
	}
	mounts, err := runtimeMounts([]string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ld.so.cache", "/etc/alternatives"})
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/app", "--dir", "/run")
	return args, nil
}

// runtimeMounts binds each existing path read-only. On merged-/usr systems
// /bin, /lib and similar paths are symlinks into /usr; they are recreated as
// the same symlinks instead of separate mounts, provided they point inside a
// path bound earlier. Anything else is bound as before.
func runtimeMounts(paths []string) ([]string, error) {
	var args []string
	var bound []mount
	for _, path := range paths {
		real, err := filepath.EvalSymlinks(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return nil, err
			}
			if insideMount(path, target, real, bound) {
				args = append(args, "--symlink", target, path)
				continue
			}
		}
		args = append(args, "--ro-bind", path, path)
		bound = append(bound, mount{path: path, real: real})
	}
	return args, nil
}

// mount is a path bound in place, and what it resolves to on the host.
type mount struct{ path, real string }

// insideMount reports whether a recreated symlink would reach the same file
// inside the sandbox as on the host. Its target, read as the sandbox sees it,
// must lie inside one bound path, and the host must resolve it inside what
// that path resolves to. A bound path that is itself a symlink is mounted
// under its own name, so the host resolution alone is not enough.
func insideMount(link, target, real string, bound []mount) bool {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	for _, m := range bound {
		if within(target, m.path) && within(real, m.real) {
			return true
		}
	}
	return false
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// Check verifies that the requested namespaces actually work by running the
// given command inside them: normally the bound server, or for a child policy
// the program the child will run. Using a command the sandbox must contain
// anyway avoids depending on a host utility that split-/usr systems keep
// elsewhere. The caller must treat an error as fatal, including when kernel or
// service policy forbids the namespaces.
//
// The probe goes through RunChild, so a server that is PID 1 can check a child
// policy while its reaper runs.
func Check(ctx context.Context, binary string, args, env []string, command ...string) error {
	if len(command) == 0 {
		return errors.New("the Bubblewrap sandbox check needs a command to run")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append(append(append([]string{}, args...), "--"), command...)...)
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := RunChild(cmd); err != nil {
		return fmt.Errorf("the Bubblewrap sandbox is unavailable: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

// RuntimeEnv intentionally excludes inherited loader settings and credentials.
func RuntimeEnv() []string {
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C", "HOME=/tmp", "TMPDIR=/tmp"}
}

// Binary rejects the historical setuid installation mode. These policies
// require unprivileged user namespaces rather than a privileged launcher.
func Binary(path string) (string, error) {
	binary, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("the Bubblewrap launcher is required: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(binary)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSetuid != 0 {
		return "", errors.New("a setuid Bubblewrap is unsupported; use unprivileged user namespaces")
	}
	return binary, nil
}
