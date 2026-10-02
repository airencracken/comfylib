// SPDX-License-Identifier: AGPL-3.0-or-later

// Package svcconfig reads an installed service's settings the way its service
// manager would pass them, so that a command an operator runs by hand acts on
// the same instance as the running service. It understands OpenRC conf.d files
// and systemd units with their drop-ins, Environment= and EnvironmentFile=.
//
// It deliberately evaluates nothing. A value that needs shell expansion or a
// systemd specifier is refused with a hint to set the variable explicitly,
// rather than guessed at.
package svcconfig

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths records where a service's configuration lives and which service
// managers are installed and running. Detect fills it in; tests construct it
// directly.
type Paths struct {
	// Name is how messages refer to the application, for example "Imvault".
	Name string
	// Prefix starts the application's setting names, for example "IMVAULT_".
	Prefix string
	// OpenRCConfig is the conf.d file, which may not exist.
	OpenRCConfig string
	// OpenRCInstalled is set when the conf.d file or init script exists, or
	// cannot be checked.
	OpenRCInstalled bool
	// OpenRCActive is set when OpenRC is the running service manager.
	OpenRCActive bool
	// SystemdUnit is the unit file systemd would load, or empty.
	SystemdUnit string
	// SystemdActive is set when systemd is the running service manager.
	SystemdActive bool
	// DefaultDataDir is the data directory the service uses when its
	// configuration does not set one.
	DefaultDataDir string

	// systemdDirs overrides where drop-ins are searched, for tests.
	systemdDirs []string
}

// systemdUnitDirs are the unit directories in systemd's order of precedence.
func systemdUnitDirs() []string {
	return []string{
		"/etc/systemd/system",
		"/run/systemd/system",
		"/usr/local/lib/systemd/system",
		"/usr/lib/systemd/system",
		"/lib/systemd/system",
	}
}

// Detect looks for the service called app: /etc/conf.d/<app> and
// /etc/init.d/<app> for OpenRC, <app>.service in the systemd unit directories.
// /run/openrc/softlevel and /run/systemd/system mark the running manager.
// Name and Prefix are derived from app ("imvault" gives "Imvault" and
// "IMVAULT_"); callers may change them.
func Detect(app, defaultDataDir string) Paths {
	paths := Paths{Name: displayName(app), Prefix: strings.ToUpper(app) + "_", DefaultDataDir: defaultDataDir}
	// A name that is not a single path element would make every path below
	// point somewhere else, so it finds nothing.
	if app == "" || app == "." || app == ".." || strings.ContainsAny(app, "/\x00") {
		return paths
	}
	paths.OpenRCConfig = "/etc/conf.d/" + app
	_, configErr := os.Stat(paths.OpenRCConfig)
	_, initErr := os.Stat("/etc/init.d/" + app)
	// A file that exists but cannot be checked still counts as installed, so
	// that the error surfaces when it is read instead of being ignored.
	paths.OpenRCInstalled = !errors.Is(configErr, os.ErrNotExist) || !errors.Is(initErr, os.ErrNotExist)
	for _, dir := range systemdUnitDirs() {
		if _, err := os.Stat(filepath.Join(dir, app+".service")); err == nil {
			paths.SystemdUnit = filepath.Join(dir, app+".service")
			break
		}
	}
	_, openRCErr := os.Stat("/run/openrc/softlevel")
	_, systemdErr := os.Stat("/run/systemd/system")
	paths.OpenRCActive = openRCErr == nil
	paths.SystemdActive = systemdErr == nil
	return paths
}

func displayName(app string) string {
	if app == "" {
		return app
	}
	return strings.ToUpper(app[:1]) + app[1:]
}

// managers records which service configuration a command follows: the running
// manager's when it is installed, otherwise every installed one.
type managers struct{ openRC, systemd bool }

func (p Paths) managers() managers {
	openRC := p.OpenRCInstalled || p.OpenRCConfig != "" && fileExists(p.OpenRCConfig)
	systemd := p.SystemdUnit != "" && fileExists(p.SystemdUnit)
	switch {
	case p.OpenRCActive && openRC:
		return managers{openRC: true}
	case p.SystemdActive && systemd:
		return managers{systemd: true}
	}
	return managers{openRC: openRC, systemd: systemd}
}

// Managed reports whether a service is installed. A command that refuses to
// run as root can use it to decide whether there is a service account to
// switch to instead.
func (p Paths) Managed() bool {
	m := p.managers()
	return m.openRC || m.systemd
}

