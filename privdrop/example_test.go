// SPDX-License-Identifier: AGPL-3.0-or-later

package privdrop_test

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/airencracken/comfylib/privdrop"
	"github.com/airencracken/comfylib/svcconfig"
)

// main hands a command started with sudo to the service account before doing
// anything else.
func ExampleReexec() {
	paths := svcconfig.Detect("app", "/var/lib/app")
	handled, status, err := privdrop.Reexec(privdrop.Request{
		Args:        os.Args[1:],
		Commands:    map[string]bool{"create-admin": true, "backup": true},
		Paths:       paths,
		DefaultUser: "app",
		// The child cannot read root-only configuration, so further
		// settings are resolved here.
		Settings: func(command, dataDir string) (map[string]string, error) {
			settings, err := paths.Settings("APP_DB")
			if err != nil {
				return nil, err
			}
			return map[string]string{"APP_DB": cmp.Or(settings["APP_DB"], filepath.Join(dataDir, "app.db"))}, nil
		},
	})
	if handled {
		if err != nil {
			slog.Error("App", "error", err)
		}
		os.Exit(status)
	}
	// Not handled: run the command here. A command that writes the
	// instance still refuses root when no service account was found.
	if err := privdrop.RefuseRoot(os.Geteuid(), "backup", "sudo -u app env APP_DATA_DIR=/var/lib/app app backup"); err != nil {
		slog.Error("App", "error", err)
		os.Exit(1)
	}
}

func ExampleWithEnvironment() {
	env := privdrop.WithEnvironment([]string{"PATH=/bin", "APP_DATA_DIR=./data"}, map[string]string{"APP_DATA_DIR": "/var/lib/app", "APP_DB": ""})
	fmt.Println(env)
	// Output: [PATH=/bin APP_DATA_DIR=/var/lib/app]
}

func ExampleRefuseRoot() {
	fmt.Println(privdrop.RefuseRoot(0, "backup", "sudo -u app app backup"))
	fmt.Println(privdrop.RefuseRoot(1000, "backup", "sudo -u app app backup"))
	// Output:
	// backup writes the instance's files and must not run as root; run it as the service account, for example: sudo -u app app backup
	// <nil>
}
