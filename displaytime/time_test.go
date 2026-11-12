// SPDX-License-Identifier: AGPL-3.0-or-later
package displaytime

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTimezoneValidationAndForms(t *testing.T) {
	for _, name := range []string{"", "Local", "../etc/passwd", "/usr/share/zoneinfo/UTC", "UTC\x00", " UTC", "UTC ", "Etc/../UTC", "Etc//UTC", "Unknown/Place", "America/New_York\n", strings.Repeat("x", 65), "UTC+05:30", "America/Los Ángeles"} {
		if _, err := Parse(name); !errors.Is(err, ErrTimezone) {
			t.Errorf("accepted %q: %v", name, err)
		}
	}
	for _, name := range Suggestions() {
		loc, err := Parse(name)
		if err != nil || loc.String() != name {
			t.Fatalf("suggestion %s: %v", name, err)
		}
	}
	a := Suggestions()
	a[0] = "broken"
	if Suggestions()[0] != "UTC" {
		t.Fatal("mutable shared suggestions")
	}
	for _, v := range []url.Values{{}, {"timezone": {}}, {"timezone": {"UTC", "Asia/Tokyo"}}, {"timezone": {"Local"}}} {
		if _, err := ParseForm(v); err == nil {
			t.Fatal("ambiguous/invalid form accepted", v)
		}
	}
	name, err := ParseForm(url.Values{"timezone": {" America/Los_Angeles "}, "user_id": {"999"}})
	if err != nil || name != "America/Los_Angeles" {
		t.Fatal(name, err)
	}
	if New("invalid").Name() != "UTC" || (Formatter{}).Name() != "UTC" || In(nil).Name() != "UTC" {
		t.Fatal("invalid preferences must fall back to UTC")
	}
}

func TestLocalCalendarDSTAndMachineInstants(t *testing.T) {
	for _, tc := range []struct{ zone, instant, want string }{
		{"America/Los_Angeles", "2026-01-01T00:15:00Z", "Dec 31, 2025 · 16:15 PST"},
		{"Asia/Kolkata", "2026-01-01T00:15:00Z", "Jan 1, 2026 · 05:45 IST"},
		{"Asia/Kathmandu", "2026-01-01T00:15:00Z", "Jan 1, 2026 · 06:00 +0545"},
		{"America/New_York", "2026-03-08T06:59:00Z", "Mar 8, 2026 · 01:59 EST"},
		{"America/New_York", "2026-03-08T07:00:00Z", "Mar 8, 2026 · 03:00 EDT"},
		{"America/New_York", "2026-11-01T05:30:00Z", "Nov 1, 2026 · 01:30 EDT"},
		{"America/New_York", "2026-11-01T06:30:00Z", "Nov 1, 2026 · 01:30 EST"},
	} {
		t.Run(tc.zone+tc.instant, func(t *testing.T) {
			stamp, err := time.Parse(time.RFC3339, tc.instant)
			if err != nil {
				t.Fatal(err)
			}
			f := New(tc.zone)
			for _, value := range []any{stamp, &stamp, stamp.Unix(), int(stamp.Unix())} {
				if got := f.Stamp(value); got != tc.want {
					t.Errorf("%T: %s, want %s", value, got, tc.want)
				}
				if got := ISO(value); got != tc.instant {
					t.Fatal("display changed machine timestamp", got)
				}
			}
		})
	}
	for _, v := range []any{nil, (*time.Time)(nil), time.Time{}, "not an instant"} {
		if New("UTC").Stamp(v) != "" || ISO(v) != "" {
			t.Fatal("missing timestamp became a real date", v)
		}
	}
	for n := int64(0); n < 200; n++ {
		stamp := time.Unix(1700000000+n*791919, 0)
		for _, zone := range Suggestions() {
			f := New(zone)
			if f.Format(stamp, "-0700") != stamp.In(f.Location()).Format("-0700") || ISO(stamp.In(f.Location())) != ISO(stamp) {
				t.Fatalf("instant property %s %v", zone, stamp)
			}
		}
	}
}

