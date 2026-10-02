// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// waitForZombie polls until pid has exited and is waiting to be collected.
func waitForZombie(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if zombieChildOf(pid, os.Getpid()) {
			return
		}
	}
	t.Fatalf("process %d never became a zombie", pid)
}

func TestReaperCollectsUntrackedZombies(t *testing.T) {
	orphan := exec.Command("true")
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	waitForZombie(t, orphan.Process.Pid)
	if n := ReapOrphans(); n < 1 {
		t.Fatalf("reaped %d processes, want the zombie", n)
	}
	if zombieChildOf(orphan.Process.Pid, os.Getpid()) {
		t.Fatal("the zombie is still there")
	}
}

func TestReaperLeavesTrackedChildrenForTheirOwner(t *testing.T) {
	child := exec.Command("true")
	owned.Lock()
	if err := child.Start(); err != nil {
		owned.Unlock()
		t.Fatal(err)
	}
	owned.pids[child.Process.Pid] = struct{}{}
	owned.Unlock()
	t.Cleanup(func() {
		owned.Lock()
		delete(owned.pids, child.Process.Pid)
		owned.Unlock()
	})

	waitForZombie(t, child.Process.Pid)
	ReapOrphans()
	if !zombieChildOf(child.Process.Pid, os.Getpid()) {
		t.Fatal("the reaper took a child that os/exec still has to wait for")
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("the owner could not wait for its child: %v", err)
	}
}

// While a child runs, its PID is in the registry, so the reaper cannot take
// it between its exit and os/exec's Wait. Racing a real reaper finds a missing
// record only by luck; checking the registry finds it every time.
func TestRunChildRecordsItsChildWhileRunning(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	done := make(chan error, 1)
	go func() { done <- RunChild(cmd) }()
	recorded := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && !recorded; time.Sleep(5 * time.Millisecond) {
		owned.Lock()
		if cmd.Process != nil {
			_, recorded = owned.pids[cmd.Process.Pid]
		}
		owned.Unlock()
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill() // ends the sleep; its status is not under test
	}
	<-done
	if !recorded {
		t.Fatal("RunChild did not record its child")
	}
}

// RunChild must forget a child once it has been waited for; otherwise a
// recycled PID would be shielded from the reaper forever.
func TestRunChildReleasesItsRecord(t *testing.T) {
	for _, name := range []string{"true", "false"} {
		cmd := exec.Command(name)
		_ = RunChild(cmd) // false fails on purpose; only the registry matters
		owned.Lock()
		_, still := owned.pids[cmd.Process.Pid]
		owned.Unlock()
		if still {
			t.Fatalf("%s is still recorded after Wait", name)
		}
	}
	if err := RunChild(exec.Command("/does/not/exist")); err == nil {
		t.Fatal("a child that could not start reported success")
	}
}

func TestRunChildSurvivesAConcurrentReaper(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				ReapOrphans()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 50; i++ {
		if err := RunChild(exec.Command("true")); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		var exit *exec.ExitError
		if err := RunChild(exec.Command("false")); !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("run %d lost its exit status: %v", i, err)
		}
	}
}

// Many owners and several reapers at once, with untracked zombies appearing
// alongside: every owner must still see its child's own exit status, and
// every untracked zombie must be collected.
func TestReaperConcurrencyWithManyOwners(t *testing.T) {
	stop := make(chan struct{})
	var reapers sync.WaitGroup
	for range 3 {
		reapers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					ReapOrphans()
				}
			}
		})
	}
	var owners sync.WaitGroup
	failures := make(chan error, 256)
	for worker := range 8 {
		owners.Go(func() {
			for i := range 15 {
				code := (worker + i) % 4
				err := RunChild(exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)))
				var exit *exec.ExitError
				switch {
				case code == 0 && err != nil:
					failures <- err
				case code != 0 && (!errors.As(err, &exit) || exit.ExitCode() != code):
					failures <- fmt.Errorf("exit %d was reported as %v", code, err)
				}
			}
		})
	}
	var orphans []int
	for range 10 {
		orphan := exec.Command("true")
		if err := orphan.Start(); err != nil {
			t.Fatal(err)
		}
		orphans = append(orphans, orphan.Process.Pid)
	}
	owners.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range orphans {
		for zombieChildOf(pid, os.Getpid()) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if zombieChildOf(pid, os.Getpid()) {
			t.Errorf("untracked zombie %d was never reaped", pid)
		}
	}
	close(stop)
	reapers.Wait()
}

func TestZombieDetectionReadsTheRightFields(t *testing.T) {
	if zombieChildOf(os.Getpid(), os.Getppid()) {
		t.Fatal("a running process was read as a zombie")
	}
	if zombieChildOf(-1, 1) || zombieChildOf(1<<30, 1) {
		t.Fatal("a missing process was read as a zombie")
	}
	for stat, want := range map[string]bool{
		"42 (sleep) Z 7 42 42":         true,
		"42 (sleep) S 7 42 42":         false,
		"42 (sleep) Z 8 42 42":         false,
		"42 (a) Z 9) Z 7 1 1":          true,
		"42 (name with spaces) Z 7 1":  true,
		"42 (x) Z":                     false,
		"42 (x)":                       false,
		"no parenthesis Z 7":           false,
		"42 (x) Z 77 1 1":              false,
		"42 ()) Z 7":                   true,
		"42 (evil\n) Z 8\n) Z 7 1 1 1": true,
	} {
		if got := zombieStat(stat, 7); got != want {
			t.Errorf("zombieStat(%q) = %t, want %t", stat, got, want)
		}
	}
}
