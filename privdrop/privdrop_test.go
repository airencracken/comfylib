// SPDX-License-Identifier: AGPL-3.0-or-later

package privdrop

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/airencracken/comfylib/svcconfig"
)

func TestWithEnvironmentReplacesOnlyTheConfiguredKeys(t *testing.T) {
	got := WithEnvironment([]string{"PATH=/bin", "APP_DATA_DIR=/wrong", "APP_DATA_DIR_EXTRA=kept", "HOME=/root", "APP_BASE_URL=https://old.example.org", "NOEQUALS", "APP_DATA_DIR=/also-wrong"},
		map[string]string{"APP_DATA_DIR": "/var/lib/app", "APP_BASE_URL": "https://board.example.org", "APP_SMTP_HOST": ""})
	want := []string{"PATH=/bin", "APP_DATA_DIR_EXTRA=kept", "HOME=/root", "NOEQUALS", "APP_BASE_URL=https://board.example.org", "APP_DATA_DIR=/var/lib/app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
	// Imvault's single-key case.
	got = WithEnvironment([]string{"PATH=/bin", "APP_DATA_DIR=/wrong", "HOME=/root"}, map[string]string{"APP_DATA_DIR": "/var/lib/app"})
	if want := []string{"PATH=/bin", "HOME=/root", "APP_DATA_DIR=/var/lib/app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
	if got := WithEnvironment(nil, nil); len(got) != 0 {
		t.Fatalf("empty environment = %#v", got)
	}
}

func TestHasHelpFlag(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"-help"}, {"--h"}, {"--output", "x", "-h"}} {
		if !HasHelpFlag(args) {
			t.Errorf("%q is a help request", args)
		}
	}
	for _, args := range [][]string{nil, {}, {"--", "--help"}, {"--helpful"}, {"-hh"}, {"--output", "--help.txt"}} {
		if HasHelpFlag(args) {
			t.Errorf("%q is not a help request", args)
		}
	}
}

func TestRefuseRoot(t *testing.T) {
	if err := RefuseRoot(1000, "backup", "sudo -u app app backup"); err != nil {
		t.Fatal(err)
	}
	err := RefuseRoot(0, "backup", "sudo -u app env APP_DATA_DIR=/var/lib/app app backup")
	if err == nil || !strings.Contains(err.Error(), "backup writes the instance's files and must not run as root") || !strings.HasSuffix(err.Error(), "for example: sudo -u app env APP_DATA_DIR=/var/lib/app app backup") {
		t.Fatalf("root refusal = %v", err)
	}
}

// accounts is a fake user database.
func accounts() lookup {
	users := map[string]*user.User{
		"app":       {Username: "app", Uid: "990", Gid: "990"},
		"rootish":   {Username: "rootish", Uid: "0", Gid: "990"},
		"badid":     {Username: "badid", Uid: "x", Gid: "990"},
		"rootgroup": {Username: "rootgroup", Uid: "991", Gid: "0"},
		"wheel":     {Username: "wheel", Uid: "992", Gid: "992"},
		"badgroups": {Username: "badgroups", Uid: "993", Gid: "993"},
		"nogroups":  {Username: "nogroups", Uid: "994", Gid: "994"},
	}
	groups := map[string]*user.Group{
		"app": {Name: "app", Gid: "990"}, "990": {Name: "app", Gid: "990"},
		"root": {Name: "root", Gid: "0"}, "0": {Name: "root", Gid: "0"},
		"992": {Name: "wheel", Gid: "992"}, "993": {Name: "bad", Gid: "993"},
		"broken": {Name: "broken", Gid: "gid"}, "994": {Name: "nogroups", Gid: "994"},
	}
	supplementary := map[string][]string{"app": {"990", "44"}, "rootish": {"990"}, "rootgroup": {"990"}, "wheel": {"992", "0"}, "badgroups": {"993", "x"}}
	return lookup{
		user: func(name string) (*user.User, error) {
			if u, ok := users[name]; ok {
				return u, nil
			}
			return nil, user.UnknownUserError(name)
		},
		group: func(name string) (*user.Group, error) {
			if g, ok := groups[name]; ok {
				return g, nil
			}
			return nil, user.UnknownGroupError(name)
		},
		groupIDs: func(u *user.User) ([]string, error) {
			if ids, ok := supplementary[u.Username]; ok {
				return ids, nil
			}
			return nil, errors.New("no group database")
		},
	}
}

