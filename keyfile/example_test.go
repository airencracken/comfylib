// SPDX-License-Identifier: AGPL-3.0-or-later

package keyfile_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/comfylib/keyfile"
)

func ExampleLoadOrCreate() {
	dir, err := os.MkdirTemp("", "keyfile-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "secret.key")

	// The first call creates the file; every later call reads it back.
	first, err := keyfile.LoadOrCreate(path, 32, keyfile.Hex)
	if err != nil {
		fmt.Println(err)
		return
	}
	second, err := keyfile.LoadOrCreate(path, 32, keyfile.Hex)
	fmt.Println(len(first), bytes.Equal(first, second), err)

	// A damaged file is reported, never replaced.
	if err := os.WriteFile(path, []byte("damaged\n"), 0o600); err != nil {
		fmt.Println(err)
		return
	}
	_, err = keyfile.LoadOrCreate(path, 32, keyfile.Hex)
	fmt.Println(errors.Is(err, keyfile.ErrInvalid))
	// Output:
	// 32 true <nil>
	// true
}
