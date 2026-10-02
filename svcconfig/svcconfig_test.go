// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, contents string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// testPaths is an installation of the application "app" with no drop-in
// directories, so that the host's own systemd configuration never leaks in.
func testPaths() Paths {
	return Paths{Name: "App", Prefix: "APP_", DefaultDataDir: "/var/lib/app", systemdDirs: []string{}}
}

func openRCPaths(t *testing.T, contents string) Paths {
	t.Helper()
	p := testPaths()
	p.OpenRCConfig = writeTestFile(t, filepath.Join(t.TempDir(), "app.confd"), contents)
	p.OpenRCInstalled, p.OpenRCActive = true, true
	return p
}

func systemdPaths(t *testing.T, contents string) Paths {
	t.Helper()
	p := testPaths()
	p.SystemdUnit = writeTestFile(t, filepath.Join(t.TempDir(), "app.service"), contents)
	p.SystemdActive = true
	return p
}

func TestDataDir(t *testing.T) {
	t.Setenv("APP_DATA_DIR", "")
	t.Run("portable default is ./data, made absolute", func(t *testing.T) {
		want, err := filepath.Abs("data")
		if err != nil {
			t.Fatal(err)
		}
		got, err := testPaths().DataDir("APP_DATA_DIR")
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("OpenRC conf.d literal", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "service data")
		got, err := openRCPaths(t, "APP_DATA_DIR=\""+want+"\" # comment\n").DataDir("APP_DATA_DIR")
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("OpenRC default when unset", func(t *testing.T) {
		got, err := openRCPaths(t, "# nothing here\nAPP_OTHER=1\n").DataDir("APP_DATA_DIR")
		if err != nil || got != "/var/lib/app" {
			t.Fatalf("resolve = %q, %v", got, err)
		}
	})
	t.Run("systemd environment file overrides unit", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "systemd data")
		envFile := writeTestFile(t, filepath.Join(t.TempDir(), "app.env"), "APP_DATA_DIR='"+want+"'\n")
		got, err := systemdPaths(t, "[Service]\nEnvironment=APP_DATA_DIR=/var/lib/app\nEnvironmentFile=-"+envFile+"\n").DataDir("APP_DATA_DIR")
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("service value is cleaned", func(t *testing.T) {
		got, err := openRCPaths(t, "APP_DATA_DIR=/srv//app/./data/../data/\n").DataDir("APP_DATA_DIR")
		if err != nil || got != "/srv/app/data" {
			t.Fatalf("resolve = %q, %v", got, err)
		}
	})
	t.Run("relative service value is refused", func(t *testing.T) {
		_, err := openRCPaths(t, "APP_DATA_DIR=data\n").DataDir("APP_DATA_DIR")
		if err == nil || !strings.Contains(err.Error(), "not absolute") || !strings.Contains(err.Error(), "APP_DATA_DIR") {
			t.Fatalf("relative data directory error = %v", err)
		}
	})
	t.Run("explicit environment wins, made absolute and clean", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "explicit")
		t.Setenv("APP_DATA_DIR", want+"/./")
		got, err := openRCPaths(t, "APP_DATA_DIR=/srv/other\n").DataDir("APP_DATA_DIR")
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
		t.Setenv("APP_DATA_DIR", "relative/../dir")
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if got, err := testPaths().DataDir("APP_DATA_DIR"); err != nil || got != filepath.Join(cwd, "dir") {
			t.Fatalf("relative explicit value resolved to %q, %v", got, err)
		}
	})
}

