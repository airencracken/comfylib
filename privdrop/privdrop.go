// SPDX-License-Identifier: AGPL-3.0-or-later

// Package privdrop re-runs an administrative command as the installed
// service's account when an operator starts it as root, for example with
// sudo. Run as root, such a command would leave root-owned database journals
// and files the service can no longer read.
//
// The child receives settings already resolved from the service
// configuration, so it does not need to read files only root can.
package privdrop

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"syscall"

	"github.com/airencracken/comfylib/svcconfig"
)

// Request describes a command that may need to run as the service account.
type Request struct {
	// Args are the program's arguments without the program name, normally
	// os.Args[1:]. Args[0] is the command.
	Args []string
	// Commands are the commands that write the instance's files.
	Commands map[string]bool
	// Paths locates the installed service. Paths.Name appears in messages,
	// and the data directory is passed to the child as
	// Paths.Prefix+"DATA_DIR".
	Paths svcconfig.Paths
	// DefaultUser is the OpenRC account when conf.d does not set one.
	DefaultUser string
	// Settings, when set, returns further settings the child needs for a
	// command, given the resolved data directory. They are resolved here
	// because the service account usually cannot read the configuration.
	Settings func(command, dataDir string) (map[string]string, error)

	hooks hooks
}

// hooks replace the process's identity and the system calls in tests; a nil
// field means the real one.
type hooks struct {
	geteuid    func() int
	lookup     lookup
	executable func() (string, error)
	run        func(*exec.Cmd) error
}

func (h hooks) euid() int {
	if h.geteuid != nil {
		return h.geteuid()
	}
	return os.Geteuid()
}

func (h hooks) self() (string, error) {
	if h.executable != nil {
		return h.executable()
	}
	return os.Executable()
}

func (h hooks) start(cmd *exec.Cmd) error {
	if h.run != nil {
		return h.run(cmd)
	}
	return cmd.Run()
}

// Reexec runs the command as the service account when the process is root,
// the command is in Commands, no help was asked for, and a service is
// installed. handled reports whether it did, or tried and failed; the caller
// then exits with status. When it returns false, the caller runs the command
// itself, and should refuse to if it is still root (see RefuseRoot).
func Reexec(r Request) (handled bool, status int, err error) {
	if r.hooks.euid() != 0 || len(r.Args) == 0 || !r.Commands[r.Args[0]] || HasHelpFlag(r.Args[1:]) {
		return false, 0, nil
	}
	command := r.Args[0]
	username, group, managed, err := r.Paths.Account(r.DefaultUser)
	if err != nil {
		return true, 1, err
	}
	if !managed {
		return false, 0, nil
	}
	if username == "root" {
		return true, 1, fmt.Errorf("the configured %s service user is root; %s refuses to write the instance's files as root", r.Paths.Name, command)
	}
	settings, err := r.childSettings(command)
	if err != nil {
		return true, 1, err
	}
	credential, err := r.hooks.lookup.credential(username, group)
	if err != nil {
		return true, 1, err
	}
	executable, err := r.hooks.self()
	if err != nil {
		return true, 1, fmt.Errorf("find the %s executable: %w", r.Paths.Name, err)
	}
	child := exec.Command(executable, r.Args...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = WithEnvironment(os.Environ(), settings)
	child.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
	if err := r.hooks.start(child); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return true, exitError.ExitCode(), nil
		}
		return true, 1, fmt.Errorf("run %s as %s service user %q: %w", command, r.Paths.Name, username, err)
	}
	return true, 0, nil
}

// childSettings resolves the data directory and any further settings the
// command needs.
func (r Request) childSettings(command string) (map[string]string, error) {
	dataKey := r.Paths.Prefix + "DATA_DIR"
	dataDir, err := r.Paths.DataDir(dataKey)
	if err != nil {
		return nil, err
	}
	settings := map[string]string{}
	if r.Settings != nil {
		extra, err := r.Settings(command, dataDir)
		if err != nil {
			return nil, err
		}
		maps.Copy(settings, extra)
	}
	// The data directory is the one resolved here, whatever Settings said.
	settings[dataKey] = dataDir
	return settings, nil
}
