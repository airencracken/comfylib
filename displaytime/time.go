// SPDX-License-Identifier: AGPL-3.0-or-later
// Package displaytime validates, stores and applies an account's display
// timezone. Times stay UTC in storage, exports and machine-readable attributes,
// and reporting days stay UTC.
package displaytime

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // Static binaries work without host timezone files.
)

const MaxName = 64

var ErrTimezone = errors.New("choose a timezone such as UTC or America/Los_Angeles")
var zoneName = regexp.MustCompile(`^(UTC|[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)+)$`)

// Parse accepts IANA names and UTC, never host-dependent Local or filesystem paths.
func Parse(name string) (*time.Location, error) {
	if len(name) > MaxName || !zoneName.MatchString(name) {
		return nil, ErrTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrTimezone
	}
	return loc, nil
}

// ParseForm rejects ambiguous scalar fields and trims surrounding form whitespace.
func ParseForm(values url.Values) (string, error) {
	if len(values["timezone"]) != 1 {
		return "", ErrTimezone
	}
	name := strings.TrimSpace(values.Get("timezone"))
	_, err := Parse(name)
	return name, err
}

// Formatter is immutable and safe to use concurrently. Its zero value uses UTC.
type Formatter struct{ location *time.Location }

// In binds an already validated location; nil uses UTC.
func In(location *time.Location) Formatter { return Formatter{location} }

// New uses UTC for missing or invalid preferences, including old account records.
func New(name string) Formatter {
	loc, err := Parse(name)
	if err != nil {
		loc = time.UTC
	}
	return Formatter{loc}
}
func (f Formatter) Location() *time.Location {
	if f.location == nil {
		return time.UTC
	}
	return f.location
}
func (f Formatter) Name() string { return f.Location().String() }

// instant accepts the timestamp representations used by the applications.
// Integer values are Unix seconds; millisecond timestamps must be converted first.
func instant(value any) time.Time {
	switch v := value.(type) {
	case time.Time:
		return v
	case *time.Time:
		if v != nil {
			return *v
		}
	case int64:
		return time.Unix(v, 0)
	case int:
		return time.Unix(int64(v), 0)
	}
	return time.Time{}
}

func (f Formatter) Format(value any, layout string) string {
	t := instant(value)
	if t.IsZero() {
		return ""
	}
	return t.In(f.Location()).Format(layout)
}
func (f Formatter) Date(value any) string  { return f.Format(value, "Jan 2, 2006") }
func (f Formatter) Stamp(value any) string { return f.Format(value, "Jan 2, 2006 · 15:04 MST") }
func (f Formatter) Clock(value any) string { return f.Format(value, "15:04 MST") }

// ISO always preserves the instant in UTC, independent of display preferences.
func ISO(value any) string { return New("UTC").Format(value, time.RFC3339) }

// Funcs supplies the shared display helpers and UTC machine-readable helpers.
func (f Formatter) Funcs() template.FuncMap {
	return template.FuncMap{"date": f.Stamp, "stamp": f.Stamp, "shortDate": f.Date, "clock": f.Clock, "isoDate": ISO, "datetime": ISO}
}

// Executor permits updates to participate in a native account transaction.
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Save validates and updates one active account through an app-owned SQL
// statement. The statement binds timezone then account ID and must check the
// native active-account condition. Zero affected rows returns sql.ErrNoRows.
func Save(ctx context.Context, exec Executor, statement string, id int64, name string) error {
	if _, err := Parse(name); err != nil {
		return err
	}
	if id <= 0 {
		return sql.ErrNoRows
	}
	result, err := exec.ExecContext(ctx, statement, name, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}