func TestDataDirRefusesAmbiguousOrDynamicConfig(t *testing.T) {
	t.Setenv("APP_DATA_DIR", "")
	p := testPaths()
	p.OpenRCConfig = writeTestFile(t, filepath.Join(t.TempDir(), "openrc.conf"), "APP_DATA_DIR=/var/lib/openrc\n")
	p.OpenRCInstalled = true
	p.SystemdUnit = writeTestFile(t, filepath.Join(t.TempDir(), "app.service"), "[Service]\nEnvironment=APP_DATA_DIR=/var/lib/systemd\n")
	if _, err := p.DataDir("APP_DATA_DIR"); err == nil || !strings.Contains(err.Error(), "different App data directories") {
		t.Fatalf("ambiguous config error = %v", err)
	}
	// The running manager settles it.
	p.SystemdActive = true
	if got, err := p.DataDir("APP_DATA_DIR"); err != nil || got != "/var/lib/systemd" {
		t.Fatalf("active systemd = %q, %v", got, err)
	}
	p.OpenRCActive = true
	if got, err := p.DataDir("APP_DATA_DIR"); err != nil || got != "/var/lib/openrc" {
		t.Fatalf("active OpenRC = %q, %v", got, err)
	}
	p.OpenRCActive, p.SystemdActive = false, false
	writeTestFile(t, p.OpenRCConfig, "APP_DATA_DIR=\"${ROOT}/data\"\n")
	p.SystemdUnit = ""
	if _, err := p.DataDir("APP_DATA_DIR"); err == nil || !strings.Contains(err.Error(), "shell expression") {
		t.Fatalf("dynamic config error = %v", err)
	}
}

