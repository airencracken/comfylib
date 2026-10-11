// SPDX-License-Identifier: AGPL-3.0-or-later
package autobackup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/airencracken/comfylib/sandbox"
)

func TestRealServiceSandboxCompression(t *testing.T) {
	if mode := os.Getenv("COMFYWARE_BACKUP_CHILD"); mode != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		sandbox.StartReaper(ctx)
		data := os.Getenv("COMFYWARE_DATA_DIR")
		cfg, err := Load("songstead", data, func(string) string { return "" })
		if err != nil {
			t.Fatal(err)
		}
		if mode == "gzip" {
			t.Setenv("PATH", "/app/no-tools")
		}
		output, err := Create(ctx, cfg, snapshot([]byte("private sandbox snapshot")))
		if err != nil {
			t.Fatal(err)
		}
		m := manifest(t, output)
		want := "snapshot.tar.zst"
		if mode == "gzip" {
			want = "snapshot.tar.gz"
		}
		if m.Archive != want {
			t.Fatal(m.Archive, want)
		}
		return
	}
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 for actual Bubblewrap compression")
	}
	bwrap, err := sandbox.Binary("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Fatal("real compression integration requires zstd", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"zstd", "gzip"} {
		t.Run(mode, func(t *testing.T) {
			data := t.TempDir()
			args, environment, err := (sandbox.Service{Prefix: "COMFYWARE_", DataDir: data, Executable: executable, Env: []string{"COMFYWARE_BACKUP_CHILD=" + mode}}).Policy()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bwrap, append(args, "--", "/app/server", "-test.run=^TestRealServiceSandboxCompression$")...)
			cmd.Env = environment
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("sandbox compressed backup: %v: %s", err, out)
			}
			files, err := filepath.Glob(filepath.Join(data, "backups", "songstead-*", "snapshot.tar.*"))
			if err != nil || len(files) != 1 {
				t.Fatal(files, err)
			}
		})
	}
}
