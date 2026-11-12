// SPDX-License-Identifier: AGPL-3.0-or-later

// Package analytics stores bounded, first-party activity in an application's
// SQLite database. Applications authorize collection and admin-only reporting.
// It records internal account/content IDs, never IP addresses or browser data.
package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RetentionDays is the shared retention and maximum reporting window.
const RetentionDays = 90

// Kind identifies an application-authorized content open, rather than a fetch
// of a thumbnail, list, feed, preview or background asset.
type Kind string

const (
	Video          Kind = "video"
	Post           Kind = "post"
	Image          Kind = "image"
	Recommendation Kind = "recommendation"
	Story          Kind = "story"
	Topic          Kind = "topic"
)

const schema = `
CREATE TABLE IF NOT EXISTS comfy_analytics_meta (
 id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL CHECK(version=1), started INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS comfy_analytics_activity (
 day INTEGER NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('login','post','image','video','recommendation','story','topic')),
 member TEXT NOT NULL, item TEXT NOT NULL, count INTEGER NOT NULL CHECK(count>0), last_at INTEGER NOT NULL,
 PRIMARY KEY(day,kind,member,item)
);
CREATE INDEX IF NOT EXISTS comfy_analytics_window ON comfy_analytics_activity(kind,day);
CREATE INDEX IF NOT EXISTS comfy_analytics_expiry ON comfy_analytics_activity(day);
CREATE INDEX IF NOT EXISTS comfy_analytics_member ON comfy_analytics_activity(member,day);
`

// Executor permits login recording inside the application's session transaction.
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Initialize creates additive, separately versioned tables atomically. It must
// run on normal server/database initialization, never a read-only inspection.
// Existing application tables and their migration versions are untouched.
func Initialize(ctx context.Context, db *sql.DB, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO comfy_analytics_meta(id,version,started) VALUES(1,1,?) ON CONFLICT(id) DO NOTHING", now.Unix()); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRowContext(ctx, "SELECT version FROM comfy_analytics_meta WHERE id=1").Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return errors.New("unsupported analytics schema")
	}
	if err = Prune(ctx, tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

func identifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func validKind(kind Kind) bool {
	switch kind {
	case Post, Image, Video, Recommendation, Story, Topic:
		return true
	}
	return false
}

func day(at time.Time) int64 { return at.UTC().Unix() / 86400 }

// WindowStart is midnight UTC six days before today: the shared seven-calendar-
// day reporting window. Domain metrics can use the same boundary as activity.
func WindowStart(at time.Time) time.Time {
	return at.UTC().Truncate(24 * time.Hour).Add(-6 * 24 * time.Hour)
}

// RecordLogin counts a successful authentication only after credentials and
// account state have been verified. Use the session transaction as executor to
// prevent aborted sessions from appearing as logins. Session refreshes are not
// authentications and must not call this function.
func RecordLogin(ctx context.Context, exec Executor, member string, at time.Time) error {
	if !identifier(member) || at.Unix() < 0 {
		return errors.New("invalid analytics login")
	}
	_, err := exec.ExecContext(ctx, `INSERT INTO comfy_analytics_activity(day,kind,member,item,count,last_at)
	 VALUES(?,'login',?,'',1,?) ON CONFLICT(day,kind,member,item) DO UPDATE SET count=count+1,last_at=max(last_at,excluded.last_at)`, day(at), member, at.Unix())
	return err
}

// RecordView counts at most one authorized member/content open per UTC day.
// The unique constraint makes concurrent opens and retries idempotent. Collection
// failures should be logged by the application without preventing content access.
func RecordView(ctx context.Context, exec Executor, kind Kind, member, item string, at time.Time) error {
	if !validKind(kind) || !identifier(member) || !identifier(item) || at.Unix() < 0 {
		return errors.New("invalid analytics view")
	}
	_, err := exec.ExecContext(ctx, `INSERT INTO comfy_analytics_activity(day,kind,member,item,count,last_at)
	 VALUES(?,?,?,?,1,?) ON CONFLICT(day,kind,member,item) DO NOTHING`, day(at), string(kind), member, item, at.Unix())
	return err
}

// Prune removes expired activity with an indexed predicate. Call on startup and
// periodically while serving; expiry is independent of opening the admin page.
func Prune(ctx context.Context, exec Executor, at time.Time) error {
	_, err := exec.ExecContext(ctx, "DELETE FROM comfy_analytics_activity WHERE day<?", day(at)-RetentionDays+1)
	return err
}

// ForgetMember removes an account's activity inside the application's account
// deletion transaction. IDs are not retained as an undeclared account history.
func ForgetMember(ctx context.Context, exec Executor, member string) error {
	if !identifier(member) {
		return errors.New("invalid analytics member")
	}
	_, err := exec.ExecContext(ctx, "DELETE FROM comfy_analytics_activity WHERE member=?", member)
	return err
}

// Account resolves an internal account ID to the current admin-visible name.
// Applications supply only names the current admin is authorized to see.
type Account struct{ ID, Name string }
type Login struct {
	Name  string
	Count int64
	Last  time.Time
}
type Content struct {
	Kind  Kind
	ID    string
	Views int64
}
type Daily struct {
	Day           time.Time
	Logins, Views int64
}

// Summary contains one consistent seven-day reporting snapshot. Retention is
// ninety days; recent login rows and popular items are bounded to twenty each.
type Summary struct {
	Started                      time.Time
	Logins, ActiveMembers, Views int64
	Days                         []Daily
	LoginsByAccount              []Login
	Popular                      []Content
}

// Collect returns no partial results if any query fails. accountsQuery is an
// application-owned constant selecting internal ID and current account name,
// never HTTP input. Unknown/deleted accounts do not disclose stale identities.
func Collect(ctx context.Context, db *sql.DB, accountsQuery string, now time.Time) (Summary, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Summary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	accounts, err := readAccounts(ctx, tx, accountsQuery)
	if err != nil {
		return Summary{}, err
	}
	result, err := collect(ctx, tx, accounts, now)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return Summary{}, err
	}
	return result, nil
}