func TestTemplateIsolationConcurrencyAndBoundedCache(t *testing.T) {
	source := template.Must(template.New("test").Funcs(New("UTC").Funcs()).Parse(`{{date .When}}|{{isoDate .When}}|{{.Label}}`))
	views, err := NewTemplates(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Executing the caller's source later cannot prevent cloning our private copy.
	var original bytes.Buffer
	if err := source.Execute(&original, map[string]any{"When": int64(0)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			zone := []string{"UTC", "America/Los_Angeles", "Asia/Kolkata", "Pacific/Auckland"}[i%4]
			var out bytes.Buffer
			when := int64(1767226500)
			err := views.ExecuteTemplate(&out, zone, "test", map[string]any{"When": when, "Label": "<script>bad()</script>"})
			want := New(zone).Stamp(when) + "|" + ISO(when) + "|&lt;script&gt;bad()&lt;/script&gt;"
			if err != nil || out.String() != want {
				t.Errorf("reader %s leaked or failed: %s %v", zone, out.String(), err)
			}
		}(i)
	}
	wg.Wait()
	for _, zone := range append(Suggestions(), "Etc/GMT+1", "Etc/GMT+2", "Etc/GMT+3", "Etc/GMT+4", "Etc/GMT+5", "Etc/GMT+6", "Etc/GMT+7", "Etc/GMT+8", "Etc/GMT+9", "Etc/GMT+10") {
		var out bytes.Buffer
		if err := views.ExecuteTemplate(&out, zone, "test", map[string]any{"When": int64(0)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(views.entries) != templateLimit {
		t.Fatalf("cache capacity %d", len(views.entries))
	}
	if views.Has("absent") || !views.Has("test") {
		t.Fatal("route/template existence contract")
	}
	var out bytes.Buffer
	if views.ExecuteTemplate(&out, "UTC", "absent", nil) == nil {
		t.Fatal("missing template accepted")
	}
	if _, err := NewTemplates(source, nil); err == nil {
		t.Fatal("already executed source must fail to clone")
	}
}

type result struct {
	rows int64
	err  error
}

func (r result) LastInsertId() (int64, error) { return 0, nil }
func (r result) RowsAffected() (int64, error) { return r.rows, r.err }

type executor struct {
	calls     int
	args      []any
	statement string
	result    sql.Result
	err       error
}

func (e *executor) ExecContext(_ context.Context, q string, args ...any) (sql.Result, error) {
	e.calls++
	e.args = args
	e.statement = q
	return e.result, e.err
}
func TestSaveValidatesBindsAndFailsAtomically(t *testing.T) {
	e := &executor{result: result{rows: 1}}
	ctx := context.Background()
	if Save(ctx, e, "UPDATE native SET timezone=? WHERE id=? AND active=1", 12, "Local") == nil || e.calls != 0 {
		t.Fatal("invalid value reached storage")
	}
	if !errors.Is(Save(ctx, e, "UPDATE", 0, "UTC"), sql.ErrNoRows) || e.calls != 0 {
		t.Fatal("invalid actor reached storage")
	}
	if err := Save(ctx, e, "UPDATE native SET timezone=? WHERE id=? AND active=1", 12, "Asia/Tokyo"); err != nil || e.calls != 1 || fmt.Sprint(e.args) != "[Asia/Tokyo 12]" {
		t.Fatal("wrong update/bindings", e, err)
	}
	sentinel := errors.New("write failed")
	e.err = sentinel
	if !errors.Is(Save(ctx, e, "UPDATE", 12, "UTC"), sentinel) {
		t.Fatal("lost database failure")
	}
	e.err = nil
	for _, res := range []result{{rows: 0}, {rows: 2}, {rows: 1, err: sentinel}} {
		e.result = res
		if Save(ctx, e, "UPDATE", 12, "UTC") == nil {
			t.Fatal("partial/inactive update accepted", res)
		}
	}
}

func TestPreferenceEscapesDraftAndNativeCSRF(t *testing.T) {
	for _, csrf := range []string{"", "csrf_token"} {
		out, err := RenderPreference(Preference{Action: "/account/timezone", CSRFField: csrf, CSRF: `"><script>token</script>`, Name: `"><img src=x onerror=bad()>`, Error: "bad timezone"})
		if err != nil {
			t.Fatal(err)
		}
		s := string(out)
		if strings.Contains(s, "<script>") || strings.Contains(s, "<img ") || !strings.Contains(s, `role="alert"`) || !strings.Contains(s, `data-device-timezone hidden`) {
			t.Fatal("unsafe/missing control", s)
		}
		field := csrf
		if field == "" {
			field = "csrf"
		}
		if !strings.Contains(s, `name="`+field+`"`) {
			t.Fatal("wrong native CSRF field")
		}
	}
	for _, action := range []string{"https://evil.invalid/", "//evil.invalid/", "javascript:alert(1)", "/\\evil.invalid", "/account?next=external", "/account#fragment"} {
		if _, err := RenderPreference(Preference{Action: action}); err == nil {
			t.Fatal("unsafe action", action)
		}
	}
	if _, err := RenderPreference(Preference{Action: "/account", CSRFField: "timezone"}); err == nil {
		t.Fatal("CSRF field collision")
	}
}
