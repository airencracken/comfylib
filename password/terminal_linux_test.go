// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build linux

package password

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"testing/quick"
	"unsafe"
)

func openTerminal(t *testing.T) *os.File {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	var unlock int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))) // #nosec G103 -- fixed-size PTY unlock integer
	if errno != 0 {
		t.Fatal(errno)
	}
	var number uint32
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))) // #nosec G103 -- fixed-size PTY number integer
	if errno != 0 {
		t.Fatal(errno)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return slave
}

func state(t *testing.T, fd int) syscall.Termios {
	t.Helper()
	var result syscall.Termios
	if err := terminalIOCTL(fd, syscall.TCGETS, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHiddenInputTerminalContract(t *testing.T) {
	terminal := openTerminal(t)
	fd := int(terminal.Fd())
	original := state(t, fd)
	for _, actionErr := range []error{nil, io.ErrUnexpectedEOF} {
		value, err := WithHiddenInput(fd, func() (string, error) {
			hidden := state(t, fd)
			want := original
			want.Lflag &^= syscall.ECHO | syscall.ECHONL
			if hidden != want {
				t.Fatal("echo must be disabled before any prompt; unrelated settings must stay intact")
			}
			return "private-password", actionErr
		})
		if !errors.Is(err, actionErr) || (err != nil && value != "") || (err == nil && value != "private-password") {
			t.Fatalf("unexpected action result: error %v", err)
		}
		if state(t, fd) != original {
			t.Fatal("original terminal state not restored")
		}
	}
}

func TestHiddenInputPanicRestoresTerminal(t *testing.T) {
	terminal := openTerminal(t)
	fd := int(terminal.Fd())
	original := state(t, fd)
	func() {
		defer func() {
			if recover() != "interrupted" {
				t.Fatal("action panic lost")
			}
		}()
		_, _ = WithHiddenInput(fd, func() (string, error) { panic("interrupted") })
	}()
	if state(t, fd) != original {
		t.Fatal("terminal not restored after panic")
	}
}

func TestHiddenInputFailuresReturnNoPassword(t *testing.T) {
	for _, fd := range []int{-1, -100, 1 << 30} {
		called := false
		value, err := WithHiddenInput(fd, func() (string, error) {
			called = true
			return "secret", nil
		})
		if err == nil || value != "" || called {
			t.Fatal("invalid terminal must fail before action")
		}
	}
	terminal := openTerminal(t)
	value, err := WithHiddenInput(int(terminal.Fd()), func() (string, error) {
		if err := terminal.Close(); err != nil {
			t.Fatal(err)
		}
		return "secret", io.ErrUnexpectedEOF
	})
	if value != "" || !errors.Is(err, syscall.EBADF) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("restoration and action failures must both survive without returning a password")
	}
}

func TestHiddenInputFlagsProperty(t *testing.T) {
	terminal := openTerminal(t)
	fd := int(terminal.Fd())
	original := state(t, fd)
	property := func(bits uint8) bool {
		before := original
		flags := []uint32{syscall.ECHO, syscall.ECHONL, syscall.ICANON, syscall.ISIG, syscall.IEXTEN}
		for i, flag := range flags {
			before.Lflag &^= flag
			if bits&(1<<i) != 0 {
				before.Lflag |= flag
			}
		}
		if err := terminalIOCTL(fd, syscall.TCSETS, &before); err != nil {
			t.Fatal(err)
		}
		want := state(t, fd)
		hidden := want
		hidden.Lflag &^= syscall.ECHO | syscall.ECHONL
		matched := false
		_, err := WithHiddenInput(fd, func() (string, error) {
			matched = state(t, fd) == hidden
			return "", nil
		})
		return err == nil && matched && state(t, fd) == want
	}
	if err := quick.Check(property, nil); err != nil {
		t.Fatal(err)
	}
}
