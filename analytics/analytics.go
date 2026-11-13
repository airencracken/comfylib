// SPDX-License-Identifier: AGPL-3.0-or-later

// Package analytics keeps daily site health and security counts in an
// application's SQLite database: requests, status classes, response times and
// bytes per route pattern, plus failed sign-ins, rate-limited attempts and
// rejected tokens. It stores no account or content IDs, usernames, IP
// addresses, user agents, referrers, paths or query strings. Rows are kept for
// RetentionDays.
package analytics

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"
)

// RetentionDays is how long daily rows are kept.
const RetentionDays = 90

// latencyBounds are the upper bounds of the latency histogram buckets. The
// last bucket counts everything slower than the largest bound.
var latencyBounds = [...]time.Duration{
	5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second,
	2500 * time.Millisecond, 5 * time.Second, 10 * time.Second,
}

// LatencyBuckets returns the upper bounds of the latency histogram buckets.
// Histogram has one more bucket, for slower responses.
func LatencyBuckets() []time.Duration { return append([]time.Duration(nil), latencyBounds[:]...) }

// Histogram counts responses per latency bucket; see LatencyBuckets.
type Histogram [len(latencyBounds) + 1]int64

// Slower is what Histogram.Percentile returns for the overflow bucket.
const Slower = time.Duration(math.MaxInt64)

// Observe adds one response that took elapsed.
func (h *Histogram) Observe(elapsed time.Duration) {
	for i, bound := range latencyBounds {
		if elapsed <= bound {
			h[i]++
			return
		}
	}
	h[len(latencyBounds)]++
}

// Count returns the number of responses in the histogram.
func (h Histogram) Count() int64 {
	var n int64
	for _, c := range h {
		n += c
	}
	return n
}

// Percentile returns the upper bound of the bucket holding the p-th fraction
// of responses (p in (0, 1]), or Slower for the overflow bucket. The true
// value is at most the result. ok is false when the histogram is empty or p is
// out of range.
func (h Histogram) Percentile(p float64) (upper time.Duration, ok bool) {
	n := h.Count()
	if n == 0 || !(p > 0 && p <= 1) {
		return 0, false
	}
	rank := int64(math.Ceil(p * float64(n)))
	var seen int64
	for i, c := range h {
		seen += c
		if seen >= rank {
			if i == len(latencyBounds) {
				return Slower, true
			}
			return latencyBounds[i], true
		}
	}
	return Slower, true
}

func (h *Histogram) add(other Histogram) {
	for i, c := range other {
		h[i] += c
	}
}

// Traffic counts responses. Status5xx also counts status codes above 599.
type Traffic struct {
	Requests                                   int64
	Status2xx, Status3xx, Status4xx, Status5xx int64
	Bytes                                      int64
	Latency                                    Histogram
}

// ErrorRate is the fraction of requests that ended in a server error.
func (t Traffic) ErrorRate() float64 {
	if t.Requests == 0 {
		return 0
	}
	return float64(t.Status5xx) / float64(t.Requests)
}

func (t *Traffic) observe(status int, bytes int64, elapsed time.Duration) {
	t.Requests++
	switch {
	case status < 300:
		t.Status2xx++
	case status < 400:
		t.Status3xx++
	case status < 500:
		t.Status4xx++
	default:
		t.Status5xx++
	}
	if bytes > 0 {
		t.Bytes += bytes
	}
	t.Latency.Observe(elapsed)
}

func (t *Traffic) add(other Traffic) {
	t.Requests += other.Requests
	t.Status2xx += other.Status2xx
	t.Status3xx += other.Status3xx
	t.Status4xx += other.Status4xx
	t.Status5xx += other.Status5xx
	t.Bytes += other.Bytes
	t.Latency.add(other.Latency)
}

// latencyColumns names one column per Histogram bucket.
const latencyColumns = "latency_0,latency_1,latency_2,latency_3,latency_4,latency_5,latency_6,latency_7,latency_8,latency_9,latency_10,latency_11"

