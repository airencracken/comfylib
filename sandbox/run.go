// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Relay output through the supervisor. Journald can then attribute records to
// the service's main process even when the server lives in another PID namespace.
type serviceLog struct{ io.Writer }

// Run supervises Bubblewrap and forwards shutdown to the actual server.
// Bubblewrap's outer monitor does not forward SIGTERM itself. JSON status gives
// us the server's host PID; --as-pid-1 avoids signalling an intermediate reaper.
//
// args are the policy followed by "--" and the command, normally /app/server.
// Run returns when the server exits, or after ctx ends and the server has
// stopped.
func Run(ctx context.Context, binary string, args, env []string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	// The write end is closed once Bubblewrap has inherited it; closing it
	// again here only reports os.ErrClosed.
	defer func() {
		for _, end := range []*os.File{reader, writer} {
			if closeErr := end.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	options := []string{"--as-pid-1", "--die-with-parent", "--json-status-fd", "3"}
	cmd := exec.Command(binary, append(options, args...)...)
	cmd.Env = env
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Stdout, cmd.Stderr = serviceLog{os.Stdout}, serviceLog{os.Stderr}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	// The launcher holds its own copy of the write end. Ours has to close, or
	// the status reader would never see the end of the stream.
	if err := writer.Close(); err != nil {
		return errors.Join(err, cmd.Process.Kill(), cmd.Wait())
	}
	// A server that is still running when Run returns must not outlive it.
	defer func() {
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			err = errors.Join(err, killErr)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	child, err := awaitServer(ctx, reader, done)
	if err != nil {
		return err
	}
	// Release only frees the handle; the process itself is waited for
	// through the launcher.
	defer func() { err = errors.Join(err, child.Release()) }()
	return waitForServer(ctx, child, done)
}

type serverStatus struct {
	process *os.Process
	err     error
}

// awaitServer reads the server's host PID from Bubblewrap's JSON status.
func awaitServer(ctx context.Context, reader io.Reader, done <-chan error) (*os.Process, error) {
	status := make(chan serverStatus, 1)
	go func() {
		var value struct {
			PID int `json:"child-pid"`
		}
		err := json.NewDecoder(io.LimitReader(reader, 4096)).Decode(&value)
		var process *os.Process
		if err == nil && value.PID > 0 {
			process, err = os.FindProcess(value.PID)
		} else if err == nil {
			err = errors.New("the Bubblewrap launcher did not report its server process")
		}
		status <- serverStatus{process, err}
		// Keep the status pipe open until exit so Bubblewrap can write its final
		// status without SIGPIPE. No application output travels through it, so
		// a read error only means the pipe has closed.
		_, _ = io.Copy(io.Discard, reader)
	}()
	select {
	case value := <-status:
		if value.err != nil {
			return nil, fmt.Errorf("starting the Bubblewrap sandbox: %w", value.err)
		}
		return value.process, nil
	case err := <-done:
		return nil, fmt.Errorf("the Bubblewrap launcher exited before reporting its server: %w", err)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, errors.New("the Bubblewrap launcher did not report its server within five seconds")
	}
}

func waitForServer(ctx context.Context, child *os.Process, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if err := child.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case err := <-done:
			return err
		case <-time.After(20 * time.Second):
			return errors.New("sandbox server did not stop within twenty seconds")
		}
	}
}
