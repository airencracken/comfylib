// SPDX-License-Identifier: AGPL-3.0-or-later
package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The declarations in this file remain for one release so apps keep
// compiling. None of them store account or content IDs.

// Kind named a type of content view.
//
// Deprecated: Content views are no longer recorded.
type Kind string

// Content kinds.
//
// Deprecated: Content views are no longer recorded.
const (
	Video          Kind = "video"
	Post           Kind = "post"
	Image          Kind = "image"
	Recommendation Kind = "recommendation"
	Story          Kind = "story"
	Topic          Kind = "topic"
)

// Account mapped an account ID to a name.
//
// Deprecated: Per-account sign-ins are no longer recorded.
type Account struct{ ID, Name string }

// Login was a per-account sign-in row.
//
// Deprecated: Per-account sign-ins are no longer recorded.
type Login struct {
	Name  string
	Count int64
	Last  time.Time
}

// Content was a per-item view row.
//
// Deprecated: Content views are no longer recorded.
type Content struct {
	Kind  Kind
	ID    string
	Views int64
}

// Description returns the kind label and ID.
//
// Deprecated: Content views are no longer recorded.
func (c Content) Description() string { return fmt.Sprintf("%s %s", Label(c.Kind), c.ID) }

// Event described a content view.
//
// Deprecated: Content views are no longer recorded.
type Event struct {
	Kind         Kind
	Member, Item string
}

// RecordLogin does nothing.
//
// Deprecated: Successful sign-ins are no longer counted. Use
// RecordFailedSignIn for rejected attempts.
func RecordLogin(ctx context.Context, exec Executor, member string, at time.Time) error {
	return nil
}

// RecordView does nothing.
//
// Deprecated: Content views are no longer recorded. Use Monitor for traffic.
func RecordView(ctx context.Context, exec Executor, kind Kind, member, item string, at time.Time) error {
	return nil
}

// RecordViews does nothing.
//
// Deprecated: Content views are no longer recorded. Use Monitor for traffic.
func RecordViews(ctx context.Context, db *sql.DB, events []Event, at time.Time) error {
	return nil
}

// Track returns next unchanged and never calls resolve.
//
// Deprecated: Wrap the app's ServeMux with Monitor.Middleware instead.
func Track(db *sql.DB, next http.HandlerFunc, resolve func(*http.Request) []Event) http.HandlerFunc {
	return next
}

// ForgetMember does nothing: no account IDs are stored.
//
// Deprecated: Remove the call from account deletion.
func ForgetMember(ctx context.Context, exec Executor, member string) error {
	return nil
}

// MemberID formats a numeric ID.
//
// Deprecated: Account and content IDs are no longer recorded.
func MemberID(id int64) string { return strconv.FormatInt(id, 10) }

// NumericID returns the canonical form of a positive decimal ID, or "".
//
// Deprecated: Account and content IDs are no longer recorded.
func NumericID(raw string) string {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	return MemberID(id)
}

// Label returns a capitalized kind name, or "Content" for unknown kinds.
//
// Deprecated: Content views are no longer recorded.
func Label(kind Kind) string {
	switch kind {
	case Post, Image, Video, Recommendation, Story, Topic:
		return strings.ToUpper(string(kind[:1])) + string(kind[1:])
	}
	return "Content"
}
