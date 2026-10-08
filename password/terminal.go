// SPDX-License-Identifier: AGPL-3.0-or-later

package password

import "errors"

// WithHiddenInput disables terminal echo before action can display a prompt,
// and keeps it disabled across multiple reads. It restores the original terminal
// state on success, error or panic. Any setup, action or restoration error returns
// no password. The descriptor must refer to a terminal exclusively used by action.
// Currently supported on Linux; other platforms return an error without calling action.
func WithHiddenInput(fd int, action func() (string, error)) (value string, err error) {
	restore, err := hideEcho(fd)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, restore())
		if err != nil {
			value = ""
		}
	}()
	return action()
}