const schema = `
CREATE TABLE IF NOT EXISTS comfy_analytics_meta (
 id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL CHECK(version=1), started INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS comfy_analytics_security (
 day INTEGER PRIMARY KEY,
 failed_sign_ins INTEGER NOT NULL DEFAULT 0 CHECK(failed_sign_ins>=0),
 rate_limited INTEGER NOT NULL DEFAULT 0 CHECK(rate_limited>=0),
 rejected_tokens INTEGER NOT NULL DEFAULT 0 CHECK(rejected_tokens>=0)
);
CREATE TABLE IF NOT EXISTS comfy_analytics_routes (
 day INTEGER NOT NULL, route TEXT NOT NULL CHECK(length(route) BETWEEN 1 AND 128),
 requests INTEGER NOT NULL CHECK(requests>0),
 status_2xx INTEGER NOT NULL, status_3xx INTEGER NOT NULL, status_4xx INTEGER NOT NULL, status_5xx INTEGER NOT NULL,
 bytes INTEGER NOT NULL CHECK(bytes>=0),
 latency_0 INTEGER NOT NULL, latency_1 INTEGER NOT NULL, latency_2 INTEGER NOT NULL, latency_3 INTEGER NOT NULL,
 latency_4 INTEGER NOT NULL, latency_5 INTEGER NOT NULL, latency_6 INTEGER NOT NULL, latency_7 INTEGER NOT NULL,
 latency_8 INTEGER NOT NULL, latency_9 INTEGER NOT NULL, latency_10 INTEGER NOT NULL, latency_11 INTEGER NOT NULL,
 PRIMARY KEY(day,route)
);
CREATE INDEX IF NOT EXISTS comfy_analytics_routes_day ON comfy_analytics_routes(day);
`

