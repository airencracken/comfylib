// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// A server that has already exited when shutdown begins is not an error:
// signalling it reports os.ErrProcessDone, and its exit arrives through done.
func TestShutdownOfAnExitedServerIsClean(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan error, 1)
	done <- nil
	// With done ready and ctx ended, select may take either branch; repeat so
	// the signalling one is exercised.
	for range 50 {
		if err := waitForServer(ctx, cmd.Process, done); err != nil {
			t.Fatalf("shutdown of an exited server = %v", err)
		}
		done <- nil
	}
}

func TestShutdownReportsTheServerExit(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	want := errors.New("exit status 3")
	done := make(chan error, 1)
	done <- want
	if err := waitForServer(t.Context(), cmd.Process, done); !errors.Is(err, want) {
		t.Fatalf("waitForServer = %v", err)
	}
}