// Setting reads one setting as the installed service sees it, ignoring the
// process environment, and returns fallback when the configuration leaves it
// unset or no service is installed. With both managers installed and neither
// running, they must agree; what names the setting in that error, for example
// "Imvault data directories".
func (p Paths) Setting(key, fallback, what string) (string, error) {
	m := p.managers()
	var openRCValue, systemdValue string
	if m.openRC {
		value, _, err := readShellConfigValue(p.OpenRCConfig, key)
		if err != nil {
			return "", err
		}
		openRCValue = cmp.Or(value, fallback)
	}
	if m.systemd {
		value, _, err := p.readSystemdEnvironment(key)
		if err != nil {
			return "", err
		}
		systemdValue = cmp.Or(value, fallback)
	}
	switch {
	case m.openRC && m.systemd && openRCValue != systemdValue:
		return "", fmt.Errorf("OpenRC and systemd configure different %s (%q and %q); set %s explicitly or run under the active service manager", what, openRCValue, systemdValue, key)
	case m.openRC:
		return openRCValue, nil
	case m.systemd:
		return systemdValue, nil
	}
	return fallback, nil
}

// Settings resolves the given settings the way the running service sees them:
// the process environment first, then the service configuration. Unset
// settings are left out.
func (p Paths) Settings(keys ...string) (map[string]string, error) {
	settings := make(map[string]string, len(keys))
	managed := p.Managed()
	for _, key := range keys {
		value := os.Getenv(key)
		if value == "" && managed {
			var err error
			if value, err = p.Setting(key, "", key+" values"); err != nil {
				return nil, fmt.Errorf("%w; set %s in the environment to override the service configuration", err, key)
			}
		}
		if value != "" {
			settings[key] = value
		}
	}
	return settings, nil
}

// DataDir resolves the data directory: envKey from the environment first, then
// the installed service's setting with DefaultDataDir as its default, and
// otherwise "data" in the working directory, as a portable install uses. The
// result is always absolute and clean. A relative path in the service
// configuration is refused, because the service's working directory is not
// this one.
func (p Paths) DataDir(envKey string) (string, error) {
	if value := os.Getenv(envKey); value != "" {
		return filepath.Abs(value)
	}
	if !p.Managed() {
		return filepath.Abs("data")
	}
	value, err := p.Setting(envKey, p.DefaultDataDir, p.Name+" data directories")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("the service's %s data directory %q is not absolute; set %s explicitly", p.Name, value, envKey)
	}
	return filepath.Clean(value), nil
}

// Account reports the user and group the installed service runs as, and
// whether a service is installed at all. OpenRC reads Prefix+"USER" and
// Prefix+"GROUP" from conf.d, each defaulting to defaultUser; systemd reads
// User= and Group=, where an unset User= means root and an unset Group= the
// user's primary group, reported as "".
func (p Paths) Account(defaultUser string) (user, group string, managed bool, err error) {
	m := p.managers()
	if !m.openRC && !m.systemd {
		return "", "", false, nil
	}
	var openRCUser, openRCGroup, systemdUser, systemdGroup string
	if m.openRC {
		if openRCUser, openRCGroup, err = p.openRCAccount(defaultUser); err != nil {
			return "", "", true, err
		}
	}
	if m.systemd {
		if systemdUser, systemdGroup, err = p.systemdAccount(); err != nil {
			return "", "", true, err
		}
	}
	if m.openRC && m.systemd && (openRCUser != systemdUser || openRCGroup != systemdGroup) {
		return "", "", true, fmt.Errorf("OpenRC and systemd configure different %s service accounts (%s:%s and %s:%s); run under the active service manager", p.Name, openRCUser, openRCGroup, systemdUser, systemdGroup)
	}
	if m.openRC {
		return openRCUser, openRCGroup, true, nil
	}
	return systemdUser, systemdGroup, true, nil
}

func (p Paths) openRCAccount(defaultUser string) (string, string, error) {
	user, _, err := readShellConfigValue(p.OpenRCConfig, p.Prefix+"USER")
	if err != nil {
		return "", "", err
	}
	group, _, err := readShellConfigValue(p.OpenRCConfig, p.Prefix+"GROUP")
	if err != nil {
		return "", "", err
	}
	return cmp.Or(user, defaultUser), cmp.Or(group, defaultUser), nil
}

func (p Paths) systemdAccount() (string, string, error) {
	var user, group string
	err := scanSystemdService(p.unitDirs(), p.SystemdUnit, func(name, value, _ string, _ int) error {
		switch name {
		case "User":
			user = strings.Trim(value, "\"'")
		case "Group":
			group = strings.Trim(value, "\"'")
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return cmp.Or(user, "root"), group, nil
}

func (p Paths) unitDirs() []string {
	if p.systemdDirs != nil {
		return p.systemdDirs
	}
	return systemdUnitDirs()
}

func (p Paths) readSystemdEnvironment(key string) (string, bool, error) {
	return readSystemdEnvironment(p.unitDirs(), p.SystemdUnit, key)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