func TestCredential(t *testing.T) {
	l := accounts()
	got, err := l.credential("app", "")
	if err != nil || got.Uid != 990 || got.Gid != 990 || !slices.Equal(got.Groups, []uint32{990, 44}) {
		t.Fatalf("credential = %+v, %v", got, err)
	}
	if got, err := l.credential("app", "app"); err != nil || got.Gid != 990 {
		t.Fatalf("named group = %+v, %v", got, err)
	}
	// A numeric GID with no group of that name is still usable.
	if got, err := l.credential("app", "4242"); err != nil || got.Gid != 4242 {
		t.Fatalf("numeric group = %+v, %v", got, err)
	}
	for _, tc := range []struct{ user, group, want string }{
		{"missing", "", "look up service user"},
		{"rootish", "", "non-root numeric UID"},
		{"badid", "", "non-root numeric UID"},
		{"rootgroup", "", "non-root numeric GID"},
		{"app", "root", "non-root numeric GID"},
		{"app", "0", "non-root numeric GID"},
		{"app", "-1", "look up service group"},
		{"app", "no-such-group", "look up service group"},
		{"app", "broken", "invalid group ID"},
		{"app", "99999999999", "look up service group"},
		{"wheel", "", "belongs to the root group"},
		{"badgroups", "", "invalid supplementary group ID"},
		{"nogroups", "", "look up groups"},
	} {
		if got, err := l.credential(tc.user, tc.group); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("credential(%q, %q) = %+v, %v; want an error containing %q", tc.user, tc.group, got, err, tc.want)
		}
	}
}

// The real account database: root is always refused.
func TestCredentialRefusesTheRealRoot(t *testing.T) {
	if _, err := Credential("root", ""); err == nil {
		t.Fatal("root accepted as a service account")
	}
	if _, err := Credential("root", "root"); err == nil {
		t.Fatal("root accepted as a service account")
	}
}

// installed returns a request for an OpenRC service whose conf.d sets the
// given contents, as root, with every system call faked.
func installed(t *testing.T, contents string) (Request, *[]*exec.Cmd) {
	t.Helper()
	t.Setenv("APP_DATA_DIR", "")
	config := filepath.Join(t.TempDir(), "app.confd")
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	var ran []*exec.Cmd
	return Request{
		Args:        []string{"backup", "--output", "/tmp/out"},
		Commands:    map[string]bool{"backup": true, "create-admin": true},
		Paths:       svcconfig.Paths{Name: "App", Prefix: "APP_", OpenRCConfig: config, OpenRCInstalled: true, OpenRCActive: true, DefaultDataDir: "/var/lib/app"},
		DefaultUser: "app",
		hooks: hooks{
			geteuid:    func() int { return 0 },
			lookup:     accounts(),
			executable: func() (string, error) { return "/usr/bin/app", nil },
			run: func(cmd *exec.Cmd) error {
				ran = append(ran, cmd)
				return nil
			},
		},
	}, &ran
}

func TestReexecRunsTheCommandAsTheServiceAccount(t *testing.T) {
	r, ran := installed(t, "APP_DATA_DIR=/srv/app\n")
	r.Settings = func(command, dataDir string) (map[string]string, error) {
		if command != "backup" || dataDir != "/srv/app" {
			t.Errorf("settings asked for %q in %q", command, dataDir)
		}
		return map[string]string{"APP_DB": filepath.Join(dataDir, "app.db"), "APP_DATA_DIR": "/not/this"}, nil
	}
	t.Setenv("APP_DB", "/stale.db")
	handled, status, err := Reexec(r)
	if !handled || status != 0 || err != nil || len(*ran) != 1 {
		t.Fatalf("Reexec = %t %d %v after %d runs", handled, status, err, len(*ran))
	}
	cmd := (*ran)[0]
	if cmd.Path != "/usr/bin/app" || !slices.Equal(cmd.Args, []string{"/usr/bin/app", "backup", "--output", "/tmp/out"}) {
		t.Fatalf("ran %q %q", cmd.Path, cmd.Args)
	}
	if c := cmd.SysProcAttr.Credential; c == nil || c.Uid != 990 || c.Gid != 990 || !slices.Equal(c.Groups, []uint32{990, 44}) {
		t.Fatalf("credential = %+v", cmd.SysProcAttr.Credential)
	}
	if !slices.Contains(cmd.Env, "APP_DATA_DIR=/srv/app") || !slices.Contains(cmd.Env, "APP_DB=/srv/app/app.db") || slices.Contains(cmd.Env, "APP_DB=/stale.db") || slices.Contains(cmd.Env, "APP_DATA_DIR=/not/this") {
		t.Fatalf("child environment = %q", cmd.Env)
	}
	if cmd.Stdin != os.Stdin || cmd.Stdout != os.Stdout || cmd.Stderr != os.Stderr {
		t.Fatal("the child is not attached to the terminal")
	}
}