func collect(ctx context.Context, tx *sql.Tx, accounts []Account, now time.Time) (Summary, error) {
	var result Summary
	var started int64
	if err := tx.QueryRowContext(ctx, "SELECT started FROM comfy_analytics_meta WHERE id=1").Scan(&started); err != nil {
		return result, err
	}
	result.Started = time.Unix(started, 0).UTC()
	first, last := day(WindowStart(now)), day(now)
	rows, err := tx.QueryContext(ctx, `SELECT day,sum(CASE WHEN kind='login' THEN count ELSE 0 END),sum(CASE WHEN kind!='login' THEN count ELSE 0 END) FROM comfy_analytics_activity WHERE day>=? AND day<=? AND last_at<=? GROUP BY day ORDER BY day`, first, last, now.Unix())
	if err != nil {
		return result, err
	}
	byDay := map[int64]Daily{}
	for rows.Next() {
		var d int64
		var v Daily
		if err = rows.Scan(&d, &v.Logins, &v.Views); err != nil {
			break
		}
		v.Day = time.Unix(d*86400, 0).UTC()
		byDay[d] = v
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return result, err
	}
	for d := first; d <= last; d++ {
		v := byDay[d]
		v.Day = time.Unix(d*86400, 0).UTC()
		result.Days = append(result.Days, v)
		result.Logins += v.Logins
		result.Views += v.Views
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(DISTINCT member) FROM comfy_analytics_activity WHERE day>=? AND day<=? AND last_at<=?", first, last, now.Unix()).Scan(&result.ActiveMembers); err != nil {
		return result, err
	}
	result.LoginsByAccount, err = logins(ctx, tx, accounts, first, last, now)
	if err != nil {
		return result, err
	}
	result.Popular, err = popular(ctx, tx, first, last, now)
	return result, err
}

func logins(ctx context.Context, tx *sql.Tx, accounts []Account, first, last int64, now time.Time) ([]Login, error) {
	names := map[string]string{}
	for _, a := range accounts {
		names[a.ID] = a.Name
	}
	rows, err := tx.QueryContext(ctx, `SELECT member,sum(count),max(last_at) FROM comfy_analytics_activity WHERE kind='login' AND day>=? AND day<=? AND last_at<=? GROUP BY member ORDER BY max(last_at) DESC,member`, first, last, now.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var values []Login
	for rows.Next() {
		var id string
		var v Login
		var at int64
		if err = rows.Scan(&id, &v.Count, &at); err != nil {
			return nil, err
		}
		name, ok := names[id]
		if !ok {
			continue
		}
		v.Name = name
		v.Last = time.Unix(at, 0).UTC()
		values = append(values, v)
		if len(values) == 20 {
			break
		}
	}
	return values, rows.Err()
}

func popular(ctx context.Context, tx *sql.Tx, first, last int64, now time.Time) ([]Content, error) {
	rows, err := tx.QueryContext(ctx, `SELECT kind,item,sum(count) FROM comfy_analytics_activity WHERE kind!='login' AND day>=? AND day<=? AND last_at<=? GROUP BY kind,item ORDER BY sum(count) DESC,kind,item LIMIT 20`, first, last, now.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var values []Content
	for rows.Next() {
		var v Content
		if err = rows.Scan(&v.Kind, &v.ID, &v.Views); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

// MemberID formats an application numeric account/content identifier uniformly.
func MemberID(id int64) string { return strconv.FormatInt(id, 10) }

// NumericID canonicalizes a positive numeric route ID, so alternate spellings
// such as 001 cannot inflate views of the same content. Invalid input is empty.
func NumericID(raw string) string {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	return MemberID(id)
}

// Label supplies identical human-readable content names in every dashboard.
func Label(kind Kind) string {
	if !validKind(kind) {
		return "Content"
	}
	return strings.ToUpper(string(kind[:1])) + string(kind[1:])
}

// Description identifies content without copying titles or private content into
// the activity store. Apps may supply authorized display metadata separately.
func (c Content) Description() string { return fmt.Sprintf("%s %s", Label(c.Kind), c.ID) }

func readAccounts(ctx context.Context, tx *sql.Tx, query string) ([]Account, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var accounts []Account
	for rows.Next() {
		var account Account
		if err = rows.Scan(&account.ID, &account.Name); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}
