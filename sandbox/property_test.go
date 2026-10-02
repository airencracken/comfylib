// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// randomService builds a valid policy request with random settings, random
// extra directories and files, and secret values that must stay out of argv.
func randomService(t *testing.T, r *rand.Rand) (Service, []string) {
	t.Helper()
	root := t.TempDir()
	names := []string{"plain", "with space", "quote ' \"", "dash -x", "日本", "pct %i", "semi;colon"}
	dir := func(i int) string {
		path := filepath.Join(root, fmt.Sprintf("%d %s", i, names[r.IntN(len(names))]))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	s := Service{Prefix: "APP_", DataDir: dir(0), Executable: "/usr/bin/true", NestedSandbox: r.IntN(2) == 0}
	for i := range r.IntN(3) {
		s.WriteDirs = append(s.WriteDirs, dir(i+1))
	}
	for i := range r.IntN(3) {
		path := filepath.Join(root, fmt.Sprintf("file %d", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		s.ReadFiles = append(s.ReadFiles, path)
	}
	candidates := []string{"TZ", "AWS_SECRET_ACCESS_KEY", "OTHER_TOKEN", "LD_PRELOAD", "PATH", "HOME", "APP_DATA_DIR", "APP_PASSWORD", "APP_X", "APPX", "SSL_CERT_FILE_X"}
	for _, name := range []string{"TZ", "AWS_SECRET_ACCESS_KEY", "OTHER_TOKEN", "SSL_CERT_FILE_X"} {
		if r.IntN(2) == 0 {
			s.ForwardEnv = append(s.ForwardEnv, name)
		}
	}
	var secrets []string
	for range r.IntN(12) {
		secret := fmt.Sprintf("secret-%016x", r.Uint64())
		secrets = append(secrets, secret)
		s.Env = append(s.Env, candidates[r.IntN(len(candidates))]+"="+secret)
	}
	return s, secrets
}

func TestPolicyInvariants(t *testing.T) {
	seed := rand.Uint64()
	t.Logf("seed %d", seed)
	r := rand.New(rand.NewPCG(seed, 0))
	for range 200 {
		s, secrets := randomService(t, r)
		args, env, err := s.Policy()
		if err != nil {
			t.Fatalf("valid policy %+v refused: %v", s, err)
		}
		checkPolicy(t, s, secrets, args, env)
	}
}

func checkPolicy(t *testing.T, s Service, secrets, args, env []string) {
	t.Helper()
	// The data directory and the listed extra directories are the only
	// writable binds, in that order.
	if got, want := writableBinds(args), append([]string{s.DataDir}, s.WriteDirs...); !slices.Equal(got, want) {
		t.Fatalf("writable binds %q, want %q", got, want)
	}
	// No secret value appears among the arguments.
	joined := strings.Join(args, "\x00")
	for _, secret := range secrets {
		if strings.Contains(joined, secret) {
			t.Fatalf("secret %s reached argv", secret)
		}
	}
	// User namespaces are disabled unless the server builds its own sandboxes.
	if slices.Contains(args, "--disable-userns") == s.NestedSandbox {
		t.Fatalf("--disable-userns with NestedSandbox=%t: %q", s.NestedSandbox, args)
	}
	// The server keeps the network.
	if slices.Contains(args, "--unshare-net") {
		t.Fatal("service policy dropped the network")
	}
	checkEnvironment(t, s, env)
}

// checkEnvironment asserts that each variable appears once, that the sandbox's
// own values win, and that only prefixed or listed variables are forwarded.
func checkEnvironment(t *testing.T, s Service, env []string) {
	t.Helper()
	runtime := RuntimeEnv()
	seen := map[string]int{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("malformed entry %q", entry)
		}
		seen[key]++
		switch {
		case slices.Contains(runtime, entry), entry == "SSL_CERT_FILE=/app/ca-bundle.crt":
		case key == s.Prefix+"DATA_DIR":
			if value != s.DataDir {
				t.Fatalf("data directory variable %q", entry)
			}
		case strings.HasPrefix(key, s.Prefix), slices.Contains(s.ForwardEnv, key):
		default:
			t.Fatalf("unlisted variable forwarded: %q", entry)
		}
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", s.Prefix + "DATA_DIR"} {
		if seen[key] != 1 {
			t.Fatalf("%s set %d times in %q", key, seen[key], env)
		}
	}
	if seen["LD_PRELOAD"] != 0 {
		t.Fatal("loader variable forwarded")
	}
}

// Base's own arguments are well formed: every mount option has its operands,
// and network isolation always comes with --die-with-parent.
func TestBaseInvariants(t *testing.T) {
	for _, network := range []bool{false, true} {
		args, err := Base(Options{Network: network})
		if err != nil {
			t.Fatal(err)
		}
		operands := map[string]int{"--ro-bind": 2, "--symlink": 2, "--bind": 2, "--proc": 1, "--dev": 1, "--tmpfs": 1, "--dir": 1, "--cap-drop": 1}
		for i := 0; i < len(args); i++ {
			n, isMount := operands[args[i]]
			if !isMount {
				if !strings.HasPrefix(args[i], "--") {
					t.Fatalf("stray operand %q at %d in %q", args[i], i, args)
				}
				continue
			}
			for j := 1; j <= n; j++ {
				if i+j >= len(args) || strings.HasPrefix(args[i+j], "--") {
					t.Fatalf("%s lacks operands in %q", args[i], args)
				}
			}
			i += n
		}
		if slices.Contains(args, "--unshare-net") != !network || slices.Contains(args, "--die-with-parent") != !network {
			t.Fatalf("network=%t: %q", network, args)
		}
	}
}
