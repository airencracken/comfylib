// SPDX-License-Identifier: AGPL-3.0-or-later

package svcconfig

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// ParseShellValue returns the value a POSIX shell would assign for the text
// after "KEY=" on one line of an OpenRC conf.d file. Quoting, backslash escapes
// and a trailing comment are understood. Anything the shell would expand or
// treat as an operator, such as $, backquotes, ;, |, &, redirections and an
// unquoted ~, is refused, as is unquoted whitespace before anything but a
// comment, so a value is either exactly the shell's or an error.
func ParseShellValue(raw string) (string, error) {
	return parseValue(raw, true)
}

// valueParser reads one assigned value. In shell mode it follows sh; otherwise
// it follows the looser systemd EnvironmentFile= syntax, which has no
// expansion and trims surrounding whitespace.
type valueParser struct {
	raw     string
	shell   bool
	value   strings.Builder
	quote   rune
	escaped bool
}

func parseValue(raw string, shell bool) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", errors.New("value contains a NUL byte")
	}
	p := &valueParser{raw: raw, shell: shell}
	for index := 0; index < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[index:])
		done, err := p.next(index, r, raw[index:index+size])
		if err != nil {
			return "", err
		}
		if done {
			break
		}
		index += size
	}
	if p.escaped || p.quote != 0 {
		return "", errors.New("unterminated quote or escape")
	}
	if shell {
		return p.value.String(), nil
	}
	return strings.TrimSpace(p.value.String()), nil
}

// next consumes one character; text is its original bytes, which are copied
// as they are so that a value that is not UTF-8 survives unchanged.
func (p *valueParser) next(index int, r rune, text string) (bool, error) {
	switch {
	case p.escaped:
		p.escaped = false
		// Inside double quotes the shell keeps a backslash unless it
		// escapes one of the characters that are special there.
		if p.shell && p.quote == '"' && !strings.ContainsRune("$`\"\\", r) {
			p.value.WriteByte('\\')
		}
		p.value.WriteString(text)
		return false, nil
	case r == '\\' && p.quote != '\'':
		p.escaped = true
		return false, nil
	case p.quote != 0:
		return false, p.quoted(r, text)
	}
	return p.unquoted(index, r, text)
}

func (p *valueParser) quoted(r rune, text string) error {
	if p.shell && p.quote == '"' && (r == '$' || r == '`') {
		return p.unsupported()
	}
	if r == p.quote {
		p.quote = 0
	} else {
		p.value.WriteString(text)
	}
	return nil
}

func (p *valueParser) unquoted(index int, r rune, text string) (bool, error) {
	switch {
	case r == '\'' || r == '"':
		p.quote = r
	case p.shell && strings.ContainsRune("$`;|&<>()~", r):
		return false, p.unsupported()
	case r == '#' && p.commentStarts(index):
		return true, nil
	case p.shell && (r == ' ' || r == '\t'):
		if strings.HasPrefix(strings.TrimSpace(p.raw[index:]), "#") {
			return true, nil
		}
		return false, fmt.Errorf("unquoted whitespace in %q", p.raw)
	default:
		p.value.WriteString(text)
	}
	return false, nil
}

// commentStarts reports whether a # begins a comment. The shell only treats it
// so at the start of a word, and the value is never at one: KEY=#x assigns
// "#x", and whitespace before a # is handled by the caller.
func (p *valueParser) commentStarts(index int) bool {
	if p.shell {
		return false
	}
	return index == 0 || p.raw[index-1] == ' ' || p.raw[index-1] == '\t'
}

func (p *valueParser) unsupported() error {
	return fmt.Errorf("shell expression %q is not supported", p.raw)
}

// splitConfigWords splits a systemd Environment= value into its assignments,
// honouring quotes and backslash escapes.
func splitConfigWords(raw string) ([]string, error) {
	var s wordSplitter
	for index := 0; index < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[index:])
		s.next(r, raw[index:index+size])
		index += size
	}
	if s.escaped || s.quote != 0 {
		return nil, errors.New("unterminated quote or escape")
	}
	s.finish()
	return s.words, nil
}

// wordSplitter holds splitConfigWords' state. started records that a word has
// begun even if it is still empty, as with "".
type wordSplitter struct {
	words   []string
	word    strings.Builder
	quote   rune
	escaped bool
	started bool
}

// next consumes one character; text is its original bytes.
func (s *wordSplitter) next(r rune, text string) {
	switch {
	case s.escaped:
		s.word.WriteString(text)
		s.escaped = false
	case r == '\\' && s.quote != '\'':
		s.escaped, s.started = true, true
	case s.quote != 0 && r == s.quote:
		s.quote = 0
	case s.quote != 0:
		s.word.WriteString(text)
	case r == '\'' || r == '"':
		s.quote, s.started = r, true
	case r == ' ' || r == '\t':
		s.finish()
	default:
		s.word.WriteString(text)
		s.started = true
	}
}

func (s *wordSplitter) finish() {
	if s.started {
		s.words = append(s.words, s.word.String())
		s.word.Reset()
		s.started = false
	}
}

// readShellConfigValue reads key from an OpenRC conf.d file. A missing file
// sets nothing; an unreadable one is an error, since the service may well set
// the key there.
func readShellConfigValue(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s: %w; run the command as a user that can read the service configuration, or set %s explicitly", path, err, key)
	}
	defer closeQuietly(file)
	return scanAssignments(file, path, key, true)
}

// scanAssignments returns the last value assigned to key, in shell syntax or
// in systemd's environment file syntax.
func scanAssignments(r io.Reader, path, key string, shell bool) (string, bool, error) {
	var value string
	found := false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for line := 1; scanner.Scan(); line++ {
		raw, ok := assignment(scanner.Text(), key, shell)
		if !ok {
			continue
		}
		parsed, err := parseValue(raw, shell)
		if err != nil {
			if shell {
				return "", false, fmt.Errorf("parse %s:%d: %w; set %s explicitly if the value uses shell expansion", path, line, err, key)
			}
			return "", false, fmt.Errorf("parse %s:%d: %w", path, line, err)
		}
		value, found = parsed, true
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return value, found, nil
}

// assignment returns the raw value when line assigns key. The shell needs the
// name directly before "=", optionally after export; systemd allows spaces
// around it and also treats lines starting with ";" as comments.
func assignment(line, key string, shell bool) (string, bool) {
	text := strings.TrimSpace(line)
	if text == "" || text[0] == '#' || !shell && text[0] == ';' {
		return "", false
	}
	if !shell {
		name, raw, ok := strings.Cut(text, "=")
		return strings.TrimSpace(raw), ok && strings.TrimSpace(name) == key
	}
	if rest, ok := strings.CutPrefix(text, "export "); ok {
		text = strings.TrimLeft(rest, " \t")
	}
	name, raw, ok := strings.Cut(text, "=")
	return raw, ok && name == key
}

// closeQuietly closes a configuration file that has been read in full. A
// failure cannot change what was read, so there is nothing to report.
func closeQuietly(file *os.File) {
	_ = file.Close()
}
