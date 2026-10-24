// SPDX-License-Identifier: AGPL-3.0-or-later

// Package invitation describes invitation availability for display. Applications
// retain their own permissions, persistence and atomic redemption checks.
package invitation

import "time"

// Status describes why an invitation is available or unavailable.
type Status string

const (
	Open    Status = "open"
	Revoked Status = "revoked"
	Expired Status = "expired"
	UsedUp  Status = "used up"
)

// Label returns the status as a sentence-case display label.
func (s Status) Label() string {
	switch s {
	case Open:
		return "Open"
	case Revoked:
		return "Revoked"
	case Expired:
		return "Expired"
	case UsedUp:
		return "Used up"
	default:
		return ""
	}
}

// State contains only the facts needed to describe an invitation. A nil expiry
// means never; a nonpositive maximum permits unlimited uses.
type State struct {
	Revoked       bool
	ExpiresAt     *time.Time
	Uses, MaxUses int
}

// Status calculates availability at now, giving revocation precedence over
// expiry and expiry precedence over exhausted uses. Expiry is inclusive.
func (s State) Status(now time.Time) Status {
	switch {
	case s.Revoked:
		return Revoked
	case s.ExpiresAt != nil && !s.ExpiresAt.After(now):
		return Expired
	case s.MaxUses > 0 && s.Uses >= s.MaxUses:
		return UsedUp
	default:
		return Open
	}
}
