// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build linux

package password

import (
	"fmt"
	"syscall"
	"unsafe"
)

func hideEcho(fd int) (func() error, error) {
	var original syscall.Termios
	if err := terminalIOCTL(fd, syscall.TCGETS, &original); err != nil {
		return nil, fmt.Errorf("read terminal state: %w", err)
	}
	hidden := original
	hidden.Lflag &^= syscall.ECHO | syscall.ECHONL
	if err := terminalIOCTL(fd, syscall.TCSETS, &hidden); err != nil {
		return nil, fmt.Errorf("disable terminal echo: %w", err)
	}
	return func() error {
		if err := terminalIOCTL(fd, syscall.TCSETS, &original); err != nil {
			return fmt.Errorf("restore terminal state: %w", err)
		}
		return nil
	}, nil
}

func terminalIOCTL(fd int, request uintptr, state *syscall.Termios) error {
	// The kernel reads/writes exactly one Termios synchronously. The pointer
	// conversion stays in the Syscall argument so Go retains it for the call.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, uintptr(unsafe.Pointer(state))) // #nosec G103 -- fixed-size termios ioctl
	if errno != 0 {
		return errno
	}
	return nil
}
