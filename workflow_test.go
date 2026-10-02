// SPDX-License-Identifier: AGPL-3.0-or-later

package comfylib_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Workflow steps run without an implicit -e, so each command must check its
// own status; a command that does not would let a failed step pass. Actions
// are pinned to commits so a moved tag cannot change what runs.
func TestWorkflowCommandsCheckTheirStatus(t *testing.T) {
	workflows, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil || len(workflows) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, path := range workflows {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "\ndefaults:\n  run:\n    shell: bash --noprofile --norc {0}\n") {
			t.Errorf("%s does not set an explicit shell without -e", path)
		}
		if !strings.Contains(text, "\npermissions:\n  contents: read\n") {
			t.Errorf("%s does not limit its token to reading contents", path)
		}
		for _, problem := range uncheckedCommands(text) {
			t.Errorf("%s: %s", path, problem)
		}
		for _, problem := range unpinnedActions(text) {
			t.Errorf("%s: %s", path, problem)
		}
	}
}

// uncheckedCommands lists run lines that do not end in "|| exit 1".
func uncheckedCommands(text string) []string {
	var problems []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimPrefix(strings.TrimSpace(lines[i]), "- ")
		// The block ends at the first line indented no deeper than the run
		// key itself, which sits after any "- " list marker.
		indent := strings.Index(lines[i], "run:")
		if trimmed == "run: |" || trimmed == "run: >" {
			for i++; i < len(lines); i++ {
				command := strings.TrimSpace(lines[i])
				if command == "" {
					continue
				}
				if len(lines[i])-len(strings.TrimLeft(lines[i], " ")) <= indent {
					i--
					break
				}
				if !strings.HasSuffix(command, "|| exit 1") {
					problems = append(problems, "line "+strconv.Itoa(i+1)+" does not check its status: "+command)
				}
			}
		} else if command, ok := strings.CutPrefix(trimmed, "run: "); ok && !strings.HasSuffix(command, "|| exit 1") {
			problems = append(problems, "line "+strconv.Itoa(i+1)+" does not check its status: "+command)
		}
	}
	return problems
}

var pinned = regexp.MustCompile(`^uses: [\w.-]+/[\w./-]+@[0-9a-f]{40} # v\S+$`)

// unpinnedActions lists uses lines that name a tag or branch instead of a
// full commit hash with its version in a comment.
func unpinnedActions(text string) []string {
	var problems []string
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "- ")
		if strings.HasPrefix(trimmed, "uses: ") && !pinned.MatchString(trimmed) {
			problems = append(problems, "line "+strconv.Itoa(i+1)+" is not pinned to a commit: "+trimmed)
		}
	}
	return problems
}

// The checker has to reject the mistakes it exists for.
func TestWorkflowCheckerCatchesUncheckedCommands(t *testing.T) {
	bad := "steps:\n  - run: go test ./...\n  - run: |\n      go vet ./... || exit 1\n      go test ./...\n    env:\n      A: b\n  - uses: actions/checkout@v7\n"
	if got := uncheckedCommands(bad); len(got) != 2 {
		t.Fatalf("found %d unchecked commands, want 2: %v", len(got), got)
	}
	if got := unpinnedActions(bad); len(got) != 1 {
		t.Fatalf("found %d unpinned actions, want 1: %v", len(got), got)
	}
	good := "steps:\n  - run: |\n      go vet ./... || exit 1\n\n      go test ./... || exit 1\n  - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7\n"
	if got := append(uncheckedCommands(good), unpinnedActions(good)...); len(got) != 0 {
		t.Fatalf("flagged a correct workflow: %v", got)
	}
}