// Imvault reads its database path the same way as the data directory, and
// needs to know whether the service set it.
func TestSettingReadsArbitraryKeys(t *testing.T) {
	t.Setenv("APP_DB", "")
	p := openRCPaths(t, "APP_DB=/var/lib/app/custom.db\n")
	if got, err := p.Setting("APP_DB", "", "App database paths"); err != nil || got != "/var/lib/app/custom.db" {
		t.Fatalf("OpenRC database path = %q, %v", got, err)
	}
	envFile := writeTestFile(t, filepath.Join(t.TempDir(), "app.env"), "APP_DB=/srv/app/accounts.db\n")
	p = systemdPaths(t, "[Service]\nEnvironmentFile=-"+envFile+"\n")
	if got, err := p.Setting("APP_DB", "", "App database paths"); err != nil || got != "/srv/app/accounts.db" {
		t.Fatalf("systemd database path = %q, %v", got, err)
	}
	if settings, err := p.Settings("APP_DB"); err != nil || settings["APP_DB"] != "/srv/app/accounts.db" {
		t.Fatalf("settings = %v, %v", settings, err)
	}
	t.Setenv("APP_DB", "/tmp/explicit.db")
	if settings, err := p.Settings("APP_DB"); err != nil || settings["APP_DB"] != "/tmp/explicit.db" {
		t.Fatalf("explicit database path = %v, %v", settings, err)
	}
	// Unset everywhere: absent from Settings, the fallback from Setting.
	t.Setenv("APP_DB", "")
	p = systemdPaths(t, "[Service]\nEnvironment=APP_OTHER=1\n")
	if settings, err := p.Settings("APP_DB"); err != nil || len(settings) != 0 {
		t.Fatalf("unset database path = %v, %v", settings, err)
	}
	if got, err := p.Setting("APP_DB", "fallback.db", "App database paths"); err != nil || got != "fallback.db" {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	if got, err := testPaths().Setting("APP_DB", "fallback.db", "App database paths"); err != nil || got != "fallback.db" {
		t.Fatalf("unmanaged fallback = %q, %v", got, err)
	}
}

func TestAccount(t *testing.T) {
	p := openRCPaths(t, "APP_USER=board-user\nAPP_GROUP=board-group\n")
	user, group, managed, err := p.Account("app")
	if err != nil || !managed || user != "board-user" || group != "board-group" {
		t.Fatalf("service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}
	p = testPaths()
	p.OpenRCInstalled = true
	user, group, managed, err = p.Account("app")
	if err != nil || !managed || user != "app" || group != "app" {
		t.Fatalf("default service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}
	user, group, managed, err = testPaths().Account("app")
	if err != nil || managed || user != "" || group != "" {
		t.Fatalf("portable install identity = %q:%q managed=%t err=%v", user, group, managed, err)
	}
	p = systemdPaths(t, "[Unit]\nUser=ignored\n[Service]\nUser=\"svc\"\nGroup='svcgroup'\n")
	user, group, managed, err = p.Account("app")
	if err != nil || !managed || user != "svc" || group != "svcgroup" {
		t.Fatalf("systemd account = %q:%q managed=%t err=%v", user, group, managed, err)
	}
	// systemd runs a unit without User= as root.
	p = systemdPaths(t, "[Service]\nExecStart=/usr/bin/app\n")
	if user, group, _, err = p.Account("app"); err != nil || user != "root" || group != "" {
		t.Fatalf("systemd default account = %q:%q err=%v", user, group, err)
	}
	p.OpenRCConfig = writeTestFile(t, filepath.Join(t.TempDir(), "app.confd"), "")
	p.OpenRCInstalled, p.SystemdActive = true, false
	if _, _, _, err = p.Account("app"); err == nil || !strings.Contains(err.Error(), "different App service accounts") {
		t.Fatalf("disagreeing accounts error = %v", err)
	}
}

func TestManaged(t *testing.T) {
	if testPaths().Managed() {
		t.Fatal("nothing installed, yet managed")
	}
	p := testPaths()
	p.SystemdUnit = filepath.Join(t.TempDir(), "missing.service")
	p.OpenRCConfig = filepath.Join(t.TempDir(), "missing.confd")
	if p.Managed() {
		t.Fatal("missing files counted as an installed service")
	}
	if !openRCPaths(t, "").Managed() || !systemdPaths(t, "").Managed() {
		t.Fatal("installed service not detected")
	}
	p = testPaths()
	p.OpenRCInstalled = true
	if !p.Managed() {
		t.Fatal("an OpenRC init script alone did not count")
	}
}

// systemd reads EnvironmentFile= as one literal path, spaces and all.
func TestSystemdEnvironmentFilePathsAreLiteral(t *testing.T) {
	t.Setenv("APP_DATA_DIR", "")
	root := t.TempDir()
	want := filepath.Join(root, "board data")
	for _, name := range []string{"app env", "  leading and trailing  ", "quoted \"name\"", "single 'name'", "semi;colon", "-dash", "日本語 設定", "back\\slash", "$HOME", "tab\there"} {
		t.Run(name, func(t *testing.T) {
			envFile := writeTestFile(t, filepath.Join(root, name, "app.env"), "APP_DATA_DIR='"+want+"'\n")
			p := systemdPaths(t, "[Service]\nEnvironmentFile=-"+envFile+"   \n")
			got, err := p.DataDir("APP_DATA_DIR")
			if err != nil || got != want {
				t.Fatalf("resolve = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestSystemdEnvironmentFileRejectsHostilePaths(t *testing.T) {
	t.Setenv("APP_DATA_DIR", "")
	root := t.TempDir()
	present := writeTestFile(t, filepath.Join(root, "present.env"), "APP_DATA_DIR=/srv/present\n")
	for _, tc := range []struct{ setting, want string }{
		{"relative/app.env", "not a literal absolute path"},
		{"-relative.env", "not a literal absolute path"},
		{"--" + present, "not a literal absolute path"},
		{"%h/app.env", "not a literal absolute path"},
		{"/etc/%i.env", "not a literal absolute path"},
		{present + "%", "not a literal absolute path"},
		{"/etc/app\x00.env", "not a literal absolute path"},
		{`"` + present + `"`, "not a literal absolute path"},
		{"'" + present + "'", "not a literal absolute path"},
		{present + " " + present, "no such file"},
		{filepath.Join(root, "missing.env"), "no such file"},
		{root, "is a directory"},
	} {
		p := systemdPaths(t, "[Service]\nEnvironmentFile="+tc.setting+"\n")
		if got, err := p.DataDir("APP_DATA_DIR"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("EnvironmentFile=%s resolved to %q (%v); want an error containing %q", tc.setting, got, err, tc.want)
		}
	}
	// An optional missing file is skipped, and a later empty setting resets
	// the list the way systemd does.
	p := systemdPaths(t, "[Service]\nEnvironment=APP_DATA_DIR=/srv/unit\nEnvironmentFile=-"+filepath.Join(root, "missing.env")+"\nEnvironmentFile="+present+"\nEnvironmentFile=\n")
	if got, err := p.DataDir("APP_DATA_DIR"); err != nil || got != "/srv/unit" {
		t.Fatalf("resolve = %q, %v; want /srv/unit", got, err)
	}
	// On its own, an optional missing file is skipped rather than an error.
	p = systemdPaths(t, "[Service]\nEnvironment=APP_DATA_DIR=/srv/unit\nEnvironmentFile=-"+filepath.Join(root, "missing.env")+"\n")
	if got, err := p.DataDir("APP_DATA_DIR"); err != nil || got != "/srv/unit" {
		t.Fatalf("optional missing file: resolve = %q, %v; want /srv/unit", got, err)
	}
	// A symlink is followed like any path; it is the operator's own file.
	link := filepath.Join(root, "link.env")
	if err := os.Symlink(present, link); err != nil {
		t.Fatal(err)
	}
	p = systemdPaths(t, "[Service]\nEnvironmentFile="+link+"\n")
	if got, err := p.DataDir("APP_DATA_DIR"); err != nil || got != "/srv/present" {
		t.Fatalf("resolve through symlink = %q, %v", got, err)
	}
}

func TestEnvironmentFilePath(t *testing.T) {
	for setting, want := range map[string]struct {
		path     string
		optional bool
		ok       bool
	}{
		"/etc/app.env":            {"/etc/app.env", false, true},
		"-/etc/app.env":           {"/etc/app.env", true, true},
		"/etc/app env/with space": {"/etc/app env/with space", false, true},
		"/etc/../etc/app.env":     {"/etc/../etc/app.env", false, true},
		"--/etc/app.env":          {"", false, false},
		"- /etc/app.env":          {"", false, false},
		"etc/app.env":             {"", false, false},
		"":                        {"", false, false},
		"-":                       {"", false, false},
		"/etc/%n.env":             {"", false, false},
		"/etc/app\x00":            {"", false, false},
		"~/app.env":               {"", false, false},
	} {
		path, optional, ok := environmentFilePath(setting)
		if path != want.path || optional != want.optional || ok != want.ok {
			t.Errorf("environmentFilePath(%q) = %q %t %t, want %+v", setting, path, optional, ok, want)
		}
	}
}

func TestSettingsFollowTheServiceConfiguration(t *testing.T) {
	keys := []string{"APP_BASE_URL", "APP_SMTP_HOST", "APP_SMTP_FROM", "APP_NAME"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	envFile := writeTestFile(t, filepath.Join(t.TempDir(), "with space", "app.env"), "APP_BASE_URL=https://board.example.org\nAPP_SMTP_HOST=relay.example.org\nAPP_SMTP_FROM='Board <mail@example.org>'\n")
	p := systemdPaths(t, "[Service]\nEnvironment=\"APP_NAME=Our Board\"\nEnvironmentFile="+envFile+"\n")
	settings, err := p.Settings(keys...)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"APP_BASE_URL": "https://board.example.org", "APP_SMTP_HOST": "relay.example.org", "APP_SMTP_FROM": "Board <mail@example.org>", "APP_NAME": "Our Board"}
	if fmt.Sprint(settings) != fmt.Sprint(want) {
		t.Fatalf("settings = %v, want %v", settings, want)
	}
	t.Setenv("APP_BASE_URL", "https://override.example.org")
	if settings, err := p.Settings("APP_BASE_URL"); err != nil || settings["APP_BASE_URL"] != "https://override.example.org" {
		t.Fatalf("environment did not win: %v %v", settings, err)
	}
	if settings, err := testPaths().Settings("APP_SMTP_HOST"); err != nil || len(settings) != 0 {
		t.Fatalf("portable install read service settings: %v %v", settings, err)
	}
	broken := systemdPaths(t, "[Service]\nEnvironment=\"APP_SMTP_HOST=unterminated\n")
	if _, err := broken.Settings("APP_SMTP_HOST"); err == nil || !strings.Contains(err.Error(), "set APP_SMTP_HOST in the environment") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestSystemdDropInsApplyInOrder(t *testing.T) {
	t.Setenv("APP_DATA_DIR", "")
	etc, lib := t.TempDir(), t.TempDir()
	p := systemdPaths(t, "[Service]\nEnvironment=APP_DATA_DIR=/srv/unit APP_OTHER=1\nUser=unit\n")
	p.systemdDirs = []string{etc, lib}
	name := filepath.Base(p.SystemdUnit) + ".d"
	writeTestFile(t, filepath.Join(lib, name, "10-lib.conf"), "[Service]\nEnvironment=APP_DATA_DIR=/srv/lib\nUser=lib\n")
	writeTestFile(t, filepath.Join(lib, name, "20-hidden.conf"), "[Service]\nEnvironment=APP_DATA_DIR=/srv/hidden\n")
	writeTestFile(t, filepath.Join(etc, name, "20-hidden.conf"), "[Service]\nUser=etc\n")
	writeTestFile(t, filepath.Join(etc, name, "05-first.conf"), "[Service]\nEnvironment=APP_DATA_DIR=/srv/first\n")
	writeTestFile(t, filepath.Join(etc, name, "99-ignored.txt"), "[Service]\nEnvironment=APP_DATA_DIR=/srv/ignored\n")
	if err := os.MkdirAll(filepath.Join(etc, name, "30-dir.conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := p.DataDir("APP_DATA_DIR"); err != nil || got != "/srv/lib" {
		t.Fatalf("drop-in order gave %q, %v", got, err)
	}
	if user, _, _, err := p.Account("app"); err != nil || user != "etc" {
		t.Fatalf("drop-in account = %q, %v", user, err)
	}
	// An empty Environment= clears everything set before it.
	writeTestFile(t, filepath.Join(etc, name, "50-reset.conf"), "[Service]\nEnvironment=\n")
	if got, err := p.DataDir("APP_DATA_DIR"); err != nil || got != "/var/lib/app" {
		t.Fatalf("reset gave %q, %v", got, err)
	}
	var visited []string
	if err := scanSystemdService(p.systemdDirs, p.SystemdUnit, func(name, value, path string, line int) error {
		visited = append(visited, filepath.Base(path)+":"+name)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := "app.service:Environment app.service:User 05-first.conf:Environment 10-lib.conf:Environment 10-lib.conf:User 20-hidden.conf:User 50-reset.conf:Environment"
	if strings.Join(visited, " ") != want {
		t.Fatalf("visited %q\nwant    %q", strings.Join(visited, " "), want)
	}
}

func TestUnreadableConfigurationIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	t.Setenv("APP_DATA_DIR", "")
	p := openRCPaths(t, "APP_DATA_DIR=/srv/secret\n")
	if err := os.Chmod(p.OpenRCConfig, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DataDir("APP_DATA_DIR"); err == nil || !strings.Contains(err.Error(), "set APP_DATA_DIR explicitly") {
		t.Fatalf("unreadable conf.d error = %v", err)
	}
	p = systemdPaths(t, "[Service]\n")
	if err := os.Chmod(p.SystemdUnit, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DataDir("APP_DATA_DIR"); err == nil {
		t.Fatal("unreadable unit accepted")
	}
}

func TestOverlongLinesAreAnError(t *testing.T) {
	t.Setenv("APP_DATA_DIR", "")
	long := "APP_DATA_DIR=/" + strings.Repeat("a", 70*1024) + "\n"
	if _, err := openRCPaths(t, long).DataDir("APP_DATA_DIR"); err == nil {
		t.Fatal("an overlong conf.d line was accepted")
	}
	if _, err := systemdPaths(t, "[Service]\nEnvironment="+long).DataDir("APP_DATA_DIR"); err == nil {
		t.Fatal("an overlong unit line was accepted")
	}
}

func TestDetect(t *testing.T) {
	p := Detect("comfylib-test-no-such-app", "/var/lib/none")
	if p.Name != "Comfylib-test-no-such-app" || p.Prefix != "COMFYLIB-TEST-NO-SUCH-APP_" || p.DefaultDataDir != "/var/lib/none" {
		t.Fatalf("derived names = %+v", p)
	}
	if p.Managed() || p.OpenRCInstalled || p.SystemdUnit != "" {
		t.Fatalf("an absent service was detected: %+v", p)
	}
	if p.OpenRCConfig != "/etc/conf.d/comfylib-test-no-such-app" {
		t.Fatalf("conf.d path = %q", p.OpenRCConfig)
	}
	for _, app := range []string{"", ".", "..", "../etc/passwd", "a/b", "nul\x00"} {
		if p := Detect(app, "/var/lib/x"); p.OpenRCConfig != "" || p.SystemdUnit != "" || p.Managed() {
			t.Errorf("Detect(%q) looked at %+v", app, p)
		}
	}
	if Detect("imvault", "").Name != "Imvault" || Detect("witmoot", "").Prefix != "WITMOOT_" {
		t.Fatal("names are not derived from the app")
	}
}

func TestParseShellValue(t *testing.T) {
	for raw, want := range map[string]string{
		``:                    "",
		`/var/lib/app`:        "/var/lib/app",
		`"/srv/with space"`:   "/srv/with space",
		`'/srv/$literal'`:     "/srv/$literal",
		`"a\$b"`:              "a$b",
		`"a\b"`:               `a\b`,
		`"a\\b"`:              `a\b`,
		`a\ b`:                "a b",
		`'it'\''s'`:           "it's",
		`value # comment`:     "value",
		`value	# tab comment`: "value",
		`"quoted" # comment`:  "quoted",
		`value#not-comment`:   "value#not-comment",
		`#not-comment`:        "#not-comment",
		` # only a comment`:   "",
		`"  padded  "`:        "  padded  ",
		`a"b"'c'`:             "abc",
		`{a,b}*?[x]!%=`:       "{a,b}*?[x]!%=",
		"\xff\xfe-not-utf8":   "\xff\xfe-not-utf8",
		`"` + "\xc3" + `"`:    "\xc3",
		`日本語`:                 "日本語",
		`"~/quoted"`:          "~/quoted",
		`'a;b|c&d<e>f(g)h'`:   "a;b|c&d<e>f(g)h",
	} {
		got, err := ParseShellValue(raw)
		if err != nil || got != want {
			t.Errorf("ParseShellValue(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{`$HOME`, `${ROOT}/data`, `"$HOME"`, "`id`", "\"`id`\"", `a;b`, `a|b`, `a&b`, `a>b`, `a<b`, `$(id)`, `(a)`, `~/data`, `a:~/b`, `a b`, " leading", `"unterminated`, `'unterminated`, `trailing\`, "nul\x00byte"} {
		if got, err := ParseShellValue(raw); err == nil {
			t.Errorf("ParseShellValue(%q) = %q, want an error", raw, got)
		}
	}
}

func TestEnvironmentFileValues(t *testing.T) {
	for raw, want := range map[string]string{
		`/srv/app`:          "/srv/app",
		`  /srv/app  `:      "/srv/app",
		`"/srv/with space"`: "/srv/with space",
		`/srv/with space`:   "/srv/with space",
		`$HOME/literal`:     "$HOME/literal",
		`a;b`:               "a;b",
		`value # comment`:   "value",
		`#comment`:          "",
		`'single'`:          "single",
		`a\"b`:              `a"b`,
	} {
		got, err := parseValue(raw, false)
		if err != nil || got != want {
			t.Errorf("parseValue(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{`"unterminated`, `trailing\`, "nul\x00"} {
		if _, err := parseValue(raw, false); err == nil {
			t.Errorf("parseValue(%q) accepted", raw)
		}
	}
}

func TestShellAssignmentsNeedTheExactName(t *testing.T) {
	t.Setenv("APP_DATA_DIR", "")
	for contents, want := range map[string]string{
		"APP_DATA_DIR=/srv/a\n":                      "/srv/a",
		"export APP_DATA_DIR=/srv/a\n":               "/srv/a",
		"  export   APP_DATA_DIR=/srv/a  \n":         "/srv/a",
		"APP_DATA_DIR=/srv/a\nAPP_DATA_DIR=/srv/b\n": "/srv/b",
		"APP_DATA_DIR = /srv/a\n":                    "/var/lib/app",
		"APP_DATA_DIR_OLD=/srv/a\n":                  "/var/lib/app",
		"#APP_DATA_DIR=/srv/a\n":                     "/var/lib/app",
		"MY_APP_DATA_DIR=/srv/a\n":                   "/var/lib/app",
		"APP_DATA_DIR=/srv/a\r\n":                    "/srv/a",
		"APP_DATA_DIR=\n":                            "/var/lib/app",
	} {
		if got, err := openRCPaths(t, contents).DataDir("APP_DATA_DIR"); err != nil || got != want {
			t.Errorf("%q resolved to %q, %v; want %q", contents, got, err, want)
		}
	}
}
