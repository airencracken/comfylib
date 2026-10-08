// SPDX-License-Identifier: AGPL-3.0-or-later

// Package password provides confirmed password prompting. The caller supplies
// a terminal reader and applies its own password policy. WithHiddenInput guards
// the entire prompt sequence so echo is disabled before a prompt is visible.
package password

import (
	"errors"
	"fmt"
	"io"
)

// Confirm reads twice using the supplied prompts, without writing either value.
// Reader and writer failures return no password. Confirm does not validate strength.
func Confirm(out io.Writer, prompt, confirmation string, read func() ([]byte, error)) (string, error) {
	first, err := line(out, prompt, read)
	if err != nil {
		return "", err
	}
	second, err := line(out, confirmation, read)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}

func line(out io.Writer, prompt string, read func() ([]byte, error)) ([]byte, error) {
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return nil, err
	}
	value, err := read()
	if _, writeErr := fmt.Fprintln(out); err == nil {
		err = writeErr
	}
	return value, err
}