func TestReexecPassesTheExitStatusOn(t *testing.T) {
	r, _ := installed(t, "")
	r.hooks.run = func(*exec.Cmd) error { return exec.Command("sh", "-c", "exit 3").Run() }
	if handled, status, err := Reexec(r); !handled || status != 3 || err != nil {
		t.Fatalf("Reexec = %t %d %v", handled, status, err)
	}
	r.hooks.run = func(*exec.Cmd) error { return syscall.EPERM }
	if handled, status, err := Reexec(r); !handled || status != 1 || err == nil || !strings.Contains(err.Error(), `run backup as App service user "app"`) {
		t.Fatalf("Reexec = %t %d %v", handled, status, err)
	}
	r.hooks.executable = func() (string, error) { return "", errors.New("gone") }
	if handled, status, err := Reexec(r); !handled || status != 1 || err == nil {
		t.Fatalf("Reexec = %t %d %v", handled, status, err)
	}
}

func TestReexecLeavesOtherCasesToTheCaller(t *testing.T) {
	r, ran := installed(t, "")
	cases := map[string]func(*Request){
		"not root":          func(r *Request) { r.hooks.geteuid = func() int { return 1000 } },
		"other command":     func(r *Request) { r.Args = []string{"restore"} },
		"no command":        func(r *Request) { r.Args = nil },
		"help":              func(r *Request) { r.Args = []string{"backup", "--help"} },
		"short help":        func(r *Request) { r.Args = []string{"create-admin", "-h"} },
		"no service":        func(r *Request) { r.Paths = svcconfig.Paths{Name: "App", Prefix: "APP_"} },
		"command is a flag": func(r *Request) { r.Args = []string{"--help", "backup"} },
	}
	for name, mutate := range cases {
		request := r
		mutate(&request)
		if handled, status, err := Reexec(request); handled || status != 0 || err != nil {
			t.Errorf("%s: Reexec = %t %d %v", name, handled, status, err)
		}
	}
	if len(*ran) != 0 {
		t.Fatalf("ran %d children", len(*ran))
	}
}

func TestReexecRefusesUnsafeAccounts(t *testing.T) {
	for contents, want := range map[string]string{
		"APP_USER=root\n":                 "configured App service user is root",
		"APP_USER=rootish\n":              "non-root numeric UID",
		"APP_USER=app\nAPP_GROUP=root\n":  "non-root numeric GID",
		"APP_USER=wheel\nAPP_GROUP=992\n": "belongs to the root group",
		"APP_USER=nobody-here\n":          "look up service user",
		"APP_USER=$(id -un)\n":            "shell expression",
		"APP_DATA_DIR=relative\n":         "not absolute",
	} {
		r, ran := installed(t, contents)
		handled, status, err := Reexec(r)
		if !handled || status != 1 || err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: Reexec = %t %d %v; want an error containing %q", contents, handled, status, err, want)
		}
		if len(*ran) != 0 {
			t.Errorf("%q: ran a child anyway", contents)
		}
	}
	r, ran := installed(t, "")
	r.Settings = func(string, string) (map[string]string, error) { return nil, errors.New("settings failed") }
	if handled, status, err := Reexec(r); !handled || status != 1 || err == nil || len(*ran) != 0 {
		t.Fatalf("settings failure: Reexec = %t %d %v", handled, status, err)
	}
}
