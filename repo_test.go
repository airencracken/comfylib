// SPDX-License-Identifier: AGPL-3.0-or-later

package comfylib_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// Every Go file, fixtures included, carries the licence identifier first.
func TestSourceFilesCarryTheLicenceHeader(t *testing.T) {
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }() // read-only; nothing to lose
	err = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || strings.HasPrefix(entry.Name(), ".") && path != ".") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(data, []byte("// SPDX-License-Identifier: AGPL-3.0-or-later\n")) {
			t.Errorf("%s does not start with the SPDX licence line", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// comfylib adds nothing to either app's dependency tree, and a replace
// directive would break every build that resolves it through the proxy.
func TestModuleUsesOnlyTheStandardLibrary(t *testing.T) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		directive, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch directive {
		case "require", "replace", "toolchain", "tool":
			t.Errorf("go.mod has a %s directive: %s", directive, line)
		}
	}
	if _, err := os.Stat("go.sum"); err == nil {
		t.Error("go.sum exists, so something was added as a dependency")
	}
}

// mutationSchema is the subset of JSON Schema that tools/mutation-schema.json
// uses. Keywords outside the subset make the test fail rather than be ignored.
type mutationSchema struct {
	Schema               string                     `json:"$schema"`
	ID                   string                     `json:"$id"`
	Title                string                     `json:"title"`
	Type                 string                     `json:"type"`
	MinItems             *int                       `json:"minItems"`
	MinLength            *int                       `json:"minLength"`
	Pattern              string                     `json:"pattern"`
	Items                *mutationSchema            `json:"items"`
	Required             []string                   `json:"required"`
	Properties           map[string]*mutationSchema `json:"properties"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

func loadSchema(t *testing.T) *mutationSchema {
	t.Helper()
	data, err := os.ReadFile("tools/mutation-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var schema mutationSchema
	if err := decoder.Decode(&schema); err != nil {
		t.Fatalf("the schema uses keywords this test does not check: %v", err)
	}
	return &schema
}

// validate returns every way value breaks schema, located by path.
func validate(schema *mutationSchema, value any, path string) []string {
	switch schema.Type {
	case "array":
		return validateArray(schema, value, path)
	case "object":
		return validateObject(schema, value, path)
	case "string":
		return validateString(schema, value, path)
	default:
		return []string{fmt.Sprintf("%s: schema type %q is not supported by this test", path, schema.Type)}
	}
}

func validateArray(schema *mutationSchema, value any, path string) []string {
	items, ok := value.([]any)
	if !ok {
		return []string{path + ": want an array"}
	}
	var problems []string
	if schema.MinItems != nil && len(items) < *schema.MinItems {
		problems = append(problems, fmt.Sprintf("%s: want at least %d items", path, *schema.MinItems))
	}
	for i, item := range items {
		if schema.Items != nil {
			problems = append(problems, validate(schema.Items, item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return problems
}

func validateObject(schema *mutationSchema, value any, path string) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return []string{path + ": want an object"}
	}
	var problems []string
	for _, key := range schema.Required {
		if _, ok := object[key]; !ok {
			problems = append(problems, fmt.Sprintf("%s: missing %q", path, key))
		}
	}
	// additionalProperties is either false or a schema for the extra values.
	var additional *mutationSchema
	closed := string(schema.AdditionalProperties) == "false"
	if len(schema.AdditionalProperties) > 0 && !closed {
		if err := json.Unmarshal(schema.AdditionalProperties, &additional); err != nil {
			return append(problems, fmt.Sprintf("%s: bad additionalProperties in schema: %v", path, err))
		}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		property, known := schema.Properties[key]
		switch {
		case known:
			problems = append(problems, validate(property, object[key], path+"."+key)...)
		case closed:
			problems = append(problems, fmt.Sprintf("%s: unknown field %q", path, key))
		case additional != nil:
			problems = append(problems, validate(additional, object[key], path+"."+key)...)
		}
	}
	return problems
}

func validateString(schema *mutationSchema, value any, path string) []string {
	text, ok := value.(string)
	if !ok {
		return []string{path + ": want a string"}
	}
	var problems []string
	if schema.MinLength != nil && utf8.RuneCountInString(text) < *schema.MinLength {
		problems = append(problems, fmt.Sprintf("%s: want at least %d characters", path, *schema.MinLength))
	}
	if schema.Pattern != "" && !regexp.MustCompile(schema.Pattern).MatchString(text) {
		problems = append(problems, fmt.Sprintf("%s: %q does not match %s", path, text, schema.Pattern))
	}
	return problems
}

func mutationTables(t *testing.T) []string {
	t.Helper()
	tables, err := filepath.Glob("mutations/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 {
		t.Fatal("no mutation tables found")
	}
	return tables
}

// Every table in mutations/ must match the schema, and its anchors must match
// the tree, so a stale table fails here as well as in the slower engine run.
func TestMutationTablesMatchTheSchema(t *testing.T) {
	schema := loadSchema(t)
	names := map[string]string{}
	for _, table := range mutationTables(t) {
		data, err := os.ReadFile(table)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Errorf("%s: %v", table, err)
			continue
		}
		problems := validate(schema, value, table)
		for _, problem := range problems {
			t.Error(problem)
		}
		if len(problems) > 0 {
			continue
		}
		for i, item := range value.([]any) {
			entry := item.(map[string]any)
			where := fmt.Sprintf("%s[%d] (%s)", table, i, entry["name"])
			checkMutationEntry(t, where, entry)
			name := entry["name"].(string)
			if previous, ok := names[name]; ok {
				t.Errorf("%s: name already used by %s", where, previous)
			}
			names[name] = where
		}
	}
}

func checkMutationEntry(t *testing.T, where string, entry map[string]any) {
	t.Helper()
	file := entry["file"].(string)
	before, after := entry["before"].(string), entry["after"].(string)
	if before == after {
		t.Errorf("%s: before and after are the same", where)
	}
	if slices.Contains(strings.Split(filepath.ToSlash(file), "/"), "..") {
		t.Errorf("%s: file leaves the repository", where)
	}
	if _, err := regexp.Compile(entry["run"].(string)); err != nil {
		t.Errorf("%s: run is not a valid regexp: %v", where, err)
	}
	if info, err := os.Stat(entry["package"].(string)); err != nil || !info.IsDir() {
		t.Errorf("%s: package %s is not a directory", where, entry["package"])
	}
	source, err := os.ReadFile(file)
	if err != nil {
		t.Errorf("%s: %v", where, err)
		return
	}
	if count := strings.Count(string(source), before); count != 1 {
		t.Errorf("%s: anchor occurs %d times in %s, want exactly once", where, count, file)
	}
}

// The validator itself has to reject what the schema forbids, or the test
// above proves nothing.
func TestSchemaValidatorRejectsMalformedTables(t *testing.T) {
	schema := loadSchema(t)
	valid := `{"name":"n","file":"a/b.go","before":"x","after":"","package":"./a","run":"^T$"}`
	if problems := validate(schema, decode(t, "["+valid+"]"), "valid"); len(problems) != 0 {
		t.Fatalf("a valid table was rejected: %v", problems)
	}
	for name, table := range map[string]string{
		"not a list":       `{"name":"n"}`,
		"empty list":       `[]`,
		"not an object":    `["x"]`,
		"missing run":      `[{"name":"n","file":"a.go","before":"x","after":"","package":"./a"}]`,
		"unknown field":    `[` + strings.Replace(valid, `"name"`, `"extra":1,"name"`, 1) + `]`,
		"empty name":       `[` + strings.Replace(valid, `"name":"n"`, `"name":""`, 1) + `]`,
		"empty before":     `[` + strings.Replace(valid, `"before":"x"`, `"before":""`, 1) + `]`,
		"absolute file":    `[` + strings.Replace(valid, `"file":"a/b.go"`, `"file":"/a/b.go"`, 1) + `]`,
		"module package":   `[` + strings.Replace(valid, `"package":"./a"`, `"package":"example.com/a"`, 1) + `]`,
		"spaced package":   `[` + strings.Replace(valid, `"package":"./a"`, `"package":"./a b"`, 1) + `]`,
		"numeric run":      `[` + strings.Replace(valid, `"run":"^T$"`, `"run":1`, 1) + `]`,
		"env list":         `[` + strings.Replace(valid, `"name"`, `"env":["A=1"],"name"`, 1) + `]`,
		"env number value": `[` + strings.Replace(valid, `"name"`, `"env":{"A":1},"name"`, 1) + `]`,
	} {
		if problems := validate(schema, decode(t, table), name); len(problems) == 0 {
			t.Errorf("%s: accepted %s", name, table)
		}
	}
}

func decode(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// The engine's own fixture tables follow the same format.
func TestEngineFixtureTablesMatchTheSchema(t *testing.T) {
	schema := loadSchema(t)
	tables, err := filepath.Glob("tools/testdata/fixture/*.json")
	if err != nil || len(tables) == 0 {
		t.Fatalf("no fixture tables: %v", err)
	}
	for _, table := range tables {
		data, err := os.ReadFile(table)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		for _, problem := range validate(schema, value, table) {
			t.Error(problem)
		}
	}
}