// Executor runs a statement; *sql.DB and *sql.Tx satisfy it.
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Initialize creates the analytics tables, removes per-account and per-item
// rows left by earlier versions, and prunes expired rows, all in one
// transaction. Run it when the app opens its database for writing. It does not
// touch the app's own tables.
func Initialize(ctx context.Context, db *sql.DB, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	if err = RemoveDetail(ctx, tx); err != nil {
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

// RemoveDetail drops the table of per-account sign-ins and per-item views
// that versions before 0.7.0 kept. Initialize calls it; call it yourself only
// to run it inside your own migration. It is safe to run more than once.
func RemoveDetail(ctx context.Context, exec Executor) error {
	_, err := exec.ExecContext(ctx, "DROP TABLE IF EXISTS comfy_analytics_activity")
	return err
}

func day(at time.Time) int64 { return at.UTC().Unix() / 86400 }

// WindowStart is midnight UTC six days before at: the start of the seven-day
// reporting window. Use it for app metrics that should match the dashboard.
func WindowStart(at time.Time) time.Time {
	return at.UTC().Truncate(24 * time.Hour).Add(-6 * 24 * time.Hour)
}

// RecordFailedSignIn counts one sign-in attempt rejected for a wrong
// password, unknown account or failed second factor.
func RecordFailedSignIn(ctx context.Context, exec Executor, at time.Time) error {
	return countSecurity(ctx, exec, at, "failed_sign_ins")
}

// RecordRateLimited counts one attempt refused by a rate limit, such as a
// sign-in lockout from ratelimit.Window.
func RecordRateLimited(ctx context.Context, exec Executor, at time.Time) error {
	return countSecurity(ctx, exec, at, "rate_limited")
}

// RecordRejectedToken counts one invalid, expired or used invitation or
// password-reset token.
func RecordRejectedToken(ctx context.Context, exec Executor, at time.Time) error {
	return countSecurity(ctx, exec, at, "rejected_tokens")
}

// securityCounts holds one upsert per security column.
var securityCounts = func() map[string]string {
	statements := map[string]string{}
	for _, column := range []string{"failed_sign_ins", "rate_limited", "rejected_tokens"} {
		statements[column] = "INSERT INTO comfy_analytics_security(day," + column + ") VALUES(?,1) ON CONFLICT(day) DO UPDATE SET " + column + "=" + column + "+1"
	}
	return statements
}()

// countSecurity adds one to column for at's UTC day.
func countSecurity(ctx context.Context, exec Executor, at time.Time, column string) error {
	if at.Unix() < 0 {
		return errors.New("invalid event time")
	}
	_, err := exec.ExecContext(ctx, securityCounts[column], day(at))
	return err
}

// Prune deletes rows older than RetentionDays. Initialize and Maintenance
// call it.
func Prune(ctx context.Context, exec Executor, at time.Time) error {
	cutoff := day(at) - RetentionDays + 1
	if _, err := exec.ExecContext(ctx, "DELETE FROM comfy_analytics_security WHERE day<?", cutoff); err != nil {
		return err
	}
	_, err := exec.ExecContext(ctx, "DELETE FROM comfy_analytics_routes WHERE day<?", cutoff)
	return err
}

// Security counts rejected access attempts.
type Security struct {
	FailedSignIns, RateLimited, RejectedTokens int64
}

func (s *Security) add(other Security) {
	s.FailedSignIns += other.FailedSignIns
	s.RateLimited += other.RateLimited
	s.RejectedTokens += other.RejectedTokens
}

// Daily holds one UTC day of the report.
type Daily struct {
	Day time.Time
	// Deprecated: Always 0. Sign-ins and views are no longer counted.
	Logins, Views int64
	Traffic
	Security
}

// Route holds the traffic of one route pattern, such as "GET /posts/{id}".
type Route struct {
	Pattern string
	Traffic
}

// RouteLimit bounds Summary.Slowest and Summary.Failing.
const RouteLimit = 5

// Summary is a seven-day snapshot read in one transaction. Traffic and
// Security hold the totals for the whole window.
type Summary struct {
	Started time.Time
	// Deprecated: Always 0. Sign-ins, accounts and views are no longer counted.
	Logins, ActiveMembers, Views int64
	Traffic
	Security
	Days []Daily
	// Slowest lists the routes with the highest 95th-percentile latency;
	// Failing lists routes with errors, most server errors first.
	Slowest, Failing []Route
	// Deprecated: Always empty.
	LoginsByAccount []Login
	// Deprecated: Always empty.
	Popular []Content
}

// Collect reads the seven-day report ending at now, in one read-only
// transaction. On any error it returns an empty Summary. accountsQuery is
// ignored; pass "".
func Collect(ctx context.Context, db *sql.DB, accountsQuery string, now time.Time) (Summary, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Summary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := collect(ctx, tx, now)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return Summary{}, err
	}
	return result, nil
}

func collect(ctx context.Context, tx *sql.Tx, now time.Time) (Summary, error) {
	var result Summary
	var started int64
	if err := tx.QueryRowContext(ctx, "SELECT started FROM comfy_analytics_meta WHERE id=1").Scan(&started); err != nil {
		return result, err
	}
	result.Started = time.Unix(started, 0).UTC()
	first, last := day(WindowStart(now)), day(now)
	days := make([]Daily, last-first+1)
	for i := range days {
		days[i].Day = time.Unix((first+int64(i))*86400, 0).UTC()
	}
	if err := readSecurity(ctx, tx, days, first, last); err != nil {
		return result, err
	}
	routes, err := readRoutes(ctx, tx, days, first, last)
	if err != nil {
		return result, err
	}
	for _, d := range days {
		result.Traffic.add(d.Traffic)
		result.Security.add(d.Security)
	}
	result.Days = days
	result.Slowest, result.Failing = rankRoutes(routes)
	return result, nil
}

func readSecurity(ctx context.Context, tx *sql.Tx, days []Daily, first, last int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT day,failed_sign_ins,rate_limited,rejected_tokens FROM comfy_analytics_security WHERE day>=? AND day<=?", first, last)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d int64
		var v Security
		if err = rows.Scan(&d, &v.FailedSignIns, &v.RateLimited, &v.RejectedTokens); err != nil {
			break
		}
		days[d-first].Security = v
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

const selectRoutes = "SELECT day,route,requests,status_2xx,status_3xx,status_4xx,status_5xx,bytes," + latencyColumns + " FROM comfy_analytics_routes WHERE day>=? AND day<=?"

func readRoutes(ctx context.Context, tx *sql.Tx, days []Daily, first, last int64) (map[string]*Traffic, error) {
	rows, err := tx.QueryContext(ctx, selectRoutes, first, last)
	if err != nil {
		return nil, err
	}
	routes := map[string]*Traffic{}
	for rows.Next() {
		var d int64
		var route string
		var t Traffic
		dest := []any{&d, &route, &t.Requests, &t.Status2xx, &t.Status3xx, &t.Status4xx, &t.Status5xx, &t.Bytes}
		for i := range t.Latency {
			dest = append(dest, &t.Latency[i])
		}
		if err = rows.Scan(dest...); err != nil {
			break
		}
		days[d-first].Traffic.add(t)
		if routes[route] == nil {
			routes[route] = &Traffic{}
		}
		routes[route].add(t)
	}
	return routes, errors.Join(err, rows.Err(), rows.Close())
}

func rankRoutes(routes map[string]*Traffic) (slowest, failing []Route) {
	all := make([]Route, 0, len(routes))
	for pattern, t := range routes {
		all = append(all, Route{Pattern: pattern, Traffic: *t})
	}
	sort.Slice(all, func(i, j int) bool {
		a, _ := all[i].Latency.Percentile(.95)
		b, _ := all[j].Latency.Percentile(.95)
		if a != b {
			return a > b
		}
		if all[i].Requests != all[j].Requests {
			return all[i].Requests > all[j].Requests
		}
		return all[i].Pattern < all[j].Pattern
	})
	slowest = append(slowest, all[:min(len(all), RouteLimit)]...)
	sort.Slice(all, func(i, j int) bool {
		if all[i].Status5xx != all[j].Status5xx {
			return all[i].Status5xx > all[j].Status5xx
		}
		if all[i].Status4xx != all[j].Status4xx {
			return all[i].Status4xx > all[j].Status4xx
		}
		return all[i].Pattern < all[j].Pattern
	})
	for _, r := range all {
		if len(failing) == RouteLimit || r.Status4xx+r.Status5xx == 0 {
			break
		}
		failing = append(failing, r)
	}
	return slowest, failing
}
