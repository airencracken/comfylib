// SPDX-License-Identifier: AGPL-3.0-or-later

package invitation_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/airencracken/comfylib/invitation"
)

func TestAvailabilityBoundariesAndPrecedence(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 5, time.UTC)
	past, future := now.Add(-time.Nanosecond), now.Add(time.Nanosecond)
	zero := time.Time{}
	for _, tc := range []struct {
		name  string
		state invitation.State
		want  invitation.Status
	}{
		{"unlimited", invitation.State{Uses: math.MaxInt}, invitation.Open},
		{"negative maximum is unlimited", invitation.State{MaxUses: -1}, invitation.Open},
		{"remaining", invitation.State{Uses: 1, MaxUses: 2, ExpiresAt: &future}, invitation.Open},
		{"last use", invitation.State{Uses: 2, MaxUses: 2}, invitation.UsedUp},
		{"overused without subtraction overflow", invitation.State{Uses: math.MaxInt, MaxUses: 1}, invitation.UsedUp},
		{"expired", invitation.State{ExpiresAt: &past}, invitation.Expired},
		{"exact expiry", invitation.State{ExpiresAt: &now}, invitation.Expired},
		{"explicit zero date expires", invitation.State{ExpiresAt: &zero}, invitation.Expired},
		{"expiry beats usage", invitation.State{ExpiresAt: &now, Uses: 2, MaxUses: 1}, invitation.Expired},
		{"revocation beats both", invitation.State{Revoked: true, ExpiresAt: &past, Uses: 2, MaxUses: 1}, invitation.Revoked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.state
			if got := tc.state.Status(now); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(tc.state, before) {
				t.Fatal("status calculation mutated its input")
			}
		})
	}
	for status, label := range map[invitation.Status]string{
		invitation.Open: "Open", invitation.Expired: "Expired", invitation.Revoked: "Revoked", invitation.UsedUp: "Used up", invitation.Status("<script>"): "",
	} {
		if got := status.Label(); got != label {
			t.Fatalf("%q label = %q, want %q", status, got, label)
		}
	}
}

func FuzzAvailabilityProperties(f *testing.F) {
	f.Add(int64(0), int64(1), 0, 1, false, true)
	f.Add(int64(100), int64(100), 1, 1, false, true)
	f.Add(int64(-1), int64(0), math.MaxInt, 0, true, false)
	f.Fuzz(func(t *testing.T, current, expires int64, uses, maximum int, revoked, hasExpiry bool) {
		current, expires = current%(1<<40), expires%(1<<40)
		now, expiry := time.Unix(current, 0), time.Unix(expires, 0)
		state := invitation.State{Revoked: revoked, Uses: uses, MaxUses: maximum}
		if hasExpiry {
			state.ExpiresAt = &expiry
		}
		got := state.Status(now)
		want := invitation.Open
		switch {
		case revoked:
			want = invitation.Revoked
		case hasExpiry && expires <= current:
			want = invitation.Expired
		case maximum > 0 && uses >= maximum:
			want = invitation.UsedUp
		}
		if got != want {
			t.Fatalf("%+v at %v: got %s, want %s", state, now, got, want)
		}
		if got != state.Status(now.In(time.FixedZone("alternate", 3600))) {
			t.Fatal("availability depends on time zone")
		}
	})
}
