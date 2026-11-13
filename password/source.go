// SPDX-License-Identifier: AGPL-3.0-or-later

package password

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Source selects a password source. With no explicit source, Read prompts twice.
// Password values are never accepted as command-line arguments.
type Source struct {
	Prompt, Stdin bool
	File          string
}

// Flags adds the common password source flags to a command.
func Flags(flags *flag.FlagSet) *Source {
	s := new(Source)
	flags.BoolVar(&s.Prompt, "password-prompt", false, "prompt twice with terminal echo disabled (the default)")
	flags.BoolVar(&s.Stdin, "password-stdin", false, "read exactly one password line from standard input")
	flags.StringVar(&s.File, "password-file", "", "read a private regular file containing one password line")
	return s
}

// Length limits for new passwords. MaxBytes is bcrypt's input limit, so
// accented letters and emoji, which take several bytes, reach it sooner than
// 72 characters.
const (
	MinCharacters = 12
	MaxBytes      = 72
)

// RuleError is a password rule failure. Its text is a plain sentence for the
// person choosing the password.
type RuleError string

func (e RuleError) Error() string { return string(e) }

// The rule failures Validate returns.
const (
	ErrTooShort RuleError = "Use at least 12 characters."
	ErrTooLong  RuleError = "Use a shorter password."
	ErrInvalid  RuleError = "That password has characters that can't be saved. Try typing it again."
)

// Validate applies the common policy for newly chosen passwords: at least
// MinCharacters characters and at most MaxBytes bytes of valid UTF-8. The
// error is ErrTooShort, ErrTooLong or ErrInvalid.
func Validate(value string) error {
	switch {
	case !utf8.ValidString(value):
		return ErrInvalid
	case len(value) > MaxBytes:
		return ErrTooLong
	case utf8.RuneCountInString(value) < MinCharacters:
		return ErrTooShort
	}
	return nil
}

// Fits reports whether value is within MaxBytes, for checking a password at
// sign-in before hashing it.
func Fits(value string) bool { return len(value) <= MaxBytes }

// Read rejects conflicting sources, oversized and multiline input, and public
// password files. Terminal echo is hidden before either prompt is displayed.
func (s Source) Read(in io.Reader, out io.Writer) (string, error) {
	count := 0
	if s.Prompt {
		count++
	}
	if s.Stdin {
		count++
	}
	if s.File != "" {
		count++
	}
	if count > 1 {
		return "", errors.New("choose only one of --password-prompt, --password-stdin, or --password-file")
	}
	var value string
	var err error
	switch {
	case s.File != "":
		value, err = readFile(s.File)
	case s.Stdin:
		value, err = readLine(in)
	default:
		f, ok := in.(*os.File)
		if !ok || f == nil {
			return "", errors.New("password prompt requires a terminal; use --password-stdin or --password-file")
		}
		value, err = WithHiddenInput(int(f.Fd()), func() (string, error) {
			return Confirm(out, "Password: ", "Confirm password: ", newLineReader(in))
		})
	}
	if errors.Is(err, syscall.ENOTTY) {
		return "", errors.New("password prompt requires a terminal; use --password-stdin or --password-file")
	}
	if err != nil {
		return "", err
	}
	if err = Validate(value); err != nil {
		return "", err
	}
	return value, nil
}

func readFile(path string) (value string, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
		return "", errors.New("password file must be a private regular file, at most 1024 bytes")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			value = ""
		}
	}()
	return readLine(f)
}

func readLine(in io.Reader) (string, error) {
	if in == nil {
		return "", errors.New("password input is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(in, 1025))
	if err != nil {
		return "", err
	}
	if len(data) > 1024 {
		return "", errors.New("password input is too long")
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("provide exactly one password line")
	}
	return value, nil
}

func newLineReader(in io.Reader) func() ([]byte, error) {
	reader := bufio.NewReader(io.LimitReader(in, 2050))
	return func() ([]byte, error) {
		value, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(value) > 1024 {
			return nil, errors.New("password input is too long")
		}
		return []byte(strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")), nil
	}
}
