// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig_test

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/airencracken/comfylib/svcconfig"
)

// An administrative command finds the instance the installed service uses.
func ExampleDetect() {
	paths := svcconfig.Detect("app", "/var/lib/app")
	dataDir, err := paths.DataDir("APP_DATA_DIR")
	if err != nil {
		log.Fatal(err)
	}
	settings, err := paths.Settings("APP_DB", "APP_BASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(dataDir, settings["APP_DB"], paths.Managed())
}

// Paths can also be built by hand, as tests do.
func ExamplePaths_DataDir() {
	dir, err := os.MkdirTemp("", "example")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	config := filepath.Join(dir, "app.confd")
	contents := "APP_DATA_DIR=\"/srv/app data/\" # where the board lives\nAPP_USER=board\n"
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		log.Fatal(err)
	}
	if err := os.Unsetenv("APP_DATA_DIR"); err != nil {
		log.Fatal(err)
	}
	paths := svcconfig.Paths{Name: "App", Prefix: "APP_", OpenRCConfig: config, OpenRCInstalled: true, DefaultDataDir: "/var/lib/app"}
	dataDir, err := paths.DataDir("APP_DATA_DIR")
	if err != nil {
		log.Fatal(err)
	}
	user, group, managed, err := paths.Account("app")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(dataDir)
	fmt.Println(user, group, managed)
	// Output:
	// /srv/app data
	// board app true
}

func ExampleParseShellValue() {
	for _, raw := range []string{`'/srv/it'\''s'`, `"/srv/a b" # comment`, `${ROOT}/data`} {
		value, err := svcconfig.ParseShellValue(raw)
		fmt.Printf("%q %v\n", value, err)
	}
	// Output:
	// "/srv/it's" <nil>
	// "/srv/a b" <nil>
	// "" shell expression "${ROOT}/data" is not supported
}
