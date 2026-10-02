// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/airencracken/comfylib/sandbox"
)

// A server confines itself: it builds its policy from its own environment,
// proves the namespaces work by running itself inside them, then supervises
// the confined copy until SIGTERM.
func Example_server() {
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	policy := sandbox.Service{
		Prefix:     "APP_",
		DataDir:    "/var/lib/app",
		Executable: executable,
		Env:        os.Environ(),
		// Variables outside the prefix the server still needs.
		ForwardEnv: []string{"TZ"},
	}
	args, env, err := policy.Policy()
	if err != nil {
		log.Fatal(err)
	}
	bwrap, err := sandbox.Binary("bwrap")
	if err != nil {
		log.Fatal(err)
	}
	if err := sandbox.Check(context.Background(), bwrap, args, env, "/app/server", "--help"); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := sandbox.Run(ctx, bwrap, append(args, "--", "/app/server", "serve"), env); err != nil {
		log.Fatal(err)
	}
}

// Inside the confined server, which is PID 1 of its namespace, children run
// in a policy without network and are started through RunChild so the reaper
// never takes them.
func Example_child() {
	ctx := context.Background()
	sandbox.StartReaper(ctx)
	bwrap, err := sandbox.Binary("bwrap")
	if err != nil {
		log.Fatal(err)
	}
	args, err := sandbox.Base(sandbox.Options{Network: false})
	if err != nil {
		log.Fatal(err)
	}
	converter := "/usr/bin/ffmpeg"
	if err := sandbox.Check(ctx, bwrap, args, sandbox.RuntimeEnv(), converter, "-version"); err != nil {
		log.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bwrap, append(args, "--", converter, "-i", "/tmp/in")...)
	cmd.Env = sandbox.RuntimeEnv()
	if err := sandbox.RunChild(cmd); err != nil {
		log.Fatal(err)
	}
}

// The server receives its own settings and the listed variables, never the
// rest of the launcher's environment, and its data directory is the one
// writable host path.
func ExampleService_Policy() {
	root, err := os.MkdirTemp("", "example")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	data := filepath.Join(root, "data")
	bundle := filepath.Join(root, "ca.pem")
	if err := os.Mkdir(data, 0o700); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(bundle, nil, 0o600); err != nil {
		log.Fatal(err)
	}
	args, env, err := sandbox.Service{
		Prefix:     "APP_",
		DataDir:    data,
		Executable: "/bin/sh",
		ForwardEnv: []string{"TZ"},
		Env:        []string{"APP_SMTP_PASSWORD=secret", "TZ=UTC", "GITHUB_TOKEN=unrelated", "SSL_CERT_FILE=" + bundle},
	}.Policy()
	if err != nil {
		log.Fatal(err)
	}
	for _, entry := range env {
		if !strings.HasPrefix(entry, "APP_DATA_DIR=") {
			fmt.Println(entry)
		}
	}
	fmt.Println("user namespaces disabled:", slices.Contains(args, "--disable-userns"))
	fmt.Println("secret in arguments:", strings.Contains(strings.Join(args, " "), "secret"))
	// Output:
	// PATH=/usr/local/bin:/usr/bin:/bin
	// LANG=C
	// HOME=/tmp
	// TMPDIR=/tmp
	// SSL_CERT_FILE=/app/ca-bundle.crt
	// APP_SMTP_PASSWORD=secret
	// TZ=UTC
	// user namespaces disabled: true
	// secret in arguments: false
}
