// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanSystemdService calls visit for each setting in the [Service] sections of
// a unit and its drop-ins, in the order systemd applies them. Names and values
// are trimmed; comments and other sections are skipped. An empty unitPath
// visits nothing.
func ScanSystemdService(unitPath string, visit func(name, value, path string, line int) error) error {
	return scanSystemdService(systemdUnitDirs(), unitPath, visit)
}

func scanSystemdService(dirs []string, unitPath string, visit func(name, value, path string, line int) error) error {
	if unitPath == "" {
		return nil
	}
	configPaths, err := unitConfigFiles(dirs, unitPath)
	if err != nil {
		return err
	}
	for _, path := range configPaths {
		if err := scanSystemdFile(path, visit); err != nil {
			return err
		}
	}
	return nil
}

func scanSystemdFile(path string, visit func(name, value, path string, line int) error) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read systemd unit %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()
	inService := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			inService = text == "[Service]"
			continue
		}
		if !inService || text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		if name, value, ok := strings.Cut(text, "="); ok {
			if err := visit(strings.TrimSpace(name), strings.TrimSpace(value), path, line); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// ReadSystemdEnvironment reads key from a unit's Environment= settings and
// EnvironmentFile= files, with later files taking precedence as in systemd.
// found reports whether any of them set it.
func ReadSystemdEnvironment(unitPath, key string) (value string, found bool, err error) {
	return readSystemdEnvironment(systemdUnitDirs(), unitPath, key)
}

func readSystemdEnvironment(dirs []string, unitPath, key string) (string, bool, error) {
	unitValues := make(map[string]string)
	var environmentFiles []string
	err := scanSystemdService(dirs, unitPath, func(name, raw, path string, line int) error {
		switch name {
		case "Environment":
			return applyEnvironment(unitValues, raw, path, line)
		case "EnvironmentFile":
			// An empty assignment resets the list, as in systemd.
			if raw == "" {
				environmentFiles = nil
				return nil
			}
			environmentFiles = append(environmentFiles, raw)
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	value, found := unitValues[key]
	for _, environmentFile := range environmentFiles {
		fileValue, fileFound, err := readEnvironmentFile(environmentFile, unitPath, key)
		if err != nil {
			return "", false, err
		}
		if fileFound {
			value, found = fileValue, true
		}
	}
	return value, found, nil
}

func applyEnvironment(values map[string]string, raw, path string, line int) error {
	if raw == "" {
		clear(values)
		return nil
	}
	words, err := splitConfigWords(raw)
	if err != nil {
		return fmt.Errorf("parse systemd unit %s:%d: %w", path, line, err)
	}
	for _, word := range words {
		if name, value, ok := strings.Cut(word, "="); ok {
			values[name] = value
		}
	}
	return nil
}

// environmentFilePath interprets one EnvironmentFile= setting. systemd takes
// the whole value as one literal path, spaces included and without quotes,
// optionally prefixed with "-" when the file may be missing. A "%" would be a
// specifier this package does not expand, so it is refused rather than read
// as a different file.
func environmentFilePath(setting string) (path string, optional bool, ok bool) {
	path, optional = strings.CutPrefix(setting, "-")
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "%\x00") {
		return "", false, false
	}
	return path, optional, true
}

// readEnvironmentFile reads key from the file one EnvironmentFile= setting
// names.
func readEnvironmentFile(setting, unitPath, key string) (string, bool, error) {
	path, optional, ok := environmentFilePath(setting)
	if !ok {
		return "", false, fmt.Errorf("systemd EnvironmentFile %q in %s is not a literal absolute path; set %s explicitly", setting, unitPath, key)
	}
	file, err := os.Open(path)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read systemd environment file %s: %w", path, err)
	}
	defer closeQuietly(file)
	return scanAssignments(file, path, key, false)
}

// unitConfigFiles lists the unit followed by its drop-ins. A drop-in name in a
// directory earlier in dirs hides the same name in later ones, and the
// survivors apply in name order.
func unitConfigFiles(dirs []string, unitPath string) ([]string, error) {
	unitName := filepath.Base(unitPath)
	selected := make(map[string]string)
	for _, directory := range dirs {
		dropIns := filepath.Join(directory, unitName+".d")
		entries, err := os.ReadDir(dropIns)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read systemd overrides in %s: %w", dropIns, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".conf") {
				continue
			}
			if _, exists := selected[name]; !exists {
				selected[name] = filepath.Join(dropIns, name)
			}
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	files := []string{unitPath}
	for _, name := range names {
		files = append(files, selected[name])
	}
	return files, nil
}
