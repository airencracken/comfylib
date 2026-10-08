// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build !linux

package password

import "errors"

func hideEcho(_ int) (func() error, error) {
	return nil, errors.New("hidden terminal input is supported only on Linux")
}
